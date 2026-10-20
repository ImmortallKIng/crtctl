package system

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
)

// Фиксация конфигурации канала DP через debugfs amdgpu (link_settings).
// Нужна переходникам DP→VGA, которые не работают на пониженной скорости или числе линий.

func linkFile(debugfsDir string) string { return filepath.Join(debugfsDir, "link_settings") }

// ReadLink возвращает содержимое link_settings как есть.
func ReadLink(debugfsDir string) (string, error) {
	b, err := os.ReadFile(linkFile(debugfsDir))
	if errors.Is(err, os.ErrNotExist) {
		return "", i18n.Errorf("%s does not exist: link forcing is supported only by amdgpu (and needs sudo)", linkFile(debugfsDir))
	}
	return strings.TrimSpace(string(b)), err
}

// LinkValue — строка для link_settings: "2 0xa".
func LinkValue(lanes int, rate byte) string { return fmt.Sprintf("%d 0x%x", lanes, rate) }

func ForceLink(debugfsDir, value string) error {
	return os.WriteFile(linkFile(debugfsDir), []byte(value), 0)
}

func unitName(conn string) string { return "crtctl-link-" + conn + ".service" }

func UnitPath(conn string) string { return filepath.Join(UnitDir, unitName(conn)) }

// InstallLinkUnit ставит systemd-сервис, который фиксирует канал при каждой загрузке.
func InstallLinkUnit(st *State, conn, debugfsDir, value string, log io.Writer) error {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return i18n.Errorf("this system does not use systemd; to keep the link forced, run this at every boot (for example from rc.local): echo \"%s\" > %s", value, linkFile(debugfsDir))
	}
	unit := fmt.Sprintf(`# Created by crtctl
[Unit]
Description=crtctl: force DisplayPort link on %[1]s (%[2]s)
After=sys-kernel-debug.mount systemd-modules-load.service
Requires=sys-kernel-debug.mount

[Service]
Type=oneshot
ExecStart=/bin/sh -c 'echo "%[2]s" > %[3]s'

[Install]
WantedBy=graphical.target
`, conn, value, linkFile(debugfsDir))
	if err := writeAtomic(UnitPath(conn), []byte(unit), 0o644); err != nil {
		return err
	}
	if err := run(log, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := run(log, "systemctl", "enable", "--now", unitName(conn)); err != nil {
		return err
	}
	st.Conn(conn).Link = value
	return st.Save()
}

func RemoveLinkUnit(st *State, conn string, log io.Writer) error {
	if _, err := os.Stat(UnitPath(conn)); errors.Is(err, os.ErrNotExist) {
		return i18n.Errorf("service %s does not exist", unitName(conn))
	}
	_ = run(log, "systemctl", "disable", unitName(conn))
	if err := os.Remove(UnitPath(conn)); err != nil {
		return err
	}
	if err := run(log, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if c := st.Get(conn); c != nil {
		c.Link = ""
	}
	return st.Save()
}

// Legacy ищет следы ручной настройки (install-edid.sh и подобного), которые могут конфликтовать.
func Legacy() []string {
	var out []string
	confs, _ := filepath.Glob("/etc/dracut.conf.d/*.conf")
	for _, f := range confs {
		if strings.HasPrefix(filepath.Base(f), "crtctl-") {
			continue
		}
		if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), "firmware/edid") {
			out = append(out, i18n.T("%s: adds an EDID to the initramfs", f))
		}
	}
	units, _ := filepath.Glob(filepath.Join(UnitDir, "*.service"))
	for _, f := range units {
		if strings.HasPrefix(filepath.Base(f), "crtctl-") {
			continue
		}
		if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), "link_settings") {
			out = append(out, i18n.T("%s: forces the DisplayPort link", f))
		}
	}
	return out
}

// LinkUnitFailed — сервис фиксации канала есть, но при последнем запуске упал.
func LinkUnitFailed(conn string) bool {
	if _, err := os.Stat(UnitPath(conn)); err != nil {
		return false
	}
	return exec.Command("systemctl", "is-failed", "--quiet", unitName(conn)).Run() == nil
}
