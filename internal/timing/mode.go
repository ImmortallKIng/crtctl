// Package timing описывает видеорежимы и считает их тайминги (DMT, CVT, GTF).
package timing

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
)

// Mode — полный тайминг видеорежима. PixelClock в кГц, как в DRM.
type Mode struct {
	Source     string // "DMT 0x25", "CVT", "GTF", "EDID DTD", "KMS"...
	PixelClock int
	HActive    int
	HSyncStart int
	HSyncEnd   int
	HTotal     int
	VActive    int
	VSyncStart int
	VSyncEnd   int
	VTotal     int
	HSyncPos   bool
	VSyncPos   bool
	Interlaced bool
	Reduced    bool // reduced blanking (для CRT не годится)
}

// HFreq — строчная частота в кГц.
func (m Mode) HFreq() float64 {
	if m.HTotal == 0 {
		return 0
	}
	return float64(m.PixelClock) / float64(m.HTotal)
}

// VFreq — кадровая частота в Гц.
func (m Mode) VFreq() float64 {
	if m.VTotal == 0 {
		return 0
	}
	return m.HFreq() * 1000 / float64(m.VTotal)
}

// PixelClockMHz — частота пикселей в МГц.
func (m Mode) PixelClockMHz() float64 { return float64(m.PixelClock) / 1000 }

func (m Mode) Name() string {
	return fmt.Sprintf("%dx%d@%.2f", m.HActive, m.VActive, m.VFreq())
}

func pol(p bool) string {
	if p {
		return "+"
	}
	return "-"
}

// Modeline — строка в формате xrandr/xorg.
func (m Mode) Modeline() string {
	return fmt.Sprintf("Modeline \"%dx%d_%.2f\" %.2f %d %d %d %d %d %d %d %d %shsync %svsync",
		m.HActive, m.VActive, m.VFreq(), m.PixelClockMHz(),
		m.HActive, m.HSyncStart, m.HSyncEnd, m.HTotal,
		m.VActive, m.VSyncStart, m.VSyncEnd, m.VTotal,
		pol(m.HSyncPos), pol(m.VSyncPos))
}

// Same — совпадают ли тайминги (без учёта источника).
func (m Mode) Same(o Mode) bool {
	a, b := m, o
	a.Source, b.Source = "", ""
	return a == b
}

// Request — запрос режима: ширина, высота, частота.
type Request struct {
	W, H int
	Hz   float64
}

func (r Request) String() string {
	return fmt.Sprintf("%dx%d@%s", r.W, r.H, strconv.FormatFloat(r.Hz, 'f', -1, 64))
}

// ParseRequest разбирает "1280x1024@85" или "1280x1024" (тогда Hz = 0).
func ParseRequest(s string) (Request, error) {
	var r Request
	res, hz, hasHz := strings.Cut(s, "@")
	w, h, ok := strings.Cut(res, "x")
	if !ok {
		return r, i18n.Errorf("expected WxH[@Hz], got %q", s)
	}
	var err error
	if r.W, err = strconv.Atoi(w); err != nil {
		return r, i18n.Errorf("width in %q: %w", s, err)
	}
	if r.H, err = strconv.Atoi(h); err != nil {
		return r, i18n.Errorf("height in %q: %w", s, err)
	}
	if hasHz {
		if r.Hz, err = strconv.ParseFloat(hz, 64); err != nil {
			return r, i18n.Errorf("refresh rate in %q: %w", s, err)
		}
	}
	if r.W <= 0 || r.H <= 0 || r.Hz < 0 {
		return r, i18n.Errorf("invalid mode %q", s)
	}
	return r, nil
}

// Kind — способ расчёта тайминга.
type Kind string

const (
	KindAuto Kind = "auto" // DMT, если есть точное совпадение, иначе CVT
	KindDMT  Kind = "dmt"
	KindCVT  Kind = "cvt"
	KindCVTR Kind = "cvt-rb"
	KindGTF  Kind = "gtf"
)

func ParseKind(s string) (Kind, error) {
	switch k := Kind(strings.ToLower(s)); k {
	case KindAuto, KindDMT, KindCVT, KindCVTR, KindGTF:
		return k, nil
	}
	return "", i18n.Errorf("unknown timing type %q (auto, dmt, cvt, cvt-rb, gtf)", s)
}

// Calc считает режим заданным способом.
func Calc(r Request, k Kind) (Mode, error) {
	if r.Hz <= 0 {
		return Mode{}, i18n.Errorf("%s: refresh rate is missing", r)
	}
	switch k {
	case KindDMT:
		if m, ok := FindDMT(r.W, r.H, r.Hz); ok {
			return m, nil
		}
		return Mode{}, i18n.Errorf("%s: no such mode in the VESA DMT standard", r)
	case KindAuto:
		if m, ok := FindDMT(r.W, r.H, r.Hz); ok {
			return m, nil
		}
		return CVT(r.W, r.H, r.Hz, false), nil
	case KindCVT:
		return CVT(r.W, r.H, r.Hz, false), nil
	case KindCVTR:
		return CVT(r.W, r.H, r.Hz, true), nil
	case KindGTF:
		return GTF(r.W, r.H, r.Hz), nil
	}
	return Mode{}, i18n.Errorf("unknown timing type %q", k)
}

