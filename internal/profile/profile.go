// Package profile хранит характеристики мониторов и считает допустимые пределы режимов.
package profile

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
	"github.com/ImmortallKIng/crtctl/internal/timing"
)

//go:embed monitors/*.json
var builtin embed.FS

type Monitor struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Match     []string          `json:"match,omitempty"` // "HWP 2845" — производитель и модель из EDID
	HMin      float64           `json:"hmin_khz"`
	HMax      float64           `json:"hmax_khz"`
	VMin      float64           `json:"vmin_hz"`
	VMax      float64           `json:"vmax_hz"`
	Bandwidth float64           `json:"bandwidth_mhz,omitempty"` // полоса видеоусилителя: выше — мыло, но не вред
	MaxRes    string            `json:"max_res,omitempty"`
	Notes     string            `json:"notes,omitempty"`
	NameL10n  map[string]string `json:"name_l10n,omitempty"`  // перевод имени: {"ru": "..."}
	NotesL10n map[string]string `json:"notes_l10n,omitempty"` // перевод заметок
	Sources   []string          `json:"sources,omitempty"`
	File      string            `json:"-"`
}

// BuiltinFile — значение File у встроенных профилей.
var BuiltinFile = i18n.N("(built-in)")

// UserDir — каталог пользовательских профилей.
// Под sudo — каталог того пользователя, который вызвал sudo, а не root.
func UserDir() string {
	if os.Geteuid() == 0 {
		if name := os.Getenv("SUDO_USER"); name != "" && name != "root" {
			if u, err := user.Lookup(name); err == nil {
				return filepath.Join(u.HomeDir, ".config", "crtctl", "monitors")
			}
		}
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "crtctl", "monitors")
	}
	return ""
}

// SystemDir — профили для всех пользователей; сюда пишет crtctl setup.
const SystemDir = "/etc/crtctl/monitors"

// SaveSystem сохраняет профиль в SystemDir и возвращает путь.
func SaveSystem(m Monitor) (string, error) {
	if err := os.MkdirAll(SystemDir, 0o755); err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	path := filepath.Join(SystemDir, m.ID+".json")
	return path, os.WriteFile(path, append(b, '\n'), 0o644)
}

