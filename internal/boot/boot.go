// Package boot делает EDID постоянным на разных дистрибутивах: кладёт файл туда,
// где его найдёт ядро, добавляет в initramfs и прописывает параметр ядра в загрузчик.
//
// Все пути и внешние команды идут через Sys, чтобы каждый вариант можно было проверить
// тестами на поддельной файловой системе.
package boot

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
)

// Runner выполняет внешние команды. В тестах — запись вызовов.
type Runner interface {
	Run(log io.Writer, name string, args ...string) error
	Output(name string, args ...string) ([]byte, error)
	Has(name string) bool
}

// ExecRunner — настоящие команды.
type ExecRunner struct{}

func (ExecRunner) Run(log io.Writer, name string, args ...string) error {
	fmt.Fprintf(log, "  $ %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(lookup(name), args...)
	cmd.Stdout, cmd.Stderr = log, log
	return cmd.Run()
}

func (ExecRunner) Output(name string, args ...string) ([]byte, error) {
	return exec.Command(lookup(name), args...).Output()
}

func (ExecRunner) Has(name string) bool { return lookup(name) != name || inPath(name) }

func inPath(name string) bool { _, err := exec.LookPath(name); return err == nil }

// lookup ищет программу и в /usr/sbin, /sbin — у пользователя их часто нет в PATH.
func lookup(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, d := range []string{"/usr/sbin", "/sbin", "/usr/bin", "/bin"} {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return name
}

// Sys — доступ к системе: корень файловой системы, команды и журнал.
type Sys struct {
	Root string
	R    Runner
	Log  io.Writer
}

// Real — настоящая система.
func Real(log io.Writer) Sys { return Sys{Root: "/", R: ExecRunner{}, Log: log} }

func (s Sys) P(p string) string { return filepath.Join(s.Root, p) }

func (s Sys) Exists(p string) bool { _, err := os.Stat(s.P(p)); return err == nil }

func (s Sys) Read(p string) (string, error) {
	b, err := os.ReadFile(s.P(p))
	return string(b), err
}

func (s Sys) Write(p, data string, mode os.FileMode) error {
	full := s.P(p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp := full + ".crtctl-tmp"
	if err := os.WriteFile(tmp, []byte(data), mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, full)
}

func (s Sys) Remove(p string) error {
	if err := os.Remove(s.P(p)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s Sys) Glob(pattern string) []string {
	m, _ := filepath.Glob(s.P(pattern))
	var out []string
	for _, x := range m {
		rel, _ := filepath.Rel(s.Root, x)
		out = append(out, "/"+rel)
	}
	sort.Strings(out)
	return out
}

// backup сохраняет копию файла перед первой правкой.
func (s Sys) backup(p string) {
	b := p + ".crtctl-backup"
	if s.Exists(b) || !s.Exists(p) {
		return
	}
	if data, err := s.Read(p); err == nil {
		_ = s.Write(b, data, 0o644)
	}
}

// Immutable — /usr только для чтения (Fedora Silverblue/Kinoite, Bazzite, другие ostree).
func (s Sys) Immutable() bool { return s.Exists("/run/ostree-booted") }

// NixOS — конфигурация описывается декларативно, менять файлы бесполезно.
func (s Sys) NixOS() bool { return s.Exists("/etc/NIXOS") }

// FirmwareDir — куда класть EDID. На неизменяемых системах /usr недоступна для записи:
// тогда файл идёт в /etc/crtctl/firmware, а ядру сообщается firmware_class.path.
func (s Sys) FirmwareDir() (dir string, classPath string) {
	if s.Immutable() {
		return "/etc/crtctl/firmware/edid", "/etc/crtctl/firmware"
	}
	if s.Exists("/usr/lib/firmware") || !s.Exists("/lib/firmware") {
		return "/usr/lib/firmware/edid", ""
	}
	return "/lib/firmware/edid", ""
}

// --- параметры ядра в виде токенов ---

// SetTokens убирает из строки параметров key=... и, если value не пустое, добавляет key=value.
func SetTokens(cmdline string, params map[string]string) string {
	var out []string
	for _, tok := range strings.Fields(cmdline) {
		k, _, _ := strings.Cut(tok, "=")
		if _, managed := params[k]; managed {
			continue
		}
		out = append(out, tok)
	}
	for _, k := range sortedKeys(params) {
		if v := params[k]; v != "" {
			out = append(out, k+"="+v)
		}
	}
	return strings.Join(out, " ")
}

// GetTokens — значения ключей из строки параметров.
func GetTokens(cmdline string, keys ...string) map[string]string {
	out := map[string]string{}
	for _, tok := range strings.Fields(cmdline) {
		k, v, _ := strings.Cut(tok, "=")
		for _, want := range keys {
			if k == want {
				out[k] = v
			}
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// --- initramfs ---

// Initramfs — генератор initramfs. Видеодрайвер часто грузится из initramfs,
// поэтому EDID должен лежать и там, иначе ядро его не найдёт.
type Initramfs interface {
	Name() string
	// Sync включает в образ ровно эти файлы (пустой список — убрать нашу настройку).
	Sync(files []string) error
	Rebuild(allKernels bool) error
}

// DetectInitramfs выбирает генератор по установленным программам.
func DetectInitramfs(s Sys) Initramfs {
	switch {
	case s.Immutable() && s.R.Has("rpm-ostree"):
		return ostreeInitramfs{s}
	case s.R.Has("dracut") && (s.Exists("/etc/dracut.conf.d") || s.Exists("/etc/dracut.conf")):
		return dracut{s}
	case s.R.Has("mkinitcpio"):
		return mkinitcpio{s}
	case s.R.Has("update-initramfs"):
		return initramfsTools{s}
	}
	return nil
}

type dracut struct{ s Sys }

const dracutConf = "/etc/dracut.conf.d/crtctl-edid.conf"

func (dracut) Name() string { return "dracut" }

func (d dracut) Sync(files []string) error {
	if len(files) == 0 {
		return d.s.Remove(dracutConf)
	}
	return d.s.Write(dracutConf, fmt.Sprintf("# crtctl: EDID files for drm.edid_firmware\ninstall_items+=\" %s \"\n", strings.Join(files, " ")), 0o644)
}

func (d dracut) Rebuild(all bool) error {
	if d.s.R.Has("dracut-rebuild") { // EndeavourOS: initramfs в раскладке kernel-install
		return d.s.R.Run(d.s.Log, "dracut-rebuild")
	}
	args := []string{"-f"}
	if all {
		args = append(args, "--regenerate-all")
	}
	return d.s.R.Run(d.s.Log, "dracut", args...)
}

type mkinitcpio struct{ s Sys }

const mkinitcpioConf = "/etc/mkinitcpio.conf.d/crtctl-edid.conf"

func (mkinitcpio) Name() string { return "mkinitcpio" }

func (m mkinitcpio) Sync(files []string) error {
	if len(files) == 0 {
		return m.s.Remove(mkinitcpioConf)
	}
	return m.s.Write(mkinitcpioConf, fmt.Sprintf("# crtctl: EDID files for drm.edid_firmware\nFILES+=(%s)\n", strings.Join(files, " ")), 0o644)
}

func (m mkinitcpio) Rebuild(all bool) error {
	return m.s.R.Run(m.s.Log, "mkinitcpio", "-P") // все пресеты; по одному ядру mkinitcpio не умеет
}

type initramfsTools struct{ s Sys }

const initramfsHook = "/etc/initramfs-tools/hooks/crtctl-edid"

func (initramfsTools) Name() string { return "initramfs-tools" }

func (it initramfsTools) Sync(files []string) error {
	if len(files) == 0 {
		return it.s.Remove(initramfsHook)
	}
	var b strings.Builder
	b.WriteString(`#!/bin/sh
# crtctl: EDID files for drm.edid_firmware
PREREQ=""
prereqs() { echo "$PREREQ"; }
case "$1" in prereqs) prereqs; exit 0;; esac
. /usr/share/initramfs-tools/hook-functions
`)
	for _, f := range files {
		// Ядро ищет прошивки в /lib/firmware: кладём туда, а с usrmerge это та же папка
		fmt.Fprintf(&b, "mkdir -p \"${DESTDIR}/lib/firmware/edid\"\ncp \"%s\" \"${DESTDIR}/lib/firmware/edid/\"\n", f)
	}
	return it.s.Write(initramfsHook, b.String(), 0o755)
}

func (it initramfsTools) Rebuild(all bool) error {
	k := "all"
	if !all {
		if out, err := it.s.R.Output("uname", "-r"); err == nil && len(strings.TrimSpace(string(out))) > 0 {
			k = strings.TrimSpace(string(out))
		}
	}
	return it.s.R.Run(it.s.Log, "update-initramfs", "-u", "-k", k)
}

// ostreeInitramfs — rpm-ostree сам собирает initramfs; файлы из /etc отслеживаются явно.
type ostreeInitramfs struct{ s Sys }

const ostreeTracked = "/etc/crtctl/ostree-tracked"

func (ostreeInitramfs) Name() string { return "rpm-ostree initramfs-etc" }

func (o ostreeInitramfs) Sync(files []string) error {
	prev := map[string]bool{}
	if t, err := o.s.Read(ostreeTracked); err == nil {
		for _, f := range strings.Fields(t) {
			prev[f] = true
		}
	}
	want := map[string]bool{}
	var args []string
	for _, f := range files {
		want[f] = true
		if !prev[f] {
			args = append(args, "--track="+f)
		}
	}
	for f := range prev {
		if !want[f] {
			args = append(args, "--untrack="+f)
		}
	}
	if len(args) > 0 {
		sort.Strings(args)
		if err := o.s.R.Run(o.s.Log, "rpm-ostree", append([]string{"initramfs-etc"}, args...)...); err != nil {
			return err
		}
	}
	return o.s.Write(ostreeTracked, strings.Join(files, "\n")+"\n", 0o644)
}

func (ostreeInitramfs) Rebuild(bool) error { return nil } // новый деплой соберётся при перезагрузке

// --- загрузчик: параметры ядра ---

// Cmdline — способ прописать параметры ядра в загрузчик.
type Cmdline interface {
	Name() string
	// Set ставит параметры; пустое значение — убрать параметр.
	Set(params map[string]string) error
	// Get — значения параметров в конфигурации загрузчика (для проверки после Set).
	Get(keys ...string) (map[string]string, error)
}

// DetectCmdline выбирает способ по установленным программам и признакам загрузчика.
func DetectCmdline(s Sys) Cmdline {
	switch {
	case s.Immutable() && s.R.Has("rpm-ostree"):
		return ostreeKargs{s}
	case s.R.Has("grubby"):
		return grubby{s}
	case s.R.Has("kernelstub") && s.Exists("/etc/kernelstub/configuration"):
		return kernelstub{s}
	case s.Exists("/etc/default/grub") && (s.Exists("/boot/grub/grub.cfg") || s.Exists("/boot/grub2/grub.cfg")):
		if s.R.Has("update-grub") && grubSourcesDropins(s) {
			return grubDropin{s}
		}
		return grubDefault{s}
	case s.R.Has("bootctl") && (s.Exists("/etc/kernel/cmdline") || len(s.loaderEntries()) > 0):
		return systemdBoot{s}
	}
	return nil
}

// grubSourcesDropins — grub-mkconfig в Debian и Ubuntu читает /etc/default/grub.d/*.cfg.
func grubSourcesDropins(s Sys) bool {
	for _, p := range []string{"/usr/sbin/grub-mkconfig", "/sbin/grub-mkconfig", "/usr/bin/grub-mkconfig"} {
		if b, err := s.Read(p); err == nil && strings.Contains(b, "default/grub.d") {
			return true
		}
	}
	return false
}

type grubby struct{ s Sys }

func (grubby) Name() string { return "grubby" }

func (g grubby) Set(params map[string]string) error {
	for _, k := range sortedKeys(params) {
		var arg string
		if v := params[k]; v == "" {
			arg = "--remove-args=" + k
		} else {
			arg = "--args=" + k + "=" + v // заменяет прежнее значение того же ключа
		}
		if err := g.s.R.Run(g.s.Log, "grubby", "--update-kernel=ALL", arg); err != nil {
			return err
		}
	}
	return nil
}

func (g grubby) Get(keys ...string) (map[string]string, error) {
	out, err := g.s.R.Output("grubby", "--info=DEFAULT")
	if err != nil {
		return nil, err
	}
	return GetTokens(strings.ReplaceAll(string(out), `"`, " "), keys...), nil
}

type ostreeKargs struct{ s Sys }

func (ostreeKargs) Name() string { return "rpm-ostree kargs" }

func (o ostreeKargs) Get(keys ...string) (map[string]string, error) {
	out, err := o.s.R.Output("rpm-ostree", "kargs")
	if err != nil {
		return nil, err
	}
	return GetTokens(string(out), keys...), nil
}

func (o ostreeKargs) Set(params map[string]string) error {
	cur, err := o.Get(sortedKeys(params)...)
	if err != nil {
		return err
	}
	var args []string
	for _, k := range sortedKeys(params) {
		old, had := cur[k]
		switch v := params[k]; {
		case v == "" && had:
			args = append(args, "--delete="+k+"="+old)
		case v != "" && had && old != v:
			args = append(args, "--replace="+k+"="+v)
		case v != "" && !had:
			args = append(args, "--append="+k+"="+v)
		}
	}
	if len(args) == 0 {
		return nil
	}
	return o.s.R.Run(o.s.Log, "rpm-ostree", append([]string{"kargs"}, args...)...)
}

// grubDropin — Debian/Ubuntu: свой файл в /etc/default/grub.d, основной конфиг не трогаем.
type grubDropin struct{ s Sys }

const grubDropinFile = "/etc/default/grub.d/99-crtctl.cfg"

func (grubDropin) Name() string { return "GRUB (/etc/default/grub.d)" }

func (g grubDropin) Set(params map[string]string) error {
	cur, _ := g.Get(sortedKeys(params)...)
	for k, v := range params {
		cur[k] = v
	}
	var toks []string
	for _, k := range sortedKeys(cur) {
		if cur[k] != "" {
			toks = append(toks, k+"="+cur[k])
		}
	}
	if len(toks) == 0 {
		if err := g.s.Remove(grubDropinFile); err != nil {
			return err
		}
	} else {
		data := fmt.Sprintf("# crtctl: kernel parameters for CRT monitors\nGRUB_CMDLINE_LINUX_DEFAULT=\"${GRUB_CMDLINE_LINUX_DEFAULT} %s\"\n", strings.Join(toks, " "))
		if err := g.s.Write(grubDropinFile, data, 0o644); err != nil {
			return err
		}
	}
	return g.s.R.Run(g.s.Log, "update-grub")
}

func (g grubDropin) Get(keys ...string) (map[string]string, error) {
	data, err := g.s.Read(grubDropinFile)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return GetTokens(strings.NewReplacer(`"`, " ", "}", " ").Replace(data), keys...), nil
}

// grubDefault — остальные дистрибутивы с GRUB: правим GRUB_CMDLINE_LINUX_DEFAULT в /etc/default/grub.
type grubDefault struct{ s Sys }

const grubDefaultFile = "/etc/default/grub"

func (grubDefault) Name() string { return "GRUB (/etc/default/grub)" }

func (g grubDefault) Get(keys ...string) (map[string]string, error) {
	data, err := g.s.Read(grubDefaultFile)
	if err != nil {
		return nil, err
	}
	_, val, _, _ := grubLine(data)
	return GetTokens(val, keys...), nil
}

// grubLine находит GRUB_CMDLINE_LINUX_DEFAULT: номер строки (-1 — нет), значение и кавычку.
func grubLine(data string) (idx int, val string, quote string, lines []string) {
	lines = strings.Split(data, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(t, "GRUB_CMDLINE_LINUX_DEFAULT="); ok {
			q := ""
			if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
				q = v[:1]
				v = strings.TrimSuffix(v[1:], q)
			}
			return i, v, q, lines
		}
	}
	return -1, "", `"`, lines
}

func (g grubDefault) Set(params map[string]string) error {
	data, err := g.s.Read(grubDefaultFile)
	if err != nil {
		return err
	}
	g.s.backup(grubDefaultFile)
	idx, val, q, lines := grubLine(data)
	if q == "" {
		q = `"`
	}
	line := "GRUB_CMDLINE_LINUX_DEFAULT=" + q + SetTokens(val, params) + q
	if idx >= 0 {
		lines[idx] = line
	} else {
		lines = append(lines, line)
	}
	if err := g.s.Write(grubDefaultFile, strings.Join(lines, "\n"), 0o644); err != nil {
		return err
	}
	if g.s.R.Has("update-grub") {
		return g.s.R.Run(g.s.Log, "update-grub")
	}
	for _, c := range []struct{ tool, cfg string }{{"grub2-mkconfig", "/boot/grub2/grub.cfg"}, {"grub-mkconfig", "/boot/grub/grub.cfg"}, {"grub2-mkconfig", "/boot/grub/grub.cfg"}} {
		if g.s.R.Has(c.tool) && g.s.Exists(c.cfg) {
			return g.s.R.Run(g.s.Log, c.tool, "-o", c.cfg)
		}
	}
	return i18n.Errorf("%s was updated, but no grub-mkconfig was found to regenerate the GRUB menu", grubDefaultFile)
}

// systemdBoot — /etc/kernel/cmdline (для будущих ядер и UKI) плюс уже созданные записи меню.
type systemdBoot struct{ s Sys }

func (systemdBoot) Name() string { return "systemd-boot" }

func (s Sys) loaderEntries() []string {
	var out []string
	for _, root := range []string{"/boot", "/efi", "/boot/efi"} {
		out = append(out, s.Glob(root+"/loader/entries/*.conf")...)
	}
	return out
}

func (sb systemdBoot) Get(keys ...string) (map[string]string, error) {
	if data, err := sb.s.Read("/etc/kernel/cmdline"); err == nil {
		return GetTokens(data, keys...), nil
	}
	for _, e := range sb.s.loaderEntries() {
		data, _ := sb.s.Read(e)
		for _, l := range strings.Split(data, "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "options"); ok {
				return GetTokens(v, keys...), nil
			}
		}
	}
	return map[string]string{}, nil
}

func (sb systemdBoot) Set(params map[string]string) error {
	base, err := sb.s.Read("/etc/kernel/cmdline")
	if err != nil {
		// Файла нет — kernel-install берёт параметры текущей загрузки; фиксируем их явно
		cur, _ := sb.s.Read("/proc/cmdline")
		var toks []string
		for _, t := range strings.Fields(cur) {
			if !strings.HasPrefix(t, "BOOT_IMAGE=") && !strings.HasPrefix(t, "initrd=") {
				toks = append(toks, t)
			}
		}
		base = strings.Join(toks, " ")
	}
	if err := sb.s.Write("/etc/kernel/cmdline", SetTokens(base, params)+"\n", 0o644); err != nil {
		return err
	}
	for _, e := range sb.s.loaderEntries() {
		data, err := sb.s.Read(e)
		if err != nil {
			continue
		}
		lines := strings.Split(data, "\n")
		changed := false
		for i, l := range lines {
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "options"); ok {
				lines[i] = "options " + SetTokens(v, params)
				changed = true
			}
		}
		if changed {
			sb.s.backup(e)
			if err := sb.s.Write(e, strings.Join(lines, "\n"), 0o644); err != nil {
				return err
			}
			fmt.Fprintln(sb.s.Log, "  "+i18n.T("updated %s", e))
		}
	}
	return nil
}

// kernelstub — Pop!_OS.
type kernelstub struct{ s Sys }

func (kernelstub) Name() string { return "kernelstub" }

func (k kernelstub) Get(keys ...string) (map[string]string, error) {
	data, err := k.s.Read("/etc/kernelstub/configuration")
	if err != nil {
		return nil, err
	}
	// Конфигурация — JSON; параметры лежат строками в "kernel_options"
	return GetTokens(strings.NewReplacer(`"`, " ", ",", " ", "[", " ", "]", " ").Replace(data), keys...), nil
}

func (k kernelstub) Set(params map[string]string) error {
	cur, err := k.Get(sortedKeys(params)...)
	if err != nil {
		return err
	}
	for _, key := range sortedKeys(params) {
		if old, ok := cur[key]; ok {
			if err := k.s.R.Run(k.s.Log, "kernelstub", "-d", key+"="+old); err != nil {
				return err
			}
		}
		if v := params[key]; v != "" {
			if err := k.s.R.Run(k.s.Log, "kernelstub", "-a", key+"="+v); err != nil {
				return err
			}
		}
	}
	return nil
}

// Describe — коротко о том, чем будет делаться постоянная установка (для doctor и info).
func Describe(s Sys) string {
	in, cl := DetectInitramfs(s), DetectCmdline(s)
	a, b := i18n.T("unknown"), i18n.T("unknown")
	if in != nil {
		a = in.Name()
	}
	if cl != nil {
		b = cl.Name()
	}
	return i18n.T("initramfs: %s, kernel parameters: %s", a, b)
}