// FindDMT ищет DMT-режим с нормальным гашением, частота которого отличается не больше чем на 0.6 Гц.
func FindDMT(w, h int, hz float64) (Mode, bool) {
	for _, m := range dmtTable {
		if m.HActive == w && m.VActive == h && !m.Reduced && math.Abs(m.VFreq()-hz) < 0.6 {
			return m, true
		}
	}
	return Mode{}, false
}

// DMTModes — все DMT-режимы с нормальным гашением, отсортированные по разрешению и частоте.
func DMTModes() []Mode {
	var out []Mode
	for _, m := range dmtTable {
		if !m.Reduced {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.HActive != b.HActive {
			return a.HActive < b.HActive
		}
		if a.VActive != b.VActive {
			return a.VActive < b.VActive
		}
		return a.VFreq() < b.VFreq()
	})
	return out
}

// ParseModeline разбирает modeline в формате xorg/xrandr:
// [Modeline] ["имя"] clock hdisp hss hse htotal vdisp vss vse vtotal [+-hsync] [+-vsync].
func ParseModeline(s string) (Mode, error) {
	f := strings.Fields(s)
	if len(f) > 0 && strings.EqualFold(f[0], "modeline") {
		f = f[1:]
	}
	if len(f) > 0 && strings.HasPrefix(f[0], `"`) {
		// имя в кавычках может содержать пробелы
		i := 0
		for ; i < len(f); i++ {
			if strings.HasSuffix(f[i], `"`) && (i > 0 || len(f[i]) > 1) {
				break
			}
		}
		f = f[min(i+1, len(f)):]
	}
	if len(f) < 9 {
		return Mode{}, i18n.Errorf("a modeline needs a clock and 8 timing numbers: %q", s)
	}
	clk, err := strconv.ParseFloat(f[0], 64)
	if err != nil || clk <= 0 {
		return Mode{}, i18n.Errorf("bad pixel clock in the modeline: %q", f[0])
	}
	var n [8]int
	for i := range n {
		if n[i], err = strconv.Atoi(f[1+i]); err != nil {
			return Mode{}, i18n.Errorf("bad number in the modeline: %q", f[1+i])
		}
	}
	m := Mode{Source: "modeline", PixelClock: int(math.Round(clk * 1000)),
		HActive: n[0], HSyncStart: n[1], HSyncEnd: n[2], HTotal: n[3],
		VActive: n[4], VSyncStart: n[5], VSyncEnd: n[6], VTotal: n[7]}
	if !(m.HActive < m.HSyncStart && m.HSyncStart < m.HSyncEnd && m.HSyncEnd <= m.HTotal &&
		m.VActive < m.VSyncStart && m.VSyncStart < m.VSyncEnd && m.VSyncEnd <= m.VTotal) {
		return Mode{}, i18n.Errorf("the modeline timings are not in increasing order: %q", s)
	}
	for _, fl := range f[9:] {
		switch strings.ToLower(fl) {
		case "+hsync":
			m.HSyncPos = true
		case "+vsync":
			m.VSyncPos = true
		case "interlace":
			m.Interlaced = true
		}
	}
	return m, nil
}

// XrandrArgs — тайминги в виде аргументов xrandr --newmode / sway modeline (без имени).
func (m Mode) XrandrArgs() []string {
	return []string{fmt.Sprintf("%.3f", m.PixelClockMHz()),
		strconv.Itoa(m.HActive), strconv.Itoa(m.HSyncStart), strconv.Itoa(m.HSyncEnd), strconv.Itoa(m.HTotal),
		strconv.Itoa(m.VActive), strconv.Itoa(m.VSyncStart), strconv.Itoa(m.VSyncEnd), strconv.Itoa(m.VTotal),
		pol(m.HSyncPos) + "hsync", pol(m.VSyncPos) + "vsync"}
}

// Aspect — соотношение сторон в виде «4:3», «5:4» или десятичной дроби.
func (m Mode) Aspect() string {
	g := gcd(m.HActive, m.VActive)
	if g == 0 {
		return ""
	}
	a, b := m.HActive/g, m.VActive/g
	if a <= 32 && b <= 32 {
		return fmt.Sprintf("%d:%d", a, b)
	}
	return fmt.Sprintf("%.2f:1", float64(m.HActive)/float64(m.VActive))
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
