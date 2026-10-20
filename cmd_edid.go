package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/ddc"
	"github.com/ImmortallKIng/crtctl/internal/drm"
	"github.com/ImmortallKIng/crtctl/internal/edid"
	"github.com/ImmortallKIng/crtctl/internal/profile"
	"github.com/ImmortallKIng/crtctl/internal/timing"
)

func cmdEDID(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println(T("Usage: crtctl edid read|show|build ..."))
		fmt.Println()
		fmt.Println("  read  " + T("read the EDID straight from the monitor over DDC"))
		fmt.Println("  show  " + T("decode an EDID file, or the EDID the kernel uses for a connector"))
		fmt.Println("  build " + T("create an EDID with the modes you want"))
		fmt.Println()
		fmt.Println(T("Details: crtctl edid build -h"))
		if len(args) == 0 {
			return fmt.Errorf("%s", T("no subcommand given"))
		}
		return nil
	}
	switch args[0] {
	case "read":
		return edidRead(args[1:])
	case "show":
		return edidShow(args[1:])
	case "build":
		return edidBuild(args[1:])
	}
	return fmt.Errorf("%s", T("unknown subcommand edid %q: read, show, build", args[0]))
}

func edidRead(args []string) error {
	var out string
	fs := newFlags("edid read", "[CONNECTOR]", "Reads the EDID straight from the monitor over DDC, bypassing the kernel.")
	fs.StringVar(&out, "o", "", "output file (default: edid-CONNECTOR.bin)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	conn, _, err := pickConnector(pos)
	if err != nil {
		return err
	}
	c, err := drm.Find(conn)
	if err != nil {
		return err
	}
	if c.DDCBus < 0 {
		return fmt.Errorf("%s", T("%s: no DDC bus found", conn))
	}
	raw, err := ddc.ReadEDID(c.DDCBus)
	if err != nil {
		return err
	}
	if out == "" {
		out = "edid-" + conn + ".bin"
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		return err
	}
	e, err := edid.Parse(raw)
	if err != nil {
		return err
	}
	fmt.Println(T("Read %d bytes from /dev/i2c-%d → %s", len(raw), c.DDCBus, out))
	fmt.Println()
	printEDID(e, nil)
	return nil
}

func edidShow(args []string) error {
	var lf limitFlags
	fs := newFlags("edid show", "FILE|CONNECTOR", "Decodes an EDID file, or the EDID the kernel uses for a connector.\nFor every single field use: edid-decode FILE")
	lf.register(fs)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return fmt.Errorf("%s", T("give a file or a connector"))
	}
	raw, err := os.ReadFile(pos[0])
	if err != nil {
		c, err2 := drm.Find(pos[0])
		if err2 != nil {
			return fmt.Errorf("%s", T("%q is neither a readable file nor a connector", pos[0]))
		}
		if len(c.EDID) == 0 {
			return fmt.Errorf("%s", T("%s: the kernel has no EDID", c.Name))
		}
		raw = c.EDID
	}
	e, err := edid.Parse(raw)
	if err != nil {
		return err
	}
	var l *profile.Limits
	if lf.any() {
		x := profile.NewLimits()
		if lf.profile != "" {
			p, err := profile.Get(lf.profile)
			if err != nil {
				return err
			}
			x.FromMonitor(p, T("profile %s", p.ID))
		}
		x.Override(lf.hmin, lf.hmax, lf.vmin, lf.vmax, lf.pclk)
		l = &x
	}
	printEDID(e, l)
	return nil
}

func printEDID(e *edid.EDID, l *profile.Limits) {
	fmt.Printf("%-11s %s\n", T("Monitor:"), edidLine(e))
	fmt.Printf("%-11s %s\n", T("ID:"), T("%s, EDID %s, extension blocks: %d", e.ID(), e.Version, e.Extensions))
	if e.Range != nil {
		r := e.Range
		fmt.Printf("%-11s %s\n", T("Ranges:"), T("horizontal %d–%d kHz, vertical %d–%d Hz, up to %d MHz", r.HMin, r.HMax, r.VMin, r.VMax, r.PclkMax))
	}
	if e.HasAudio {
		fmt.Printf("%-11s %s\n", T("Audio:"), T("declared"))
	}
	for _, w := range e.Warnings {
		fmt.Println("⚠", w)
	}
	fmt.Println()
	for i, m := range e.Modes() {
		mark := " "
		if i == 0 && e.PreferredDTD && len(e.Detailed) > 0 {
			mark = "!"
		}
		v := ""
		if l != nil {
			v = verdict(*l, m)
		}
		src := m.Source
		if src == "DTD" {
			src = "DTD " + classify(m)
		}
		fmt.Printf(" %s %s  %-14s %s\n", mark, modeLine(m), src, v)
	}
	fmt.Println()
	fmt.Println(T("! preferred mode"))
}

