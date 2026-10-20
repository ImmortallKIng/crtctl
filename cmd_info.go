package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/drm"
	"github.com/ImmortallKIng/crtctl/internal/edid"
	"github.com/ImmortallKIng/crtctl/internal/profile"
	"github.com/ImmortallKIng/crtctl/internal/system"
)

// connectedNames — подключённые коннекторы (или переданные явно).
func connectedNames(pos []string) ([]string, []string, error) {
	if len(pos) > 0 {
		return pos, nil, nil
	}
	all, err := drm.List()
	if err != nil {
		return nil, nil, err
	}
	var on, off []string
	for _, c := range all {
		if c.Connected() {
			on = append(on, c.Name)
		} else {
			off = append(off, c.Name)
		}
	}
	return on, off, nil
}

func cmdInfo(args []string) error {
	var lf limitFlags
	fs := newFlags("info", "[CONNECTOR]", `Shows every connected output: the monitor, the EDID the kernel uses and the real
EDID read over DDC, the monitor profile, the limits, the adapter and the current mode.
With sudo it also reads the DisplayPort adapter registers and remembers its limit.`)
	lf.register(fs)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	names, off, err := connectedNames(pos)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Println(T("No monitor is connected."))
	}
	for i, n := range names {
		if i > 0 {
			fmt.Println()
		}
		t, err := loadTarget(n, &lf)
		if err != nil {
			return err
		}
		printInfo(t)
	}
	if len(off) > 0 {
		fmt.Println()
		fmt.Println(T("Not connected: %s", strings.Join(off, ", ")))
	}
	if os.Geteuid() != 0 {
		fmt.Println(T("Tip: sudo crtctl info also reads the DisplayPort adapter registers."))
	}
	return nil
}

func edidLine(e *edid.EDID) string {
	in := T("digital input")
	if !e.Digital {
		in = T("analog input")
	}
	return T("%s, made in %d, %s, %d×%d cm", e.Title(), e.Year, in, e.WidthCM, e.HeightCM)
}

func printInfo(t *target) {
	c := t.Conn
	state := T("not connected")
	if c.Connected() {
		state = T("connected")
		if c.Enabled {
			state = T("connected, on")
		}
	}
	fmt.Printf("%s (%s, %s) — %s\n", c.Name, c.Card, t.Driver, state)
	if !c.Connected() {
		return
	}
	row := func(k, v string) { fmt.Printf("  %s %s\n", padRight(T("%s:", k), 19), v) }

	switch m := t.Monitor(); {
	case m != nil:
		title := edidLine(m)
		if t.Profile != nil {
			title += " — " + t.Profile.DisplayName()
		}
		row(T("Monitor"), title)
	case t.Unverified:
		row(T("Monitor"), T("unknown: it does not send an EDID, and the kernel uses a replaced one"))
	default:
		row(T("Monitor"), T("unknown: neither the kernel nor crtctl could read its EDID"))
	}

	switch {
	case t.KernelEDID == nil:
		row(T("Kernel EDID"), T("none: the driver could not read the EDID, so almost no modes are available"))
	case t.Runtime != nil && bytes.Equal(t.Runtime, t.KernelEDID.Raw):
		row(T("Kernel EDID"), T("%d bytes, replaced until reboot (debugfs)", len(t.KernelEDID.Raw)))
	case t.Override != "":
		src := "drm.edid_firmware=" + t.Override
		if t.State != nil && t.State.EDIDFile != "" && system.FirmwareName(t.State.EDIDFile) == t.Override {
			src += " (" + T("installed by crtctl") + ")"
		}
		row(T("Kernel EDID"), T("%d bytes, replaced: %s", len(t.KernelEDID.Raw), src))
	case t.KernelOverridden():
		row(T("Kernel EDID"), T("%d bytes, replaced (differs from the monitor's own)", len(t.KernelEDID.Raw)))
	default:
		row(T("Kernel EDID"), T("%d bytes, the monitor's own", len(t.KernelEDID.Raw)))
	}
	switch {
	case t.RealEDID != nil:
		row(T("EDID over DDC"), T("%d bytes from /dev/i2c-%d", len(t.RealRaw), c.DDCBus))
	case t.RealErr != nil:
		row(T("EDID over DDC"), T("not read: %s", t.RealErr.Error()))
	}
	if t.State != nil && t.State.MonitorID != "" && t.RealEDID != nil && t.State.MonitorID != t.RealEDID.ID() {
		row("⚠ "+T("WARNING"), T("the installed EDID was made for «%s», but %s is connected", t.State.MonitorTitle, t.RealEDID.Title()))
	}
	if t.Unverified && t.Profile == nil {
		row("⚠ "+T("WARNING"), t.unverifiedHint())
	}

	if t.Profile != nil {
		row(T("Profile"), fmt.Sprintf("%s (%s)", t.Profile.ID, t.ProfileSrc))
	} else if m := t.Monitor(); m == nil || !m.Digital {
		row(T("Profile"), T("none (list: crtctl profiles; your own: crtctl profiles --new ID)"))
	}
	switch m := t.Monitor(); {
	case t.Limits.Known() || t.Limits.PclkMax > 0:
		row(T("Limits"), t.Limits.String())
		var src []string
		for _, k := range []struct{ key, name string }{{"H", T("horizontal")}, {"V", T("vertical")}, {"pclk", T("pixel clock")}} {
			if s := t.Limits.From[k.key]; s != "" {
				src = append(src, k.name+": "+s)
			}
		}
		row(T("Source"), strings.Join(src, "; "))
	case m != nil && m.Digital:
		row(T("Limits"), T("digital input: CRT scan limits do not apply"))
	default:
		row(T("Limits"), T("unknown: use --profile or --hmax/--vmax"))
	}

	switch a := t.Adapter; {
	case a.Kind == "dp-vga":
		row(T("Adapter"), T("%s; limit about %.1f MHz", a.Desc, a.PclkMHz))
	case a.Kind == "digital" && a.Info != nil:
		row(T("DP receiver"), a.Desc)
	case a.Desc != "":
		row(T("Adapter"), T("%s; limit about %.1f MHz", a.Desc, a.PclkMHz)+" ("+a.Source+")")
	}
	if t.DPCDErr != nil {
		row(T("Adapter"), T("not read: %s", t.DPCDErr.Error()))
	}
	if c.IsDP() {
		if t.State != nil && t.State.Link != "" {
			row(T("DP link"), T("forced to «%s» (service %s)", t.State.Link, system.UnitPath(c.Name)))
		}
		if os.Geteuid() == 0 {
			if s, err := system.ReadLink(c.DebugfsDir()); err == nil {
				for i, l := range strings.Split(s, "\n") {
					k := ""
					if i == 0 {
						k = "link_settings:"
					}
					fmt.Printf("  %s %s\n", padRight(k, 19), strings.TrimSpace(l))
				}
			}
		}
	}

	if t.KMS != nil && t.KMS.Current != nil {
		m := *t.KMS.Current
		row(T("Current mode"), fmt.Sprintf("%s  %s  %s", modeLine(m), classify(m), verdict(t.Limits, m)))
	}
	if t.KMS != nil {
		row(T("Kernel modes"), T("%d (full list with checks: crtctl modes %s --kernel)", len(t.KMS.Modes), c.Name))
	}
}

