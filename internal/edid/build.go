package edid

import (
	"math"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
	"github.com/ImmortallKIng/crtctl/internal/timing"
)

// MaxExtensions — сколько блоков CTA добавляем максимум. Собираем EDID 1.4:
// в отличие от 1.3, ему не нужен block map при нескольких расширениях.
const MaxExtensions = 3

const dtdsPerExtension = 6 // (127-4)/18

// BuildOptions — из чего собирать EDID.
type BuildOptions struct {
	// Base — исходный EDID монитора. Из него берутся производитель, модель, серийный номер,
	// дата, размер, гамма, цветность и тип входа: так KDE и другие программы узнают монитор.
	Base  *EDID
	Name  string       // до 13 символов, пусто — без дескриптора имени
	Range *RangeLimits // nil — без дескриптора диапазонов
	// Modes — режимы в DTD; первый станет предпочтительным.
	Modes []timing.Mode
	// Extra — DMT-режимы, которые можно закодировать дёшево (Established/Standard).
	Extra []timing.Mode
}

// Capacity — сколько DTD помещается при данных опциях.
func Capacity(withName, withRange bool) int {
	base := 4
	if withName {
		base--
	}
	if withRange {
		base--
	}
	return base + MaxExtensions*dtdsPerExtension
}

// Build собирает EDID. Режимы, не поместившиеся в Established/Standard, игнорируются,
// а не поместившиеся DTD дают ошибку.
func Build(o BuildOptions) ([]byte, error) {
	if len(o.Modes) == 0 {
		return nil, i18n.Errorf("no modes given")
	}
	for _, m := range o.Modes {
		if err := checkDTD(m); err != nil {
			return nil, err
		}
	}
	if c := Capacity(o.Name != "", o.Range != nil); len(o.Modes) > c {
		return nil, i18n.Errorf("too many modes: %d, at most %d fit", len(o.Modes), c)
	}

	b := make([]byte, 128)
	copy(b, header)
	if o.Base != nil && len(o.Base.Raw) >= 128 {
		copy(b[8:18], o.Base.Raw[8:18])   // производитель, модель, серийник, дата
		copy(b[20:35], o.Base.Raw[20:35]) // тип входа, размер, гамма, возможности, цветность
	} else {
		copy(b[8:18], []byte{0x0e, 0x54, 0x01, 0x00, 0, 0, 0, 0, 1, 34}) // "CRT", модель 1
		b[20] = 0x08                                                     // аналоговый вход, раздельная синхронизация
		b[21], b[22] = 40, 30
		b[23] = 120 // гамма 2.2
		b[24] = 0x08
		copy(b[25:35], []byte{0xee, 0x91, 0xa3, 0x54, 0x4c, 0x99, 0x26, 0x0f, 0x50, 0x54}) // sRGB
	}
	b[18], b[19] = 1, 4
	// Первый DTD — предпочтительный; без «continuous frequency», чтобы ядро
	// не добавляло от себя режимы из диапазона (переходник может их не потянуть).
	b[24] = b[24]&^0x01 | 0x02

	est, std := encodeExtra(o.Extra)
	b[35], b[36], b[37] = est[0], est[1], est[2]
	copy(b[38:54], std[:])

	var descs [][]byte
	modes := o.Modes
	nBase := 4
	if o.Name != "" {
		nBase--
	}
	if o.Range != nil {
		nBase--
	}
	for len(modes) > 0 && len(descs) < nBase {
		descs = append(descs, encodeDTD(modes[0], b[21], b[22]))
		modes = modes[1:]
	}
	if o.Name != "" {
		descs = append(descs, textDescriptor(0xfc, o.Name))
	}
	if o.Range != nil {
		descs = append(descs, rangeDescriptor(*o.Range))
	}
	for len(descs) < 4 {
		descs = append(descs, []byte{0, 0, 0, 0x10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}) // пустой
	}
	for i, d := range descs {
		copy(b[54+18*i:], d)
	}

	var exts [][]byte
	for len(modes) > 0 {
		n := min(len(modes), dtdsPerExtension)
		x := make([]byte, 128)
		x[0], x[1], x[2], x[3] = 0x02, 0x03, 4, 0x00
		for i := 0; i < n; i++ {
			copy(x[4+18*i:], encodeDTD(modes[i], b[21], b[22]))
		}
		modes = modes[n:]
		setChecksum(x)
		exts = append(exts, x)
	}
	b[126] = byte(len(exts))
	setChecksum(b)
	for _, x := range exts {
		b = append(b, x...)
	}
	return b, nil
}

func setChecksum(b []byte) {
	var s byte
	for _, c := range b[:127] {
		s += c
	}
	b[127] = -s
}