// asciiName — имя для EDID: только печатный ASCII, до 13 символов.
func asciiName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		}
	}
	n := strings.TrimSpace(b.String())
	if len(n) > 13 {
		n = strings.TrimSpace(n[:13])
	}
	return n
}

func edidBuild(args []string) error {
	var lf limitFlags
	k := kindFlag{timing.KindAuto}
	var out, name, base string
	var best, force, noRange bool
	fs := newFlags("edid build", "[CONNECTOR] [MODE...]", `Creates an EDID for a connector. The first mode becomes the preferred one.
The monitor identity (vendor, model, serial number) is copied from its real EDID,
so KDE and other programs keep recognising it. Every mode is checked against the
limits; modes outside them are rejected (--force builds anyway).

  --best   add the highest safe refresh rate for every 4:3/5:4 resolution
           (the same as crtctl modes; rates below 60 Hz are skipped)

Example: crtctl edid build DP-2 1280x1024@85 --best`)
	lf.register(fs)
	fs.Var(&k, "kind", "timing formula: auto (DMT, otherwise CVT), cvt, gtf")
	fs.StringVar(&out, "o", "", "output file (default: crtctl-CONNECTOR.bin)")
	fs.StringVar(&name, "name", "", "monitor name stored in the EDID, up to 13 ASCII characters")
	fs.StringVar(&base, "base", "", "copy the monitor identity from this EDID file")
	fs.BoolVar(&best, "best", false, "add the best modes for standard resolutions")
	fs.BoolVar(&force, "force", false, "do not reject modes outside the limits")
	fs.BoolVar(&noRange, "no-range", false, "do not store the frequency ranges")
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
	l := t.Limits
	if !l.Known() && !force {
		return noLimitsError(t)
	}

	raw, err := buildEDIDFor(t, buildOpts{kind: k.v, requested: rest, best: best, force: force,
		noRange: noRange, name: name, baseFile: base, profileFlag: lf.profile})
	if err != nil {
		if errorsIsNoModes(err) {
			fs.Usage()
			return fmt.Errorf("%s", T("no modes given (list them, or use --best)"))
		}
		return err
	}
	if out == "" {
		out = "crtctl-" + conn + ".bin"
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		return err
	}
	e, _ := edid.Parse(raw)
	fmt.Println(T("EDID for %s written to %s (%d bytes)", conn, out, len(raw)))
	fmt.Println()
	printEDID(e, &l)
	fmt.Println()
	fmt.Println(T("Try it until the next reboot, then install it permanently:"))
	fmt.Printf("  sudo crtctl apply %s %s --test\n  sudo crtctl apply %s %s\n", conn, out, conn, out)
	return nil
}

var errNoModes = fmt.Errorf("%s", "no modes")

func errorsIsNoModes(err error) bool { return err == errNoModes }

// buildOpts — параметры сборки EDID (общие для edid build и setup).
type buildOpts struct {
	kind                 timing.Kind
	requested            []string
	best, force, noRange bool
	name, baseFile       string
	profileFlag          string
}

