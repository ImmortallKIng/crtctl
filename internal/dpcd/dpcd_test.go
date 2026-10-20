package dpcd

import (
	"math"
	"testing"
)

// Реальный дамп безымянного переходника DP→VGA с RX 9070 XT.
func TestDecodeNoNameVGA(t *testing.T) {
	r0 := []byte{0x11, 0x0a, 0x82, 0x41, 0x00, 0x03, 0x01, 0x01, 0, 0, 0, 0, 0, 0, 0, 0}
	r80 := []byte{0x09, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	r500 := make([]byte, 16)
	in := Decode(r0, r80, r500)
	if in.Rev != "1.1" || in.Lanes != 2 || in.LinkRateCode != 0x0a || !in.Branch || in.PortType != "VGA" || in.DetailedCaps {
		t.Fatalf("%+v", in)
	}
	// 2 × 2.7 Гбит/с × 0.8 / 24 = 180 МГц, минус 0.5% downspread (0x003 = 0x41)
	if !in.Downspread || math.Abs(in.HardLimitMHz()-179.1) > 0.01 || in.OUI != "" || in.MaxPclkMHz != 0 {
		t.Fatalf("limit=%v oui=%q max=%v", in.HardLimitMHz(), in.OUI, in.MaxPclkMHz)
	}
}

func TestDecodeDetailedVGA(t *testing.T) {
	r0 := []byte{0x12, 0x14, 0x84, 0, 0, 0x13, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	r80 := []byte{0x09, 0x1e, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0} // 30*8 = 240 МГц
	r500 := []byte{0x00, 0x1c, 0xf8, 'R', 'T', 'D', '2', '1', '6', 0x10, 1, 2, 0, 0, 0, 0}
	in := Decode(r0, r80, r500)
	if in.MaxPclkMHz != 240 || in.HardLimitMHz() != 240 || in.OUI != "00-1c-f8" || in.DeviceID != "RTD216" || in.FWRev != "1.2" {
		t.Fatalf("%+v", in)
	}
}

func TestHexRoundTrip(t *testing.T) {
	r0 := []byte{0x11, 0x0a, 0x82, 0x41, 0x00, 0x03, 0x01, 0x01, 0, 0, 0, 0, 0, 0, 0, 0}
	in := Decode(r0, make([]byte, 16), make([]byte, 16))
	back, err := DecodeHex(in.Hex())
	if err != nil || back.HardLimitMHz() != in.HardLimitMHz() || back.Lanes != 2 {
		t.Fatalf("%v %+v", err, back)
	}
}
