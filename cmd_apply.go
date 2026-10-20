package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/boot"
	"github.com/ImmortallKIng/crtctl/internal/dpcd"
	"github.com/ImmortallKIng/crtctl/internal/drm"
	"github.com/ImmortallKIng/crtctl/internal/edid"
	"github.com/ImmortallKIng/crtctl/internal/system"
)

func cmdApply(args []string) error {
	var test, currentKernel, force bool
	var method string
	fs := newFlags("apply", "CONNECTOR FILE.bin", `Installs an EDID for a connector:
  1) right now: through debugfs; the driver re-reads it without replugging the cable;
  2) permanently: the EDID goes into the firmware folder and the initramfs, and the
     drm.edid_firmware kernel parameter is added; the initramfs tool and the boot loader
     are detected automatically. --method service uses a systemd service instead.
Before installing, it checks that the EDID was built for the monitor that is connected.`)
	fs.BoolVar(&test, "test", false, "only until the next reboot (debugfs); nothing is written permanently")
	fs.BoolVar(&currentKernel, "current-kernel", false, "rebuild the initramfs only for the running kernel (faster)")
	fs.BoolVar(&force, "force", false, "install even if the EDID was built for another monitor")
	fs.StringVar(&method, "method", "", "how to install permanently: boot (kernel parameter, works with Secure Boot) or service (debugfs at boot); default: automatic")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		fs.Usage()
		return fmt.Errorf("%s", T("give a connector and an EDID file (build one with: crtctl edid build CONNECTOR --best)"))
	}
	if err := system.RequireRoot("apply"); err != nil {
		return err
	}
	conn, file := pos[0], pos[1]
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	e, err := edid.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if len(e.Warnings) > 0 {
		return fmt.Errorf("%s: %s", file, strings.Join(e.Warnings, "; "))
	}
	if len(e.Detailed) == 0 {
		return fmt.Errorf("%s", T("%s has no detailed modes; build it with crtctl edid build", file))
	}
	t, err := loadTarget(conn, nil)
	if err != nil {
		return err
	}
	switch {
	case t.RealEDID != nil && t.RealEDID.ID() != e.ID():
		msg := T("the EDID in %s was built for %s, but %s is connected", file, e.Title(), t.RealEDID.Title())
		if !force {
			return fmt.Errorf("%s", T("%s (add --force if this is intentional)", msg))
		}
		fmt.Println("⚠", msg)
	case t.RealEDID == nil && t.Conn.Connected():
		fmt.Println("· " + T("The monitor does not send an EDID, so crtctl cannot check that %s is really what is connected.", e.Title()))
	}
	profileID := ""
	if t.Profile != nil {
		profileID = t.Profile.ID
	}
	fmt.Println(T("Installing an EDID on %s: %s, %d modes, preferred %s", conn, e.Title(), len(e.Detailed), e.Detailed[0].Name()))
	fmt.Println()

	fmt.Println(T("Loading the EDID through debugfs and asking the driver to re-read it..."))
	how, err := system.ApplyRuntime(t.Conn, raw, os.Stdout)
	switch {
	case system.IsNotConnected(err):
		fmt.Println("· " + T("%s is not connected now; the EDID will be used as soon as a monitor is connected.", conn))
	case err != nil && test:
		return err
	case err != nil:
		fmt.Println("⚠", err)
		fmt.Println("  " + T("Continuing with the permanent install: after a reboot the kernel loads the EDID from firmware."))
	default:
		fmt.Println("✓ " + T("The kernel picked up the new EDID (%s); the modes should already be in your display settings.", how))
	}
	if test {
		fmt.Println()
		fmt.Println(T("--test: everything goes back to normal after a reboot. To install permanently, run the same command without --test."))
		return nil
	}
	fmt.Println()
	st, err := system.LoadState()
	if err != nil {
		return err
	}
	err = system.InstallPersistent(st, system.PersistOptions{
		Conn: t.Conn, EDID: raw, MonitorID: e.ID(), Title: e.Title(), Profile: profileID,
		NoDDC: t.RealEDID == nil, Method: method, AllKernels: !currentKernel, Sys: boot.Real(os.Stdout),
		XOutput: xOutputWithNotice(t),
	})
	switch {
	case errors.Is(err, system.ErrManual):
		fmt.Println()
		fmt.Println("· " + T("The EDID file is ready; finish the steps above and reboot."))
		return nil
	case err != nil:
		return err
	}
	fmt.Println()
	if cs := st.Get(conn); cs != nil && cs.Method == system.MethodService {
		fmt.Println("✓ " + T("Done. At every boot a service loads the EDID from %s.", cs.EDIDFile))
	} else if cs != nil {
		fmt.Println("✓ " + T("Done. From now on the kernel loads the EDID from %s at every boot.", cs.EDIDFile))
	}
	fmt.Println("  " + T("Undo: sudo crtctl remove %s", conn))
	if leg := system.Legacy(); len(leg) > 0 {
		fmt.Println()
		fmt.Println("⚠ " + T("Settings made outside crtctl are still present; check that they do not duplicate these:"))
		for _, l := range leg {
			fmt.Println("   ", l)
		}
	}
	return nil
}

