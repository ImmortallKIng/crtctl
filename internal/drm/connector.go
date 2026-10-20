// Package drm находит коннекторы видеокарты через sysfs и читает режимы через KMS.
package drm

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
)

const sysDRM = "/sys/class/drm"

type Connector struct {
	Name     string // "DP-2"
	Card     string // "card1"
	SysPath  string
	Status   string // connected / disconnected / unknown
	Enabled  bool
	EDID     []byte // то, что видит ядро
	Modes    []string
	ID       uint32 // KMS connector_id
	DDCBus   int    // номер /dev/i2c-N, по которому идёт EDID; -1 — нет
	AuxDev   string // /dev/drm_dp_auxN для DP, иначе ""
	PCI      string // 0000:28:00.0
	CardPath string // /dev/dri/card1
}

func (c Connector) Connected() bool { return c.Status == "connected" }
func (c Connector) IsDP() bool {
	return strings.HasPrefix(c.Name, "DP-") || strings.HasPrefix(c.Name, "eDP-")
}

// DebugfsDir — каталог коннектора в debugfs (нужен root).
func (c Connector) DebugfsDir() string {
	for _, d := range []string{c.PCI, strings.TrimPrefix(c.Card, "card")} {
		p := filepath.Join("/sys/kernel/debug/dri", d, c.Name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join("/sys/kernel/debug/dri", c.PCI, c.Name)
}

func readTrim(p string) string {
	b, _ := os.ReadFile(p)
	return strings.TrimSpace(string(b))
}

// List возвращает все коннекторы всех видеокарт, кроме writeback.
func List() ([]Connector, error) {
	ents, err := os.ReadDir(sysDRM)
	if err != nil {
		return nil, err
	}
	var out []Connector
	for _, e := range ents {
		card, name, ok := strings.Cut(e.Name(), "-")
		if !ok || !strings.HasPrefix(card, "card") || strings.HasPrefix(name, "Writeback") {
			continue
		}
		p := filepath.Join(sysDRM, e.Name())
		c := Connector{Name: name, Card: card, SysPath: p, DDCBus: -1, CardPath: "/dev/dri/" + card}
		c.Status = readTrim(filepath.Join(p, "status"))
		c.Enabled = readTrim(filepath.Join(p, "enabled")) == "enabled"
		c.EDID, _ = os.ReadFile(filepath.Join(p, "edid"))
		if m := readTrim(filepath.Join(p, "modes")); m != "" {
			c.Modes = strings.Split(m, "\n")
		}
		if id, err := strconv.ParseUint(readTrim(filepath.Join(p, "connector_id")), 10, 32); err == nil {
			c.ID = uint32(id)
		}
		if dev, err := filepath.EvalSymlinks(filepath.Join(sysDRM, card, "device")); err == nil {
			c.PCI = filepath.Base(dev)
		}
		c.DDCBus, c.AuxDev = findBuses(p)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Card != out[j].Card {
			return out[i].Card < out[j].Card
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// findBuses: у DP EDID идёт через AUX — его i2c-шина лежит прямо в каталоге коннектора.
// Симлинк ddc у DP указывает на «аппаратную» шину, которой DP не пользуется.
func findBuses(p string) (int, string) {
	bus, aux := -1, ""
	ents, _ := os.ReadDir(p)
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, "i2c-") {
			if v, err := strconv.Atoi(strings.TrimPrefix(n, "i2c-")); err == nil {
				bus = v
			}
		}
		if strings.HasPrefix(n, "drm_dp_aux") {
			aux = "/dev/" + n
		}
	}
	if bus < 0 {
		if t, err := filepath.EvalSymlinks(filepath.Join(p, "ddc")); err == nil {
			if v, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(t), "i2c-")); err == nil {
				bus = v
			}
		}
	}
	return bus, aux
}

// Find ищет коннектор по имени: "DP-2" или "card1-DP-2".
func Find(name string) (Connector, error) {
	all, err := List()
	if err != nil {
		return Connector{}, err
	}
	var hits []Connector
	for _, c := range all {
		if c.Name == name || c.Card+"-"+c.Name == name {
			hits = append(hits, c)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		var names []string
		for _, c := range all {
			names = append(names, c.Name)
		}
		return Connector{}, i18n.Errorf("connector %q not found (available: %s)", name, strings.Join(names, ", "))
	}
	return Connector{}, i18n.Errorf("connector %q exists on several cards; use the full name, e.g. card1-%s", name, name)
}