// buildEDIDFor собирает EDID для коннектора по его пределам.
func buildEDIDFor(t *target, o buildOpts) ([]byte, error) {
	l := t.Limits
	// Идентификация: только из надёжного источника
	var baseEDID *edid.EDID
	switch {
	case o.baseFile != "":
		raw, err := os.ReadFile(o.baseFile)
		if err != nil {
			return nil, err
		}
		if baseEDID, err = edid.Parse(raw); err != nil {
			return nil, err
		}
	case t.Monitor() != nil:
		baseEDID = t.Monitor()
	case t.Unverified && t.State != nil && t.State.MonitorNoDDC && o.profileFlag == t.State.Profile:
		baseEDID = t.KernelEDID // это наш прошлый EDID для того же монитора без DDC
	}

	var modes []timing.Mode
	addMode := func(m timing.Mode, why string) error {
		for _, o := range modes {
			if o.Same(m) {
				return nil
			}
		}
		if ps := l.Check(m); profile.HasHard(ps) {
			if !o.force {
				return fmt.Errorf("%s", T("%s (%s): %s; add --force if you are sure", m.Name(), why, ps[0].Text))
			}
			fmt.Println("⚠ " + T("%s is out of limits (%s), added because of --force", m.Name(), ps[0].Text))
		}
		modes = append(modes, m)
		return nil
	}
	for _, s := range o.requested {
		r, err := timing.ParseRequest(s)
		if err != nil {
			return nil, err
		}
		m, err := timing.Calc(r, o.kind)
		if err != nil {
			return nil, err
		}
		if err := addMode(m, T("requested")); err != nil {
			return nil, err
		}
	}
	if o.best {
		if len(modes) == 0 {
			var declared []timing.Mode
			if t.RealEDID != nil {
				declared = t.RealEDID.Modes()
			}
			if m, why, ok := pickPrimary(l, o.kind, declared); ok {
				if err := addMode(m, T("preferred")); err != nil {
					return nil, err
				}
				fmt.Println(T("Preferred mode: %s (%s)", m.Name(), why))
			}
		}
		for _, r := range crtResolutions {
			// Ниже 60 Гц CRT заметно мерцает — такие режимы не предлагаем
			if m, ok := l.MaxRefresh(r[0], r[1], o.kind, 1); ok && m.VFreq() >= 59.5 {
				if err := addMode(m, "--best"); err != nil {
					return nil, err
				}
			}
		}
	}
	if len(modes) == 0 {
		return nil, errNoModes
	}

	// Дешёвые режимы (Established/Standard): стандартные из EDID монитора и базовые DMT
	var extra []timing.Mode
	seen := func(m timing.Mode) bool {
		for _, o := range append(append([]timing.Mode{}, modes...), extra...) {
			if o.Same(m) {
				return true
			}
		}
		return false
	}
	var pool []timing.Mode
	if baseEDID != nil {
		pool = append(pool, baseEDID.Standard...)
		pool = append(pool, baseEDID.Established...)
	}
	for _, r := range []string{"640x480@60", "800x600@60", "1024x768@60", "800x600@75", "1024x768@75", "1280x1024@60"} {
		req, _ := timing.ParseRequest(r)
		if m, err := timing.Calc(req, timing.KindDMT); err == nil {
			pool = append(pool, m)
		}
	}
	for _, m := range pool {
		if !seen(m) && !profile.HasHard(l.Check(m)) {
			extra = append(extra, m)
		}
	}

	opts := edid.BuildOptions{Base: baseEDID, Modes: modes, Extra: extra}
	switch {
	case o.name != "":
		opts.Name = asciiName(o.name)
	case baseEDID != nil && baseEDID.Name != "":
		opts.Name = asciiName(baseEDID.Name)
	case t.Profile != nil:
		opts.Name = asciiName(strings.SplitN(t.Profile.Name, "(", 2)[0])
	}
	if l.Known() && !o.noRange {
		opts.Range = &edid.RangeLimits{VMin: int(l.VMin), VMax: int(l.VMax + 0.999), HMin: int(l.HMin), HMax: int(l.HMax + 0.999), PclkMax: int(l.PclkMax + 0.999)}
		if opts.Range.PclkMax == 0 {
			var mx float64
			for _, m := range modes {
				mx = max(mx, m.PixelClockMHz())
			}
			opts.Range.PclkMax = int(mx/10+1) * 10
		}
	}
	if c := edid.Capacity(opts.Name != "", opts.Range != nil); len(modes) > c {
		fmt.Println("⚠ " + T("%d modes requested, but only %d fit into an EDID; the last ones were dropped", len(modes), c))
		opts.Modes = modes[:c]
	}
	return edid.Build(opts)
}

// pickPrimary — основной режим. Сначала из заявленных самим монитором (за них ручается
// производитель): самый крупный с частотой от 85 Гц, проходящий пределы. Если таких нет —
// самое крупное разрешение, у которого 85 (75, 60) Гц проходит с запасом 3%.
func pickPrimary(l profile.Limits, k timing.Kind, declared []timing.Mode) (timing.Mode, string, bool) {
	var best *timing.Mode
	for i, m := range declared {
		if m.VFreq() < 84.5 || profile.HasHard(l.Check(m)) {
			continue
		}
		if best == nil || m.HActive*m.VActive > best.HActive*best.VActive ||
			(m.HActive*m.VActive == best.HActive*best.VActive && m.VFreq() > best.VFreq()) {
			best = &declared[i]
		}
	}
	if best != nil {
		return *best, T("declared by the monitor itself in its EDID"), true
	}
	const margin = 1.03
	tight := l
	if tight.HMax > 0 {
		tight.HMax /= margin
	}
	if tight.PclkMax > 0 {
		tight.PclkMax /= margin
	}
	for _, hz := range []float64{85, 75, 60} {
		for i := len(crtResolutions) - 1; i >= 0; i-- {
			r := crtResolutions[i]
			m, err := timing.Calc(timing.Request{W: r[0], H: r[1], Hz: hz}, k)
			if err == nil && len(tight.Check(m)) == 0 {
				return m, T("the largest one that fits with a 3 percent margin"), true
			}
		}
	}
	return timing.Mode{}, "", false
}
