package system

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ImmortallKIng/crtctl/internal/boot"
	"github.com/ImmortallKIng/crtctl/internal/drm"
)

func TestCmdlineEDID(t *testing.T) {
	got := CmdlineEDID("ro quiet drm.edid_firmware=DP-2:edid/a.bin,HDMI-A-1:edid/b.bin amdgpu.x=1")
	want := map[string]string{"DP-2": "edid/a.bin", "HDMI-A-1": "edid/b.bin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if g := CmdlineEDID("drm_kms_helper.edid_firmware=edid/x.bin"); g["*"] != "edid/x.bin" {
		t.Fatalf("%v", g)
	}
	arg := FormatEDIDArg([][2]string{{"DP-2", "/usr/lib/firmware/edid/crtctl-DP-2.bin"}, {"HDMI-A-1", "/usr/lib/firmware/edid/b.bin"}})
	if arg != "DP-2:edid/crtctl-DP-2.bin,HDMI-A-1:edid/b.bin" {
		t.Fatal(arg)
	}
}

func TestParseKscreen(t *testing.T) {
	b, err := os.ReadFile("testdata/kscreen-o.txt")
	if err != nil {
		t.Fatal(err)
	}
	outs := ParseKscreen(string(b))
	if len(outs) != 2 {
		t.Fatalf("выходов %d", len(outs))
	}
	var tv, crt *KDEOutput
	for i := range outs {
		switch outs[i].Name {
		case "HDMI-A-1":
			tv = &outs[i]
		case "DP-2":
			crt = &outs[i]
		}
	}
	if tv == nil || crt == nil {
		t.Fatalf("%+v", outs)
	}
	if len(tv.CustomModes) != 3 || tv.CustomModes[0] != "768x576@49.81" || len(crt.CustomModes) != 0 {
		t.Errorf("custom: tv=%v crt=%v", tv.CustomModes, crt.CustomModes)
	}
	if !crt.Enabled || !crt.Connected || len(crt.Modes) < 10 {
		t.Errorf("crt: %+v", crt)
	}
	cur := 0
	for _, m := range crt.Modes {
		if m.Current {
			cur++
		}
	}
	if cur != 1 {
		t.Errorf("текущих режимов у DP-2: %d", cur)
	}
}

type fakeRunner struct {
	have  map[string]bool
	out   map[string]string
	calls []string
}

