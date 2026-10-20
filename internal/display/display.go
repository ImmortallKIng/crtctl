// Package display временно включает произвольный режим через графическую сессию:
// KDE (kscreen-doctor), X11 (xrandr), Sway (swaymsg), Hyprland (hyprctl).
package display

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
	"github.com/ImmortallKIng/crtctl/internal/sysenv"
	"github.com/ImmortallKIng/crtctl/internal/system"
	"github.com/ImmortallKIng/crtctl/internal/timing"
)

// Session — включённый пробный режим: Restore возвращает прежний, Cleanup убирает
// добавленный режим из списка (если его не решили оставить).
type Session struct {
	Shown   string // как режим называется в этой среде
	Restore func() error
	Cleanup func() error
}

// Backend — способ включить режим в графической сессии.
type Backend interface {
	Name() string
	// Exact — умеет ли среда задать точные тайминги (иначе только CVT).
	Exact() bool
	Apply(output string, connectorID uint32, kernelEDID []byte, m timing.Mode, requestedHz float64) (*Session, error)
}

// Detect — бэкенд для текущей сессии или nil (тогда остаётся временный EDID).
func Detect(s sysenv.Session) Backend {
	switch s.Kind {
	case "kde":
		if sysenv.HasCmd("kscreen-doctor") != "" {
			return kde{}
		}
	case "x11":
		if sysenv.HasCmd("xrandr") != "" {
			return xrandr{}
		}
	case "sway":
		if sysenv.HasCmd("swaymsg") != "" {
			return sway{}
		}
	case "hyprland":
		if sysenv.HasCmd("hyprctl") != "" {
			return hyprland{}
		}
	}
	return nil
}

