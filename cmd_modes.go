package main

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/ImmortallKIng/crtctl/internal/profile"
	"github.com/ImmortallKIng/crtctl/internal/timing"
)

// Разрешения для таблицы: 4:3 и 5:4 — родные для CRT.
var crtResolutions = [][2]int{
	{640, 480}, {768, 576}, {800, 600}, {1024, 768}, {1152, 864}, {1280, 960}, {1280, 1024},
	{1400, 1050}, {1600, 1200}, {1792, 1344}, {1856, 1392}, {1920, 1440}, {2048, 1536},
}

var wideResolutions = [][2]int{
	{1280, 720}, {1280, 800}, {1440, 900}, {1680, 1050}, {1920, 1080}, {1920, 1200},
}

type kindFlag struct{ v timing.Kind }

func (k *kindFlag) String() string { return string(k.v) }
func (k *kindFlag) Set(s string) (err error) {
	k.v, err = timing.ParseKind(s)
	return
}

// limitsFor — пределы для коннектора (указанного или единственного подключённого)
// либо только из флагов, если коннектор не нужен.
func limitsFor(pos []string, lf *limitFlags, needConn bool) (*target, profile.Limits, []string, error) {
	conn, rest, err := pickConnector(pos)
	if err != nil {
		if needConn || !lf.any() {
			return nil, profile.Limits{}, nil, err
		}
		l := profile.NewLimits()
		if lf.profile != "" {
			p, err := profile.Get(lf.profile)
			if err != nil {
				return nil, l, nil, err
			}
			l.FromMonitor(p, T("profile %s", p.ID))
		}
		l.Override(lf.hmin, lf.hmax, lf.vmin, lf.vmax, lf.pclk)
		return nil, l, pos, nil
	}
	t, err := loadTarget(conn, lf)
	if err != nil {
		return nil, profile.Limits{}, nil, err
	}
	return t, t.Limits, rest, nil
}

// bindingReason — во что упирается режим, если поднять частоту ещё на шаг.
func bindingReason(l profile.Limits, w, h int, hz, step float64, k timing.Kind) string {
	m, err := timing.Calc(timing.Request{W: w, H: h, Hz: hz + step}, k)
	if err != nil {
		return ""
	}
	for _, p := range l.Check(m) {
		if !p.Hard {
			continue
		}
		switch p.Kind {
		case "hmax":
			return T("monitor horizontal")
		case "vmax":
			return T("monitor vertical")
		case "pclk":
			return T("adapter")
		}
	}
	return ""
}

// noLimitsError — понятное объяснение, почему таблицу не построить.
func noLimitsError(t *target) error {
	if t != nil {
		if m := t.Monitor(); m != nil && m.Digital {
			return fmt.Errorf("%s", T("%s is a digital input: CRT scan limits do not apply to it", t.Conn.Name))
		}
		if t.Unverified {
			return fmt.Errorf("%s", t.unverifiedHint())
		}
	}
	return fmt.Errorf("%s", T("the monitor limits are unknown: use --profile or --hmax/--vmax"))
}

func cmdModes(args []string) error {
	var lf limitFlags
	k := kindFlag{timing.KindAuto}
	var wide, kernel bool
	var step float64
	fs := newFlags("modes", "[CONNECTOR]", `For every resolution finds the highest refresh rate that both the monitor and the
adapter can handle, and what limits it. Timing "auto" uses the VESA DMT standard mode
when one exists, otherwise CVT. --kernel checks the modes the kernel offers right now.`)
	lf.register(fs)
	fs.Var(&k, "kind", "timing formula: auto, cvt, gtf, dmt")
	fs.BoolVar(&wide, "wide", false, "also show widescreen resolutions")
	fs.BoolVar(&kernel, "kernel", false, "check the modes the kernel offers right now")
	fs.Float64Var(&step, "step", 1, "refresh rate search step, Hz")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if step <= 0 {
		step = 1
	}
	t, l, _, err := limitsFor(pos, &lf, kernel)
	if err != nil {
		return err
	}
	if t != nil {
		fmt.Printf("%s — %s\n", t.Conn.Name, t.monitorTitle())
	}
	if !l.Known() {
		return noLimitsError(t)
	}
	fmt.Println(T("Limits: %s", l.String()))
	if t != nil && t.Conn.IsDP() && l.PclkMax == 0 {
		fmt.Println("⚠ " + T("The DisplayPort adapter limit is unknown, so the table may promise too much. Run once: sudo crtctl info %s (it is remembered), or use --pclk.", t.Conn.Name))
	}
	fmt.Println()

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if kernel {
		if t.KMS == nil {
			return fmt.Errorf("%s", T("could not read the modes from the kernel (KMS)"))
		}
		fmt.Fprintf(tw, "\t%s\t%s\t%s\t%s\t%s\t%s\n", T("MODE"), T("HZ"), T("KHZ"), T("MHZ"), T("TYPE"), T("CHECK"))
		for i, m := range t.KMS.Modes {
			mark := ""
			if t.KMS.Current != nil && sameTiming(*t.KMS.Current, m) {
				mark = "*"
			}
			if i == t.KMS.Preferred {
				mark += "!"
			}
			fmt.Fprintf(tw, "%s\t%dx%d\t%.2f\t%.2f\t%.2f\t%s\t%s\n", mark, m.HActive, m.VActive, m.VFreq(), m.HFreq(), m.PixelClockMHz(), classify(m), verdict(l, m))
		}
		tw.Flush()
		fmt.Println()
		fmt.Println(T("* current, ! preferred"))
		return nil
	}

	res := append([][2]int{}, crtResolutions...)
	if wide {
		res = append(res, wideResolutions...)
	}
	fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", T("RESOLUTION"), T("MAX HZ"), T("KHZ"), T("MHZ"), T("TYPE"), T("LIMITED BY"), T("NOTES"))
	for _, r := range res {
		m, ok := l.MaxRefresh(r[0], r[1], k.v, step)
		if !ok {
			fmt.Fprintf(tw, "%dx%d\t—\t\t\t\t\t%s\n", r[0], r[1], T("does not fit at any refresh rate"))
			continue
		}
		note := ""
		if ps := l.Check(m); len(ps) > 0 {
			note = shortSoft(ps)
		} else if m.VFreq() < 59.5 {
			note = T("below 60 Hz: visible flicker")
		}
		fmt.Fprintf(tw, "%dx%d\t%.2f\t%.2f\t%.2f\t%s\t%s\t%s\n", r[0], r[1], m.VFreq(), m.HFreq(), m.PixelClockMHz(), m.Source,
			bindingReason(l, r[0], r[1], m.VFreq(), step, k.v), note)
	}
	tw.Flush()
	fmt.Println()
	fmt.Println(T("Build an EDID with these modes: crtctl edid build %s --best", connOr(t, T("CONNECTOR"))))
	return nil
}

