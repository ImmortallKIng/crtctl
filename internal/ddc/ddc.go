// Package ddc читает EDID монитора напрямую по шине DDC (/dev/i2c-N),
// минуя ядро. Помогает, когда драйвер видеокарты не смог прочитать EDID сам.
package ddc

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
)

const (
	i2cRDWR    = 0x0707
	i2cMRD     = 0x0001
	addrEDID   = 0x50
	addrSegPtr = 0x30
)

type i2cMsg struct {
	Addr  uint16
	Flags uint16
	Len   uint16
	_     uint16
	Buf   uintptr
}

type rdwrData struct {
	Msgs  uintptr
	NMsgs uint32
	_     uint32
}

var edidHeader = []byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}

// readBlock читает 128 байт блока n одной транзакцией (с указателем сегмента для n >= 2).
func readBlock(f *os.File, n int) ([]byte, error) {
	seg := []byte{byte(n / 2)}
	off := []byte{byte(n % 2 * 128)}
	buf := make([]byte, 128)
	var msgs []i2cMsg
	if n >= 2 {
		msgs = append(msgs, i2cMsg{Addr: addrSegPtr, Len: 1, Buf: uintptr(unsafe.Pointer(&seg[0]))})
	}
	msgs = append(msgs,
		i2cMsg{Addr: addrEDID, Len: 1, Buf: uintptr(unsafe.Pointer(&off[0]))},
		i2cMsg{Addr: addrEDID, Flags: i2cMRD, Len: 128, Buf: uintptr(unsafe.Pointer(&buf[0]))},
	)
	d := rdwrData{Msgs: uintptr(unsafe.Pointer(&msgs[0])), NMsgs: uint32(len(msgs))}
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), i2cRDWR, uintptr(unsafe.Pointer(&d)))
	runtime.KeepAlive(seg)
	runtime.KeepAlive(off)
	runtime.KeepAlive(buf)
	runtime.KeepAlive(msgs)
	if e != 0 {
		return nil, e
	}
	return buf, nil
}

func sumOK(b []byte) bool {
	var s byte
	for _, c := range b {
		s += c
	}
	return s == 0
}

// ReadEDID читает EDID с шины /dev/i2c-<bus>, с повторами при сбоях.
func ReadEDID(bus int) ([]byte, error) {
	f, err := os.OpenFile(fmt.Sprintf("/dev/i2c-%d", bus), os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, i18n.Errorf("no access to /dev/i2c-%d (run as root, or install ddcutil, which adds a udev rule)", bus)
		}
		return nil, err
	}
	defer f.Close()

	var lastErr error
	block := func(n int) ([]byte, error) {
		for try := 0; try < 3; try++ {
			b, err := readBlock(f, n)
			if err == nil && sumOK(b) && (n > 0 || bytes.Equal(b[:8], edidHeader)) {
				return b, nil
			}
			if err != nil {
				lastErr = err
			} else {
				lastErr = i18n.Errorf("block %d is corrupted (checksum or header)", n)
			}
		}
		return nil, lastErr
	}
	base, err := block(0)
	if err != nil {
		return nil, i18n.Errorf("could not read EDID from /dev/i2c-%d: %w", bus, err)
	}
	out := base
	for n := 1; n <= int(base[126]) && n < 8; n++ {
		b, err := block(n)
		if err != nil {
			return out, i18n.Errorf("extension block %d: %w", n, err)
		}
		out = append(out, b...)
	}
	return out, nil
}

// GPUBuses — шины i2c, принадлежащие видеокарте (PCI-адрес). Нужны, когда драйвер
// не связывает коннектор с шиной DDC в sysfs (так делает проприетарный драйвер NVIDIA).
func GPUBuses(pci string) []int {
	var out []int
	ents, _ := os.ReadDir("/sys/class/i2c-adapter")
	for _, e := range ents {
		dev, err := filepath.EvalSymlinks(filepath.Join("/sys/class/i2c-adapter", e.Name()))
		if err != nil || !strings.Contains(dev, "/"+pci+"/") {
			continue
		}
		// Только шины DDC: у NVIDIA они называются «NVIDIA i2c adapter N», у nouveau — «nvkm-…».
		// Служебные шины видеокарты (SMU, SMBus, датчики) не трогаем.
		name, _ := os.ReadFile(filepath.Join("/sys/class/i2c-adapter", e.Name(), "name"))
		n := strings.TrimSpace(string(name))
		if !strings.HasPrefix(n, "NVIDIA i2c adapter") && !strings.HasPrefix(n, "nvkm-") {
			continue
		}
		if num, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "i2c-")); err == nil {
			out = append(out, num)
		}
	}
	sort.Ints(out)
	return out
}

// FindBus ищет среди шин видеокарты ту, где отвечает монитор с этим EDID.
// Если точного совпадения нет (ядро EDID не знает или он подменён), подходит единственная
// ответившая шина — но только когда на видеокарте подключён один монитор (single).
func FindBus(pci string, want []byte, single bool) (int, []byte) {
	var hits []int
	var raws [][]byte
	for _, b := range GPUBuses(pci) {
		raw, err := ReadEDID(b)
		if err != nil {
			continue
		}
		if want != nil && bytes.Equal(raw[:min(len(raw), len(want))], want[:min(len(raw), len(want))]) && len(raw) >= 128 {
			return b, raw
		}
		hits = append(hits, b)
		raws = append(raws, raw)
	}
	if len(hits) == 1 && (single || want == nil) {
		return hits[0], raws[0]
	}
	return -1, nil
}