// ByName — бэкенд по имени (для --via).
func ByName(name string) Backend {
	switch name {
	case "kde":
		return kde{}
	case "x11", "xrandr":
		return xrandr{}
	case "sway":
		return sway{}
	case "hyprland":
		return hyprland{}
	}
	return nil
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// --- KDE ---

type kde struct{}

func (kde) Name() string { return "KDE (kscreen-doctor)" }
func (kde) Exact() bool  { return false }

func (kde) Apply(output string, _ uint32, _ []byte, m timing.Mode, hz float64) (*Session, error) {
	before, err := system.KDEOutputByName(output)
	if err != nil {
		return nil, err
	}
	prev := ""
	for _, x := range before.Modes {
		if x.Current {
			prev = x.ID
		}
	}
	existing := findKDE(before, m.HActive, m.VActive, m.VFreq())
	added := false
	if existing == nil {
		if err := system.KDEAddModes(output, [][3]float64{{float64(m.HActive), float64(m.VActive), hz}}); err != nil {
			return nil, err
		}
		added = true
	}
	after, err := system.KDEOutputByName(output)
	if err != nil {
		return nil, err
	}
	target := findKDE(after, m.HActive, m.VActive, m.VFreq())
	if target == nil {
		return nil, i18n.Errorf("KWin did not create the mode %s", m.Name())
	}
	if err := system.KDESetMode(output, target.ID); err != nil {
		return nil, err
	}
	s := &Session{Shown: fmt.Sprintf("%dx%d@%.2f", target.W, target.H, target.Hz)}
	s.Restore = func() error {
		if prev == "" {
			return nil
		}
		return system.KDESetMode(output, prev)
	}
	s.Cleanup = func() error {
		if !added {
			return nil
		}
		out, err := system.KDEOutputByName(output)
		if err != nil {
			return err
		}
		want := fmt.Sprintf("%dx%d@", target.W, target.H)
		for i, c := range out.CustomModes {
			if strings.HasPrefix(c, want) {
				if hz, err := strconv.ParseFloat(strings.TrimPrefix(c, want), 64); err == nil && math.Abs(hz-target.Hz) < 0.15 {
					return system.KDERemoveCustom(output, []int{i})
				}
			}
		}
		return nil
	}
	return s, nil
}

func findKDE(o *system.KDEOutput, w, h int, hz float64) *system.KDEMode {
	var best *system.KDEMode
	for i := range o.Modes {
		m := &o.Modes[i]
		if m.W == w && m.H == h && math.Abs(m.Hz-hz) < 0.6 && (best == nil || math.Abs(m.Hz-hz) < math.Abs(best.Hz-hz)) {
			best = m
		}
	}
	return best
}

// --- X11 ---

type xrandr struct{}

func (xrandr) Name() string { return "X11 (xrandr)" }
func (xrandr) Exact() bool  { return true }

// XOutput — выход из xrandr --verbose.
type XOutput struct {
	Name        string
	Connected   bool
	ConnectorID uint32
	EDID        string // hex
	CurrentID   string // XID текущего режима, например 0x4a
}

var (
	xOutRe   = regexp.MustCompile(`^(\S+) (connected|disconnected)`)
	xModeRe  = regexp.MustCompile(`^\s+\S+ \((0x[0-9a-f]+)\) .*\*current`)
	xConnRe  = regexp.MustCompile(`^\s+CONNECTOR_ID:\s+(\d+)`)
	xEdidRe  = regexp.MustCompile(`^\s+EDID:\s*$`)
	xHexLine = regexp.MustCompile(`^\s+([0-9a-f]{32})\s*$`)
)

// ParseXrandrVerbose разбирает xrandr --verbose.
func ParseXrandrVerbose(out string) []XOutput {
	var res []XOutput
	var cur *XOutput
	inEDID := false
	for _, l := range strings.Split(out, "\n") {
		if m := xOutRe.FindStringSubmatch(l); m != nil {
			res = append(res, XOutput{Name: m[1], Connected: m[2] == "connected"})
			cur = &res[len(res)-1]
			inEDID = false
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case inEDID && xHexLine.MatchString(l):
			cur.EDID += xHexLine.FindStringSubmatch(l)[1]
			continue
		case xEdidRe.MatchString(l):
			inEDID = true
			continue
		}
		inEDID = false
		if m := xConnRe.FindStringSubmatch(l); m != nil {
			n, _ := strconv.ParseUint(m[1], 10, 32)
			cur.ConnectorID = uint32(n)
		}
		if m := xModeRe.FindStringSubmatch(l); m != nil {
			cur.CurrentID = m[1]
		}
	}
	return res
}

// MatchXOutput — выход X11 для коннектора DRM: по CONNECTOR_ID (amdgpu, intel, modesetting),
// иначе по EDID (NVIDIA). По имени не сопоставляем: у NVIDIA имена в X другие (DP-0 вместо DP-1).
func MatchXOutput(outs []XOutput, drmName string, connectorID uint32, kernelEDID []byte) *XOutput {
	for i := range outs {
		if connectorID != 0 && outs[i].ConnectorID == connectorID {
			return &outs[i]
		}
	}
	if len(kernelEDID) > 0 {
		hex := fmt.Sprintf("%x", kernelEDID)
		for i := range outs {
			if outs[i].EDID != "" && strings.HasPrefix(hex, outs[i].EDID) || (outs[i].EDID != "" && strings.HasPrefix(outs[i].EDID, hex)) {
				return &outs[i]
			}
		}
	}
	return nil
}

func (xrandr) Apply(output string, connID uint32, kedid []byte, m timing.Mode, _ float64) (*Session, error) {
	verbose, err := run("xrandr", "--verbose")
	if err != nil {
		return nil, err
	}
	xo := MatchXOutput(ParseXrandrVerbose(verbose), output, connID, kedid)
	if xo == nil {
		return nil, i18n.Errorf("xrandr does not show an output for %s", output)
	}
	name := fmt.Sprintf("crtctl_%dx%d_%.2f", m.HActive, m.VActive, m.VFreq())
	_, _ = run("xrandr", "--delmode", xo.Name, name) // остатки прошлой попытки
	_, _ = run("xrandr", "--rmmode", name)
	if _, err := run("xrandr", append([]string{"--newmode", name}, m.XrandrArgs()...)...); err != nil {
		return nil, err
	}
	cleanup := func() error {
		_, _ = run("xrandr", "--delmode", xo.Name, name)
		_, err := run("xrandr", "--rmmode", name)
		return err
	}
	if _, err := run("xrandr", "--addmode", xo.Name, name); err != nil {
		cleanup()
		return nil, i18n.Errorf("the X server refused the mode (%v). With the NVIDIA driver add Option \"ModeValidation\" \"AllowNonEdidModes\" to xorg.conf", err)
	}
	if _, err := run("xrandr", "--output", xo.Name, "--mode", name); err != nil {
		cleanup()
		return nil, err
	}
	prev := xo.CurrentID
	return &Session{
		Shown: name,
		Restore: func() error {
			if prev == "" {
				return nil
			}
			_, err := run("xrandr", "--output", xo.Name, "--mode", prev)
			return err
		},
		Cleanup: cleanup,
	}, nil
}

// --- Sway ---

type sway struct{}

func (sway) Name() string { return "Sway (swaymsg)" }
func (sway) Exact() bool  { return true }

type swayOutput struct {
	Name        string `json:"name"`
	CurrentMode struct {
		Width   int `json:"width"`
		Height  int `json:"height"`
		Refresh int `json:"refresh"` // мГц
	} `json:"current_mode"`
}

// SwayCurrent — текущий режим выхода как строка для `output X mode`.
func SwayCurrent(jsonOut []byte, output string) (string, error) {
	var outs []swayOutput
	if err := json.Unmarshal(jsonOut, &outs); err != nil {
		return "", err
	}
	for _, o := range outs {
		if o.Name == output {
			c := o.CurrentMode
			return fmt.Sprintf("%dx%d@%.3fHz", c.Width, c.Height, float64(c.Refresh)/1000), nil
		}
	}
	return "", i18n.Errorf("sway does not show output %s", output)
}

func (sway) Apply(output string, _ uint32, _ []byte, m timing.Mode, _ float64) (*Session, error) {
	out, err := exec.Command("swaymsg", "-r", "-t", "get_outputs").Output()
	if err != nil {
		return nil, err
	}
	prev, err := SwayCurrent(out, output)
	if err != nil {
		return nil, err
	}
	if _, err := run("swaymsg", append([]string{"output", output, "modeline"}, m.XrandrArgs()...)...); err != nil {
		return nil, err
	}
	return &Session{
		Shown:   m.Name(),
		Restore: func() error { _, err := run("swaymsg", "output", output, "mode", prev); return err },
		Cleanup: func() error { return nil },
	}, nil
}

// --- Hyprland ---

type hyprland struct{}

func (hyprland) Name() string { return "Hyprland (hyprctl)" }
func (hyprland) Exact() bool  { return true }

type hyprMonitor struct {
	Name        string  `json:"name"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	RefreshRate float64 `json:"refreshRate"`
	X           int     `json:"x"`
	Y           int     `json:"y"`
	Scale       float64 `json:"scale"`
}

// HyprRule — текущее правило монитора и правило с пробным modeline.
func HyprRule(jsonOut []byte, output string, m timing.Mode) (prev, test string, err error) {
	var mons []hyprMonitor
	if err := json.Unmarshal(jsonOut, &mons); err != nil {
		return "", "", err
	}
	for _, x := range mons {
		if x.Name == output {
			pos := fmt.Sprintf("%dx%d", x.X, x.Y)
			scale := strconv.FormatFloat(x.Scale, 'f', -1, 64)
			prev = fmt.Sprintf("%s,%dx%d@%.3f,%s,%s", output, x.Width, x.Height, x.RefreshRate, pos, scale)
			test = fmt.Sprintf("%s,modeline %s,%s,%s", output, strings.Join(m.XrandrArgs(), " "), pos, scale)
			return prev, test, nil
		}
	}
	return "", "", i18n.Errorf("Hyprland does not show monitor %s", output)
}

func (hyprland) Apply(output string, _ uint32, _ []byte, m timing.Mode, _ float64) (*Session, error) {
	out, err := exec.Command("hyprctl", "monitors", "-j").Output()
	if err != nil {
		return nil, err
	}
	prev, test, err := HyprRule(out, output, m)
	if err != nil {
		return nil, err
	}
	res, err := run("hyprctl", "keyword", "monitor", test)
	if err != nil || !strings.Contains(strings.ToLower(res), "ok") {
		return nil, i18n.Errorf("Hyprland did not accept the modeline: %s", strings.TrimSpace(res))
	}
	return &Session{
		Shown:   m.Name(),
		Restore: func() error { _, err := run("hyprctl", "keyword", "monitor", prev); return err },
		Cleanup: func() error { return nil },
	}, nil
}

// XOutputsFor — выходы X-сервера (xrandr --verbose) по номеру дисплея и файлу авторизации.
func XOutputsFor(disp, auth string) ([]XOutput, error) {
	cmd := exec.Command("xrandr", "--verbose")
	cmd.Env = append(cmd.Environ(), "DISPLAY="+disp)
	if auth != "" {
		cmd.Env = append(cmd.Env, "XAUTHORITY="+auth)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return ParseXrandrVerbose(string(out)), nil
}

// KDEHas — есть ли у выхода KDE режим с таким разрешением и частотой (тогда он и будет включён).
func KDEHas(output string, w, h int, hz float64) bool {
	o, err := system.KDEOutputByName(output)
	return err == nil && findKDE(o, w, h, hz) != nil
}
