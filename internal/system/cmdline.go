package system

import (
	"os"
	"sort"
	"strings"
)

// CmdlineEDID разбирает drm.edid_firmware из командной строки ядра: коннектор → файл.
// Значение без коннектора (для всех выходов) возвращается с ключом "*".
func CmdlineEDID(cmdline string) map[string]string {
	out := map[string]string{}
	for _, arg := range strings.Fields(cmdline) {
		v, ok := strings.CutPrefix(arg, "drm.edid_firmware=")
		if !ok {
			v, ok = strings.CutPrefix(arg, "drm_kms_helper.edid_firmware=")
		}
		if !ok {
			continue
		}
		for _, item := range strings.Split(v, ",") {
			if conn, file, ok := strings.Cut(item, ":"); ok {
				out[conn] = file
			} else if item != "" {
				out["*"] = item
			}
		}
	}
	return out
}

// RunningEDIDOverrides — что ядро загрузило при текущей загрузке.
func RunningEDIDOverrides() map[string]string {
	b, _ := os.ReadFile("/proc/cmdline")
	return CmdlineEDID(string(b))
}

// FormatEDIDArg собирает значение параметра: "DP-2:edid/a.bin,HDMI-A-1:edid/b.bin".
// Общая запись ("*") идёт последней и без коннектора — ядро берёт её для остальных выходов.
func FormatEDIDArg(pairs [][2]string) string {
	p := append([][2]string{}, pairs...)
	sort.SliceStable(p, func(i, j int) bool { return p[i][0] != "*" && p[j][0] == "*" })
	var items []string
	for _, x := range p {
		if x[0] == "*" {
			items = append(items, FirmwareName(x[1]))
		} else {
			items = append(items, x[0]+":"+FirmwareName(x[1]))
		}
	}
	return strings.Join(items, ",")
}