// Load загружает встроенные профили и пользовательские (последние переопределяют встроенные по id).
func Load() ([]Monitor, error) {
	byID := map[string]Monitor{}
	ents, _ := builtin.ReadDir("monitors")
	for _, e := range ents {
		b, _ := builtin.ReadFile("monitors/" + e.Name())
		var m Monitor
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, i18n.Errorf("built-in profile %s: %w", e.Name(), err)
		}
		m.File = BuiltinFile
		byID[m.ID] = m
	}
	for _, d := range []string{SystemDir, UserDir()} {
		if d == "" {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(d, "*.json"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			var m Monitor
			if err := json.Unmarshal(b, &m); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			if m.ID == "" {
				m.ID = strings.TrimSuffix(filepath.Base(f), ".json")
			}
			m.File = f
			byID[m.ID] = m
		}
	}
	var out []Monitor
	for _, m := range byID {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func Get(id string) (Monitor, error) {
	all, err := Load()
	if err != nil {
		return Monitor{}, err
	}
	for _, m := range all {
		if m.ID == id {
			return m, nil
		}
	}
	return Monitor{}, i18n.Errorf("profile %q not found (list: crtctl profiles)", id)
}

// Match ищет профиль по производителю и модели из EDID.
func Match(vendor string, product uint16) (Monitor, bool) {
	all, _ := Load()
	key := fmt.Sprintf("%s %d", vendor, product)
	for _, m := range all {
		for _, k := range m.Match {
			if strings.EqualFold(k, key) {
				return m, true
			}
		}
	}
	return Monitor{}, false
}

// Limits — итоговые ограничения для режимов. Нули означают «не ограничено».
type Limits struct {
	HMin, HMax float64 // кГц
	VMin, VMax float64 // Гц
	PclkMax    float64 // МГц, жёсткий предел (переходник, канал DP)
	Bandwidth  float64 // МГц, мягкий предел (монитор)
	From       map[string]string
}

func NewLimits() Limits { return Limits{From: map[string]string{}} }

// FromMonitor применяет диапазоны монитора.
func (l *Limits) FromMonitor(m Monitor, src string) {
	l.set("h", &l.HMin, m.HMin, src)
	l.set("H", &l.HMax, m.HMax, src)
	l.set("v", &l.VMin, m.VMin, src)
	l.set("V", &l.VMax, m.VMax, src)
	l.set("bw", &l.Bandwidth, m.Bandwidth, src)
}

// SetPclk ставит жёсткий предел частоты пикселей, если он строже текущего.
func (l *Limits) SetPclk(v float64, src string) {
	if v > 0 && (l.PclkMax == 0 || v < l.PclkMax) {
		l.PclkMax = v
		l.From["pclk"] = src
	}
}

func (l *Limits) set(key string, dst *float64, v float64, src string) {
	if v > 0 {
		*dst = v
		l.From[key] = src
	}
}

// Override — ручные значения из флагов (0 — не трогать).
func (l *Limits) Override(hmin, hmax, vmin, vmax, pclk float64) {
	src := i18n.T("command-line flag")
	l.set("h", &l.HMin, hmin, src)
	l.set("H", &l.HMax, hmax, src)
	l.set("v", &l.VMin, vmin, src)
	l.set("V", &l.VMax, vmax, src)
	if pclk > 0 {
		l.PclkMax = pclk
		l.From["pclk"] = src
	}
}

func (l Limits) Known() bool { return l.HMax > 0 }

// DisplayName и DisplayNotes — имя и заметки на языке пользователя.
func (m Monitor) DisplayName() string  { return i18n.Pick(m.Name, m.NameL10n) }
func (m Monitor) DisplayNotes() string { return i18n.Pick(m.Notes, m.NotesL10n) }

func (l Limits) String() string {
	f := func(v float64) string {
		if v == 0 {
			return "?"
		}
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
	}
	s := i18n.T("horizontal %s–%s kHz, vertical %s–%s Hz", f(l.HMin), f(l.HMax), f(l.VMin), f(l.VMax))
	if l.PclkMax > 0 {
		s += i18n.T(", pixel clock up to %s MHz", f(l.PclkMax))
	}
	if l.Bandwidth > 0 {
		s += i18n.T(", monitor bandwidth %s MHz", f(l.Bandwidth))
	}
	return s
}

// Problem — нарушение предела. Hard — режим не заработает или опасен; иначе — только качество.
type Problem struct {
	Hard bool
	Kind string // hmax, hmin, vmax, vmin, pclk, bw, rb
	Text string
}

func (l Limits) Check(m timing.Mode) []Problem {
	var p []Problem
	hf, vf, pc := m.HFreq(), m.VFreq(), m.PixelClockMHz()
	const eps = 0.01
	if l.HMax > 0 && hf > l.HMax+eps {
		p = append(p, Problem{true, "hmax", i18n.T("horizontal %.2f kHz is above the monitor limit of %.2f kHz (%s)", hf, l.HMax, l.From["H"])})
	}
	if l.HMin > 0 && hf < l.HMin-eps {
		p = append(p, Problem{true, "hmin", i18n.T("horizontal %.2f kHz is below the monitor limit of %.2f kHz (%s)", hf, l.HMin, l.From["h"])})
	}
	if l.VMax > 0 && vf > l.VMax+eps {
		p = append(p, Problem{true, "vmax", i18n.T("vertical %.2f Hz is above the limit of %.0f Hz (%s)", vf, l.VMax, l.From["V"])})
	}
	if l.VMin > 0 && vf < l.VMin-eps {
		p = append(p, Problem{true, "vmin", i18n.T("vertical %.2f Hz is below the limit of %.0f Hz (%s)", vf, l.VMin, l.From["v"])})
	}
	if l.PclkMax > 0 && pc > l.PclkMax+eps {
		p = append(p, Problem{true, "pclk", i18n.T("pixel clock %.2f MHz is above the limit of %.1f MHz (%s)", pc, l.PclkMax, l.From["pclk"])})
	}
	if l.Bandwidth > 0 && pc > l.Bandwidth+eps {
		p = append(p, Problem{false, "bw", i18n.T("pixel clock %.1f MHz exceeds the monitor bandwidth of %.0f MHz: the picture will be softer", pc, l.Bandwidth)})
	}
	if m.Reduced {
		p = append(p, Problem{false, "rb", i18n.T("reduced blanking is meant for LCDs; a CRT may not complete the beam retrace in time")})
	}
	return p
}

func HasHard(ps []Problem) bool {
	for _, p := range ps {
		if p.Hard {
			return true
		}
	}
	return false
}

// MaxRefresh ищет максимальную частоту (с шагом step Гц) для разрешения, при которой
// режим проходит жёсткие пределы. ok=false — не проходит даже минимальная.
func (l Limits) MaxRefresh(w, h int, k timing.Kind, step float64) (timing.Mode, bool) {
	top := l.VMax
	if top == 0 {
		top = 160
	}
	bottom := math.Max(l.VMin, 24)
	for hz := math.Floor(top/step) * step; hz >= bottom; hz -= step {
		m, err := timing.Calc(timing.Request{W: w, H: h, Hz: hz}, k)
		if err != nil {
			continue
		}
		if !HasHard(l.Check(m)) {
			return m, true
		}
	}
	return timing.Mode{}, false
}
