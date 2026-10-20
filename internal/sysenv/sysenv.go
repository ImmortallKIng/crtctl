// Package sysenv определяет окружение: драйвер видеокарты, графическую сессию,
// дистрибутив, ограничения ядра (lockdown) и доступность debugfs.
package sysenv

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Driver — драйвер видеокарты cardN ("amdgpu", "i915", "xe", "nouveau", "nvidia", "radeon"...).
func Driver(card string) string {
	t, err := filepath.EvalSymlinks(filepath.Join("/sys/class/drm", card, "device", "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(t)
}

// IsNvidiaProprietary — проприетарный (или open kernel) драйвер NVIDIA, не nouveau.
func IsNvidiaProprietary(driver string) bool { return driver == "nvidia" }

// NvidiaModeset — загружен ли nvidia-drm и включён ли у него modeset (без него нет KMS-коннекторов).
func NvidiaModeset() (loaded, enabled bool) {
	b, err := os.ReadFile("/sys/module/nvidia_drm/parameters/modeset")
	if err != nil {
		return false, false
	}
	v := strings.TrimSpace(string(b))
	return true, v == "Y" || v == "1"
}

// Lockdown — режим kernel lockdown: "none", "integrity", "confidentiality" или "" (нет LSM).
// В integrity и выше запись в debugfs запрещена (обычно из-за Secure Boot).
func Lockdown() string {
	b, err := os.ReadFile("/sys/kernel/security/lockdown")
	if err != nil {
		return ""
	}
	for _, f := range strings.Fields(string(b)) {
		if strings.HasPrefix(f, "[") {
			return strings.Trim(f, "[]")
		}
	}
	return ""
}

// DebugfsUsable — debugfs смонтирован и lockdown не мешает в него писать.
func DebugfsUsable() bool {
	// Смотрим /proc/mounts: обычному пользователю каталог debugfs не виден, хотя он смонтирован
	mounts, _ := os.ReadFile("/proc/mounts")
	if !strings.Contains(string(mounts), " debugfs ") {
		return false
	}
	l := Lockdown()
	return l == "" || l == "none"
}

// OS — данные /etc/os-release.
type OS struct {
	ID, Name string
	Like     []string
}

func OSRelease() OS {
	var o OS
	for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), "=")
			if !ok {
				continue
			}
			v = strings.Trim(v, `"'`)
			switch k {
			case "ID":
				o.ID = v
			case "PRETTY_NAME":
				o.Name = v
			case "ID_LIKE":
				o.Like = strings.Fields(v)
			}
		}
		f.Close()
		break
	}
	if o.Name == "" {
		o.Name = o.ID
	}
	return o
}

// Session — графическая сессия, в которой запущена команда.
type Session struct {
	Kind    string // kde, gnome, sway, hyprland, x11, wayland (другой композитор), tty
	Wayland bool
}

// DetectSession смотрит переменные окружения текущего процесса.
func DetectSession() Session {
	wl := os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland"
	x := os.Getenv("DISPLAY") != ""
	desk := strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP") + ":" + os.Getenv("XDG_SESSION_DESKTOP") + ":" + os.Getenv("DESKTOP_SESSION"))
	s := Session{Wayland: wl}
	switch {
	case os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "":
		s.Kind = "hyprland"
	case os.Getenv("SWAYSOCK") != "":
		s.Kind = "sway"
	case strings.Contains(desk, "KDE") || strings.Contains(desk, "PLASMA"):
		s.Kind = "kde"
		if !wl && x {
			s.Kind = "x11" // Plasma на X11: режимы через xrandr
		}
	case strings.Contains(desk, "GNOME") && wl:
		s.Kind = "gnome"
	case x && !wl:
		s.Kind = "x11"
	case wl:
		s.Kind = "wayland"
	default:
		s.Kind = "tty"
	}
	return s
}

// HasCmd — есть ли программа в PATH (или в /usr/sbin, /sbin — их часто нет в PATH пользователя).
func HasCmd(name string) string {
	for _, dir := range append(filepath.SplitList(os.Getenv("PATH")), "/usr/sbin", "/sbin", "/usr/bin", "/bin") {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// XServer — запущенный Xorg: номер дисплея и файл авторизации (из его командной строки).
type XServer struct {
	Display, Auth string
}

// XorgServers ищет процессы Xorg. Нужен root, чтобы прочитать файл авторизации.
func XorgServers() []XServer {
	var out []XServer
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if b := filepath.Base(args[0]); b != "Xorg" && b != "X" && b != "Xorg.bin" && b != "Xorg.wrap" {
			continue
		}
		var x XServer
		for i := 1; i < len(args); i++ {
			switch {
			case strings.HasPrefix(args[i], ":") && x.Display == "":
				x.Display = args[i]
			case args[i] == "-auth" && i+1 < len(args):
				x.Auth = args[i+1]
				i++
			}
		}
		if x.Display != "" {
			out = append(out, x)
		}
	}
	return out
}
