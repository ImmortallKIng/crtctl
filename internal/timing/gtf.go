package timing

import "math"

// GTF по стандарту VESA с параметрами по умолчанию (M=600, C=40, K=128, J=20),
// как в xserver gtf.c и edid-decode --gtf. Классический расчёт для мультичастотных CRT.

const (
	gtfCellGran      = 8.0
	gtfMinPorch      = 1.0
	gtfVSyncRqd      = 3.0
	gtfHSyncPercent  = 8.0
	gtfMinVSyncPlusB = 550.0
	gtfCPrime        = 30.0
	gtfMPrime        = 300.0
)

func GTF(w, h int, hz float64) Mode {
	hPixels := math.RoundToEven(float64(w)/gtfCellGran) * gtfCellGran
	vLines := float64(h)

	hPeriodEst := (1.0/hz - gtfMinVSyncPlusB/1e6) / (vLines + gtfMinPorch) * 1e6
	vsyncPlusBP := math.RoundToEven(gtfMinVSyncPlusB / hPeriodEst)
	totalV := vLines + vsyncPlusBP + gtfMinPorch
	vFieldRateEst := 1.0 / hPeriodEst / totalV * 1e6
	hPeriod := hPeriodEst / (hz / vFieldRateEst)

	dutyCycle := gtfCPrime - gtfMPrime*hPeriod/1000.0
	hBlank := math.RoundToEven(hPixels*dutyCycle/(100.0-dutyCycle)/(2*gtfCellGran)) * 2 * gtfCellGran
	totalPixels := hPixels + hBlank
	pixelFreq := totalPixels / hPeriod // МГц
	hSync := math.RoundToEven(gtfHSyncPercent/100.0*totalPixels/gtfCellGran) * gtfCellGran
	hFrontPorch := hBlank/2 - hSync

	m := Mode{Source: "GTF", PixelClock: int(math.Round(pixelFreq * 1000))}
	m.HActive = int(hPixels)
	m.HSyncStart = m.HActive + int(hFrontPorch)
	m.HSyncEnd = m.HSyncStart + int(hSync)
	m.HTotal = int(totalPixels)
	m.VActive = h
	m.VSyncStart = h + int(gtfMinPorch)
	m.VSyncEnd = m.VSyncStart + int(gtfVSyncRqd)
	m.VTotal = int(totalV)
	m.VSyncPos = true
	return m
}
