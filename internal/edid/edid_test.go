package edid

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ImmortallKIng/crtctl/internal/timing"
)

func load(t *testing.T, name string) *EDID {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	e, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Warnings) > 0 {
		t.Errorf("%s: предупреждения %v", name, e.Warnings)
	}
	return e
}

func names(ms []timing.Mode) string {
	var s []string
	for _, m := range ms {
		s = append(s, m.Name())
	}
	return strings.Join(s, " ")
}

func TestParseHP(t *testing.T) {
	e := load(t, "hp_ergo1600_orig.bin")
	if e.Vendor != "HWP" || e.Product != 2845 || e.Year != 1997 || e.Digital {
		t.Fatalf("идентификация: %+v", e)
	}
	want := "1600x1200@75.00 1600x1200@60.00 1280x1024@85.02 1024x768@85.00"
	if got := names(e.Detailed); got != want {
		t.Errorf("DTD:\n got %s\nwant %s", got, want)
	}
	if len(e.Standard) != 7 || len(e.Established) != 7 {
		t.Errorf("standard=%d established=%d", len(e.Standard), len(e.Established))
	}
	if mx := e.MaxHFreq(); mx < 93.7 || mx > 93.8 {
		t.Errorf("MaxHFreq = %v", mx)
	}
}

func TestParseExtensionDTDs(t *testing.T) {
	e := load(t, "hp_ergo1600_v2.bin")
	if len(e.Detailed) != 10 || e.Extensions != 1 {
		t.Fatalf("ожидалось 10 DTD в 2 блоках, got %d (%s)", len(e.Detailed), names(e.Detailed))
	}
	tv := load(t, "macrosilicon.bin")
	if tv.Name != "MACROSILICON" || !tv.HasAudio || !tv.Digital {
		t.Errorf("TV: name=%q audio=%v digital=%v", tv.Name, tv.HasAudio, tv.Digital)
	}
}

func TestBuildRoundTrip(t *testing.T) {
	base := load(t, "hp_ergo1600_orig.bin")
	var modes []timing.Mode
	for _, r := range []string{"1280x1024@85", "1280x960@85", "1600x1200@65", "1024x768@110", "800x600@140", "640x480@85", "1152x864@100", "1280x960@90"} {
		req, _ := timing.ParseRequest(r)
		m, err := timing.Calc(req, timing.KindAuto)
		if err != nil {
			t.Fatal(err)
		}
		modes = append(modes, m)
	}
	extra := []timing.Mode{}
	for _, r := range []string{"640x480@60", "800x600@60", "1024x768@75", "1280x1024@60", "1600x1200@60"} {
		req, _ := timing.ParseRequest(r)
		m, _ := timing.Calc(req, timing.KindDMT)
		extra = append(extra, m)
	}
	rl := &RangeLimits{VMin: 50, VMax: 160, HMin: 30, HMax: 94, PclkMax: 180}
	raw, err := Build(BuildOptions{Base: base, Name: "HP Ergo 1600", Range: rl, Modes: modes, Extra: extra})
	if err != nil {
		t.Fatal(err)
	}
	e, err := Parse(raw)
	if err != nil || len(e.Warnings) > 0 {
		t.Fatalf("%v %v", err, e.Warnings)
	}
	if e.ID() != base.ID() || e.Name != "HP Ergo 1600" || *e.Range != *rl || !e.PreferredDTD {
		t.Errorf("id=%s name=%q range=%+v pref=%v", e.ID(), e.Name, e.Range, e.PreferredDTD)
	}
	if len(e.Detailed) != len(modes) {
		t.Fatalf("DTD %d != %d", len(e.Detailed), len(modes))
	}
	for i := range modes {
		if !e.Detailed[i].Same(timing.Mode{Source: "DTD", PixelClock: modes[i].PixelClock, HActive: modes[i].HActive,
			HSyncStart: modes[i].HSyncStart, HSyncEnd: modes[i].HSyncEnd, HTotal: modes[i].HTotal, VActive: modes[i].VActive,
			VSyncStart: modes[i].VSyncStart, VSyncEnd: modes[i].VSyncEnd, VTotal: modes[i].VTotal,
			HSyncPos: modes[i].HSyncPos, VSyncPos: modes[i].VSyncPos}) {
			t.Errorf("DTD %d: %+v != %+v", i, e.Detailed[i], modes[i])
		}
	}
	if got := names(e.Established) + " | " + names(e.Standard); got != "640x480@59.94 800x600@60.32 1024x768@75.03 | 1280x1024@60.02 1600x1200@60.00" {
		t.Errorf("extra: %s", got)
	}

	// Сверка с edid-decode: структура и контрольные суммы
	if _, err := exec.LookPath("edid-decode"); err != nil {
		t.Skip("edid-decode не установлен")
	}
	f := filepath.Join(t.TempDir(), "x.bin")
	os.WriteFile(f, raw, 0o644)
	out, _ := exec.Command("edid-decode", f).CombinedOutput()
	s := string(out)
	for _, want := range []string{"Manufacturer: HWP", "Display Product Name: 'HP Ergo 1600'", "Range Limits Only): 50-160 Hz V, 30-94 kHz H, max dotclock 180 MHz",
		"DTD 8:  1280x960    89.91", "Extension blocks: 1"} {
		if !strings.Contains(s, want) {
			t.Errorf("edid-decode не нашёл %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Checksum: 0x") && strings.Contains(strings.ToLower(s), "checksum is invalid") {
		t.Error("edid-decode: неверная контрольная сумма")
	}
}

func TestBuildTooMany(t *testing.T) {
	m := timing.CVT(640, 480, 60, false)
	ms := make([]timing.Mode, Capacity(true, true)+1)
	for i := range ms {
		ms[i] = m
	}
	if _, err := Build(BuildOptions{Name: "x", Range: &RangeLimits{}, Modes: ms}); err == nil {
		t.Fatal("ожидалась ошибка переполнения")
	}
}