func (f *fakeRunner) Run(_ io.Writer, name string, args ...string) error {
	f.calls = append(f.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return nil
}
func (f *fakeRunner) Output(name string, args ...string) ([]byte, error) {
	return []byte(f.out[strings.TrimSpace(name+" "+strings.Join(args, " "))]), nil
}
func (f *fakeRunner) Has(name string) bool { return f.have[name] }

func fakeSys(t *testing.T, files map[string]string, cmds ...string) (boot.Sys, *fakeRunner) {
	t.Helper()
	root := t.TempDir()
	for p, d := range files {
		full := filepath.Join(root, p)
		if strings.HasSuffix(p, "/") {
			os.MkdirAll(full, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(d), 0o644)
	}
	r := &fakeRunner{have: map[string]bool{}, out: map[string]string{}}
	for _, c := range cmds {
		r.have[c] = true
	}
	return boot.Sys{Root: root, R: r, Log: io.Discard}, r
}

func TestPersistBootKeepsForeignEntry(t *testing.T) {
	s, r := fakeSys(t, map[string]string{
		"/etc/dracut.conf.d/": "", "/usr/lib/firmware/": "",
		"/proc/cmdline": "ro quiet drm.edid_firmware=HDMI-A-1:edid/tv.bin",
	}, "dracut", "grubby")
	arg := "DP-2:edid/crtctl-DP-2.bin,HDMI-A-1:edid/tv.bin"
	r.out["grubby --info=DEFAULT"] = `args="ro quiet drm.edid_firmware=` + arg + `"`
	st, _ := LoadStateFrom(filepath.Join(t.TempDir(), "state.json"))
	conn := drm.Connector{Name: "DP-2", Card: "card1", PCI: "0000:28:00.0", SysPath: "/sys/class/drm/card1-DP-2"}
	err := InstallPersistent(st, PersistOptions{Conn: conn, EDID: []byte("EDID"), MonitorID: "HWP 2845 1", Title: "HP", AllKernels: true, Sys: s})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := s.Read("/usr/lib/firmware/edid/crtctl-DP-2.bin"); b != "EDID" {
		t.Fatalf("firmware file: %q", b)
	}
	want := "grubby --update-kernel=ALL --args=drm.edid_firmware=" + arg + "\ndracut -f --regenerate-all"
	if got := strings.Join(r.calls, "\n"); got != want {
		t.Fatalf("calls:\n%s", got)
	}
	if c := st.Get("DP-2"); c.Method != MethodBoot || c.EDIDFile != "/usr/lib/firmware/edid/crtctl-DP-2.bin" {
		t.Fatalf("%+v", c)
	}

	// Удаление: своя запись уходит, чужая остаётся, конфиг dracut удалён
	r.calls = nil
	r.out["grubby --info=DEFAULT"] = `args="ro quiet drm.edid_firmware=HDMI-A-1:edid/tv.bin"`
	if err := RemovePersistent(st, "DP-2", s, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.calls, "\n"); got != "grubby --update-kernel=ALL --args=drm.edid_firmware=HDMI-A-1:edid/tv.bin\ndracut -f --regenerate-all" {
		t.Fatalf("calls:\n%s", got)
	}
	if s.Exists("/usr/lib/firmware/edid/crtctl-DP-2.bin") || s.Exists("/etc/dracut.conf.d/crtctl-edid.conf") {
		t.Fatal("files left behind")
	}
}

func TestPersistService(t *testing.T) {
	s, r := fakeSys(t, map[string]string{"/run/systemd/system/": "", "/sys/kernel/debug/dri/": "", "/proc/cmdline": "ro"})
	st, _ := LoadStateFrom(filepath.Join(t.TempDir(), "state.json"))
	conn := drm.Connector{Name: "DP-2", Card: "card1", PCI: "0000:28:00.0", SysPath: "/sys/class/drm/card1-DP-2"}
	if m, _ := ChooseMethod(s, ""); m != MethodService {
		t.Fatalf("without a known boot loader the method must be service, got %s", m)
	}
	if err := InstallPersistent(st, PersistOptions{Conn: conn, EDID: []byte("E"), Sys: s}); err != nil {
		t.Fatal(err)
	}
	script, _ := s.Read(serviceScript)
	if !strings.Contains(script, `apply "/sys/kernel/debug/dri/0000:28:00.0/DP-2" "/etc/crtctl/edid/crtctl-DP-2.bin" "/sys/class/drm/card1-DP-2"`) {
		t.Fatal(script)
	}
	if got := strings.Join(r.calls, "\n"); got != "systemctl daemon-reload\nsystemctl enable crtctl-edid.service" {
		t.Fatalf("calls:\n%s", got)
	}
	r.calls = nil
	if err := RemovePersistent(st, "DP-2", s, true); err != nil {
		t.Fatal(err)
	}
	if s.Exists(serviceUnit) || s.Exists(serviceScript) || s.Exists("/etc/crtctl/edid/crtctl-DP-2.bin") {
		t.Fatal("service files left behind")
	}
}

func TestPersistManualOnNixOS(t *testing.T) {
	s, _ := fakeSys(t, map[string]string{"/etc/NIXOS": "", "/proc/cmdline": ""}, "bootctl")
	st, _ := LoadStateFrom(filepath.Join(t.TempDir(), "state.json"))
	err := InstallPersistent(st, PersistOptions{Conn: drm.Connector{Name: "VGA-1"}, EDID: []byte("E"), Sys: s})
	if !errors.Is(err, ErrManual) {
		t.Fatalf("want ErrManual, got %v", err)
	}
}

func TestNvidiaX11Config(t *testing.T) {
	if got := PCIToX("0000:0a:00.0"); got != "PCI:10:0:0" {
		t.Fatal(got)
	}
	s, _ := fakeSys(t, map[string]string{"/etc/X11/xorg.conf.d/": ""})
	st, _ := LoadStateFrom(filepath.Join(t.TempDir(), "state.json"))
	c := st.Conn("DP-3")
	c.EDIDFile, c.XOutput, c.PCI = "/usr/lib/firmware/edid/crtctl-DP-3.bin", "DP-1", "0000:01:00.0"
	if err := SyncNvidiaX11(st, s); err != nil {
		t.Fatal(err)
	}
	conf, _ := s.Read(NvidiaXConf)
	for _, want := range []string{`Driver     "nvidia"`, `BusID      "PCI:1:0:0"`,
		`"CustomEDID"     "DP-1:/usr/lib/firmware/edid/crtctl-DP-3.bin"`, `"ModeValidation" "DP-1: AllowNonEdidModes"`} {
		if !strings.Contains(conf, want) {
			t.Errorf("нет %q в\n%s", want, conf)
		}
	}
	// Чужая секция Device для nvidia — свою не пишем, только инструкция
	s2, _ := fakeSys(t, map[string]string{"/etc/X11/xorg.conf": "Section \"Device\"\n  Identifier \"Card0\"\n  Driver \"nvidia\"\nEndSection\n"})
	if err := SyncNvidiaX11(st, s2); !errors.Is(err, ErrManual) || s2.Exists(NvidiaXConf) {
		t.Fatalf("with a foreign nvidia Device section: err=%v exists=%v", err, s2.Exists(NvidiaXConf))
	}
	// Закомментированная секция не мешает
	s3, _ := fakeSys(t, map[string]string{"/etc/X11/xorg.conf": "#Section \"Device\"\n#  Driver \"nvidia\"\n#EndSection\n"})
	if err := SyncNvidiaX11(st, s3); err != nil || !s3.Exists(NvidiaXConf) {
		t.Fatalf("commented section: %v", err)
	}
	c.XOutput = ""
	SyncNvidiaX11(st, s)
	if s.Exists(NvidiaXConf) {
		t.Fatal("config must be removed when nothing needs it")
	}
}
