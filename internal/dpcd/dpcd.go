// Package dpcd читает регистры DisplayPort приёмника, например переходника DP→VGA.
// Нужен root: /dev/drm_dp_aux* доступны только ему.
package dpcd

import (
	"fmt"
	"os"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
)

type Info struct {
	Rev                 string  // "1.1"
	LinkRateGbps        float64 // на линию
	LinkRateCode        byte    // 0x06, 0x0a, 0x14, 0x1e
	Lanes               int
	Downspread          bool   // канал с модуляцией частоты (SSC): минус 0.5% пропускной способности
	Branch              bool   // есть выход дальше (переходник)
	PortType            string // VGA, DVI/HDMI, DP, other
	DetailedCaps        bool
	MaxPclkMHz          float64 // из подробных возможностей порта, 0 — не сообщается
	OUI                 string
	DeviceID            string
	HWRev, FWRev        string
	LinkPclkMaxMHz      float64 // что пролезает через канал DP при 24 бит/пиксель
	Raw0, Raw80, Raw500 []byte
}

// RateName — название скорости линии по коду DPCD.
func RateName(code byte) string {
	switch code {
	case 0x06:
		return "RBR 1.62"
	case 0x0a:
		return "HBR 2.7"
	case 0x14:
		return "HBR2 5.4"
	case 0x1e:
		return "HBR3 8.1"
	}
	return fmt.Sprintf("0x%02x", code)
}

func Read(auxDev string) (*Info, error) {
	f, err := os.Open(auxDev)
	if err != nil {
		if os.IsPermission(err) {
			return nil, i18n.Errorf("no access to %s: run with sudo", auxDev)
		}
		return nil, err
	}
	defer f.Close()
	read := func(off int64) ([]byte, error) {
		b := make([]byte, 16)
		if _, err := f.ReadAt(b, off); err != nil {
			return nil, i18n.Errorf("reading DPCD 0x%03x: %w", off, err)
		}
		return b, nil
	}
	r0, err := read(0x000)
	if err != nil {
		return nil, err
	}
	r80, err := read(0x080)
	if err != nil {
		return nil, err
	}
	r500, err := read(0x500)
	if err != nil {
		return nil, err
	}
	return Decode(r0, r80, r500), nil
}

func Decode(r0, r80, r500 []byte) *Info {
	in := &Info{Raw0: r0, Raw80: r80, Raw500: r500}
	in.Rev = fmt.Sprintf("%d.%d", r0[0]>>4, r0[0]&0xf)
	in.LinkRateCode = r0[1]
	in.LinkRateGbps = float64(r0[1]) * 0.27
	in.Lanes = int(r0[2] & 0x1f)
	in.Downspread = r0[3]&0x01 != 0
	in.Branch = r0[5]&0x01 != 0
	in.DetailedCaps = r0[5]&0x10 != 0
	switch r0[5] >> 1 & 3 {
	case 0:
		in.PortType = "DP"
	case 1:
		in.PortType = "VGA"
	case 2:
		in.PortType = "DVI/HDMI"
	case 3:
		in.PortType = "other"
	}
	if in.Branch && in.DetailedCaps {
		switch r80[0] & 7 {
		case 1:
			in.PortType = "VGA"
			in.MaxPclkMHz = float64(r80[1]) * 8
		case 2:
			in.PortType = "DVI"
			in.MaxPclkMHz = float64(r80[1]) * 2.5
		case 3:
			in.PortType = "HDMI"
			in.MaxPclkMHz = float64(r80[1]) * 2.5
		case 0:
			in.PortType = "DP"
		case 5:
			in.PortType = "DP++"
		}
	}
	if o := r500[0:3]; o[0]|o[1]|o[2] != 0 {
		in.OUI = fmt.Sprintf("%02x-%02x-%02x", o[0], o[1], o[2])
	}
	in.DeviceID = strings.TrimRight(strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return -1
		}
		return r
	}, string(r500[3:9])), " ")
	if r500[9] != 0 {
		in.HWRev = fmt.Sprintf("%d.%d", r500[9]>>4, r500[9]&0xf)
	}
	if r500[10]|r500[11] != 0 {
		in.FWRev = fmt.Sprintf("%d.%d", r500[10], r500[11])
	}
	// 8b/10b: 80% полезной нагрузки, 24 бит на пиксель; downspread съедает ещё 0.5%
	in.LinkPclkMaxMHz = float64(in.Lanes) * in.LinkRateGbps * 0.8 * 1000 / 24
	if in.Downspread {
		in.LinkPclkMaxMHz *= 0.995
	}
	return in
}

// HardLimitMHz — реальный потолок частоты пикселей через переходник.
func (in *Info) HardLimitMHz() float64 {
	if in.MaxPclkMHz > 0 && in.MaxPclkMHz < in.LinkPclkMaxMHz {
		return in.MaxPclkMHz
	}
	return in.LinkPclkMaxMHz
}

// Encode/DecodeHex — хранение сырых регистров в состоянии, чтобы пересчитывать пределы
// новой логикой без повторного чтения под root.
func (in *Info) Hex() string {
	return fmt.Sprintf("%x", append(append(append([]byte{}, in.Raw0...), in.Raw80...), in.Raw500...))
}

func DecodeHex(s string) (*Info, error) {
	var b []byte
	if _, err := fmt.Sscanf(s, "%x", &b); err != nil || len(b) != 48 {
		return nil, i18n.Errorf("saved DPCD registers are corrupted")
	}
	return Decode(b[0:16], b[16:32], b[32:48]), nil
}