// --- doctor ---

type finding struct {
	level string // "✗" — проблема, "⚠" — предупреждение, "·" — подсказка
	text  string
	fix   []string
}

func cmdDoctor(args []string) error {
	var lf limitFlags
	fs := newFlags("doctor", "[CONNECTOR]", `Checks every connected output and prints the exact command that fixes each problem.
With sudo it also checks the DisplayPort adapter and debugfs.`)
	lf.register(fs)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	names, _, err := connectedNames(pos)
	if err != nil {
		return err
	}
	fmt.Println(T("System: %s", systemSummary()))
	if env := environmentFindings(); len(env) > 0 {
		printFindings(os.Stdout, env)
	}
	fmt.Println()
	if len(names) == 0 {
		fmt.Println(T("No monitor is connected."))
	}
	for i, n := range names {
		if i > 0 {
			fmt.Println()
		}
		t, err := loadTarget(n, &lf)
		if err != nil {
			return err
		}
		fmt.Printf("%s — %s\n", n, t.monitorTitle())
		fs := diagnose(t)
		if len(fs) == 0 {
			fmt.Println("  ✓ " + T("no problems found"))
		}
		printFindings(os.Stdout, fs)
	}
	if leg := system.Legacy(); len(leg) > 0 {
		fmt.Println()
		fmt.Println(T("Found settings made outside crtctl (they may duplicate or conflict with it):"))
		for _, l := range leg {
			fmt.Println("  ⚠", l)
		}
	}
	if os.Geteuid() != 0 {
		fmt.Println()
		fmt.Println("· " + T("Adapter and debugfs checks need root: sudo crtctl doctor"))
	} else if _, err := os.Stat("/sys/kernel/debug/dri"); err != nil {
		fmt.Println()
		fmt.Println("✗ " + T("debugfs is not mounted, so apply --test, reprobe and link will not work:"))
		fmt.Println("      $ sudo mount -t debugfs none /sys/kernel/debug")
	}
	return nil
}

