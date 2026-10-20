package main

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"github.com/ImmortallKIng/crtctl/internal/display"
	"github.com/ImmortallKIng/crtctl/internal/drm"
	"github.com/ImmortallKIng/crtctl/internal/edid"
	"github.com/ImmortallKIng/crtctl/internal/profile"
	"github.com/ImmortallKIng/crtctl/internal/sysenv"
	"github.com/ImmortallKIng/crtctl/internal/system"
	"github.com/ImmortallKIng/crtctl/internal/timing"
)

func cmdTry(args []string) error {
	var lf limitFlags
	k := kindFlag{timing.KindAuto}
	var modeline, via string
	var seconds int
	var force bool
	fs := newFlags("try", "[CONNECTOR] WxH@Hz", `Shows any resolution and refresh rate on the monitor right away, for example 1000x500@75,
so you can see how it looks. If you do not press Enter in time, the previous mode comes back.
It uses your desktop session: KDE, X11 (any desktop), Sway or Hyprland. On other desktops
(GNOME…) it adds the mode to a temporary EDID instead: run it with sudo and --via edid.
The mode is checked against the monitor and adapter limits first.`)
	lf.register(fs)
	fs.Var(&k, "kind", "timing formula: auto (DMT, otherwise CVT), cvt, gtf, dmt")
	fs.StringVar(&modeline, "modeline", "", `exact timings instead of WxH@Hz, e.g. "157.5 1280 1344 1504 1728 1024 1025 1028 1072 +hsync +vsync"`)
	fs.StringVar(&via, "via", "", "how to switch: kde, x11, sway, hyprland, edid (default: your session)")
	fs.IntVar(&seconds, "seconds", 15, "seconds to keep the mode before going back, unless you press Enter")
	fs.BoolVar(&force, "force", false, "allow a mode outside the limits")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	conn, rest, err := pickConnector(pos)
	if err != nil {
		return err
	}
	t, err := loadTarget(conn, &lf)
	if err != nil {
		return err
	}
	if !t.Conn.Connected() {
		return fmt.Errorf("%s", T("%s is not connected", conn))
	}

	// Режим
	var m timing.Mode
	var hz float64
	var req string // как режим записал пользователь — для подсказок
	switch {
	case modeline != "":
		if m, err = timing.ParseModeline(modeline); err != nil {
			return err
		}
		hz = m.VFreq()
		req = fmt.Sprintf("--modeline %q", modeline)
	case len(rest) == 1:
		r, err := timing.ParseRequest(rest[0])
		if err != nil {
			return err
		}
		if r.Hz == 0 {
			return fmt.Errorf("%s", T("%s: give the refresh rate, for example %dx%d@85", rest[0], r.W, r.H))
		}
		if m, err = timing.Calc(r, k.v); err != nil {
			return err
		}
		hz = r.Hz
		req = rest[0]
	default:
		fs.Usage()
		return fmt.Errorf("%s", T("give one mode, for example 1000x500@75"))
	}

	// Способ
	var be display.Backend
	switch via {
	case "edid":
		if os.Geteuid() != 0 {
			return fmt.Errorf("%s", T("--via edid needs root: sudo crtctl try %s %s --via edid", conn, req))
		}
	case "":
		if os.Geteuid() != 0 {
			be = display.Detect(sysenv.DetectSession())
		}
	default:
		if be = display.ByName(via); be == nil {
			return fmt.Errorf("%s", T("unknown method %q: kde, x11, sway, hyprland, edid", via))
		}
	}
	if be == nil && os.Geteuid() != 0 {
		return fmt.Errorf("%s", T("this desktop cannot switch to custom modes by itself; try it through a temporary EDID: sudo crtctl try %s %s --via edid", conn, req))
	}
	if be != nil && os.Geteuid() == 0 {
		return fmt.Errorf("%s", T("run crtctl try without sudo: it talks to your desktop session (or use --via edid with sudo)"))
	}
	if be != nil && !be.Exact() && m.Source != "CVT" && !display.KDEHas(conn, m.HActive, m.VActive, m.VFreq()) {
		fmt.Println("· " + T("%s builds modes with CVT only, so %s timings become CVT. For exact timings use: sudo crtctl try %s %s --via edid", be.Name(), m.Source, conn, req))
		m = timing.CVT(m.HActive, m.VActive, hz, false)
	}

	fmt.Println(T("Trying %s (%s), %s", modeLine(m), m.Aspect(), m.Source))
	limited := t.Limits.Known() || t.Limits.PclkMax > 0
	if limited {
		ps := t.Limits.Check(m)
		for _, p := range ps {
			mark := "⚠"
			if p.Hard {
				mark = "✗"
			}
			fmt.Printf("  %s %s\n", mark, p.Text)
		}
		if profile.HasHard(ps) && !force {
			return fmt.Errorf("%s", T("the mode is outside the limits; add --force if you are sure"))
		}
	} else if !force {
		return noLimitsError(t)
	}

	if be == nil {
		return tryViaEDID(t, m, seconds)
	}
	fmt.Println(T("Switching through %s...", be.Name()))
	sess, err := be.Apply(t.Conn.Name, t.Conn.ID, t.Conn.EDID, m, hz)
	if err != nil {
		return err
	}
	reportActual(t, m)
	kept := waitConfirm(seconds)
	if kept {
		fmt.Println("✓ " + T("Mode kept. It stays until you change it or log out; to keep it for good: crtctl edid build %s %s --best", conn, req))
		return nil
	}
	fmt.Println(T("Going back to the previous mode."))
	if err := sess.Restore(); err != nil {
		return err
	}
	return sess.Cleanup()
}

