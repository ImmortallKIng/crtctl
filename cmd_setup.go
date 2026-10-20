package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/boot"
	"github.com/ImmortallKIng/crtctl/internal/edid"
	"github.com/ImmortallKIng/crtctl/internal/i18n"
	"github.com/ImmortallKIng/crtctl/internal/profile"
	"github.com/ImmortallKIng/crtctl/internal/sysenv"
	"github.com/ImmortallKIng/crtctl/internal/system"
	"github.com/ImmortallKIng/crtctl/internal/timing"
)

func cmdSetup(args []string) error {
	var lf limitFlags
	var yes, noTest, currentKernel, dry bool
	var method string
	fs := newFlags("setup", "[CONNECTOR]", `Plug in the CRT and run this: crtctl finds the monitor and the adapter, works out
the safe limits (creating a monitor profile if the monitor is unknown), builds an EDID
with the best modes, lets you check it before anything is written, and installs it
permanently. Asks before every step; --yes accepts the defaults.`)
	lf.register(fs)
	fs.BoolVar(&yes, "yes", false, `accept the default answer to every question; the check before installing still waits for your "yes" (add --no-test to skip it)`)
	fs.BoolVar(&noTest, "no-test", false, "skip the check before installing")
	fs.BoolVar(&dry, "dry-run", false, "only show what would be done; changes nothing and needs no root")
	fs.BoolVar(&currentKernel, "current-kernel", false, "rebuild the initramfs only for the running kernel (faster)")
	fs.StringVar(&method, "method", "", "how to install permanently: boot (kernel parameter, works with Secure Boot) or service (debugfs at boot); default: automatic")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if os.Geteuid() != 0 && !dry {
		return reexecSudo(append([]string{"setup"}, args...))
	}
	assumeYes = yes || dry

	fmt.Println(T("crtctl setup — step-by-step CRT setup"))
	fmt.Println(T("System: %s", systemSummary()))
	env := environmentFindings()
	printFindings(os.Stdout, env)
	for _, f := range env {
		if f.level == "✗" {
			return fmt.Errorf("%s", T("fix the problem above first, then run crtctl setup again"))
		}
	}

	// 1. Какой выход
	fmt.Println()
	fmt.Println("1. " + T("Looking for the CRT monitor..."))
	t, err := chooseCRT(pos, &lf)
	if err != nil {
		return err
	}
	fmt.Println("   " + T("Output: %s (%s)", t.Conn.Name, t.Driver))
	fmt.Println("   " + T("Monitor: %s", t.monitorTitle()))
	if a := t.Adapter; a.Desc != "" {
		fmt.Println("   " + T("Adapter: %s; limit about %.1f MHz", a.Desc, a.PclkMHz))
		fmt.Println("           " + a.Source)
	}

	// 2. Пределы
	fmt.Println()
	fmt.Println("2. " + T("Working out the safe limits..."))
	if err := ensureLimits(t, &lf, dry); err != nil {
		return err
	}
	if t, err = loadTarget(t.Conn.Name, &lf); err != nil {
		return err
	}
	if !t.Limits.Known() {
		return noLimitsError(t)
	}
	fmt.Println("   " + T("Limits: %s", t.Limits.String()))

	// 3. Режимы
	fmt.Println()
	fmt.Println("3. " + T("Choosing the modes..."))
	raw, err := buildEDIDFor(t, buildOpts{kind: timing.KindAuto, best: true, profileFlag: lf.profile})
	if err != nil {
		return err
	}
	e, _ := edid.Parse(raw)
	printEDID(e, &t.Limits)
	fmt.Println()
	if !askYesNo(T("Use these modes?"), true) {
		fmt.Println(T("Nothing was changed. To choose modes yourself: crtctl edid build %s MODE... and sudo crtctl apply.", t.Conn.Name))
		return nil
	}

	if dry {
		printPlan(t)
		return nil
	}

	// 4. Канал DP для переходников, которые гаснут на низких режимах
	st, err := system.LoadState()
	if err != nil {
		return err
	}
	if t.Adapter.Kind == "dp-vga" && t.Driver == "amdgpu" && (st.Get(t.Conn.Name) == nil || st.Get(t.Conn.Name).Link == "") {
		fmt.Println()
		fmt.Println("4. " + T("DisplayPort link"))
		fmt.Println("   " + T("Many DP→VGA adapters go black on low resolutions because the driver slows the link down. Keeping the link at the adapter's maximum avoids that and is harmless."))
		if askYesNo(T("Keep the DP link at the maximum?"), true) {
			in := t.Adapter.Info
			v := system.LinkValue(in.Lanes, in.LinkRateCode)
			if err := system.ForceLink(t.Conn.DebugfsDir(), v); err != nil {
				fmt.Println("   ⚠ " + err.Error())
			} else if err := system.InstallLinkUnit(st, t.Conn.Name, t.Conn.DebugfsDir(), v, os.Stdout); err != nil {
				fmt.Println("   ⚠ " + err.Error())
			} else {
				fmt.Println("   ✓ " + T("%s link forced: %d lanes × %s Gbit/s («%s»)", t.Conn.Name, in.Lanes, rateName(in.LinkRateCode), v))
			}
		}
	}

	// 5. Проверка до установки: без подтверждения — откат, как при смене режима в Windows
	fmt.Println()
	fmt.Println("5. " + T("Checking before installing..."))
	switch {
	case noTest:
		fmt.Println("   " + T("skipped (--no-test)"))
	case !sysenv.DebugfsUsable():
		fmt.Println("   " + T("not possible here (debugfs is closed, usually because of Secure Boot); the modes appear after the reboot"))
	default:
		prev := system.RuntimeOverride(t.Conn)
		before, _ := os.ReadFile(t.Conn.SysPath + "/edid")
		how, err := system.ApplyRuntime(t.Conn, raw, os.Stdout)
		if err != nil {
			fmt.Println("   ⚠ " + err.Error())
			if !askYesNo(T("The check did not work, so the modes will only appear after a reboot. Install anyway?"), false) {
				restoreRuntime(t, prev, before)
				fmt.Println(T("Restored the previous EDID; nothing was installed."))
				return nil
			}
		} else {
			fmt.Println("   ✓ " + T("The new modes are active until reboot (%s).", how))
			fmt.Println("   " + T("Open the display settings and try a few of them, or in another terminal: crtctl try %s 1024x768@85", t.Conn.Name))
			if !askConfirm(T("Does everything look right? Install it permanently?"), 60) {
				restoreRuntime(t, prev, before)
				fmt.Println(T("Restored the previous EDID; nothing was installed."))
				return nil
			}
		}
	}

	// 6. Установка
	fmt.Println()
	fmt.Println("6. " + T("Installing permanently..."))
	profileID := ""
	if t.Profile != nil {
		profileID = t.Profile.ID
	}
	err = system.InstallPersistent(st, system.PersistOptions{
		Conn: t.Conn, EDID: raw, MonitorID: e.ID(), Title: e.Title(), Profile: profileID,
		NoDDC: t.RealEDID == nil, Method: method, AllKernels: !currentKernel, Sys: boot.Real(os.Stdout),
		XOutput: xOutputWithNotice(t),
	})
	switch {
	case errors.Is(err, system.ErrManual):
		fmt.Println("· " + T("The EDID file is ready; finish the steps above and reboot."))
		return nil
	case err != nil:
		return err
	}
	fmt.Println()
	fmt.Println("✓ " + T("Done: %s is set up. The modes stay after a reboot.", t.Conn.Name))
	fmt.Println("  " + T("Experiment with any mode: crtctl try %s 1000x500@75", t.Conn.Name))
	fmt.Println("  " + T("Check later: crtctl doctor"))
	fmt.Println("  " + T("Undo: sudo crtctl remove %s", t.Conn.Name))
	if leg := system.Legacy(); len(leg) > 0 {
		fmt.Println()
		fmt.Println("⚠ " + T("Settings made outside crtctl are still present; check that they do not duplicate these:"))
		for _, l := range leg {
			fmt.Println("   ", l)
		}
	}
	return nil
}

