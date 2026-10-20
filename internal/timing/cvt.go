package timing

// CVT 1.1 так же, как его считает libxcvt (утилита cvt, KDE addCustomMode, xrandr).
// Арифметика намеренно повторяет libxcvt вплоть до float32 и целочисленных отсечений,
// чтобы режимы совпадали с тем, что генерирует KWin.

const (
	cvtHGranularity = 8
	cvtMinVPorch    = 3
	cvtMinVBPorch   = 6
	cvtClockStep    = 250 // кГц
	cvtMinVSyncBP   = 550.0
	cvtHSyncPercent = 8
	cvtCPrime       = 30.0 // (C-J)*K/256 + J, C=40, J=20, K=128
	cvtMPrime       = 300.0
	cvtRBMinVBlank  = 460.0
	cvtRBHSync      = 32
	cvtRBHBlank     = 160
	cvtRBVFPorch    = 3
)

func cvtVSyncWidth(w, h int) int {
	switch {
	case h%3 == 0 && h*4/3 == w:
		return 4
	case h%9 == 0 && h*16/9 == w:
		return 5
	case h%10 == 0 && h*16/10 == w:
		return 6
	case h%4 == 0 && h*5/4 == w:
		return 7
	case h%9 == 0 && h*15/9 == w:
		return 7
	}
	return 10
}

// CVT считает режим по VESA CVT; reduced = reduced blanking v1.
func CVT(w, h int, hz float64, reduced bool) Mode {
	vfieldRate := float32(hz)
	hdisplay := (w + cvtHGranularity - 1) / cvtHGranularity * cvtHGranularity
	vdisplay := h
	vsync := cvtVSyncWidth(w, h)

	m := Mode{Source: "CVT", HActive: hdisplay, VActive: vdisplay, Reduced: reduced}
	var hperiod float32
	if !reduced {
		hperiod = (1000000.0/vfieldRate - cvtMinVSyncBP) / float32(vdisplay+cvtMinVPorch)
		vsyncAndBP := int(cvtMinVSyncBP/hperiod) + 1
		if vsyncAndBP < vsync+cvtMinVBPorch {
			vsyncAndBP = vsync + cvtMinVBPorch
		}
		m.VTotal = vdisplay + vsyncAndBP + cvtMinVPorch

		hblankPct := cvtCPrime - cvtMPrime*hperiod/1000.0
		if hblankPct < 20 {
			hblankPct = 20
		}
		hblank := int(float32(hdisplay) * hblankPct / (100.0 - hblankPct))
		hblank -= hblank % (2 * cvtHGranularity)

		m.HTotal = hdisplay + hblank
		m.HSyncEnd = hdisplay + hblank/2
		// Как в libxcvt: округление вверх, причём кратное 8 значение всё равно сдвигается на 8.
		m.HSyncStart = m.HSyncEnd - m.HTotal*cvtHSyncPercent/100
		m.HSyncStart += cvtHGranularity - m.HSyncStart%cvtHGranularity
		m.VSyncStart = vdisplay + cvtMinVPorch
		m.VSyncEnd = m.VSyncStart + vsync
		m.VSyncPos = true
	} else {
		m.Source = "CVT-RB"
		hperiod = (1000000.0/vfieldRate - cvtRBMinVBlank) / float32(vdisplay)
		vbilines := int(cvtRBMinVBlank/hperiod + 1)
		if vbilines < cvtRBVFPorch+vsync+cvtMinVBPorch {
			vbilines = cvtRBVFPorch + vsync + cvtMinVBPorch
		}
		m.VTotal = vdisplay + vbilines
		m.HTotal = hdisplay + cvtRBHBlank
		m.HSyncEnd = hdisplay + cvtRBHBlank/2
		m.HSyncStart = m.HSyncEnd - cvtRBHSync
		m.VSyncStart = vdisplay + cvtRBVFPorch
		m.VSyncEnd = m.VSyncStart + vsync
		m.HSyncPos = true
	}
	clock := int(float32(m.HTotal) * 1000.0 / hperiod)
	m.PixelClock = clock - clock%cvtClockStep
	return m
}