// waitConfirm ждёт Enter не дольше seconds. Конец ввода (не терминал) — не подтверждение.
func waitConfirm(seconds int) bool {
	fmt.Println(T("Press Enter within %d seconds to keep it; otherwise the previous mode comes back.", seconds))
	_, got := readLineTimeout(time.Duration(seconds) * time.Second)
	return got
}

// tryViaEDID — для сред без своего способа: режим кладётся во временный EDID первым,
// пользователь выбирает его в настройках экрана, потом прежний EDID возвращается.
func tryViaEDID(t *target, m timing.Mode, seconds int) error {
	if err := system.RequireRoot("try --via edid"); err != nil {
		return err
	}
	base := t.KernelEDID
	if base == nil {
		base = t.Monitor()
	}
	modes := []timing.Mode{m}
	var extra []timing.Mode
	if base != nil {
		for _, x := range base.Detailed {
			if len(modes) < edid.Capacity(false, false) && !x.Same(m) && !x.Interlaced {
				modes = append(modes, x)
			}
		}
		extra = append(extra, base.Standard...)
		extra = append(extra, base.Established...)
	}
	raw, err := edid.Build(edid.BuildOptions{Base: base, Modes: modes, Extra: extra})
	if err != nil {
		return err
	}
	prev := system.RuntimeOverride(t.Conn)
	before, _ := os.ReadFile(t.Conn.SysPath + "/edid")
	fmt.Println(T("Loading a temporary EDID with the test mode first..."))
	if _, err := system.ApplyRuntime(t.Conn, raw, os.Stdout); err != nil {
		return err
	}
	fmt.Println("✓ " + T("%s is now in the display settings. Choose it there and look at the picture.", m.Name()))
	fmt.Println(T("When you are done, press Enter here to bring the previous EDID back (or wait %d seconds).", max(seconds, 120)))
	readLineTimeout(time.Duration(max(seconds, 120)) * time.Second) // Enter или время вышло — в обоих случаях возврат
	fmt.Println(T("Restoring the previous EDID..."))
	if prev != nil {
		_, err = system.ApplyRuntime(t.Conn, prev, os.Stdout)
		return err
	}
	if _, err := system.ResetRuntime(t.Conn); err != nil {
		return err
	}
	system.Reprobe(t.Conn, func(cur []byte) bool { return bytes.Equal(cur, before) }, true, os.Stdout)
	return nil
}

// reportActual — что видеокарта выдаёт на самом деле (режим CRTC из ядра) после переключения.
func reportActual(t *target, want timing.Mode) {
	var cur *timing.Mode
	for i := 0; i < 8 && cur == nil; i++ { // композитору нужно мгновение
		time.Sleep(250 * time.Millisecond)
		if st, err := drm.ReadKMS(t.Conn); err == nil && st.Current != nil &&
			st.Current.HActive == want.HActive && st.Current.VActive == want.VActive {
			cur = st.Current
		}
	}
	if cur == nil {
		fmt.Println("⚠ " + T("The driver does not report the new mode yet; check the picture and the display settings."))
		return
	}
	fmt.Println("✓ " + T("The monitor now gets: %s", modeLine(*cur)+"  "+classify(*cur)))
}