func diagnose(t *target) []finding {
	var out []finding
	c := t.Conn
	add := func(level, text string, fix ...string) { out = append(out, finding{level, text, fix}) }
	build := "crtctl edid build " + c.Name + " --best"
	apply := "sudo crtctl apply " + c.Name + " crtctl-" + c.Name + ".bin"

	if t.KernelEDID == nil {
		if t.RealEDID != nil {
			add("✗", T("The kernel could not read the EDID, although the monitor sends it over DDC (typical for DP→VGA adapters). That is why almost no modes are available."),
				build, apply)
		} else {
			add("✗", T("No EDID at all: the monitor has no DDC, or the adapter does not pass it. Pick a monitor profile:"),
				"crtctl profiles", "crtctl edid build "+c.Name+" --profile ID --best", apply)
		}
		if t.KMS != nil && len(t.KMS.Modes) <= 1 {
			add("·", T("Without an EDID the driver offers only 640x480."))
		}
	}

	if t.Unverified && t.Profile == nil {
		add("⚠", t.unverifiedHint())
	}
	if t.State != nil && t.State.MonitorID != "" && t.RealEDID != nil && t.RealEDID.ID() != t.State.MonitorID {
		add("✗", T("The EDID installed on %s was made for «%s», but %s is connected. Its modes were calculated for another monitor and may be unsafe for this one.",
			c.Name, t.State.MonitorTitle, t.RealEDID.Title()), "sudo crtctl remove "+c.Name)
	}
	if t.State != nil && t.State.EDIDFile != "" {
		installed, err := os.ReadFile(t.State.EDIDFile)
		switch {
		case err != nil:
			add("✗", T("The installed EDID file is missing: %s", t.State.EDIDFile), "sudo crtctl apply "+c.Name+" FILE.bin")
		case system.SHA256(installed) != t.State.EDIDSHA256:
			add("⚠", T("The file %s was changed outside crtctl.", t.State.EDIDFile))
		case t.State.Method == system.MethodService:
			if t.KernelEDID == nil || !bytes.Equal(installed, t.KernelEDID.Raw) {
				add("⚠", T("The EDID is installed through the boot service, but the kernel does not use it now. Check: systemctl status crtctl-edid"), "sudo systemctl restart crtctl-edid")
			}
		case t.Override != system.FirmwareName(t.State.EDIDFile):
			if t.KernelEDID != nil && bytes.Equal(installed, t.KernelEDID.Raw) {
				add("·", T("The EDID is installed and already active until reboot; after a reboot the kernel will load it by itself."))
			} else {
				add("⚠", T("The EDID is installed, but this boot does not use it yet: reboot."))
			}
		case t.KernelEDID != nil && !bytes.Equal(installed, t.KernelEDID.Raw) && t.Runtime == nil:
			add("⚠", T("The kernel loaded an older version of the EDID: the initramfs is outdated. Install it again:"), "sudo crtctl apply "+c.Name+" "+t.State.EDIDFile)
		}
	}

	switch m := t.Monitor(); {
	case t.Limits.Known():
		if t.KMS != nil && t.KMS.Current != nil {
			if ps := t.Limits.Check(*t.KMS.Current); profile.HasHard(ps) {
				add("✗", T("The current mode %s is out of limits: %s", t.KMS.Current.Name(), ps[0].Text))
			}
		}
		if t.KMS != nil {
			var bad []string
			for _, m := range t.KMS.Modes {
				if profile.HasHard(t.Limits.Check(m)) {
					bad = append(bad, m.Name())
				}
			}
			if len(bad) > 0 {
				add("⚠", T("%d modes in the list are out of limits; do not choose them: %s", len(bad), strings.Join(bad, ", ")))
			}
		}
	case m != nil && m.Digital:
	case !t.Unverified:
		add("⚠", T("The monitor limits are unknown, so crtctl cannot tell which modes are safe."), "crtctl profiles", "crtctl info "+c.Name+" --profile ID")
	}

	if c.IsDP() {
		isVGA := t.Adapter.Kind == "dp-vga" && t.Adapter.Info != nil && t.Adapter.Info.PortType == "VGA"
		switch {
		case system.LinkUnitFailed(c.Name):
			add("✗", T("The service that forces the DP link failed at boot."), "systemctl status crtctl-link-"+c.Name, "sudo systemctl restart crtctl-link-"+c.Name)
		case isVGA && t.Driver == "amdgpu" && (t.State == nil || t.State.Link == ""):
			add("·", T("DP→VGA adapter. If low resolutions (800x600, 1024x768@60…) give a black screen, the adapter cannot handle a slower link. Force the link to its maximum:"),
				"sudo crtctl link force "+c.Name+" --persistent")
		}
		if t.DPCDErr != nil {
			add("·", T("Adapter registers not read: %s", t.DPCDErr.Error()))
		}
	}

	if out2, err := exec.Command("journalctl", "-k", "-b", "--no-pager", "-q").Output(); err == nil && t.KernelEDID == nil {
		for _, l := range strings.Split(string(out2), "\n") {
			if strings.Contains(l, "connector: "+c.Name) && strings.Contains(l, "EDID err") {
				add("·", T("Kernel log: %s", strings.TrimSpace(l[strings.Index(l, "EDID err"):])))
				break
			}
		}
	}
	return out
}
