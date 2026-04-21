package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ShellEntry 保存的 Shell 路径
type ShellEntry struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

// DashboardDefaults 仪表盘默认值
type DashboardDefaults struct {
	Provider         string `json:"provider"`
	Preset           string `json:"preset"`
	OpenCodeProvider string `json:"openCodeProvider"`
	OpenCodePreset   string `json:"openCodePreset"`
	Mode             string `json:"mode"`
	Shell            string `json:"shell"`
	ClaudeMode       string `json:"claudeMode"`
	ClaudeShell      string `json:"claudeShell"`
	OpenCodeMode     string `json:"openCodeMode"`
	OpenCodeShell    string `json:"openCodeShell"`
	CodexMode        string `json:"codexMode"`
	CodexShell       string `json:"codexShell"`
	AmagiCodePreset   string `json:"amagiCodePreset"`
	AmagiCodeMode     string `json:"amagiCodeMode"`
	AmagiCodeShell    string `json:"amagiCodeShell"`
	UseProxy         bool   `json:"useProxy"`
}

// TerminalSettings 终端设置
type TerminalSettings struct {
	Scrollback int `json:"scrollback"`
}

// AppSettings 应用设置
type AppSettings struct {
	Dashboard     DashboardDefaults `json:"dashboard"`
	ShellPaths    []ShellEntry      `json:"shellPaths"`
	Terminal      TerminalSettings  `json:"terminal"`
	RemoteHost    string            `json:"remoteHost"`
	RemotePort    int               `json:"remotePort"`
	MobileWebRoot string            `json:"mobileWebRoot"`
	GitHubToken   string            `json:"githubToken"`
}

func defaultSettings() *AppSettings {
	return &AppSettings{
		Dashboard: DashboardDefaults{
			Mode:             "embedded",
			Shell:            "pwsh",
			ClaudeMode:        "embedded",
			ClaudeShell:       "pwsh",
			OpenCodeMode:      "embedded",
			OpenCodeShell:     "pwsh",
			CodexMode:         "embedded",
			CodexShell:        "pwsh",
			AmagiCodeMode:     "embedded",
			AmagiCodeShell:    "pwsh",
		},
		ShellPaths: []ShellEntry{},
		Terminal: TerminalSettings{
			Scrollback: 100000,
		},
		RemoteHost: "0.0.0.0",
		RemotePort: 8680,
	}
}

// Service 管理应用设置（settings.json）
type Service struct {
	configPath string
	settings   *AppSettings
	mu         sync.RWMutex
}

func NewService(configDir string) *Service {
	return &Service{
		configPath: filepath.Join(configDir, "settings.json"),
		settings:   defaultSettings(),
	}
}

func (s *Service) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.settings = defaultSettings()
			return nil
		}
		return fmt.Errorf("read settings: %w", err)
	}

	var cfg AppSettings
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("parse settings: %w", err)
	}
	if cfg.ShellPaths == nil {
		cfg.ShellPaths = []ShellEntry{}
	}
	if cfg.Terminal.Scrollback <= 0 {
		cfg.Terminal.Scrollback = 100000
	}
	if cfg.RemotePort <= 0 {
		cfg.RemotePort = 8680
	}
	normalizeDashboardDefaults(&cfg.Dashboard)
	s.settings = &cfg
	return nil
}

func (s *Service) Save() error {
	s.mu.RLock()
	cfg := s.settings
	path := s.configPath
	s.mu.RUnlock()

	if cfg == nil {
		return errors.New("settings not loaded")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir settings dir: %w", err)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	b = append(b, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write temp settings: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace settings: %w", err)
	}
	return nil
}

// --- Dashboard Defaults ---

func (s *Service) GetDashboardDefaults() DashboardDefaults {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.Dashboard
}

func (s *Service) SetDashboardDefaults(d DashboardDefaults) error {
	s.mu.Lock()
	normalizeDashboardDefaults(&d)
	s.settings.Dashboard = d
	s.mu.Unlock()
	return s.Save()
}

