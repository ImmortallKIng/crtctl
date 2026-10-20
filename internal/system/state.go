// Package system применяет настройки: EDID в ядро, канал DP, кастомные режимы KDE.
package system

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ImmortallKIng/crtctl/internal/i18n"
)

const (
	StateDir  = "/etc/crtctl"
	StateFile = StateDir + "/state.json"
	UnitDir   = "/etc/systemd/system"
)

// ConnState — что crtctl установил для одного коннектора.
type ConnState struct {
	EDIDFile     string  `json:"edid_file,omitempty"`     // /usr/lib/firmware/edid/crtctl-DP-2.bin
	EDIDSHA256   string  `json:"edid_sha256,omitempty"`   // контроль, что файл не подменили
	MonitorID    string  `json:"monitor_id,omitempty"`    // EDID.ID() монитора, для которого собран EDID
	MonitorTitle string  `json:"monitor_title,omitempty"` // для сообщений
	Profile      string  `json:"profile,omitempty"`
	MonitorNoDDC bool    `json:"monitor_no_ddc,omitempty"` // монитор не отдавал EDID при установке
	Method       string  `json:"method,omitempty"`         // boot (по умолчанию) или service
	DebugfsDir   string  `json:"debugfs_dir,omitempty"`    // для service
	SysPath      string  `json:"sys_path,omitempty"`       // для service
	PCI          string  `json:"pci,omitempty"`            // видеокарта (BusID для X)
	XOutput      string  `json:"x_output,omitempty"`       // имя выхода в X у NVIDIA (CustomEDID)
	Installed    string  `json:"installed,omitempty"`
	Link         string  `json:"link,omitempty"`         // "2 0xa", если канал зафиксирован
	AdapterPclk  float64 `json:"adapter_pclk,omitempty"` // МГц, последнее измерение по DPCD
	AdapterInfo  string  `json:"adapter_info,omitempty"`
	AdapterDPCD  string  `json:"adapter_dpcd,omitempty"` // сырые регистры 0x000, 0x080, 0x500
}

type State struct {
	Connectors map[string]*ConnState `json:"connectors"`
	file       string                // где хранится (для тестов — временный файл)
}

// LoadState читает состояние. Файла нет — пустое состояние.
func LoadState() (*State, error) { return LoadStateFrom(StateFile) }

// LoadStateFrom читает состояние из указанного файла.
func LoadStateFrom(file string) (*State, error) {
	s := &State{Connectors: map[string]*ConnState{}, file: file}
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, i18n.Errorf("%s is damaged: %w (delete it to start over)", file, err)
	}
	if s.Connectors == nil {
		s.Connectors = map[string]*ConnState{}
	}
	return s, nil
}

// Save пишет состояние атомарно. Файл читаем всем: info и doctor работают без root.
func (s *State) Save() error {
	for k, c := range s.Connectors {
		if *c == (ConnState{}) {
			delete(s.Connectors, k)
		}
	}
	file := s.file
	if file == "" {
		file = StateFile
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func (s *State) Conn(name string) *ConnState {
	c, ok := s.Connectors[name]
	if !ok {
		c = &ConnState{}
		s.Connectors[name] = c
	}
	return c
}

// Get возвращает состояние коннектора или nil.
func (s *State) Get(name string) *ConnState { return s.Connectors[name] }

// EDIDOverrides — пары коннектор → файл для параметра ядра, отсортированные.
func (s *State) EDIDOverrides() [][2]string {
	var out [][2]string
	for name, c := range s.Connectors {
		if c.EDIDFile != "" && c.Method != MethodService {
			out = append(out, [2]string{name, c.EDIDFile})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func RequireRoot(what string) error {
	if os.Geteuid() != 0 {
		return i18n.Errorf("%s needs root: run it with sudo", what)
	}
	return nil
}

func RequireUser(what string) error {
	if os.Geteuid() == 0 {
		return i18n.Errorf("%s must run as your normal user (without sudo): it talks to your KDE session", what)
	}
	return nil
}

// FirmwareName — путь EDID относительно /usr/lib/firmware, как его ждёт ядро.
// FirmwareName — путь EDID относительно каталога прошивок, как его ждёт drm.edid_firmware.
func FirmwareName(path string) string {
	for _, root := range []string{"/usr/lib/firmware/", "/lib/firmware/", "/etc/crtctl/firmware/"} {
		if rest, ok := strings.CutPrefix(path, root); ok {
			return rest
		}
	}
	return path
}
