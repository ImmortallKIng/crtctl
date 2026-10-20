package boot

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner запоминает команды и отдаёт заготовленный вывод.
type fakeRunner struct {
	have    map[string]bool
	outputs map[string]string
	calls   []string
}

func (f *fakeRunner) Run(_ io.Writer, name string, args ...string) error {
	f.calls = append(f.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return nil
}

func (f *fakeRunner) Output(name string, args ...string) ([]byte, error) {
	return []byte(f.outputs[strings.TrimSpace(name+" "+strings.Join(args, " "))]), nil
}

func (f *fakeRunner) Has(name string) bool { return f.have[name] }

func newSys(t *testing.T, files map[string]string, cmds ...string) (Sys, *fakeRunner) {
	t.Helper()
	root := t.TempDir()
	for p, data := range files {
		full := filepath.Join(root, p)
		if strings.HasSuffix(p, "/") {
			os.MkdirAll(full, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := &fakeRunner{have: map[string]bool{}, outputs: map[string]string{}}
	for _, c := range cmds {
		r.have[c] = true
	}
	return Sys{Root: root, R: r, Log: io.Discard}, r
}

func read(t *testing.T, s Sys, p string) string {
	t.Helper()
	d, err := s.Read(p)
	if err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return d
}

func calls(r *fakeRunner) string { return strings.Join(r.calls, "\n") }

const arg = "DP-2:edid/crtctl-DP-2.bin"

func TestSetTokens(t *testing.T) {
	got := SetTokens("ro quiet drm.edid_firmware=old splash", map[string]string{"drm.edid_firmware": "new"})
	if got != "ro quiet splash drm.edid_firmware=new" {
		t.Fatal(got)
	}
	if got := SetTokens("ro drm.edid_firmware=old", map[string]string{"drm.edid_firmware": ""}); got != "ro" {
		t.Fatal(got)
	}
}

func TestFedora(t *testing.T) {
	s, r := newSys(t, map[string]string{"/etc/dracut.conf.d/": "", "/usr/lib/firmware/": ""}, "dracut", "grubby")
	in, cl := DetectInitramfs(s), DetectCmdline(s)
	if in.Name() != "dracut" || cl.Name() != "grubby" {
		t.Fatalf("%s %s", in.Name(), cl.Name())
	}
	if dir, cp := s.FirmwareDir(); dir != "/usr/lib/firmware/edid" || cp != "" {
		t.Fatal(dir, cp)
	}
	in.Sync([]string{"/usr/lib/firmware/edid/crtctl-DP-2.bin"})
	if !strings.Contains(read(t, s, dracutConf), `install_items+=" /usr/lib/firmware/edid/crtctl-DP-2.bin "`) {
		t.Fatal(read(t, s, dracutConf))
	}
	cl.Set(map[string]string{"drm.edid_firmware": arg})
	in.Rebuild(true)
	want := "grubby --update-kernel=ALL --args=drm.edid_firmware=" + arg + "\ndracut -f --regenerate-all"
	if calls(r) != want {
		t.Fatalf("calls:\n%s", calls(r))
	}
	in.Sync(nil)
	if s.Exists(dracutConf) {
		t.Fatal("dracut conf should be removed")
	}
}

func TestUbuntu(t *testing.T) {
	s, r := newSys(t, map[string]string{
		"/etc/default/grub":       "GRUB_CMDLINE_LINUX_DEFAULT=\"quiet splash\"\n",
		"/boot/grub/grub.cfg":     "",
		"/usr/sbin/grub-mkconfig": "for x in ${sysconfdir}/default/grub.d/*.cfg ; do",
		"/usr/lib/firmware/":      "",
	}, "update-initramfs", "update-grub", "uname")
	r.outputs["uname -r"] = "6.8.0-45-generic\n"
	in, cl := DetectInitramfs(s), DetectCmdline(s)
	if in.Name() != "initramfs-tools" || !strings.Contains(cl.Name(), "grub.d") {
		t.Fatalf("%s / %s", in.Name(), cl.Name())
	}
	in.Sync([]string{"/usr/lib/firmware/edid/crtctl-DP-2.bin"})
	hook := read(t, s, initramfsHook)
	if !strings.Contains(hook, `cp "/usr/lib/firmware/edid/crtctl-DP-2.bin" "${DESTDIR}/lib/firmware/edid/"`) || !strings.Contains(hook, "hook-functions") {
		t.Fatal(hook)
	}
	if st, _ := os.Stat(s.P(initramfsHook)); st.Mode()&0o111 == 0 {
		t.Fatal("hook must be executable")
	}
	cl.Set(map[string]string{"drm.edid_firmware": arg})
	if got := read(t, s, grubDropinFile); !strings.Contains(got, `GRUB_CMDLINE_LINUX_DEFAULT="${GRUB_CMDLINE_LINUX_DEFAULT} drm.edid_firmware=`+arg+`"`) {
		t.Fatal(got)
	}
	if v, _ := cl.Get("drm.edid_firmware"); v["drm.edid_firmware"] != arg {
		t.Fatalf("Get: %v", v)
	}
	if read(t, s, "/etc/default/grub") != "GRUB_CMDLINE_LINUX_DEFAULT=\"quiet splash\"\n" {
		t.Fatal("main grub config must stay untouched")
	}
	in.Rebuild(false)
	cl.Set(map[string]string{"drm.edid_firmware": ""})
	if s.Exists(grubDropinFile) {
		t.Fatal("drop-in should be removed")
	}
	if calls(r) != "update-grub\nupdate-initramfs -u -k 6.8.0-45-generic\nupdate-grub" {
		t.Fatalf("calls:\n%s", calls(r))
	}
}

func TestArchGrub(t *testing.T) {
	s, r := newSys(t, map[string]string{
		"/etc/default/grub":      "GRUB_TIMEOUT=5\nGRUB_CMDLINE_LINUX_DEFAULT='loglevel=3 quiet drm.edid_firmware=old.bin'\n",
		"/boot/grub/grub.cfg":    "",
		"/usr/bin/grub-mkconfig": "#!/bin/sh\n. /etc/default/grub\n",
		"/usr/lib/firmware/":     "",
	}, "mkinitcpio", "grub-mkconfig")
	in, cl := DetectInitramfs(s), DetectCmdline(s)
	if in.Name() != "mkinitcpio" || !strings.Contains(cl.Name(), "/etc/default/grub)") {
		t.Fatalf("%s / %s", in.Name(), cl.Name())
	}
	in.Sync([]string{"/usr/lib/firmware/edid/crtctl-DP-2.bin"})
	if got := read(t, s, mkinitcpioConf); !strings.Contains(got, "FILES+=(/usr/lib/firmware/edid/crtctl-DP-2.bin)") {
		t.Fatal(got)
	}
	cl.Set(map[string]string{"drm.edid_firmware": arg})
	got := read(t, s, "/etc/default/grub")
	if !strings.Contains(got, "GRUB_CMDLINE_LINUX_DEFAULT='loglevel=3 quiet drm.edid_firmware="+arg+"'") || !strings.Contains(got, "GRUB_TIMEOUT=5") {
		t.Fatal(got)
	}
	if !s.Exists("/etc/default/grub.crtctl-backup") {
		t.Fatal("backup missing")
	}
	in.Rebuild(true)
	if calls(r) != "grub-mkconfig -o /boot/grub/grub.cfg\nmkinitcpio -P" {
		t.Fatalf("calls:\n%s", calls(r))
	}
}

func TestArchSystemdBoot(t *testing.T) {
	s, r := newSys(t, map[string]string{
		"/boot/loader/entries/arch.conf": "title Arch\nlinux /vmlinuz-linux\noptions root=UUID=1 rw quiet\n",
		"/proc/cmdline":                  "initrd=\\initramfs-linux.img root=UUID=1 rw quiet",
		"/usr/lib/firmware/":             "",
	}, "mkinitcpio", "bootctl")
	cl := DetectCmdline(s)
	if cl == nil || cl.Name() != "systemd-boot" {
		t.Fatalf("%v", cl)
	}
	cl.Set(map[string]string{"drm.edid_firmware": arg})
	if got := read(t, s, "/etc/kernel/cmdline"); got != "root=UUID=1 rw quiet drm.edid_firmware="+arg+"\n" {
		t.Fatalf("%q", got)
	}
	if got := read(t, s, "/boot/loader/entries/arch.conf"); !strings.Contains(got, "options root=UUID=1 rw quiet drm.edid_firmware="+arg) {
		t.Fatal(got)
	}
	cl.Set(map[string]string{"drm.edid_firmware": ""})
	if got := read(t, s, "/boot/loader/entries/arch.conf"); strings.Contains(got, "edid") {
		t.Fatal(got)
	}
	if len(r.calls) != 0 {
		t.Fatal(calls(r))
	}
}

func TestPopOS(t *testing.T) {
	s, r := newSys(t, map[string]string{
		"/etc/kernelstub/configuration": `{"user": {"kernel_options": ["quiet", "splash", "drm.edid_firmware=old.bin"]}}`,
		"/usr/lib/firmware/":            "",
	}, "kernelstub", "update-initramfs")
	cl := DetectCmdline(s)
	if cl.Name() != "kernelstub" {
		t.Fatal(cl.Name())
	}
	cl.Set(map[string]string{"drm.edid_firmware": arg})
	if calls(r) != "kernelstub -d drm.edid_firmware=old.bin\nkernelstub -a drm.edid_firmware="+arg {
		t.Fatalf("calls:\n%s", calls(r))
	}
}

func TestSilverblue(t *testing.T) {
	s, r := newSys(t, map[string]string{"/run/ostree-booted": "", "/usr/lib/firmware/": ""}, "rpm-ostree", "dracut")
	r.outputs["rpm-ostree kargs"] = "rhgb quiet root=UUID=1 ostree=/ostree/boot.1/fedora/abc/0\n"
	in, cl := DetectInitramfs(s), DetectCmdline(s)
	if !strings.Contains(in.Name(), "ostree") || !strings.Contains(cl.Name(), "ostree") {
		t.Fatalf("%s / %s", in.Name(), cl.Name())
	}
	dir, cp := s.FirmwareDir()
	if dir != "/etc/crtctl/firmware/edid" || cp != "/etc/crtctl/firmware" {
		t.Fatal(dir, cp)
	}
	in.Sync([]string{"/etc/crtctl/firmware/edid/crtctl-DP-2.bin"})
	cl.Set(map[string]string{"drm.edid_firmware": arg, "firmware_class.path": cp})
	want := "rpm-ostree initramfs-etc --track=/etc/crtctl/firmware/edid/crtctl-DP-2.bin\n" +
		"rpm-ostree kargs --append=drm.edid_firmware=" + arg + " --append=firmware_class.path=/etc/crtctl/firmware"
	if calls(r) != want {
		t.Fatalf("calls:\n%s", calls(r))
	}
	r.calls = nil
	in.Sync(nil)
	if calls(r) != "rpm-ostree initramfs-etc --untrack=/etc/crtctl/firmware/edid/crtctl-DP-2.bin" {
		t.Fatalf("calls:\n%s", calls(r))
	}
}

func TestOpenSUSE(t *testing.T) {
	s, r := newSys(t, map[string]string{
		"/etc/default/grub":    "GRUB_CMDLINE_LINUX_DEFAULT=\"splash=silent quiet\"\n",
		"/boot/grub2/grub.cfg": "",
		"/etc/dracut.conf.d/":  "",
		"/usr/lib/firmware/":   "",
	}, "dracut", "grub2-mkconfig")
	cl := DetectCmdline(s)
	cl.Set(map[string]string{"drm.edid_firmware": arg})
	if calls(r) != "grub2-mkconfig -o /boot/grub2/grub.cfg" {
		t.Fatalf("calls:\n%s", calls(r))
	}
}

func TestNothingDetected(t *testing.T) {
	s, _ := newSys(t, map[string]string{"/etc/NIXOS": ""})
	if DetectInitramfs(s) != nil || DetectCmdline(s) != nil || !s.NixOS() {
		t.Fatal("NixOS must not be auto-configured")
	}
}
