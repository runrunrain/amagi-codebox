package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ShellEntry 保存的 Shell 路径
type ShellEntry struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

// WorkDirEntry 保存的工作目录
type WorkDirEntry struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

// DashboardDefaults 仪表盘默认值
type DashboardDefaults struct {
	Provider          string `json:"provider"`
	Preset            string `json:"preset"`
	OpenCodeProvider  string `json:"openCodeProvider"`
	OpenCodePreset    string `json:"openCodePreset"`
	OpenCodePresetKey string `json:"openCodePresetKey"` // 新模型：直接指定 opencode_presets 的 key
	Mode              string `json:"mode"`
	Shell             string `json:"shell"`
	ClaudeMode        string `json:"claudeMode"`
	ClaudeShell       string `json:"claudeShell"`
	OpenCodeMode      string `json:"openCodeMode"`
	OpenCodeShell     string `json:"openCodeShell"`
	CodexMode         string `json:"codexMode"`
	CodexShell        string `json:"codexShell"`
	PiMode            string `json:"piMode"`
	PiShell           string `json:"piShell"`
	// Deprecated: retained only so legacy settings.json files can still be decoded.
	AmagiCodePreset string `json:"amagiCodePreset"`
	// Deprecated: retained only so legacy settings.json files can still be decoded.
	AmagiCodeMode string `json:"amagiCodeMode"`
	// Deprecated: retained only so legacy settings.json files can still be decoded.
	AmagiCodeShell string `json:"amagiCodeShell"`
	UseProxy       bool   `json:"useProxy"`
	UseHeadroom    bool   `json:"useHeadroom"`
	// CodexGlobalHeadroom enables a second, independent headroom instance that
	// compresses Codex desktop traffic globally. Unlike UseHeadroom (which is a
	// per-claude-session toggle on port 8787 with an Anthropic target), this
	// toggle drives a persistent 8788 instance with an OpenAI target and rewrites
	// ~/.codex/config.toml's top-level openai_base_url. defaultSettings leaves
	// these zero so a fresh install starts with the feature off.
	CodexGlobalHeadroom       bool   `json:"codexGlobalHeadroom"`
	CodexGlobalHeadroomTarget string `json:"codexGlobalHeadroomTarget"`
	CodexGlobalHeadroomPort   int    `json:"codexGlobalHeadroomPort"`
}

// CodexGlobalHeadroomState is the frontend-facing snapshot of the Codex global
// headroom toggle. It bundles the three persisted DashboardDefaults fields so
// callers (the App bound method) can read/return them atomically without
// touching the full DashboardDefaults struct.
type CodexGlobalHeadroomState struct {
	Enabled bool   `json:"enabled"`
	Target  string `json:"target"`
	Port    int    `json:"port"`
}

// TerminalSettings 终端设置
type TerminalSettings struct {
	Scrollback int `json:"scrollback"`
}

// AppSettings 应用设置
type AppSettings struct {
	Dashboard             DashboardDefaults `json:"dashboard"`
	ShellPaths            []ShellEntry      `json:"shellPaths"`
	SavedWorkDirs         []WorkDirEntry    `json:"savedWorkDirs"`
	Terminal              TerminalSettings  `json:"terminal"`
	RemoteHost            string            `json:"remoteHost"`
	RemotePort            int               `json:"remotePort"`
	RemoteEnabled         bool              `json:"remoteEnabled"`
	RemoteSecurityVersion int               `json:"remoteSecurityVersion,omitempty"`
	MobileWebRoot         string            `json:"mobileWebRoot"`
	GitHubToken           string            `json:"githubToken"`
}

func defaultSettings() *AppSettings {
	return &AppSettings{
		Dashboard: DashboardDefaults{
			Mode:           "embedded",
			Shell:          "wsl",
			ClaudeMode:     "embedded",
			ClaudeShell:    "wsl",
			OpenCodeMode:   "embedded",
			OpenCodeShell:  "wsl",
			CodexMode:      "embedded",
			CodexShell:     "wsl",
			PiMode:         "embedded",
			PiShell:        "wsl",
			AmagiCodeMode:  "embedded",
			AmagiCodeShell: "wsl",
		},
		ShellPaths:    []ShellEntry{},
		SavedWorkDirs: []WorkDirEntry{},
		Terminal: TerminalSettings{
			Scrollback: 100000,
		},
		RemoteHost:            "127.0.0.1",
		RemotePort:            8680,
		RemoteSecurityVersion: 1,
	}
}

