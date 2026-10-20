package drm

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
	"github.com/ImmortallKIng/crtctl/internal/timing"
)

// Структуры из include/uapi/drm/drm_mode.h.

type modeInfo struct {
	Clock                                         uint32
	HDisplay, HSyncStart, HSyncEnd, HTotal, HSkew uint16
	VDisplay, VSyncStart, VSyncEnd, VTotal, VScan uint16
	VRefresh, Flags, Type                         uint32
	Name                                          [32]byte
}

type getConnector struct {
	EncodersPtr, ModesPtr, PropsPtr, PropValuesPtr uint64
	CountModes, CountProps, CountEncoders          uint32
	EncoderID, ConnectorID                         uint32
	ConnectorType, ConnectorTypeID                 uint32
	Connection, MMWidth, MMHeight, Subpixel, Pad   uint32
}

type getEncoder struct {
	EncoderID, EncoderType, CrtcID, PossibleCrtcs, PossibleClones uint32
}

type getCrtc struct {
	SetConnectorsPtr                    uint64
	CountConnectors, CrtcID, FbID, X, Y uint32
	GammaSize, ModeValid                uint32
	Mode                                modeInfo
}

const (
	ioctlGetConnector = 0xC05064A7
	ioctlGetEncoder   = 0xC01464A6
	ioctlGetCrtc      = 0xC06864A1

	modeFlagPHSync    = 1 << 0
	modeFlagPVSync    = 1 << 2
	modeFlagInterlace = 1 << 4
	modeTypePreferred = 1 << 3
)

func init() {
	// Защита от ошибок в раскладке структур
	if unsafe.Sizeof(modeInfo{}) != 68 || unsafe.Sizeof(getConnector{}) != 80 ||
		unsafe.Sizeof(getEncoder{}) != 20 || unsafe.Sizeof(getCrtc{}) != 104 {
		panic("drm: KMS struct layout mismatch")
	}
}

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

func (mi modeInfo) toMode() timing.Mode {
	return timing.Mode{
		Source:     "KMS",
		PixelClock: int(mi.Clock),
		HActive:    int(mi.HDisplay), HSyncStart: int(mi.HSyncStart), HSyncEnd: int(mi.HSyncEnd), HTotal: int(mi.HTotal),
		VActive: int(mi.VDisplay), VSyncStart: int(mi.VSyncStart), VSyncEnd: int(mi.VSyncEnd), VTotal: int(mi.VTotal),
		HSyncPos:   mi.Flags&modeFlagPHSync != 0,
		VSyncPos:   mi.Flags&modeFlagPVSync != 0,
		Interlaced: mi.Flags&modeFlagInterlace != 0,
	}
}

// KMSState — режимы коннектора с полными таймингами и текущий режим.
type KMSState struct {
	Modes     []timing.Mode
	Preferred int // индекс в Modes, -1 — нет
	Current   *timing.Mode
}

// ReadKMS читает режимы через ioctl. Принудительного опроса монитора не делает:
// ядро запускает его только для DRM-мастера (композитора), остальным отдаёт кэш.
func ReadKMS(c Connector) (*KMSState, error) {
	if c.ID == 0 {
		return nil, i18n.Errorf("%s: connector_id is unknown", c.Name)
	}
	f, err := os.OpenFile(c.CardPath, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fd := f.Fd()

	var gc getConnector
	gc.ConnectorID = c.ID
	if err := ioctl(fd, ioctlGetConnector, unsafe.Pointer(&gc)); err != nil {
		return nil, fmt.Errorf("DRM_IOCTL_MODE_GETCONNECTOR: %w", err)
	}
	st := &KMSState{Preferred: -1}
	if gc.CountModes > 0 {
		modes := make([]modeInfo, gc.CountModes)
		n := gc.CountModes
		gc = getConnector{ConnectorID: c.ID, CountModes: n, ModesPtr: uint64(uintptr(unsafe.Pointer(&modes[0])))}
		err := ioctl(fd, ioctlGetConnector, unsafe.Pointer(&gc))
		runtime.KeepAlive(modes)
		if err != nil {
			return nil, fmt.Errorf("DRM_IOCTL_MODE_GETCONNECTOR: %w", err)
		}
		for i := 0; i < int(min(n, gc.CountModes)); i++ {
			if modes[i].Type&modeTypePreferred != 0 && st.Preferred < 0 {
				st.Preferred = len(st.Modes)
			}
			st.Modes = append(st.Modes, modes[i].toMode())
		}
	}
	if gc.EncoderID != 0 {
		enc := getEncoder{EncoderID: gc.EncoderID}
		if err := ioctl(fd, ioctlGetEncoder, unsafe.Pointer(&enc)); err == nil && enc.CrtcID != 0 {
			cr := getCrtc{CrtcID: enc.CrtcID}
			if err := ioctl(fd, ioctlGetCrtc, unsafe.Pointer(&cr)); err == nil && cr.ModeValid != 0 {
				m := cr.Mode.toMode()
				st.Current = &m
			}
		}
	}
	return st, nil
}