func cmdRemove(args []string) error {
	var onlyEDID, onlyLink, currentKernel bool
	fs := newFlags("remove", "CONNECTOR", `Undoes everything crtctl did for a connector: the EDID (debugfs, firmware, initramfs,
kernel parameter) and the DisplayPort link forcing. The picture does not change
until the next reboot.`)
	fs.BoolVar(&onlyEDID, "edid", false, "only the EDID")
	fs.BoolVar(&onlyLink, "link", false, "only the DisplayPort link forcing")
	fs.BoolVar(&currentKernel, "current-kernel", false, "rebuild the initramfs only for the running kernel (faster)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return fmt.Errorf("%s", T("give a connector"))
	}
	if err := system.RequireRoot("remove"); err != nil {
		return err
	}
	conn := pos[0]
	c, err := drm.Find(conn)
	if err != nil {
		return err
	}
	st, err := system.LoadState()
	if err != nil {
		return err
	}
	cs := st.Get(conn)
	did := false
	if !onlyLink {
		if was, err := system.ResetRuntime(c); err != nil {
			fmt.Println("⚠ " + T("could not clear the debugfs EDID: %v", err))
		} else if was {
			fmt.Println("✓ " + T("The temporary (debugfs) EDID is cleared."))
			did = true
		}
		if cs != nil && cs.EDIDFile != "" {
			if err := system.RemovePersistent(st, conn, boot.Real(os.Stdout), !currentKernel); err != nil && !errors.Is(err, system.ErrManual) {
				return err
			}
			fmt.Println("✓ " + T("The EDID is removed from firmware, the initramfs and the kernel parameters."))
			did = true
		}
	}
	if !onlyEDID {
		if _, err := os.Stat(system.UnitPath(conn)); err == nil {
			if err := system.RemoveLinkUnit(st, conn, os.Stdout); err != nil {
				return err
			}
			fmt.Println("✓ " + T("The DisplayPort link service is removed."))
			did = true
		}
	}
	if !did {
		fmt.Println(T("crtctl has nothing installed for %s.", conn))
		if leg := system.Legacy(); len(leg) > 0 {
			fmt.Println(T("Settings made outside crtctl:"))
			for _, l := range leg {
				fmt.Println("  ", l)
			}
		}
		return nil
	}
	fmt.Println()
	fmt.Println(T("The current picture stays as it is until a reboot (or until the cable is replugged)."))
	fmt.Println(T("If only 640x480 is available after the reboot, the driver cannot read the EDID through your adapter; install it again with crtctl apply."))
	return nil
}

