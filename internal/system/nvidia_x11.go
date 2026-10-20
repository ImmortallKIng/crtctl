package system

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/boot"
	"github.com/ImmortallKIng/crtctl/internal/i18n"
)

// X-драйвер NVIDIA читает EDID сам и не видит подмену в ядре, поэтому на X11 тот же EDID
// передаётся ему через xorg.conf (CustomEDID), а режимы вне EDID разрешаются ModeValidation.

const NvidiaXConf = "/etc/X11/xorg.conf.d/90-crtctl-nvidia.conf"

var deviceNvidiaRe = regexp.MustCompile(`(?is)Section\s+"Device".*?Driver\s+"nvidia".*?EndSection`)

// foreignNvidiaDevice — файл с чужой секцией Device для nvidia (тогда свою добавлять нельзя:
// X возьмёт только одну). "" — таких нет.
func foreignNvidiaDevice(s boot.Sys) string {
	files := append([]string{"/etc/X11/xorg.conf"}, s.Glob("/etc/X11/xorg.conf.d/*.conf")...)
	for _, f := range files {
		if f == NvidiaXConf {
			continue
		}
		if data, err := s.Read(f); err == nil && deviceNvidiaRe.MatchString(stripComments(data)) {
			return f
		}
	}
	return ""
}

func stripComments(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = l[:i]
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// PCIToX переводит "0000:01:00.0" в формат BusID X-сервера "PCI:1:0:0" (десятичный).
func PCIToX(pci string) string {
	p := strings.FieldsFunc(pci, func(r rune) bool { return r == ':' || r == '.' })
	if len(p) != 4 {
		return ""
	}
	var n [3]int64
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseInt(p[i+1], 16, 64)
		if err != nil {
			return ""
		}
		n[i] = v
	}
	return fmt.Sprintf("PCI:%d:%d:%d", n[0], n[1], n[2])
}

// SyncNvidiaX11 пишет (или убирает) конфиг X для всех коннекторов с XOutput.
func SyncNvidiaX11(st *State, s boot.Sys) error {
	var names []string
	for n, c := range st.Connectors {
		if c.XOutput != "" && c.EDIDFile != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return s.Remove(NvidiaXConf)
	}
	var edids, modes []string
	busID := ""
	for _, n := range names {
		c := st.Connectors[n]
		edids = append(edids, c.XOutput+":"+c.EDIDFile)
		modes = append(modes, c.XOutput+": AllowNonEdidModes")
		if busID == "" {
			busID = PCIToX(c.PCI)
		}
	}
	lines := []string{
		`    Option     "CustomEDID"     "` + strings.Join(edids, "; ") + `"`,
		`    Option     "ModeValidation" "` + strings.Join(modes, "; ") + `"`,
	}
	if f := foreignNvidiaDevice(s); f != "" {
		fmt.Fprintln(s.Log, i18n.T("%s already has a Device section for the NVIDIA driver; add these lines to it and restart X:", f))
		for _, l := range lines {
			fmt.Fprintln(s.Log, l)
		}
		_ = s.Remove(NvidiaXConf)
		return ErrManual
	}
	conf := "# Created by crtctl: the NVIDIA X driver reads EDIDs itself and ignores the kernel\n" +
		"# override, so the same EDID is given to it here. Remove with: sudo crtctl remove CONNECTOR\n" +
		"Section \"Device\"\n    Identifier \"crtctl-nvidia\"\n    Driver     \"nvidia\"\n"
	if busID != "" {
		conf += "    BusID      \"" + busID + "\"\n"
	}
	conf += strings.Join(lines, "\n") + "\nEndSection\n"
	if err := s.Write(NvidiaXConf, conf, 0o644); err != nil {
		return err
	}
	fmt.Fprintln(s.Log, i18n.T("X server configuration written to %s; log out and back in (or restart X) to apply it.", NvidiaXConf))
	return nil
}