func rateName(code byte) string {
	return map[byte]string{0x06: "RBR 1.62", 0x0a: "HBR 2.7", 0x14: "HBR2 5.4", 0x1e: "HBR3 8.1"}[code]
}

// isCRTCandidate — похоже, что на выходе аналоговый монитор.
func isCRTCandidate(t *target) bool {
	switch t.Adapter.Kind {
	case "dp-vga", "dp-unknown", "tmds-vga", "native":
		return true
	}
	analog, known := t.analogSink()
	return analog || (!known && t.Conn.Connected())
}

// chooseCRT выбирает выход: указанный, единственный похожий на CRT или по вопросу.
func chooseCRT(pos []string, lf *limitFlags) (*target, error) {
	if len(pos) > 0 {
		return loadTarget(pos[0], lf)
	}
	names, _, err := connectedNames(nil)
	if err != nil {
		return nil, err
	}
	var all, crt []*target
	for _, n := range names {
		t, err := loadTarget(n, lf)
		if err != nil {
			return nil, err
		}
		all = append(all, t)
		if isCRTCandidate(t) {
			crt = append(crt, t)
		}
	}
	switch {
	case len(crt) == 1:
		return crt[0], nil
	case len(all) == 0:
		return nil, fmt.Errorf("%s", T("no monitor is connected"))
	case len(crt) == 0:
		crt = all
		fmt.Println("   " + T("No output looks like a CRT (no analog EDID, no VGA adapter). Pick one anyway:"))
	default:
		fmt.Println("   " + T("Several outputs look like a CRT:"))
	}
	for i, t := range crt {
		fmt.Printf("   %d) %s — %s\n", i+1, t.Conn.Name, t.monitorTitle())
	}
	n := askChoice(T("Which one?"), len(crt))
	if n == 0 {
		return nil, fmt.Errorf("%s", T("no output chosen; name it: crtctl setup CONNECTOR"))
	}
	return crt[n-1], nil
}

