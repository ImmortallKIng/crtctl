// Package edid разбирает и собирает EDID 1.3 с блоками расширения CTA-861.
package edid

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
	"github.com/ImmortallKIng/crtctl/internal/timing"
)

var header = []byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}

// RangeLimits — дескриптор 0xFD: диапазоны частот монитора.
type RangeLimits struct {
	VMin, VMax int // Гц
	HMin, HMax int // кГц
	PclkMax    int // МГц
}

type EDID struct {
	Raw          []byte
	Vendor       string // PNP ID, например "HWP"
	Product      uint16
	Serial       uint32
	Week, Year   int
	Version      string
	Digital      bool
	WidthCM      int
	HeightCM     int
	Name         string
	SerialText   string
	Range        *RangeLimits
	Established  []timing.Mode
	Standard     []timing.Mode
	Detailed     []timing.Mode // DTD из всех блоков, по порядку
	PreferredDTD bool
	Extensions   int
	HasAudio     bool
	Warnings     []string
}

// ID — строка для сравнения мониторов: производитель, модель, серийный номер.
func (e *EDID) ID() string {
	return fmt.Sprintf("%s %d %d", e.Vendor, e.Product, e.Serial)
}

func (e *EDID) Title() string {
	if e.Name != "" {
		return fmt.Sprintf("%s (%s %d)", e.Name, e.Vendor, e.Product)
	}
	return fmt.Sprintf("%s %d", e.Vendor, e.Product)
}

// Modes — все режимы EDID без повторов: DTD, затем стандартные и установленные.
func (e *EDID) Modes() []timing.Mode {
	var out []timing.Mode
	add := func(ms []timing.Mode) {
	next:
		for _, m := range ms {
			for _, o := range out {
				if o.Same(m) {
					continue next
				}
			}
			out = append(out, m)
		}
	}
	add(e.Detailed)
	add(e.Standard)
	add(e.Established)
	return out
}

// MaxHFreq — максимальная строчная частота среди заявленных режимов (кГц).
func (e *EDID) MaxHFreq() float64 {
	var mx float64
	for _, m := range e.Modes() {
		mx = max(mx, m.HFreq())
	}
	return mx
}

func checksumOK(b []byte) bool {
	var s byte
	for _, c := range b {
		s += c
	}
	return s == 0
}

// Parse разбирает EDID. Ошибка — только если это совсем не EDID.
func Parse(raw []byte) (*EDID, error) {
	if len(raw) < 128 {
		return nil, i18n.Errorf("EDID is too short: %d bytes", len(raw))
	}
	if !bytes.Equal(raw[:8], header) {
		return nil, i18n.Errorf("not an EDID: header is missing")
	}
	e := &EDID{Raw: raw}
	b := raw[:128]
	if !checksumOK(b) {
		e.Warnings = append(e.Warnings, i18n.T("bad checksum in the base block"))
	}
	v := uint16(b[8])<<8 | uint16(b[9])
	e.Vendor = string([]byte{byte(v>>10&31) + '@', byte(v>>5&31) + '@', byte(v&31) + '@'})
	e.Product = uint16(b[10]) | uint16(b[11])<<8
	e.Serial = uint32(b[12]) | uint32(b[13])<<8 | uint32(b[14])<<16 | uint32(b[15])<<24
	e.Week, e.Year = int(b[16]), int(b[17])+1990
	e.Version = fmt.Sprintf("%d.%d", b[18], b[19])
	e.Digital = b[20]&0x80 != 0
	e.WidthCM, e.HeightCM = int(b[21]), int(b[22])
	e.PreferredDTD = b[24]&0x02 != 0 || b[19] >= 4
	e.Extensions = int(b[126])

	e.Established = parseEstablished(b[35:38])
	for i := 0; i < 8; i++ {
		if m, ok := parseStandard(b[38+2*i], b[39+2*i], b[19]); ok {
			e.Standard = append(e.Standard, m)
		}
	}
	for i := 0; i < 4; i++ {
		e.parseDescriptor(b[54+18*i : 72+18*i])
	}
	for n := 1; n <= e.Extensions && len(raw) >= (n+1)*128; n++ {
		e.parseExtension(raw[n*128 : (n+1)*128])
	}
	if len(raw) < (e.Extensions+1)*128 {
		e.Warnings = append(e.Warnings, i18n.T("%d extension blocks declared, %d present", e.Extensions, len(raw)/128-1))
	}
	return e, nil
}

