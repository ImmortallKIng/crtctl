package display

import (
	"strings"
	"testing"

	"github.com/ImmortallKIng/crtctl/internal/timing"
)

// Фрагменты реального формата xrandr --verbose (modesetting/amdgpu и NVIDIA).
const xrandrAMD = `Screen 0: minimum 320 x 200, current 3840 x 1440, maximum 16384 x 16384
DisplayPort-0 connected primary 2560x1440+1280+0 (0x5c) normal (normal left inverted right x axis y axis) 597mm x 336mm
	Identifier: 0x56
	Timestamp:  12345
	EDID:
		00ffffffffffff0061a9764f00000000
		32230104b53c22783f2fa5ae4e45a227
	CONNECTOR_ID: 101
	supported: 101
  2560x1440 (0x5c) 645.000MHz +HSync -VSync *current +preferred
        h: width  2560 start 2568 end 2600 total 2640 skew    0 clock 244.32KHz
DisplayPort-1 connected 1280x1024+0+0 (0x4a) normal (normal left inverted right x axis y axis) 400mm x 300mm
	Identifier: 0x57
	EDID:
		00ffffffffffff0022f01d0bc24b3704
		0707010e2e281e9ae89d19a255479827
	CONNECTOR_ID: 458
  1280x1024 (0x4a) 157.500MHz +HSync +VSync *current +preferred
        h: width  1280 start 1344 end 1504 total 1728 skew    0 clock  91.15KHz
  1024x768 (0x4b) 94.500MHz +HSync +VSync
HDMI-A-0 disconnected (normal left inverted right x axis y axis)
	Identifier: 0x58
	CONNECTOR_ID: 471
`

const xrandrNVIDIA = `Screen 0: minimum 8 x 8, current 1280 x 1024, maximum 32767 x 32767
DP-0 disconnected (normal left inverted right x axis y axis)
DP-1 connected primary 1280x1024+0+0 (0x1c8) normal (normal left inverted right x axis y axis) 400mm x 300mm
	Identifier: 0x1c7
	EDID:
		00ffffffffffff0022f01d0bc24b3704
		0707010e2e281e9ae89d19a255479827
  1280x1024 (0x1c8) 157.500MHz +HSync +VSync *current +preferred
`

func TestParseXrandrAMD(t *testing.T) {
	outs := ParseXrandrVerbose(xrandrAMD)
	if len(outs) != 3 {
		t.Fatalf("outputs: %d", len(outs))
	}
	o := MatchXOutput(outs, "DP-2", 458, nil)
	if o == nil || o.Name != "DisplayPort-1" || o.CurrentID != "0x4a" || !o.Connected {
		t.Fatalf("%+v", o)
	}
}

func TestParseXrandrNVIDIAByEDID(t *testing.T) {
	outs := ParseXrandrVerbose(xrandrNVIDIA)
	edid := []byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00, 0x22, 0xf0, 0x1d, 0x0b, 0xc2, 0x4b, 0x37, 0x04,
		0x07, 0x07, 0x01, 0x0e, 0x2e, 0x28, 0x1e, 0x9a, 0xe8, 0x9d, 0x19, 0xa2, 0x55, 0x47, 0x98, 0x27, 0x10, 0x48}
	o := MatchXOutput(outs, "DP-3", 99, edid) // имена в X у NVIDIA не совпадают с DRM
	if o == nil || o.Name != "DP-1" || o.CurrentID != "0x1c8" {
		t.Fatalf("%+v", o)
	}
}

func TestSwayCurrent(t *testing.T) {
	js := `[{"name":"DP-2","current_mode":{"width":1280,"height":1024,"refresh":85024}},{"name":"HDMI-A-1","current_mode":{"width":720,"height":576,"refresh":50000}}]`
	got, err := SwayCurrent([]byte(js), "DP-2")
	if err != nil || got != "1280x1024@85.024Hz" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestHyprRule(t *testing.T) {
	js := `[{"name":"DP-2","width":1280,"height":1024,"refreshRate":85.02,"x":0,"y":0,"scale":1.0}]`
	m := timing.CVT(1000, 500, 75, false)
	prev, test, err := HyprRule([]byte(js), "DP-2", m)
	if err != nil {
		t.Fatal(err)
	}
	if prev != "DP-2,1280x1024@85.020,0x0,1" {
		t.Fatal(prev)
	}
	if !strings.HasPrefix(test, "DP-2,modeline ") || !strings.HasSuffix(test, "-hsync +vsync,0x0,1") {
		t.Fatal(test)
	}
}