// Service 管理应用设置（settings.json）
type Service struct {
	configPath string
	settings   *AppSettings
	mu         sync.RWMutex // guards the live settings pointer + fields
	// saveMu serializes all persistence commits so concurrent setters cannot
	// interleave or overwrite each other (R2-Minor-02). It is held across the
	// mutate+save transaction by the transactional setters (SetRemoteEndpoint /
	// SetRemoteHost / SetRemotePort / SetRemoteEnabled); Save() takes it for
	// plain callers. Inner data access still uses mu; saveMu is always acquired
	// BEFORE mu (lock ordering saveMu → mu), and no path takes mu→saveMu.
	saveMu sync.Mutex
}

func NewService(configDir string) *Service {
	return &Service{
		configPath: filepath.Join(configDir, "settings.json"),
		settings:   defaultSettings(),
	}
}

func (s *Service) Load() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
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
	if cfg.SavedWorkDirs == nil {
		cfg.SavedWorkDirs = []WorkDirEntry{}
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
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	return s.saveLocked()
}

// saveLocked marshals an immutable deep-copy snapshot and writes it atomically.
// The caller MUST hold saveMu so concurrent commits serialize. It snapshots
// settings under mu (deep copy), then marshals the snapshot — never the live
// pointer — so a concurrent setter cannot drift the marshal mid-flight
// (R2-Minor-02: "Save no longer marshals a pointer released after RLock").
//
// Commit boundary (R4-Minor): os.Rename is the LAST fallible step. The temp file
// is fully prepared (written + chmod 0600 + Sync) BEFORE the rename, and rename
// carries the temp's inode/mode onto the final path. There is NO fallible step
// after rename (the old post-rename chmod is removed), so any error returned by
// saveLocked is guaranteed to mean "not committed" — callers may safely revert
// in-memory state on error without risking a disk/memory split where disk holds
// the new value but memory was rolled back.
func (s *Service) saveLocked() error {
	cfg := s.snapshotSettings()
	if cfg == nil {
		return errors.New("settings not loaded")
	}
	path := s.configPath

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir settings dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("set settings dir permissions: %w", err)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	b = append(b, '\n')

	// Prepare the temp file with a SINGLE writable handle: create/truncate +
	// write + chmod + fsync all on the same O_RDWR handle. Windows
	// FlushFileBuffers (backing File.Sync) requires GENERIC_WRITE, which O_RDONLY
	// (os.Open) does NOT grant — a read-only reopen would make every Save fail
	// pre-commit on Windows (R5-N01). Everything before rename is abortable;
	// rename remains the LAST fallible step (atomic commit).
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("create temp settings: %w", err)
	}
	// writeFull + chmod + sync on the SAME writable handle; any failure removes
	// the temp and aborts (not committed → caller safely reverts memory).
	werr := func() error {
		if _, err := writeFull(f, b); err != nil {
			return fmt.Errorf("write temp settings: %w", err)
		}
		if err := f.Chmod(0o600); err != nil {
			return fmt.Errorf("set temp settings permissions: %w", err)
		}
		if err := settingsPreRenameSync(f); err != nil {
			return fmt.Errorf("sync temp settings: %w", err)
		}
		return nil
	}()
	cerr := f.Close()
	if werr != nil {
		_ = os.Remove(tmp)
		return werr
	}
	if cerr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close temp settings: %w", cerr)
	}
	// Atomic commit: rename is the LAST fallible step. The temp was fully
	// prepared (written + chmod 0600 + fsync) on a writable handle; rename carries
	// the temp's inode/mode onto the final path, so no post-rename step is needed.
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace settings: %w", err)
	}
	return nil
}

// settingsPreRenameSync (test-only; defaults to (*os.File).Sync) lets tests
// inject a pre-rename sync failure to prove the commit boundary (R4-Minor): any
// error before rename means "not committed" so callers safely revert memory. It
// receives the SAME writable handle used to write the temp (O_RDWR → Windows
// GENERIC_WRITE), so the Sync backing syscall (fsync / FlushFileBuffers) has the
// write access Windows requires (R5-N01). It MUST NOT be given a read-only
// handle.
var settingsPreRenameSync = func(f *os.File) error { return f.Sync() }

// snapshotSettings returns a deep copy of the current settings under mu. The
// copy is immutable w.r.t. concurrent setters, so saveLocked can marshal it
// after releasing the data lock without live-pointer drift (R2-Minor-02).
func (s *Service) snapshotSettings() *AppSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.settings
	if src == nil {
		return nil
	}
	cp := *src // shallow copy: all value-type fields (DashboardDefaults, Terminal, strings, ints) copied
	// Deep-copy slices so a concurrent append cannot race the marshal.
	cp.ShellPaths = append([]ShellEntry(nil), src.ShellPaths...)
	cp.SavedWorkDirs = append([]WorkDirEntry(nil), src.SavedWorkDirs...)
	return &cp
}

// --- Dashboard Defaults ---

