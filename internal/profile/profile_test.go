package profile

import (
	"testing"

	"github.com/ImmortallKIng/crtctl/internal/timing"
)

func TestBuiltinProfiles(t *testing.T) {
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 5 {
		t.Fatalf("профилей %d", len(all))
	}
	for _, m := range all {
		if m.ID == "" || m.Name == "" || m.HMax <= m.HMin || m.VMax <= m.VMin {
			t.Errorf("кривой профиль %+v", m)
		}
	}
	if m, ok := Match("HWP", 2845); !ok || m.ID != "hp-ergo-1600" {
		t.Errorf("Match HP: %v %v", m.ID, ok)
	}
}

func TestLimitsHPThroughAdapter(t *testing.T) {
	hp, _ := Get("hp-ergo-1600")
	l := NewLimits()
	l.FromMonitor(hp, "профиль")
	l.SetPclk(180, "переходник")

	m, ok := l.MaxRefresh(1280, 1024, timing.KindAuto, 1)
	if !ok || int(m.VFreq()+0.5) != 87 {
		t.Errorf("1280x1024: %s ok=%v", m.Name(), ok)
	}
	m, ok = l.MaxRefresh(1600, 1200, timing.KindAuto, 1)
	if !ok || m.PixelClockMHz() > 180 || m.VFreq() < 65 {
		t.Errorf("1600x1200: %s %.1f МГц ok=%v", m.Name(), m.PixelClockMHz(), ok)
	}
	dmt, _ := timing.FindDMT(1600, 1200, 75)
	if !HasHard(l.Check(dmt)) {
		t.Error("1600x1200@75 должен упираться в переходник")
	}
}

func TestLimitsF700B(t *testing.T) {
	lg, _ := Get("lg-flatron-f700b")
	l := NewLimits()
	l.FromMonitor(lg, "профиль")
	dmt, _ := timing.FindDMT(1280, 1024, 85)
	if !HasHard(l.Check(dmt)) {
		t.Error("1280x1024@85 на F700B должен быть запрещён")
	}
	m, ok := l.MaxRefresh(1280, 1024, timing.KindCVT, 1)
	if !ok || m.HFreq() > 70 || m.VFreq() < 63 {
		t.Errorf("F700B 1280x1024: %s %.2f кГц", m.Name(), m.HFreq())
	}
	m, _ = l.MaxRefresh(1024, 768, timing.KindAuto, 1)
	if ps := l.Check(m); HasHard(ps) {
		t.Errorf("%s: %v", m.Name(), ps)
	}
}