func checkDTD(m timing.Mode) error {
	switch {
	case m.Interlaced:
		return i18n.Errorf("%s: interlaced modes are not supported", m.Name())
	case m.PixelClock/10 > 0xffff || m.PixelClock%10 != 0:
		return i18n.Errorf("%s: pixel clock %d kHz cannot be stored in a DTD (10 kHz steps, 655.35 MHz max)", m.Name(), m.PixelClock)
	case m.HActive > 4095 || m.VActive > 4095 || m.HTotal-m.HActive > 4095 || m.VTotal-m.VActive > 4095:
		return i18n.Errorf("%s: dimensions do not fit in a DTD", m.Name())
	case m.HSyncStart-m.HActive > 1023 || m.HSyncEnd-m.HSyncStart > 1023 ||
		m.VSyncStart-m.VActive > 63 || m.VSyncEnd-m.VSyncStart > 63:
		return i18n.Errorf("%s: sync parameters do not fit in a DTD", m.Name())
	}
	return nil
}

func encodeDTD(m timing.Mode, widthCM, heightCM byte) []byte {
	ha, hb := m.HActive, m.HTotal-m.HActive
	va, vb := m.VActive, m.VTotal-m.VActive
	hfp, hsw := m.HSyncStart-m.HActive, m.HSyncEnd-m.HSyncStart
	vfp, vsw := m.VSyncStart-m.VActive, m.VSyncEnd-m.VSyncStart
	c := m.PixelClock / 10
	wmm, hmm := int(widthCM)*10, int(heightCM)*10
	flags := byte(0x18)
	if m.VSyncPos {
		flags |= 0x04
	}
	if m.HSyncPos {
		flags |= 0x02
	}
	return []byte{
		byte(c), byte(c >> 8),
		byte(ha), byte(hb), byte(ha>>8<<4 | hb>>8),
		byte(va), byte(vb), byte(va>>8<<4 | vb>>8),
		byte(hfp), byte(hsw), byte(vfp&0xf<<4 | vsw&0xf),
		byte(hfp>>8<<6 | hsw>>8<<4 | vfp>>4<<2 | vsw>>4),
		byte(wmm), byte(hmm), byte(wmm>>8<<4 | hmm>>8),
		0, 0, flags,
	}
}

func textDescriptor(tag byte, s string) []byte {
	d := []byte{0, 0, 0, tag, 0}
	t := []byte(s)
	if len(t) > 13 {
		t = t[:13]
	}
	d = append(d, t...)
	if len(t) < 13 {
		d = append(d, '\n')
	}
	for len(d) < 18 {
		d = append(d, ' ')
	}
	return d
}

func rangeDescriptor(r RangeLimits) []byte {
	clamp := func(v int) byte { return byte(max(1, min(255, v))) }
	return []byte{0, 0, 0, 0xfd, 0,
		clamp(r.VMin), clamp(r.VMax), clamp(r.HMin), clamp(r.HMax),
		clamp(int(math.Ceil(float64(r.PclkMax) / 10))),
		0x01, 0x0a, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20} // 0x01: только диапазоны, без GTF/CVT
}

// encodeExtra раскладывает DMT-режимы по битам Established Timings и кодам Standard Timings.
func encodeExtra(modes []timing.Mode) ([3]byte, [16]byte) {
	var est [3]byte
	var std [16]byte
	for i := range std {
		std[i] = 0x01
	}
	nstd := 0
next:
	for _, m := range modes {
		for i, t := range establishedList {
			if t.w == m.HActive && t.h == m.VActive && math.Abs(t.hz-m.VFreq()) < 0.6 {
				if dm, ok := timing.FindDMT(t.w, t.h, t.hz); ok && dm.Same(m) {
					bit := 15 - i
					if bit >= 8 {
						est[0] |= 1 << (bit - 8)
					} else {
						est[1] |= 1 << bit
					}
					continue next
				}
			}
		}
		if nstd >= 8 {
			continue
		}
		if b0, b1, ok := standardCode(m); ok {
			std[2*nstd], std[2*nstd+1] = b0, b1
			nstd++
		}
	}
	return est, std
}

func standardCode(m timing.Mode) (byte, byte, bool) {
	dm, ok := timing.FindDMT(m.HActive, m.VActive, m.VFreq())
	if !ok || !dm.Same(m) || m.HActive%8 != 0 || m.HActive < 256 || m.HActive > 2288 {
		return 0, 0, false
	}
	hz := int(math.Round(m.VFreq()))
	if hz < 60 || hz > 123 {
		return 0, 0, false
	}
	var ar byte
	w, h := m.HActive, m.VActive
	switch {
	case w*10/16 == h:
		ar = 0
	case w*3/4 == h:
		ar = 1
	case w*4/5 == h:
		ar = 2
	case w*9/16 == h:
		ar = 3
	default:
		return 0, 0, false
	}
	return byte(w/8 - 31), ar<<6 | byte(hz-60), true
}