func (s *Service) GetDashboardDefaults() DashboardDefaults {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.Dashboard
}

// SetDashboardDefaults replaces the persisted dashboard defaults. The three
// CodexGlobal* fields (CodexGlobalHeadroom / CodexGlobalHeadroomTarget /
// CodexGlobalHeadroomPort) are owned exclusively by SetCodexGlobalHeadroom and
// MUST NOT be touched here: the frontend persistDefaults path (useDashboardState
// + useSessionLaunch) only reads the cached value once at init and would
// otherwise replay a stale (typically false) value back through this method on
// every session launch or dashboard save, silently disabling the codex-global
// headroom and leaving config.toml pointing at a dead 8788 proxy. We therefore
// ignore whatever the caller supplied for those three fields and re-pin them to
// the currently persisted values before swapping DashboardDefaults in.
func (s *Service) SetDashboardDefaults(d DashboardDefaults) error {
	s.mu.Lock()
	normalizeDashboardDefaults(&d)
	// Preserve the codex-global headroom fields owned by SetCodexGlobalHeadroom.
	// 不信任入参中的这三个字段：它们由 SetCodexGlobalHeadroom 独占管理。
	d.CodexGlobalHeadroom = s.settings.Dashboard.CodexGlobalHeadroom
	d.CodexGlobalHeadroomTarget = s.settings.Dashboard.CodexGlobalHeadroomTarget
	d.CodexGlobalHeadroomPort = s.settings.Dashboard.CodexGlobalHeadroomPort
	s.settings.Dashboard = d
	s.mu.Unlock()
	return s.Save()
}

// --- Codex Global Headroom ---
//
// These accessors read/write the three CodexGlobal* fields that live on
// DashboardDefaults. They mirror the Get/SetDashboardDefaults lock+Save
// pattern so the App bound method can toggle the feature without round-tripping
// the full DashboardDefaults struct (which would risk clobbering unrelated
// dashboard state).

// GetCodexGlobalHeadroom returns the persisted Codex global headroom toggle.
// A zero-value state (Enabled=false) means the feature is off.
//
// Footgun warning / 调用约束：本方法只读持久化，不反映运行态，也不读 config.toml。
// 前端应通过 App.GetCodexGlobalHeadroom（含 running 状态）获取快照，不要直接绑定本方法。
func (s *Service) GetCodexGlobalHeadroom() CodexGlobalHeadroomState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return CodexGlobalHeadroomState{
		Enabled: s.settings.Dashboard.CodexGlobalHeadroom,
		Target:  s.settings.Dashboard.CodexGlobalHeadroomTarget,
		Port:    s.settings.Dashboard.CodexGlobalHeadroomPort,
	}
}

// SetCodexGlobalHeadroom persists the Codex global headroom toggle. When
// disabling, target/port are cleared so no stale configuration survives.
//
// Footgun warning / 调用约束：本方法只写持久化，不启动/停止代理，也不写 ~/.codex/config.toml。
// 直接调用会导致持久化与运行态/config 不一致。前端必须通过 App.SetCodexGlobalHeadroom，
// 由 App 层完成 StartForOpenAI/Stop + syncCodexGlobalHeadroomConfig + 持久化的原子编排。
// 本方法导出仅因 App 跨包调用需要；wails 会把它暴露给前端生成绑定，但前端不应直接调用。
func (s *Service) SetCodexGlobalHeadroom(enabled bool, target string, port int) error {
	s.mu.Lock()
	s.settings.Dashboard.CodexGlobalHeadroom = enabled
	if enabled {
		s.settings.Dashboard.CodexGlobalHeadroomTarget = target
		s.settings.Dashboard.CodexGlobalHeadroomPort = port
	} else {
		s.settings.Dashboard.CodexGlobalHeadroomTarget = ""
		s.settings.Dashboard.CodexGlobalHeadroomPort = 0
	}
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
		d.OpenCodeMode = "embedded"
	}
	if d.CodexMode == "" {
		d.CodexMode = "embedded"
	}
	if d.PiMode == "" {
		d.PiMode = "embedded"
	}
	if d.AmagiCodeMode == "" {
		d.AmagiCodeMode = "embedded"
	}

	if d.ClaudeShell == "" {
		if d.Shell != "" {
			d.ClaudeShell = d.Shell
		} else {
			d.ClaudeShell = "wsl"
		}
	}
	if d.OpenCodeShell == "" {
		if d.Shell != "" {
			d.OpenCodeShell = d.Shell
		} else {
			d.OpenCodeShell = "wsl"
		}
	}
	if d.CodexShell == "" {
		if d.Shell != "" {
			d.CodexShell = d.Shell
		} else {
			d.CodexShell = "wsl"
		}
	}
	if d.PiShell == "" {
		if d.Shell != "" {
			d.PiShell = d.Shell
		} else {
			d.PiShell = "wsl"
		}
	}
	if d.AmagiCodeShell == "" {
		if d.Shell != "" {
			d.AmagiCodeShell = d.Shell
		} else {
			d.AmagiCodeShell = "wsl"
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
		return "127.0.0.1"
	}
	return host
}

