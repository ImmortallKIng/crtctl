package system

import (
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
)

// Кастомные режимы KDE Plasma (6.6+) через kscreen-doctor. KWin считает их по CVT
// и хранит отдельно для каждого монитора (по EDID).

type KDEMode struct {
	ID        string
	W, H      int
	Hz        float64
	Current   bool
	Preferred bool
}

type KDEOutput struct {
	Name        string
	Enabled     bool
	Connected   bool
	Modes       []KDEMode
	CustomModes []string // в порядке индексов для removeCustomMode
}

var (
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	modeRe = regexp.MustCompile(`^(\d+):(\d+)x(\d+)@([\d.]+)([*!]*)$`)
	custRe = regexp.MustCompile(`^\s*(\d+): (\S+)$`)
)

func ParseKscreen(out string) []KDEOutput {
	var res []KDEOutput
	var cur *KDEOutput
	inCustom := false
	for _, line := range strings.Split(ansiRe.ReplaceAllString(out, ""), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "Output:"):
			f := strings.Fields(t)
			if len(f) >= 3 {
				res = append(res, KDEOutput{Name: f[2]})
				cur = &res[len(res)-1]
			}
			inCustom = false
		case cur == nil:
		case t == "enabled":
			cur.Enabled = true
		case t == "connected":
			cur.Connected = true
		case strings.HasPrefix(t, "Modes:"):
			inCustom = false
			for _, f := range strings.Fields(strings.TrimPrefix(t, "Modes:")) {
				if m := modeRe.FindStringSubmatch(f); m != nil {
					w, _ := strconv.Atoi(m[2])
					h, _ := strconv.Atoi(m[3])
					hz, _ := strconv.ParseFloat(m[4], 64)
					cur.Modes = append(cur.Modes, KDEMode{ID: m[1], W: w, H: h, Hz: hz,
						Current: strings.Contains(m[5], "*"), Preferred: strings.Contains(m[5], "!")})
				}
			}
		case strings.HasPrefix(t, "Custom modes:"):
			inCustom = !strings.Contains(t, "None")
		case inCustom && custRe.MatchString(line):
			cur.CustomModes = append(cur.CustomModes, custRe.FindStringSubmatch(line)[2])
		default:
			inCustom = false
		}
	}
	return res
}

func kscreen(args ...string) (string, error) {
	if _, err := exec.LookPath("kscreen-doctor"); err != nil {
		return "", i18n.Errorf("kscreen-doctor not found: custom KDE modes need Plasma 6.6 or newer")
	}
	out, err := exec.Command("kscreen-doctor", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("kscreen-doctor %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

func KDEOutputs() ([]KDEOutput, error) {
	out, err := kscreen("-o")
	if err != nil {
		return nil, err
	}
	return ParseKscreen(out), nil
}

func KDEOutputByName(name string) (*KDEOutput, error) {
	outs, err := KDEOutputs()
	if err != nil {
		return nil, err
	}
	for i := range outs {
		if outs[i].Name == name {
			return &outs[i], nil
		}
	}
	return nil, i18n.Errorf("KDE does not see output %s", name)
}

// KDEAddModes добавляет режимы одной командой (KWin применяет их разом).
func KDEAddModes(conn string, reqs [][3]float64) error {
	var args []string
	for _, r := range reqs {
		args = append(args, fmt.Sprintf("output.%s.addCustomMode.%d.%d.%d.full", conn, int(r[0]), int(r[1]), int(r[2]*1000+0.5)))
	}
	_, err := kscreen(args...)
	return err
}

// KDERemoveCustom удаляет кастомные режимы по индексам (с конца, чтобы индексы не сдвигались).
func KDERemoveCustom(conn string, idx []int) error {
	idx = append([]int(nil), idx...)
	sort.Ints(idx)
	for i := len(idx) - 1; i >= 0; i-- {
		if _, err := kscreen(fmt.Sprintf("output.%s.removeCustomMode.%d", conn, idx[i])); err != nil {
			return err
		}
	}
	return nil
}

func KDESetMode(conn, id string) error {
	_, err := kscreen(fmt.Sprintf("output.%s.mode.%s", conn, id))
	return err
}