func connOr(t *target, def string) string {
	if t != nil {
		return t.Conn.Name
	}
	return def
}

func cmdCalc(args []string) error {
	var lf limitFlags
	k := kindFlag{timing.KindAuto}
	var all bool
	fs := newFlags("calc", "[CONNECTOR] MODE...  (e.g. 1280x1024@85)", `Computes the exact timings of a mode and checks them against the monitor limits
(when a connector, a profile or limits are given). --all shows DMT, CVT and GTF side by side.`)
	lf.register(fs)
	fs.Var(&k, "kind", "timing formula: auto, cvt, cvt-rb, gtf, dmt")
	fs.BoolVar(&all, "all", false, "show every timing formula")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	var l profile.Limits
	modes := pos
	if (len(pos) > 0 && !looksLikeMode(pos[0])) || lf.any() {
		var t *target
		t, l, modes, err = limitsFor(pos, &lf, false)
		if err != nil {
			return err
		}
		switch {
		case t != nil && l.Known():
			fmt.Printf("%s: %s\n\n", t.Conn.Name, l.String())
		case t != nil:
			fmt.Printf("%s: %s\n\n", t.Conn.Name, noLimitsError(t))
		default:
			fmt.Println(T("Limits: %s", l.String()))
			fmt.Println()
		}
	}
	if len(modes) == 0 {
		fs.Usage()
		return fmt.Errorf("%s", T("no mode given"))
	}
	kinds := []timing.Kind{k.v}
	if all {
		kinds = []timing.Kind{timing.KindDMT, timing.KindCVT, timing.KindGTF, timing.KindCVTR}
	}
	for _, s := range modes {
		r, err := timing.ParseRequest(s)
		if err != nil {
			return err
		}
		shown := 0
		for _, kd := range kinds {
			m, err := timing.Calc(r, kd)
			if err != nil {
				if all {
					continue
				}
				return err
			}
			shown++
			fmt.Printf("%s  [%s]\n  %s\n", modeLine(m), m.Source, m.Modeline())
			fmt.Println("  " + T("horizontal: active %d, front porch %d, sync %d, back porch %d (%s)   vertical: active %d, front porch %d, sync %d, back porch %d (%s)",
				m.HActive, m.HSyncStart-m.HActive, m.HSyncEnd-m.HSyncStart, m.HTotal-m.HSyncEnd, pol(m.HSyncPos),
				m.VActive, m.VSyncStart-m.VActive, m.VSyncEnd-m.VSyncStart, m.VTotal-m.VSyncEnd, pol(m.VSyncPos)))
			if l.Known() || l.PclkMax > 0 {
				ps := l.Check(m)
				sort.SliceStable(ps, func(i, j int) bool { return ps[i].Hard && !ps[j].Hard })
				if len(ps) == 0 {
					fmt.Println("  ✓ " + T("within limits"))
				}
				for _, p := range ps {
					mark := "⚠"
					if p.Hard {
						mark = "✗"
					}
					fmt.Printf("  %s %s\n", mark, p.Text)
				}
			}
			fmt.Println()
		}
		if shown == 0 {
			return fmt.Errorf("%s", T("%s: no timing formula produced this mode", s))
		}
	}
	return nil
}

func pol(p bool) string {
	if p {
		return "+"
	}
	return "−"
}