func (e *EDID) parseDescriptor(d []byte) {
	if d[0] != 0 || d[1] != 0 {
		if m, ok := ParseDTD(d); ok {
			e.Detailed = append(e.Detailed, m)
		}
		return
	}
	text := func() string {
		s := string(d[5:18])
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[:i]
		}
		return strings.TrimSpace(s)
	}
	switch d[3] {
	case 0xfc:
		e.Name = text()
	case 0xff:
		e.SerialText = text()
	case 0xfd:
		r := &RangeLimits{VMin: int(d[5]), VMax: int(d[6]), HMin: int(d[7]), HMax: int(d[8]), PclkMax: int(d[9]) * 10}
		// EDID 1.4: смещения +255 в байте 4
		if d[4]&0x01 != 0 {
			r.VMin += 255
		}
		if d[4]&0x02 != 0 {
			r.VMax += 255
		}
		if d[4]&0x04 != 0 {
			r.HMin += 255
		}
		if d[4]&0x08 != 0 {
			r.HMax += 255
		}
		e.Range = r
	}
}

func (e *EDID) parseExtension(x []byte) {
	if !checksumOK(x) {
		e.Warnings = append(e.Warnings, i18n.T("bad checksum in extension block 0x%02x", x[0]))
	}
	if x[0] != 0x02 { // только CTA-861
		return
	}
	if x[1] >= 2 && x[3]&0x40 != 0 {
		e.HasAudio = true
	}
	dtdStart := int(x[2])
	if dtdStart == 0 {
		return
	}
	// Блоки данных: ищем аудио
	for i := 4; i < dtdStart && i < 127; {
		tag, l := x[i]>>5, int(x[i]&31)
		if tag == 1 {
			e.HasAudio = true
		}
		i += 1 + l
	}
	for off := dtdStart; off+18 <= 127; off += 18 {
		if m, ok := ParseDTD(x[off : off+18]); ok {
			e.Detailed = append(e.Detailed, m)
		}
	}
}

// ParseDTD разбирает 18-байтный Detailed Timing Descriptor.
func ParseDTD(d []byte) (timing.Mode, bool) {
	clk := int(d[0]) | int(d[1])<<8
	if clk == 0 {
		return timing.Mode{}, false
	}
	ha := int(d[2]) | int(d[4]>>4)<<8
	hb := int(d[3]) | int(d[4]&0xf)<<8
	va := int(d[5]) | int(d[7]>>4)<<8
	vb := int(d[6]) | int(d[7]&0xf)<<8
	hfp := int(d[8]) | int(d[11]>>6)<<8
	hsw := int(d[9]) | int(d[11]>>4&3)<<8
	vfp := int(d[10]>>4) | int(d[11]>>2&3)<<4
	vsw := int(d[10]&0xf) | int(d[11]&3)<<4
	m := timing.Mode{
		Source:     "DTD",
		PixelClock: clk * 10,
		HActive:    ha, HSyncStart: ha + hfp, HSyncEnd: ha + hfp + hsw, HTotal: ha + hb,
		VActive: va, VSyncStart: va + vfp, VSyncEnd: va + vfp + vsw, VTotal: va + vb,
		Interlaced: d[17]&0x80 != 0,
		// DRM читает полярности из этих битов при любом типе синхронизации
		VSyncPos: d[17]&0x04 != 0,
		HSyncPos: d[17]&0x02 != 0,
	}
	return m, true
}

var establishedList = []struct {
	w, h int
	hz   float64
}{
	{720, 400, 70}, {720, 400, 88}, {640, 480, 60}, {640, 480, 67}, {640, 480, 72}, {640, 480, 75}, {800, 600, 56}, {800, 600, 60},
	{800, 600, 72}, {800, 600, 75}, {832, 624, 75}, {1024, 768, 87}, {1024, 768, 60}, {1024, 768, 70}, {1024, 768, 75}, {1280, 1024, 75},
}

func parseEstablished(b []byte) []timing.Mode {
	var out []timing.Mode
	bits := uint(b[0])<<8 | uint(b[1])
	for i, t := range establishedList {
		if bits&(1<<(15-i)) == 0 {
			continue
		}
		if m, ok := timing.FindDMT(t.w, t.h, t.hz); ok {
			out = append(out, m)
		}
		// 720x400@70/88, 640x480@67, 832x624, 1024x768i — не DMT (IBM/Apple), пропускаем
	}
	return out
}

func parseStandard(b0, b1, rev byte) (timing.Mode, bool) {
	if (b0 == 0x01 && b1 == 0x01) || b0 == 0 {
		return timing.Mode{}, false
	}
	w := (int(b0) + 31) * 8
	var h int
	switch b1 >> 6 {
	case 0:
		if rev < 3 {
			h = w
		} else {
			h = w * 10 / 16
		}
	case 1:
		h = w * 3 / 4
	case 2:
		h = w * 4 / 5
	case 3:
		h = w * 9 / 16
	}
	hz := float64(b1&0x3f) + 60
	if m, ok := timing.FindDMT(w, h, hz); ok {
		return m, true
	}
	// Ядро для нестандартных берёт CVT/GTF; показываем CVT
	return timing.CVT(w, h, hz, false), true
}
