package timing

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func modelineTail(m Mode) string {
	return " " + strings.SplitN(m.Modeline(), "\" ", 2)[1]
}

func TestCVTMatchesLibxcvt(t *testing.T) {
	for _, g := range cvtGolden {
		m := CVT(g.w, g.h, g.hz, g.rb)
		if got := modelineTail(m); got != g.line {
			t.Errorf("CVT %dx%d@%v rb=%v:\n got %s\nwant %s", g.w, g.h, g.hz, g.rb, got, g.line)
		}
	}
}

func TestGTFMatchesEdidDecode(t *testing.T) {
	for _, g := range gtfGolden {
		m := GTF(g.w, g.h, g.hz)
		got := []int{m.HSyncStart - m.HActive, m.HSyncEnd - m.HSyncStart, m.HTotal - m.HSyncEnd,
			m.VSyncStart - m.VActive, m.VSyncEnd - m.VSyncStart, m.VTotal - m.VSyncEnd}
		want := []int{g.hf, g.hs, g.hb, g.vf, g.vs, g.vb}
		if fmt.Sprint(got) != fmt.Sprint(want) || abs(m.PixelClock-g.clk) > 1 {
			t.Errorf("GTF %dx%d@%v: got clk=%d %v, want clk=%d %v", g.w, g.h, g.hz, m.PixelClock, got, g.clk, want)
		}
	}
}

func TestDMTLookup(t *testing.T) {
	m, ok := FindDMT(1280, 1024, 85)
	if !ok || m.PixelClock != 157500 || math.Abs(m.HFreq()-91.146) > 0.001 {
		t.Fatalf("DMT 1280x1024@85: %+v ok=%v", m, ok)
	}
	if _, ok := FindDMT(1280, 1024, 87); ok {
		t.Fatal("1280x1024@87 не должен находиться в DMT")
	}
}

func TestParseRequest(t *testing.T) {
	r, err := ParseRequest("1280x1024@85.5")
	if err != nil || r.W != 1280 || r.H != 1024 || r.Hz != 85.5 {
		t.Fatalf("got %+v %v", r, err)
	}
	for _, bad := range []string{"1280", "x1024@85", "1280x1024@abc", "0x10@60"} {
		if _, err := ParseRequest(bad); err == nil {
			t.Errorf("%q должен давать ошибку", bad)
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestParseModeline(t *testing.T) {
	m, err := ParseModeline(`Modeline "1280x1024_85.00"  159.50  1280 1376 1512 1744  1024 1027 1034 1078 -hsync +vsync`)
	if err != nil || !m.Same(CVT(1280, 1024, 85, false)) {
		t.Fatalf("%v %+v", err, m)
	}
	m2, err := ParseModeline(m.Modeline())
	if err != nil || !m2.Same(m) {
		t.Fatalf("round trip: %v", err)
	}
	if _, err := ParseModeline("100 1 2 3"); err == nil {
		t.Fatal("short modeline must fail")
	}
	if _, err := ParseModeline("100 640 600 700 800 480 490 492 525"); err == nil {
		t.Fatal("non-increasing timings must fail")
	}
	if a := CVT(1000, 500, 75, false).Aspect(); a != "2:1" {
		t.Fatal(a)
	}
}