func (s *Service) GetRemoteEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.RemoteEnabled
}

func (s *Service) SetRemoteEnabled(enabled bool) error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	old := s.settings.RemoteEnabled
	s.settings.RemoteEnabled = enabled
	s.mu.Unlock()
	if err := s.saveLocked(); err != nil {
		// Revert in-memory so a Save failure leaves memory == disk (R3-Minor-02 ③).
		s.mu.Lock()
		s.settings.RemoteEnabled = old
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *Service) SetRemoteHost(host string) error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	old := s.settings.RemoteHost
	s.settings.RemoteHost = host
	s.mu.Unlock()
	if err := s.saveLocked(); err != nil {
		// Revert in-memory so a Save failure leaves memory == disk (R3-Minor-02 ③).
		s.mu.Lock()
		s.settings.RemoteHost = old
		s.mu.Unlock()
		return err
	}
	return nil
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
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	old := s.settings.RemotePort
	s.settings.RemotePort = port
	s.mu.Unlock()
	if err := s.saveLocked(); err != nil {
		// Revert in-memory so a Save failure leaves memory == disk (R3-Minor-02 ③).
		s.mu.Lock()
		s.settings.RemotePort = old
		s.mu.Unlock()
		return err
	}
	return nil
}

// SetRemoteEndpoint updates the remote listen host AND port in a single
// transaction: both are validated before any mutation, both are set under one
// data lock, and a single saveLocked persists them atomically (R2-Minor-02).
// The whole validate→mutate→persist sequence runs under saveMu so two concurrent
// endpoint calls (or an endpoint vs another remote setter) serialize and cannot
// overwrite each other. If persistence fails the in-memory values are reverted
// under saveMu so a partial failure never leaves host updated without port (or
// vice-versa) and cannot clobber a concurrent setter's committed value.
func (s *Service) SetRemoteEndpoint(host string, port int) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return errors.New("remote host must not be empty")
	}
	if port < 1024 || port > 65535 {
		return fmt.Errorf("port %d out of valid range [1024, 65535]", port)
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	oldHost := s.settings.RemoteHost
	oldPort := s.settings.RemotePort
	s.settings.RemoteHost = host
	s.settings.RemotePort = port
	s.mu.Unlock()
	if err := s.saveLocked(); err != nil {
		// Revert in-memory so a Save failure leaves no partial state. The revert
		// is under saveMu (still held) so it cannot be observed or overwritten by
		// a concurrent setter (R2-Minor-02).
		s.mu.Lock()
		s.settings.RemoteHost = oldHost
		s.settings.RemotePort = oldPort
		s.mu.Unlock()
		return err
	}
	return nil
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

// --- Saved Work Directories ---

func (s *Service) GetSavedWorkDirs() []WorkDirEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]WorkDirEntry, len(s.settings.SavedWorkDirs))
	copy(out, s.settings.SavedWorkDirs)
	return out
}

func (s *Service) AddSavedWorkDir(path string, label string) error {
	if path == "" {
		return errors.New("path is required")
	}
	// Label 为空时使用路径末段
	if label == "" {
		label = filepath.Base(path)
	}
	s.mu.Lock()
	// 去重：按 path 去重
	for _, e := range s.settings.SavedWorkDirs {
		if e.Path == path {
			// 更新 label 并返回现有列表
			for i := range s.settings.SavedWorkDirs {
				if s.settings.SavedWorkDirs[i].Path == path {
					s.settings.SavedWorkDirs[i].Label = label
					break
				}
			}
			// 显式释放写锁后再 Save，避免 Save 内取 RLock 造成死锁
			s.mu.Unlock()
			return s.Save()
		}
	}
	s.settings.SavedWorkDirs = append(s.settings.SavedWorkDirs, WorkDirEntry{
		Path:  path,
		Label: label,
	})
	// 显式释放写锁后再 Save，避免 Save 内取 RLock 造成死锁
	s.mu.Unlock()
	return s.Save()
}

func (s *Service) RemoveSavedWorkDir(path string) error {
	s.mu.Lock()
	paths := s.settings.SavedWorkDirs
	for i, e := range paths {
		if e.Path == path {
			s.settings.SavedWorkDirs = append(paths[:i], paths[i+1:]...)
			s.mu.Unlock()
			return s.Save()
		}
	}
	s.mu.Unlock()
	return nil
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
