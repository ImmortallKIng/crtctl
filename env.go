package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/boot"
	"github.com/ImmortallKIng/crtctl/internal/drm"
	"github.com/ImmortallKIng/crtctl/internal/sysenv"
)

// systemSummary — одна строка о системе для doctor и отчётов об ошибках.
func systemSummary() string {
	osr := sysenv.OSRelease()
	var drivers []string
	seen := map[string]bool{}
	if all, err := drm.List(); err == nil {
		for _, c := range all {
			if d := sysenv.Driver(c.Card); d != "" && !seen[d] {
				seen[d] = true
				drivers = append(drivers, d)
			}
		}
	}
	sort.Strings(drivers)
	sess := sysenv.DetectSession().Kind
	if os.Geteuid() == 0 {
		sess = T("unknown under sudo")
	}
	return T("%s; driver: %s; session: %s; %s", osr.Name, strings.Join(drivers, ", "), sess, boot.Describe(boot.Real(io.Discard)))
}

// environmentFindings — проблемы окружения, общие для всех выходов.
func environmentFindings() []finding {
	var out []finding
	add := func(level, text string, fix ...string) { out = append(out, finding{level, text, fix}) }

	if loaded, enabled := sysenv.NvidiaModeset(); loaded && !enabled {
		add("✗", T("The NVIDIA driver runs without kernel modesetting (nvidia-drm.modeset=1 is off). Without it Linux does not see the outputs, and EDID tricks do not work. Add the kernel parameter and reboot:"),
			"nvidia-drm.modeset=1")
	}
	if v, err := os.ReadFile("/sys/module/nvidia/version"); err == nil {
		ver := strings.TrimSpace(string(v))
		if ver == "580.105.08" {
			add("⚠", T("NVIDIA driver %s has a known bug: custom EDIDs are ignored. Update to 580.119.02 or newer.", ver))
		}
		if sysenv.DetectSession().Kind == "x11" || (os.Geteuid() == 0 && len(sysenv.XorgServers()) > 0) {
			add("·", T("NVIDIA on X11: the X driver reads the EDID by itself and ignores the kernel override, so crtctl apply and setup also pass the EDID to it through xorg.conf.d (log out and back in afterwards)."))
		}
	}
	switch l := sysenv.Lockdown(); l {
	case "", "none":
	default:
		add("·", T("Kernel lockdown is on (%s, usually because of Secure Boot): debugfs is closed, so apply --test, reprobe, link and the service install method do not work. The permanent install through the kernel parameter still works.", l))
	}
	s := boot.Real(io.Discard)
	if s.NixOS() {
		add("·", T("NixOS: crtctl prints the configuration.nix lines instead of changing the boot setup."))
	} else if boot.DetectCmdline(s) == nil {
		add("⚠", T("The boot loader was not recognised, so the permanent install will use a systemd service (or manual steps)."))
	}
	return out
}

func printFindings(w io.Writer, fs []finding) {
	for _, f := range fs {
		fmt.Fprintf(w, "  %s %s\n", f.level, f.text)
		for _, x := range f.fix {
			fmt.Fprintf(w, "      $ %s\n", x)
		}
	}
}

// xOutputWithNotice — для NVIDIA на X11 имя выхода в X; если X запущен, а выход не найден, —
// подсказка, что сделать вручную.
func xOutputWithNotice(t *target) string {
	if t.Driver != "nvidia" || os.Geteuid() != 0 || len(sysenv.XorgServers()) == 0 {
		return ""
	}
	if name := nvidiaXOutput(t); name != "" {
		return name
	}
	fmt.Println("⚠ " + T("NVIDIA on X11: could not find which X output is %s (is xrandr installed?). To pass the EDID to the X driver, add Option \"CustomEDID\" \"OUTPUT:FILE\" to the Device section; the output name is shown by xrandr.", t.Conn.Name))
	return ""
}
