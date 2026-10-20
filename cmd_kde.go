package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
	"github.com/ImmortallKIng/crtctl/internal/profile"
	"github.com/ImmortallKIng/crtctl/internal/system"
	"github.com/ImmortallKIng/crtctl/internal/timing"
)

var kdeAbout = map[string]string{
	"list":   i18n.N("Lists the modes of an output in KDE with their frequencies, checked against the limits."),
	"add":    i18n.N("Adds custom KDE modes. KWin computes them with CVT; for exact VESA DMT timings use an EDID instead."),
	"best":   i18n.N("Adds the highest safe refresh rate (CVT) for every 4:3/5:4 resolution that is not there yet."),
	"remove": i18n.N("Removes custom modes: their numbers from crtctl kde list, or all."),
	"set":    i18n.N("Switches to a mode (WxH@Hz or the KDE number). If you do not press Enter in time, the previous mode comes back."),
}

func cmdKDE(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println(T("Usage: crtctl kde list|add|best|remove|set [CONNECTOR] ..."))
		fmt.Println()
		for _, s := range []string{"list", "add", "best", "remove", "set"} {
			fmt.Printf("  %-7s %s\n", s, T(kdeAbout[s]))
		}
		if len(args) == 0 {
			return fmt.Errorf("%s", T("no subcommand given"))
		}
		return nil
	}
	sub, args := args[0], args[1:]
	about, ok := kdeAbout[sub]
	if !ok {
		return fmt.Errorf("%s", T("unknown subcommand kde %q: list, add, best, remove, set", sub))
	}
	var lf limitFlags
	var force, noRevert bool
	var revert int
	fs := newFlags("kde "+sub, "[CONNECTOR] [MODE...]", about)
	lf.register(fs)
	fs.BoolVar(&force, "force", false, "allow modes outside the limits")
	fs.IntVar(&revert, "revert", 15, "seconds to confirm the new mode (set)")
	fs.BoolVar(&noRevert, "no-revert", false, "do not wait for confirmation (set)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := system.RequireUser("crtctl kde"); err != nil {
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
	out, err := system.KDEOutputByName(conn)
	if err != nil {
		return err
	}
	l := t.Limits
	limited := l.Known() || l.PclkMax > 0

	switch sub {
	case "list":
		fmt.Println(T("%s in KDE: %d modes, %d custom", conn, len(out.Modes), len(out.CustomModes)))
		if l.Known() {
			fmt.Println(T("Limits: %s", l.String()))
		}
		fmt.Println()
		for _, m := range out.Modes {
			tm, src := kdeTiming(t, m)
			mark := " "
			if m.Current {
				mark = "*"
			}
			fmt.Printf(" %s %4s  %s  %-8s %s\n", mark, m.ID, modeLine(tm), src, verdict(l, tm))
		}
		if len(out.CustomModes) > 0 {
			fmt.Println()
			fmt.Println(T("Custom modes (number for crtctl kde remove):"))
			for i, s := range out.CustomModes {
				fmt.Printf("  %d: %s\n", i, s)
			}
		}
		fmt.Println()
		fmt.Println(T("* current"))
		return nil

	case "add", "best":
		type want struct {
			m  timing.Mode
			hz float64 // что передать KWin
		}
		var add []want
		if sub == "add" {
			if len(rest) == 0 {
				return fmt.Errorf("%s", T("list the modes to add, for example 1024x768@85"))
			}
			for _, s := range rest {
				r, err := timing.ParseRequest(s)
				if err != nil {
					return err
				}
				if r.Hz == 0 {
					return fmt.Errorf("%s", T("%s: give the refresh rate, for example %dx%d@85", s, r.W, r.H))
				}
				m := timing.CVT(r.W, r.H, r.Hz, false)
				if d, ok := timing.FindDMT(r.W, r.H, r.Hz); ok && !sameTiming(d, m) {
					fmt.Println("· " + T("%s exists in the VESA DMT standard, but KWin computes it with CVT (%.2f kHz instead of %.2f). For exact DMT timings use crtctl edid build.", s, m.HFreq(), d.HFreq()))
				}
				add = append(add, want{m, r.Hz})
			}
		} else {
			if !l.Known() {
				return noLimitsError(t)
			}
			for _, r := range crtResolutions {
				if m, ok := l.MaxRefresh(r[0], r[1], timing.KindCVT, 1); ok && m.VFreq() >= 59.5 {
					add = append(add, want{m, math.Round(m.VFreq())})
				}
			}
		}
		var reqs [][3]float64
		for _, a := range add {
			if hasKDEMode(out, a.m.HActive, a.m.VActive, a.m.VFreq()) {
				fmt.Println("· " + T("%s is already there", a.m.Name()))
				continue
			}
			if limited {
				if ps := l.Check(a.m); profile.HasHard(ps) {
					if !force {
						return fmt.Errorf("%s", T("%s: %s (add --force if you are sure)", a.m.Name(), ps[0].Text))
					}
					fmt.Println("⚠ " + T("%s is out of limits, added because of --force", a.m.Name()))
				}
			}
			reqs = append(reqs, [3]float64{float64(a.m.HActive), float64(a.m.VActive), a.hz})
		}
		if len(reqs) == 0 {
			fmt.Println(T("Nothing to add."))
			return nil
		}
		if err := system.KDEAddModes(conn, reqs); err != nil {
			return err
		}
		// KWin пересчитывает частоту по-своему — показываем, что получилось на самом деле
		after, err := system.KDEOutputByName(conn)
		if err != nil {
			return err
		}
		fmt.Println(T("Added (as KWin created them):"))
		for _, m := range after.Modes {
			if hasKDEMode(out, m.W, m.H, m.Hz) {
				continue
			}
			tm, src := kdeTiming(t, m)
			fmt.Printf("  + %4s  %s  %-8s %s\n", m.ID, modeLine(tm), src, verdict(l, tm))
		}
		fmt.Println(T("Switch to one: crtctl kde set %s WxH@Hz", conn))
		return nil

	case "remove":
		if len(rest) == 0 {
			return fmt.Errorf("%s", T("give the custom mode numbers (see crtctl kde list %s) or all", conn))
		}
		var idx []int
		if rest[0] == "all" {
			for i := range out.CustomModes {
				idx = append(idx, i)
			}
		} else {
			for _, s := range rest {
				i, err := strconv.Atoi(s)
				if err != nil || i < 0 || i >= len(out.CustomModes) {
					return fmt.Errorf("%s", T("there is no custom mode %q (there are %d)", s, len(out.CustomModes)))
				}
				idx = append(idx, i)
			}
		}
		if len(idx) == 0 {
			fmt.Println(T("There are no custom modes."))
			return nil
		}
		for _, m := range out.Modes {
			for _, i := range idx {
				if m.Current && strings.HasPrefix(out.CustomModes[i], fmt.Sprintf("%dx%d@", m.W, m.H)) && math.Abs(parseHz(out.CustomModes[i])-m.Hz) < 0.15 {
					return fmt.Errorf("%s", T("mode %s is active right now; switch to another one first", out.CustomModes[i]))
				}
			}
		}
		if err := system.KDERemoveCustom(conn, idx); err != nil {
			return err
		}
		fmt.Println("✓ " + T("Removed custom modes: %d", len(idx)))
		return nil

	case "set":
		if len(rest) != 1 {
			return fmt.Errorf("%s", T("give one mode: WxH@Hz or its number from crtctl kde list"))
		}
		target, err := findKDEMode(out, rest[0])
		if err != nil {
			return err
		}
		tm, _ := kdeTiming(t, *target)
		if limited {
			if ps := l.Check(tm); profile.HasHard(ps) && !force {
				return fmt.Errorf("%s", T("%s: %s (add --force if you are sure)", tm.Name(), ps[0].Text))
			}
		}
		var prev string
		for _, m := range out.Modes {
			if m.Current {
				prev = m.ID
			}
		}
		if prev == target.ID {
			fmt.Println(T("This mode is already active."))
			return nil
		}
		if err := system.KDESetMode(conn, target.ID); err != nil {
			return err
		}
		fmt.Println(T("Switched to %s", modeLine(tm)))
		if noRevert || prev == "" {
			return nil
		}
		if waitConfirm(revert) {
			fmt.Println("✓ " + T("Mode kept."))
			return nil
		}
		fmt.Println(T("No confirmation; switching back."))
		return system.KDESetMode(conn, prev)
	}
	return nil
}