func cmdLink(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println(T("Usage: crtctl link show|force|remove CONNECTOR"))
		fmt.Println(T("Details: crtctl link force -h"))
		if len(args) == 0 {
			return fmt.Errorf("%s", T("no subcommand given"))
		}
		return nil
	}
	sub, args := args[0], args[1:]
	var lanes int
	var rate string
	var persistent bool
	fs := newFlags("link "+sub, "CONNECTOR", `Forces the DisplayPort link configuration (amdgpu only, through debugfs).
For light modes the driver lowers the link rate or the lane count, and many cheap
DP→VGA adapters cannot handle that: the screen goes black. "force" sets the maximum
the adapter declares; --persistent installs a systemd service that does it at every boot.`)
	fs.IntVar(&lanes, "lanes", 0, "lane count (default: the adapter's maximum)")
	fs.StringVar(&rate, "rate", "", "link rate: rbr, hbr, hbr2, hbr3 or a code 0x6/0xa/0x14/0x1e")
	fs.BoolVar(&persistent, "persistent", false, "apply at every boot")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return fmt.Errorf("%s", T("give a connector"))
	}
	if err := system.RequireRoot("link"); err != nil {
		return err
	}
	c, err := drm.Find(pos[0])
	if err != nil {
		return err
	}
	if !c.IsDP() {
		return fmt.Errorf("%s", T("%s is not a DisplayPort output", c.Name))
	}
	st, err := system.LoadState()
	if err != nil {
		return err
	}
	switch sub {
	case "show":
		if c.AuxDev != "" && c.Connected() {
			if in, err := dpcd.Read(c.AuxDev); err == nil {
				line := T("Receiver: DP %s, up to %d lanes × %s Gbit/s", in.Rev, in.Lanes, dpcd.RateName(in.LinkRateCode))
				if in.Branch {
					line += "; " + T("adapter to %s, limit about %.1f MHz", in.PortType, in.HardLimitMHz())
				}
				fmt.Println(line)
				fmt.Printf("DPCD:  000: % x\n       080: % x\n       500: % x\n", in.Raw0, in.Raw80, in.Raw500)
			}
		}
		s, err := system.ReadLink(c.DebugfsDir())
		if err != nil {
			return err
		}
		fmt.Println("link_settings:")
		for _, l := range strings.Split(s, "\n") {
			fmt.Println("  " + strings.TrimSpace(l))
		}
		if cs := st.Get(c.Name); cs != nil && cs.Link != "" {
			fmt.Println(T("Forced by crtctl: «%s» (%s)", cs.Link, system.UnitPath(c.Name)))
		}
		return nil
	case "force":
		if lanes == 0 || rate == "" {
			if c.AuxDev == "" || !c.Connected() {
				return fmt.Errorf("%s", T("nothing is connected: give --lanes and --rate yourself"))
			}
			in, err := dpcd.Read(c.AuxDev)
			if err != nil {
				return err
			}
			if lanes == 0 {
				lanes = in.Lanes
			}
			if rate == "" {
				rate = fmt.Sprintf("0x%x", in.LinkRateCode)
			}
		}
		code, err := parseRate(rate)
		if err != nil {
			return err
		}
		if lanes != 1 && lanes != 2 && lanes != 4 {
			return fmt.Errorf("%s", T("lane count must be 1, 2 or 4"))
		}
		v := system.LinkValue(lanes, code)
		if err := system.ForceLink(c.DebugfsDir(), v); err != nil {
			return err
		}
		fmt.Println("✓ " + T("%s link forced: %d lanes × %s Gbit/s («%s»)", c.Name, lanes, dpcd.RateName(code), v))
		if persistent {
			if err := system.InstallLinkUnit(st, c.Name, c.DebugfsDir(), v, os.Stdout); err != nil {
				return err
			}
			fmt.Println("✓ " + T("Applied at every boot by %s", system.UnitPath(c.Name)))
			fmt.Println("  " + T("If low modes go black again after replugging the adapter: sudo systemctl restart crtctl-link-%s", c.Name))
		} else {
			fmt.Println("  " + T("Until the next reboot. To keep it, add --persistent."))
		}
		return nil
	case "remove":
		if err := system.RemoveLinkUnit(st, c.Name, os.Stdout); err != nil {
			return err
		}
		fmt.Println("✓ " + T("The service is removed; the current link setting is reset at the next reboot."))
		return nil
	}
	return fmt.Errorf("%s", T("unknown subcommand link %q: show, force, remove", sub))
}

func parseRate(s string) (byte, error) {
	switch strings.ToLower(s) {
	case "rbr", "1.62":
		return 0x06, nil
	case "hbr", "2.7":
		return 0x0a, nil
	case "hbr2", "5.4":
		return 0x14, nil
	case "hbr3", "8.1":
		return 0x1e, nil
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 8)
	if err != nil || (v != 0x06 && v != 0x0a && v != 0x14 && v != 0x1e) {
		return 0, fmt.Errorf("%s", T("link rate %q: use rbr, hbr, hbr2, hbr3 or 0x6/0xa/0x14/0x1e", s))
	}
	return byte(v), nil
}

func cmdReprobe(args []string) error {
	var hard bool
	fs := newFlags("reprobe", "CONNECTOR", `Makes the driver read the EDID again without replugging the cable and tells the
desktop that the modes changed. Tries gentle methods first and stops as soon as the
kernel EDID changes. --hard also allows a software replug (the monitor blinks).`)
	fs.BoolVar(&hard, "hard", false, "allow a software replug (the monitor blinks)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return fmt.Errorf("%s", T("give a connector"))
	}
	if err := system.RequireRoot("reprobe"); err != nil {
		return err
	}
	c, err := drm.Find(pos[0])
	if err != nil {
		return err
	}
	before := c.EDID
	how, ok := system.Reprobe(c, func(cur []byte) bool { return !bytes.Equal(cur, before) }, hard, os.Stdout)
	after, _ := os.ReadFile(c.SysPath + "/edid")
	if ok {
		fmt.Println("✓ " + T("The kernel EDID is updated (%s): %d bytes before, %d now.", how, len(before), len(after)))
	} else {
		fmt.Println(T("The kernel EDID did not change (%d bytes). If a change was expected, try --hard.", len(after)))
	}
	return nil
}