func normalizeDashboardDefaults(d *DashboardDefaults) {
	if d.ClaudeMode == "" {
		if d.Mode != "" {
			d.ClaudeMode = d.Mode
		} else {
			d.ClaudeMode = "embedded"
		}
	}
	if d.OpenCodeMode == "" {
		if d.Mode != "" {
			d.OpenCodeMode = d.Mode
		} else {
			d.OpenCodeMode = "embedded"
		}
	}
	if d.CodexMode == "" {
		if d.Mode != "" {
			d.CodexMode = d.Mode
		} else {
			d.CodexMode = "embedded"
		}
	}
	if d.AmagiCodeMode == "" {
		if d.Mode != "" {
			d.AmagiCodeMode = d.Mode
		} else {
			d.AmagiCodeMode = "embedded"
		}
	}

	if d.ClaudeShell == "" {
		if d.Shell != "" {
			d.ClaudeShell = d.Shell
		} else {
			d.ClaudeShell = "pwsh"
		}
	}
	if d.OpenCodeShell == "" {
		if d.Shell != "" {
			d.OpenCodeShell = d.Shell
		} else {
			d.OpenCodeShell = "pwsh"
		}
	}
	if d.CodexShell == "" {
		if d.Shell != "" {
			d.CodexShell = d.Shell
		} else {
			d.CodexShell = "pwsh"
		}
	}
	if d.AmagiCodeShell == "" {
		if d.Shell != "" {
			d.AmagiCodeShell = d.Shell
		} else {
			d.AmagiCodeShell = "pwsh"
		}
	}

	d.Mode = d.ClaudeMode
	d.Shell = d.ClaudeShell
}

// --- Shell Paths ---

func (s *Service) GetShellPaths() []ShellEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ShellEntry, len(s.settings.ShellPaths))
	copy(out, s.settings.ShellPaths)
	return out
}

func (s *Service) AddShellPath(entry ShellEntry) error {
	if entry.Path == "" {
		return errors.New("path is required")
	}
	s.mu.Lock()
	for _, e := range s.settings.ShellPaths {
		if e.Path == entry.Path {
			s.mu.Unlock()
			return errors.New("shell path already exists")
		}
	}
	s.settings.ShellPaths = append(s.settings.ShellPaths, entry)
	s.mu.Unlock()
	return s.Save()
}

func (s *Service) RemoveShellPath(path string) error {
	s.mu.Lock()
	paths := s.settings.ShellPaths
	for i, e := range paths {
		if e.Path == path {
			s.settings.ShellPaths = append(paths[:i], paths[i+1:]...)
			s.mu.Unlock()
			return s.Save()
		}
	}
	s.mu.Unlock()
	return nil
}

// --- Terminal Settings ---

func (s *Service) GetTerminalSettings() TerminalSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.Terminal
}

func (s *Service) SetTerminalSettings(t TerminalSettings) error {
	if t.Scrollback <= 0 {
		t.Scrollback = 100000
	}
	s.mu.Lock()
	s.settings.Terminal = t
	s.mu.Unlock()
	return s.Save()
}

// --- Remote Host & Port ---

func (s *Service) GetRemoteHost() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	host := s.settings.RemoteHost
	if host == "" {
		return "0.0.0.0"
	}
	return host
}

func (s *Service) SetRemoteHost(host string) error {
	s.mu.Lock()
	s.settings.RemoteHost = host
	s.mu.Unlock()
	return s.Save()
}

func (s *Service) GetRemotePort() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	port := s.settings.RemotePort
	if port <= 0 {
		return 8680
	}
	return port
}

func (s *Service) SetRemotePort(port int) error {
	if port < 1024 || port > 65535 {
		return fmt.Errorf("port %d out of valid range [1024, 65535]", port)
	}
	s.mu.Lock()
	s.settings.RemotePort = port
	s.mu.Unlock()
	return s.Save()
}

// --- Mobile Web Root ---

func (s *Service) GetMobileWebRoot() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.MobileWebRoot
}

func (s *Service) SetMobileWebRoot(path string) error {
	s.mu.Lock()
	s.settings.MobileWebRoot = path
	s.mu.Unlock()
	return s.Save()
}

// --- GitHub Token ---

func (s *Service) GetGitHubToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.GitHubToken
}

func (s *Service) SetGitHubToken(token string) error {
	s.mu.Lock()
	s.settings.GitHubToken = token
	s.mu.Unlock()
	return s.Save()
}

// --- Full Settings ---

func (s *Service) GetSettings() *AppSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copy := *s.settings
	shellCopy := make([]ShellEntry, len(s.settings.ShellPaths))
	for i, e := range s.settings.ShellPaths {
		shellCopy[i] = e
	}
	copy.ShellPaths = shellCopy
	return &copy
}