func parseHz(s string) float64 {
	_, hz, _ := strings.Cut(s, "@")
	v, _ := strconv.ParseFloat(hz, 64)
	return v
}

func hasKDEMode(out *system.KDEOutput, w, h int, hz float64) bool {
	for _, m := range out.Modes {
		if m.W == w && m.H == h && math.Abs(m.Hz-hz) < 0.05 {
			return true
		}
	}
	return false
}

func findKDEMode(out *system.KDEOutput, s string) (*system.KDEMode, error) {
	for i := range out.Modes {
		if out.Modes[i].ID == s {
			return &out.Modes[i], nil
		}
	}
	r, err := timing.ParseRequest(s)
	if err != nil {
		return nil, fmt.Errorf("%s", T("%q is neither a mode number nor WxH@Hz", s))
	}
	var best *system.KDEMode
	for i := range out.Modes {
		m := &out.Modes[i]
		if m.W != r.W || m.H != r.H {
			continue
		}
		if r.Hz == 0 {
			if best == nil || m.Hz > best.Hz {
				best = m
			}
		} else if math.Abs(m.Hz-r.Hz) < 0.6 && (best == nil || math.Abs(m.Hz-r.Hz) < math.Abs(best.Hz-r.Hz)) {
			best = m
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%s", T("the output has no mode %s (add it: crtctl kde add %s %s)", s, out.Name, s))
	}
	return best, nil
}

// kdeTiming — полный тайминг режима KDE: из списка ядра, если он там есть, иначе CVT
// (так считает KWin). "≈CVT" — частоту восстановить точно не удалось.
func kdeTiming(t *target, m system.KDEMode) (timing.Mode, string) {
	if t.KMS != nil {
		for _, km := range t.KMS.Modes {
			if km.HActive == m.W && km.VActive == m.H && math.Abs(km.VFreq()-m.Hz) < 0.01 {
				return km, classify(km)
			}
		}
	}
	for _, hz := range []float64{math.Round(m.Hz), m.Hz, math.Round(m.Hz*10) / 10} {
		c := timing.CVT(m.W, m.H, hz, false)
		if math.Abs(c.VFreq()-m.Hz) < 0.01 {
			return c, "CVT"
		}
	}
	return timing.CVT(m.W, m.H, m.Hz, false), "≈CVT"
}