// ensureLimits — профиль монитора: найденный, созданный по EDID или выбранный вручную.
func ensureLimits(t *target, lf *limitFlags, dry bool) error {
	if lf.profile != "" || lf.hmax > 0 {
		return nil
	}
	if t.Profile != nil {
		fmt.Println("   ✓ " + T("Profile %s (%s)", t.Profile.ID, t.ProfileSrc))
		return nil
	}
	mon := t.Monitor()
	if mon != nil && t.Limits.Known() {
		// Пределы из EDID монитора — сохраняем их профилем, чтобы было что поправить
		p := profile.Monitor{
			ID:   "auto-" + strings.ToLower(mon.Vendor) + fmt.Sprintf("-%d", mon.Product),
			Name: mon.Title(), Match: []string{fmt.Sprintf("%s %d", mon.Vendor, mon.Product)},
			HMin: t.Limits.HMin, HMax: t.Limits.HMax, VMin: t.Limits.VMin, VMax: t.Limits.VMax, Bandwidth: t.Limits.Bandwidth,
			Notes:     autoProfileNote,
			NotesL10n: i18n.All(autoProfileNote),
			Sources:   []string{t.Limits.From["H"]},
		}
		if dry {
			fmt.Println("   · " + T("would create profile %s from the monitor's EDID", p.ID))
			lf.hmin, lf.hmax, lf.vmin, lf.vmax = p.HMin, p.HMax, p.VMin, p.VMax
			return nil
		}
		path, err := profile.SaveSystem(p)
		if err != nil {
			return err
		}
		fmt.Println("   ✓ " + T("The monitor is not in the profile list; created profile %s from its EDID: %s", p.ID, path))
		fmt.Println("     " + T("The limits are what the monitor declares about itself. If you have its datasheet, you can raise them in that file."))
		lf.profile = p.ID
		return nil
	}
	if t.Unverified && t.State != nil && t.State.Profile != "" {
		fmt.Println("   " + t.unverifiedHint())
		if askYesNo(T("Is «%s» connected?", t.State.MonitorTitle), false) {
			lf.profile = t.State.Profile
			return nil
		}
	}
	if dry {
		return fmt.Errorf("%s", T("the monitor does not tell its limits; run without --dry-run to choose a profile, or add --profile ID"))
	}
	return askProfile(t, lf)
}

