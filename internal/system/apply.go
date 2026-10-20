package system

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ImmortallKIng/crtctl/internal/boot"
	"github.com/ImmortallKIng/crtctl/internal/drm"
	"github.com/ImmortallKIng/crtctl/internal/i18n"
)

func run(log io.Writer, name string, args ...string) error {
	return boot.ExecRunner{}.Run(log, name, args...)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func SHA256(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func overridePath(c drm.Connector) string { return filepath.Join(c.DebugfsDir(), "edid_override") }

// RuntimeOverride — EDID, подложенный через debugfs сейчас (nil — нет или нет доступа).
func RuntimeOverride(c drm.Connector) []byte {
	b, err := os.ReadFile(overridePath(c))
	if err != nil || len(b) == 0 {
		return nil
	}
	return b
}

// ApplyRuntime подкладывает EDID через debugfs до перезагрузки и заставляет драйвер
// перечитать его. Возвращает способ, которым это удалось.
func ApplyRuntime(c drm.Connector, raw []byte, log io.Writer) (string, error) {
	if err := os.WriteFile(overridePath(c), raw, 0); err != nil {
		return "", i18n.Errorf("writing %s: %w (is debugfs mounted? the kernel rejects a broken EDID)", overridePath(c), err)
	}
	if !c.Connected() {
		return "", errNotConnected
	}
	how, ok := Reprobe(c, func(cur []byte) bool { return bytes.Equal(cur, raw) }, true, log)
	if !ok {
		return "", i18n.Errorf("the EDID is in place, but the driver did not re-read it by any method; please report this output")
	}
	return how, nil
}

var errNotConnected = errors.New("not connected")

// IsNotConnected — EDID подложен, но монитор не подключён: подхватится при подключении.
func IsNotConnected(err error) bool { return errors.Is(err, errNotConnected) }

// ResetRuntime убирает EDID, подложенный через debugfs. Драйвер не трогаем: до перезагрузки
// или переподключения остаётся текущая картинка. Возвращает true, если было что убирать.
func ResetRuntime(c drm.Connector) (bool, error) {
	if RuntimeOverride(c) == nil {
		return false, nil
	}
	return true, os.WriteFile(overridePath(c), []byte("reset"), 0)
}

type reprobeStep struct {
	name string
	hard bool // монитор на мгновение гаснет
	do   func() error
}

// Reprobe перебирает способы от мягкого к жёсткому, пока ok(EDID ядра) не станет true.
// hard=false — без способов, при которых выход отключается. В конце шлёт событие hotplug,
// чтобы композитор (KWin, Mutter...) перечитал список режимов.
func Reprobe(c drm.Connector, ok func([]byte) bool, hard bool, log io.Writer) (string, bool) {
	status := filepath.Join(c.SysPath, "status")
	hotplug := filepath.Join(c.DebugfsDir(), "trigger_hotplug")
	write := func(p, v string) error { return os.WriteFile(p, []byte(v), 0) }
	_, statErr := os.Stat(hotplug)
	amdgpu := statErr == nil

	var steps []reprobeStep
	if amdgpu {
		// amdgpu на status=detect EDID не перечитывает — сразу его собственный механизм
		steps = append(steps, reprobeStep{i18n.T("amdgpu re-detection (trigger_hotplug=1)"), false, func() error { return write(hotplug, "1") }})
	} else {
		steps = append(steps, reprobeStep{i18n.T("re-probe (status=detect)"), false, func() error { return write(status, "detect") }})
	}
	steps = append(steps, reprobeStep{i18n.T("forced EDID probe (status=on, then detect)"), false, func() error {
		err := write(status, "on")
		time.Sleep(500 * time.Millisecond)
		if err2 := write(status, "detect"); err == nil {
			err = err2
		}
		return err
	}})
	if amdgpu {
		steps = append(steps, reprobeStep{i18n.T("software replug (trigger_hotplug=0, then 1)"), true, func() error {
			if err := write(hotplug, "0"); err != nil {
				return err
			}
			time.Sleep(time.Second)
			return write(hotplug, "1")
		}})
	}

	cur := func() []byte { b, _ := os.ReadFile(filepath.Join(c.SysPath, "edid")); return b }
	done := ""
	if ok(cur()) {
		done = i18n.T("it was already loaded")
	}
	for _, st := range steps {
		if done != "" {
			break
		}
		if st.hard && !hard {
			continue
		}
		if st.hard {
			fmt.Fprintln(log, "  "+i18n.T("The monitor will go dark for a second"))
		}
		if err := st.do(); err != nil {
			fmt.Fprintf(log, "  · %s: %v\n", st.name, err)
			continue
		}
		for i := 0; i < 12 && done == ""; i++ { // до 3 секунд
			time.Sleep(250 * time.Millisecond)
			if ok(cur()) {
				done = st.name
			}
		}
		if done == "" {
			fmt.Fprintf(log, "  · %s\n", i18n.T("%s: the EDID did not change", st.name))
		}
	}
	sendHotplugUevent(c)
	return done, done != ""
}

// sendHotplugUevent — синтетическое событие «change HOTPLUG=1» для видеокарты:
// по нему композиторы перечитывают выходы и режимы.
func sendHotplugUevent(c drm.Connector) {
	p := filepath.Join("/sys/class/drm", c.Card, "uevent")
	_ = os.WriteFile(p, []byte(fmt.Sprintf("change 00000000-0000-0000-0000-000000000000 HOTPLUG=1 CONNECTOR=%d", c.ID)), 0)
}

// Способы постоянной установки.
const (
	MethodBoot    = "boot"    // файл прошивки + initramfs + параметр ядра (работает и с Secure Boot)
	MethodService = "service" // systemd-сервис подкладывает EDID через debugfs при загрузке
)

// PersistOptions — постоянная установка EDID.
type PersistOptions struct {
	Conn       drm.Connector
	EDID       []byte
	MonitorID  string
	Title      string
	Profile    string
	NoDDC      bool   // монитор не отдаёт EDID: опознать его потом не получится
	Method     string // "" — выбрать автоматически
	XOutput    string // NVIDIA на X11: имя выхода в X — тогда EDID передаётся и X-драйверу
	AllKernels bool
	Sys        boot.Sys
}

// ErrManual — автоматически установить нельзя; инструкция уже напечатана.
var ErrManual = errors.New("manual install required")

// ChooseMethod — boot, если распознан загрузчик; иначе service, если доступен debugfs.
func ChooseMethod(s boot.Sys, want string) (string, error) {
	switch want {
	case MethodBoot, MethodService:
		return want, nil
	case "":
	default:
		return "", i18n.Errorf("unknown install method %q (boot, service)", want)
	}
	if !s.NixOS() && boot.DetectCmdline(s) != nil {
		return MethodBoot, nil
	}
	if !s.NixOS() && s.Exists("/run/systemd/system") && debugfsUsable(s) {
		return MethodService, nil
	}
	return MethodBoot, nil // напечатаем ручную инструкцию
}

func debugfsUsable(s boot.Sys) bool {
	if !s.Exists("/sys/kernel/debug/dri") {
		return false
	}
	l, _ := s.Read("/sys/kernel/security/lockdown")
	return l == "" || strings.Contains(l, "[none]")
}

// InstallPersistent делает EDID постоянным выбранным способом.
func InstallPersistent(st *State, o PersistOptions) error {
	method, err := ChooseMethod(o.Sys, o.Method)
	if err != nil {
		return err
	}
	name := "crtctl-" + o.Conn.Name + ".bin"
	var path string
	if method == MethodService {
		path = "/etc/crtctl/edid/" + name
	} else {
		dir, _ := o.Sys.FirmwareDir()
		path = dir + "/" + name
	}
	c := st.Conn(o.Conn.Name)
	prevMethod, hadFile := c.Method, c.EDIDFile != ""
	if c.EDIDFile != "" && c.EDIDFile != path {
		_ = o.Sys.Remove(c.EDIDFile) // старая копия в другом месте (сменился способ)
	}
	if err := o.Sys.Write(path, string(o.EDID), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(o.Sys.Log, i18n.T("EDID written to %s", path))
	c.EDIDFile, c.EDIDSHA256 = path, SHA256(o.EDID)
	c.MonitorID, c.MonitorTitle, c.Profile, c.MonitorNoDDC = o.MonitorID, o.Title, o.Profile, o.NoDDC
	c.Method, c.DebugfsDir, c.SysPath = method, o.Conn.DebugfsDir(), o.Conn.SysPath
	hadX := c.XOutput != ""
	c.PCI, c.XOutput = o.Conn.PCI, o.XOutput
	c.Installed = time.Now().Format(time.RFC3339)
	if err := st.Save(); err != nil {
		return err
	}
	if hadX || o.XOutput != "" {
		defer func() {
			if err := SyncNvidiaX11(st, o.Sys); err != nil && !errors.Is(err, ErrManual) {
				fmt.Fprintln(o.Sys.Log, "⚠", err)
			}
		}()
	}
	wasBoot := prevMethod == MethodBoot || (prevMethod == "" && hadFile)
	if method == MethodService {
		if err := syncService(st, o.Sys); err != nil {
			return err
		}
		if wasBoot { // раньше стояло через загрузчик — убрать оттуда
			return syncBoot(st, o.Sys, []string{o.Conn.Name}, o.AllKernels)
		}
		return nil
	}
	if prevMethod == MethodService {
		if err := syncService(st, o.Sys); err != nil {
			return err
		}
	}
	return syncBoot(st, o.Sys, nil, o.AllKernels)
}

// RemovePersistent убирает EDID коннектора, установленный любым способом.
func RemovePersistent(st *State, conn string, s boot.Sys, allKernels bool) error {
	c := st.Get(conn)
	if c == nil || c.EDIDFile == "" {
		return i18n.Errorf("crtctl has not installed an EDID for %s", conn)
	}
	if err := s.Remove(c.EDIDFile); err != nil {
		return err
	}
	fmt.Fprintln(s.Log, i18n.T("Removed %s", c.EDIDFile))
	wasService := c.Method == MethodService
	wasX := c.XOutput != ""
	c.XOutput, c.PCI = "", ""
	c.EDIDFile, c.EDIDSHA256, c.MonitorID, c.MonitorTitle, c.Profile, c.Installed = "", "", "", "", "", ""
	c.MonitorNoDDC, c.Method, c.DebugfsDir, c.SysPath = false, "", "", ""
	if err := st.Save(); err != nil {
		return err
	}
	if wasX {
		if err := SyncNvidiaX11(st, s); err != nil && !errors.Is(err, ErrManual) {
			return err
		}
	}
	if wasService {
		return syncService(st, s)
	}
	return syncBoot(st, s, []string{conn}, allKernels)
}

// syncBoot приводит initramfs и параметры ядра в соответствие с состоянием.
// removed — коннекторы, которые нужно убрать из параметра, даже если их там поставил не crtctl.
func syncBoot(st *State, s boot.Sys, removed []string, allKernels bool) error {
	pairs := st.EDIDOverrides()
	_, classPath := s.FirmwareDir()

	managed := map[string]bool{}
	for _, r := range removed {
		managed[r] = true
	}
	for _, p := range pairs {
		managed[p[0]] = true
	}
	final := append([][2]string{}, pairs...)
	cmdline, _ := s.Read("/proc/cmdline")
	running := CmdlineEDID(cmdline)
	if len(pairs) == 0 && len(removed) == 0 {
		return nil // ничего нашего ни сейчас, ни раньше
	}
	for conn, file := range running {
		if !managed[conn] {
			final = append(final, [2]string{conn, "/usr/lib/firmware/" + file})
			fmt.Fprintln(s.Log, i18n.T("Keeping a drm.edid_firmware entry not made by crtctl: %s → %s", conn, file))
		}
	}
	params := map[string]string{"drm.edid_firmware": FormatEDIDArg(final)}
	if classPath != "" {
		params["firmware_class.path"] = ""
		if len(pairs) > 0 {
			params["firmware_class.path"] = classPath
		}
	}
	var files []string
	for _, p := range pairs {
		files = append(files, p[1])
	}

	if s.NixOS() {
		printManual(s, params, files, true)
		return ErrManual
	}
	cl := boot.DetectCmdline(s)
	in := boot.DetectInitramfs(s)
	if cl == nil {
		printManual(s, params, files, false)
		return ErrManual
	}
	fmt.Fprintln(s.Log, i18n.T("Kernel parameters via %s", cl.Name()))
	// Сначала параметры: при UKI пересборка initramfs вшивает их в образ
	if err := cl.Set(params); err != nil {
		return i18n.Errorf("could not update the kernel parameters (%s): %w", cl.Name(), err)
	}
	if in == nil {
		fmt.Fprintln(s.Log, i18n.T("The initramfs generator was not recognised. If the graphics driver loads from the initramfs, add these files to it: %s", strings.Join(files, " ")))
	} else {
		if err := in.Sync(files); err != nil {
			return err
		}
		if allKernels {
			fmt.Fprintln(s.Log, i18n.T("Rebuilding the initramfs for all installed kernels (usually 30–90 seconds per kernel)..."))
		} else {
			fmt.Fprintln(s.Log, i18n.T("Rebuilding the initramfs for the running kernel (30–90 seconds)..."))
		}
		if err := in.Rebuild(allKernels); err != nil {
			return i18n.Errorf("%s failed: %w; fix the problem and run the same crtctl command again", in.Name(), err)
		}
	}
	got, err := cl.Get(sortedParamKeys(params)...)
	if err == nil {
		for k, v := range params {
			if got[k] != v {
				return i18n.Errorf("the kernel parameters were not updated as expected (%s); check the boot loader configuration", cl.Name())
			}
		}
	}
	return nil
}

func sortedParamKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func printManual(s boot.Sys, params map[string]string, files []string, nixos bool) {
	fmt.Fprintln(s.Log, i18n.T("crtctl cannot change the boot configuration on this system automatically. Do it by hand:"))
	if len(files) > 0 {
		fmt.Fprintln(s.Log, "  1) "+i18n.T("make sure the graphics driver can load these files at boot (include them in the initramfs if it loads from there): %s", strings.Join(files, " ")))
	}
	var toks []string
	for _, k := range sortedParamKeys(params) {
		if params[k] != "" {
			toks = append(toks, k+"="+params[k])
		}
	}
	if len(toks) > 0 {
		fmt.Fprintln(s.Log, "  2) "+i18n.T("add to the kernel parameters: %s", strings.Join(toks, " ")))
	} else {
		fmt.Fprintln(s.Log, "  2) "+i18n.T("remove drm.edid_firmware from the kernel parameters"))
	}
	if nixos && len(toks) > 0 {
		fmt.Fprintln(s.Log, "  NixOS (configuration.nix):")
		fmt.Fprintf(s.Log, "    boot.kernelParams = [ %s ];\n", quoteAll(toks))
		fmt.Fprintln(s.Log, `    hardware.firmware = [ (pkgs.runCommand "crtctl-edid" {} ''`)
		fmt.Fprintln(s.Log, "      mkdir -p $out/lib/firmware/edid")
		for _, f := range files {
			fmt.Fprintf(s.Log, "      cp %s $out/lib/firmware/edid/\n", f)
		}
		fmt.Fprintln(s.Log, "    '') ];")
	}
}

func quoteAll(xs []string) string {
	var q []string
	for _, x := range xs {
		q = append(q, `"`+x+`"`)
	}
	return strings.Join(q, " ")
}

const (
	serviceScript = "/etc/crtctl/apply-edid.sh"
	serviceUnit   = "/etc/systemd/system/crtctl-edid.service"
)

// syncService — способ service: скрипт и сервис, подкладывающие EDID через debugfs при загрузке.
func syncService(st *State, s boot.Sys) error {
	var lines []string
	for _, name := range sortedConns(st) {
		c := st.Connectors[name]
		if c.EDIDFile != "" && c.Method == MethodService {
			lines = append(lines, fmt.Sprintf("apply %q %q %q", c.DebugfsDir, c.EDIDFile, c.SysPath))
		}
	}
	had := s.Exists(serviceUnit)
	if len(lines) == 0 {
		if !had {
			return nil
		}
		_ = s.R.Run(s.Log, "systemctl", "disable", "crtctl-edid.service")
		if err := s.Remove(serviceUnit); err != nil {
			return err
		}
		_ = s.Remove(serviceScript)
		return s.R.Run(s.Log, "systemctl", "daemon-reload")
	}
	script := `#!/bin/sh
# Created by crtctl: load EDID overrides through debugfs at boot.
apply() { # $1 debugfs dir, $2 EDID file, $3 sysfs connector
	i=0
	while [ ! -e "$1/edid_override" ] && [ $i -lt 30 ]; do sleep 0.5; i=$((i+1)); done
	[ -e "$1/edid_override" ] || { echo "crtctl: $1 not found" >&2; return 1; }
	cat "$2" > "$1/edid_override" || return 1
	if [ -e "$1/trigger_hotplug" ]; then echo 1 > "$1/trigger_hotplug"; else echo detect > "$3/status"; fi
}
` + strings.Join(lines, "\n") + "\n"
	unit := `# Created by crtctl
[Unit]
Description=crtctl: load EDID overrides for CRT monitors
After=sys-kernel-debug.mount systemd-modules-load.service systemd-udev-settle.service
Requires=sys-kernel-debug.mount
Before=display-manager.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh ` + serviceScript + `

[Install]
WantedBy=multi-user.target
`
	if err := s.Write(serviceScript, script, 0o755); err != nil {
		return err
	}
	if err := s.Write(serviceUnit, unit, 0o644); err != nil {
		return err
	}
	if err := s.R.Run(s.Log, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	return s.R.Run(s.Log, "systemctl", "enable", "crtctl-edid.service")
}

func sortedConns(st *State) []string {
	var out []string
	for k := range st.Connectors {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