var autoProfileNote = i18n.N("Created by crtctl setup from the monitor's own EDID: the limits are the highest values the monitor declares about itself. Raise them only according to the datasheet.")

// askProfile — монитор без EDID: выбрать профиль или ввести значения из паспорта.
func askProfile(t *target, lf *limitFlags) error {
	fmt.Println("   " + T("The monitor does not tell its limits. Choose a profile, or enter the values from its datasheet:"))
	all, err := profile.Load()
	if err != nil {
		return err
	}
	for i, p := range all {
		fmt.Printf("   %2d) %-18s %s (%g–%g kHz, %g–%g Hz)\n", i+1, p.ID, p.DisplayName(), p.HMin, p.HMax, p.VMin, p.VMax)
	}
	manual := len(all) + 1
	fmt.Printf("   %2d) %s\n", manual, T("enter the values by hand"))
	n := askChoice(T("Your choice?"), manual)
	switch {
	case n == 0:
		return fmt.Errorf("%s", T("no profile chosen; run again with --profile ID or --hmax/--vmax"))
	case n < manual:
		lf.profile = all[n-1].ID
		return nil
	}
	hmax, ok := askFloat(T("Highest horizontal frequency from the datasheet, kHz (for example 70):"))
	if !ok {
		return fmt.Errorf("%s", T("the horizontal frequency is required"))
	}
	vmax, ok := askFloat(T("Highest vertical refresh, Hz (Enter for 120):"))
	if !ok {
		vmax = 120
	}
	p := profile.Monitor{
		ID: "manual-" + strings.ToLower(t.Conn.Name), Name: T("Monitor on %s (entered by hand)", t.Conn.Name),
		HMin: 30, HMax: hmax, VMin: 50, VMax: vmax,
	}
	path, err := profile.SaveSystem(p)
	if err != nil {
		return err
	}
	fmt.Println("   ✓ " + T("Saved profile %s: %s", p.ID, path))
	lf.profile = p.ID
	return nil
}

// restoreRuntime возвращает EDID, который был до проверки.
func restoreRuntime(t *target, prev, before []byte) {
	if prev != nil {
		_, _ = system.ApplyRuntime(t.Conn, prev, os.Stdout)
		return
	}
	if _, err := system.ResetRuntime(t.Conn); err == nil {
		system.Reprobe(t.Conn, func(cur []byte) bool { return bytes.Equal(cur, before) }, true, os.Stdout)
	}
}

// printPlan — что setup сделал бы дальше (--dry-run).
func printPlan(t *target) {
	s := boot.Real(os.Stdout)
	fmt.Println()
	fmt.Println(T("Without --dry-run crtctl setup would now:"))
	if sysenv.DebugfsUsable() {
		fmt.Println("  • " + T("load the EDID until reboot and ask you to check the modes"))
	} else {
		fmt.Println("  • " + T("skip the check before installing (debugfs is closed)"))
	}
	if t.Adapter.Kind == "dp-vga" && t.Driver == "amdgpu" && (t.State == nil || t.State.Link == "") {
		fmt.Println("  • " + T("offer to keep the DisplayPort link at the adapter's maximum"))
	}
	m, _ := system.ChooseMethod(s, "")
	if m == system.MethodService {
		fmt.Println("  • " + T("install it permanently with a boot service (the boot loader was not recognised)"))
	} else {
		fmt.Println("  • " + T("install it permanently: %s", boot.Describe(s)))
	}
}
