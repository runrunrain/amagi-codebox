package headroom

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
)

// DefaultPort is the default local TCP port the Headroom proxy listens on.
// Exported so callers (e.g. app.go wiring) can reference the same constant
// when configuring the injection proxy's upstream or the CLI's base URL.
const DefaultPort = 8787

// headroomHost is the loopback address the headroom child process binds to.
// Headroom must only ever listen on loopback: it terminates TLS and forwards
// authenticated API traffic, so binding to a externally reachable interface
// would expose upstream credentials.
const headroomHost = "127.0.0.1"

// HeadroomStatus is the frontend-facing snapshot of the Headroom proxy state.
type HeadroomStatus struct {
	Running    bool   `json:"running"`
	Port       int    `json:"port"`
	BackendURL string `json:"backendUrl"`
}

// HeadroomService manages a headroom proxy subprocess that compresses
// Anthropic-compatible API traffic. Unlike ProxyService (which runs an
// in-process HTTP server), HeadroomService launches an external
// `headroom proxy` child process via ProcessRunner and supervises its
// lifecycle.
//
// Lifecycle contract:
//   - Start launches the child and returns immediately; a background
//     goroutine reaps the process and clears Running when it exits.
//   - Stop kills the child process. Best-effort: the reaper goroutine may
//     observe the exit slightly after Stop returns.
//   - All methods are safe for concurrent use.
type HeadroomService struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	running    bool
	port       int
	backendURL string
	runner     platform.ProcessRunner
	log        *logging.Service
	// venvBinDir is the CodeBox-managed headroom venv bin directory
	// ("<venv>/bin" POSIX / "<venv>/Scripts" Windows). Injected via
	// SetVenvBinDir so the proxy launch resolves the venv-installed headroom
	// binary. When empty, resolution falls back to the platform resolver only.
	venvBinDir string
}

// NewHeadroomService creates a HeadroomService backed by the given process
// runner. The runner must not be nil; callers typically pass
// platform.NewProcessRunner().
func NewHeadroomService(runner platform.ProcessRunner, log *logging.Service) *HeadroomService {
	return &HeadroomService{
		port:   DefaultPort,
		runner: runner,
		log:    log,
	}
}

// SetVenvBinDir injects the CodeBox-managed headroom venv bin directory. Must
// be called before Start so the proxy launch resolves the venv headroom. The
// directory is the platform-specific bin subdir ("<venv>/bin" or
// "<venv>/Scripts").
func (s *HeadroomService) SetVenvBinDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.venvBinDir = strings.TrimSpace(dir)
}

// SetPort overrides the listen port. Must be called before Start. Changing the
// port while the proxy is running is rejected because the child is already
// bound to the old port; call Stop first. The CodeBox claude-session headroom
// keeps DefaultPort (8787); the independent Codex-global headroom instance is
// wired with a different port (8788) so the two proxies never collide.
func (s *HeadroomService) SetPort(port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return fmt.Errorf("cannot change port while headroom is running on port %d", s.port)
	}
	s.port = port
	return nil
}

// backendKind distinguishes whether headroom forwards to an Anthropic- or
// OpenAI-compatible upstream. headroom reads different env vars to learn the
// real target depending on the wire protocol it proxies:
//   - Anthropic target -> ANTHROPIC_TARGET_API_URL
//   - OpenAI     target -> OPENAI_TARGET_API_URL
//
// The matching "*_BASE_URL" var is also the one that must be stripped from the
// inherited environment to prevent a forwarding death-loop.
type backendKind int

const (
	backendAnthropic backendKind = iota
	backendOpenAI
)

// targetEnvVar returns the headroom env var that carries the real upstream URL
// for this backend kind.
func (k backendKind) targetEnvVar() string {
	if k == backendOpenAI {
		return "OPENAI_TARGET_API_URL"
	}
	return "ANTHROPIC_TARGET_API_URL"
}

// loopVarToStrip returns the inherited env var that must be removed to prevent
// headroom forwarding back into itself or the codebox injection proxy.
func (k backendKind) loopVarToStrip() string {
	if k == backendOpenAI {
		return "OPENAI_BASE_URL"
	}
	return "ANTHROPIC_BASE_URL"
}

// Start launches the headroom proxy child process for an Anthropic-compatible
// upstream. realBackendURL is the real upstream API base URL (e.g. the
// provider's EffectiveBaseURL); headroom uses it via the
// ANTHROPIC_TARGET_API_URL environment variable to know where to forward
// compressed traffic.
//
// Death-loop prevention (critical): the child environment is derived from the
// process environment with any inherited ANTHROPIC_BASE_URL removed. If
// headroom inherited an ANTHROPIC_BASE_URL pointing at itself (DefaultPort) or
// at the codebox injection proxy (:5280), it would forward requests back into
// itself / the injection proxy that calls back into headroom, creating an
// infinite request loop. Only ANTHROPIC_TARGET_API_URL is set so headroom
// always reaches the real upstream.
func (s *HeadroomService) Start(realBackendURL string) error {
	return s.start(realBackendURL, backendAnthropic)
}

// StartForOpenAI launches the headroom proxy child process for an
// OpenAI-compatible upstream (used by the Codex desktop global compression
// path). targetURL is the real OpenAI-compatible API base URL; headroom uses
// it via OPENAI_TARGET_API_URL.
//
// This is fully independent of the claude-session headroom (Start): a separate
// HeadroomService instance (configured with a different port, e.g. 8788) must
// be used so the two proxies do not collide. The OPENAI_BASE_URL var is
// stripped from the inherited environment for the same death-loop reason
// ANTHROPIC_BASE_URL is stripped in Start().
func (s *HeadroomService) StartForOpenAI(targetURL string) error {
	return s.start(targetURL, backendOpenAI)
}

// start is the shared launch core for both backend kinds. It acquires s.mu for
// the duration of process startup and field mutation so concurrent callers
// (e.g. LaunchSession + the codex-global toggle, or the startup restore
// goroutine + a UI toggle) cannot double-spawn the child, tear s.cmd/s.running
// mid-write, or race the reaper goroutine and the other accessors
// (Stop/SetPort/IsRunning/GetStatus/GetPort).
//
// Self-deadlock safety: start() does NOT call any method that itself acquires
// s.mu (resolveHeadroomBinWithEnv / runner.Start / buildChildEnvForKind are all
// lock-free), and the reaper goroutine is started with `go func(...)` which
// only attempts to acquire s.mu after start() has returned and its deferred
// Unlock has released the lock. Holding s.mu here therefore mirrors the
// pre-refactor Start() semantics without risk of self-deadlock.
//
// start() 为两种 backend kind 的共享启动核心。它在整个进程启动与字段写入期间持有
// s.mu，使得并发调用方（如 LaunchSession 与 codex 全局开关、或启动恢复 goroutine
// 与 UI 开关）不会重复 spawn 子进程、不会撕裂 s.cmd/s.running，也不会与 reaper
// goroutine 及其他访问器（Stop/SetPort/IsRunning/GetStatus/GetPort）竞争。
func (s *HeadroomService) start(realBackendURL string, kind backendKind) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("headroom already running on port %d", s.port)
	}
	if strings.TrimSpace(realBackendURL) == "" {
		return fmt.Errorf("realBackendURL cannot be empty")
	}
	if s.runner == nil {
		return fmt.Errorf("headroom process runner is not configured")
	}

	port := s.port
	if port == 0 {
		port = DefaultPort
		s.port = port
	}

	// Resolve headroom with an augmented PATH and reuse that same environment
	// for the child process. On macOS, GUI apps launched from Dock inherit a
	// minimal PATH; without augmentation the child cannot locate headroom (or
	// the interpreter a script shim depends on) even when the resolver can.
	// start already holds s.mu (see the Lock at the top of this function), so
	// read s.venvBinDir directly and call the lock-free core. Going through
	// resolveHeadroomBinWithEnv() would re-acquire s.mu and self-deadlock.
	// start 已持 s.mu，直接读 s.venvBinDir 并调用无锁核心；经 resolveHeadroomBinWithEnv
	// 会再次获取 s.mu 造成自死锁。
	binPath, enhancedEnv := resolveHeadroomBinCore(s.venvBinDir)
	env := buildChildEnvForKind(realBackendURL, port, enhancedEnv, kind)

	spec := platform.CommandSpec{
		Path:   binPath,
		Args:   []string{"proxy", "--port", strconv.Itoa(port)},
		Env:    env,
		Policy: platform.DefaultProcessPolicy(),
	}

	cmd, err := s.runner.Start(spec)
	if err != nil {
		return fmt.Errorf("start headroom proxy: %w", err)
	}

	s.cmd = cmd
	s.running = true
	s.backendURL = strings.TrimRight(realBackendURL, "/")

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	if s.log != nil {
		s.log.Info("headroom", "代理子进程已启动", fmt.Sprintf("pid=%d port=%d upstream=%s kind=%s", pid, port, s.backendURL, kind.targetEnvVar()))
	}

	// Reap the child process and clear Running when it exits. Comparing
	// s.cmd against the captured command protects against a later Start
	// replacing the active command before the goroutine wakes up.
	go func(c *exec.Cmd, listenPort int) {
		waitErr := c.Wait()
		s.mu.Lock()
		if s.cmd == c {
			s.running = false
		}
		s.mu.Unlock()
		if s.log != nil {
			s.log.Info("headroom", "代理子进程已退出", fmt.Sprintf("port=%d err=%v", listenPort, waitErr))
		}
	}(cmd, port)

	return nil
}

// Stop terminates the headroom child process. It is a no-op if not running.
func (s *HeadroomService) Stop() error {
	s.mu.Lock()
	if !s.running || s.cmd == nil || s.cmd.Process == nil {
		s.running = false
		s.cmd = nil
		s.mu.Unlock()
		return nil
	}

	cmd := s.cmd
	// Capture the listen port while still holding the lock: SetPort can write
	// s.port concurrently, and reading it after Unlock for the log line below
	// would race under -race (data race on s.port). 在持锁时取端口，避免日志行与
	// SetPort 写入 s.port 构成 data race。
	port := s.port
	s.running = false
	s.cmd = nil
	s.mu.Unlock()

	err := cmd.Process.Kill()
	if s.log != nil {
		s.log.Info("headroom", "上下文压缩已停用", fmt.Sprintf("port=%d err=%v", port, err))
	}
	return err
}

// IsRunning reports whether the headroom child process is currently running.
func (s *HeadroomService) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// GetStatus returns a snapshot of the headroom proxy state for the frontend.
func (s *HeadroomService) GetStatus() HeadroomStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return HeadroomStatus{
		Running:    s.running,
		Port:       s.port,
		BackendURL: s.backendURL,
	}
}

// GetPort returns the port headroom is configured to listen on.
func (s *HeadroomService) GetPort() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// buildChildEnv constructs the environment for the headroom child process from
// the process environment. It is a convenience wrapper around
// buildChildEnvWithBase for callers that have not pre-augmented PATH.
func buildChildEnv(realBackendURL string, port int) []string {
	return buildChildEnvWithBase(realBackendURL, port, os.Environ())
}

// buildChildEnvWithBase constructs the environment for the headroom child
// process targeting an Anthropic-compatible upstream. It is a backward-compatible
// wrapper around buildChildEnvForKind (backendAnthropic).
func buildChildEnvWithBase(realBackendURL string, port int, baseEnv []string) []string {
	return buildChildEnvForKind(realBackendURL, port, baseEnv, backendAnthropic)
}

// buildChildEnvForKind constructs the environment for the headroom child process
// from the given base environment for the supplied backend kind.
//
// It strips any inherited "*_BASE_URL" matching the kind (death-loop
// prevention), and injects the variables headroom needs to locate its listen
// port and real upstream:
//   - <KIND>_TARGET_API_URL: the real upstream base URL (ANTHROPIC_/OPENAI_)
//   - HEADROOM_PORT: the listen port
//   - HEADROOM_HOST: the bind address (always loopback)
//
// baseEnv is expected to carry the augmented PATH produced by
// resolveHeadroomBinWithEnv so the child process can locate headroom (and any
// interpreter it depends on) even when the GUI process inherited a minimal PATH.
func buildChildEnvForKind(realBackendURL string, port int, baseEnv []string, kind backendKind) []string {
	base := append([]string(nil), baseEnv...)
	out := make([]string, 0, len(base)+3)
	stripVar := kind.loopVarToStrip()
	for _, kv := range base {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			out = append(out, kv)
			continue
		}
		// Strip the inherited *_BASE_URL regardless of case. Windows environment
		// variable names are case-insensitive; on POSIX the canonical name is
		// uppercase, so EqualFold covers both. See loopVarToStrip for why each
		// kind strips its own var.
		if strings.EqualFold(key, stripVar) {
			continue
		}
		out = append(out, kv)
	}
	out = append(out,
		kind.targetEnvVar()+"="+strings.TrimRight(realBackendURL, "/"),
		fmt.Sprintf("HEADROOM_PORT=%d", port),
		"HEADROOM_HOST="+headroomHost,
	)
	return out
}

// resolveHeadroomBinWithEnv resolves the absolute path to the headroom
// executable AND returns the enhanced environment used for resolution.
//
// It is the lock-protected entry point for callers that do NOT already hold
// s.mu (notably GetSavings). It snapshots s.venvBinDir under s.mu and delegates
// to the lock-free core resolveHeadroomBinCore, so concurrent SetVenvBinDir
// writes cannot race the read here. Callers already holding s.mu (start) MUST
// call resolveHeadroomBinCore directly to avoid self-deadlock.
//
// resolveHeadroomBinWithEnv 解析 headroom 可执行文件绝对路径并返回增强环境。
// 它是未持 s.mu 的调用方（尤其是 GetSavings）的加锁入口：在 s.mu 下快照 s.venvBinDir
// 后委托给无锁核心 resolveHeadroomBinCore，使并发的 SetVenvBinDir 写入不会与本处读取
// 竞争。已持 s.mu 的调用方（start）必须直接调用 resolveHeadroomBinCore 以免自死锁。
func (s *HeadroomService) resolveHeadroomBinWithEnv() (binPath string, enhancedEnv []string) {
	s.mu.Lock()
	venvBin := s.venvBinDir
	s.mu.Unlock()
	return resolveHeadroomBinCore(venvBin)
}

// resolveHeadroomBinCore is the lock-free resolution core. It does not touch
// any HeadroomService field, so it is safe to call with OR without s.mu held.
// venvBinDir is passed in so the caller controls how s.venvBinDir is read
// (snapshot under lock via resolveHeadroomBinWithEnv for GetSavings; read
// directly under the already-held lock in start).
//
// Why resolve both the path and the env: the resolver augments PATH with
// common install locations (~/.local/bin, /opt/homebrew/bin, login-shell
// discovery, ...) so it can find headroom even when the GUI process inherited
// a minimal PATH. But the headroom child process must run with that SAME
// augmented PATH, otherwise two failure modes arise on macOS (where GUI apps
// launched from Dock get a PATH of little more than /usr/bin:/bin):
//
//  1. When resolution falls back to the bare "headroom" name, exec.Command
//     looks it up in the child PATH -- which is the minimal GUI PATH, not the
//     augmented one -- and fails with "executable file not found in $PATH".
//  2. Even when resolution succeeds with an absolute path, headroom may be a
//     Python/script shim that itself needs the augmented PATH (e.g. to locate
//     its interpreter).
//
// On top of platform.BuildEffectiveEnv, the CodeBox-managed venv bin directory
// is prepended so the venv-installed headroom wins over any system headroom.
// This injection is independent of envcheck's buildEnhancedEnv (which covers
// detection); both must inject the venv bin or detection and launch diverge.
//
// Returning the enhanced env alongside the path lets the caller pass it through
// to the child process so resolution and execution share one consistent PATH.
// Mirrors the buildEnhancedEnv() pattern used throughout the envcheck package.
func resolveHeadroomBinCore(venvBinDir string) (binPath string, enhancedEnv []string) {
	enhancedEnv = platform.BuildEffectiveEnv(os.Environ())
	if venvBin := strings.TrimSpace(venvBinDir); venvBin != "" {
		enhancedEnv = prependEnvPATH(enhancedEnv, venvBin)
	}
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	resolved, _, err := resolver.ResolveExecutable("headroom", nil, enhancedEnv)
	if err == nil && strings.TrimSpace(resolved.Path) != "" {
		return resolved.Path, enhancedEnv
	}
	// Direct fallback: if the venv headroom binary exists on disk, use it
	// even when the resolver could not locate it (e.g. venv dir is outside
	// every PATH source the resolver inspects).
	if venvBin := strings.TrimSpace(venvBinDir); venvBin != "" {
		if candidate := headroomVenvBinaryCandidate(venvBin); candidate != "" && fileExists(candidate) {
			return candidate, enhancedEnv
		}
	}
	return "headroom", enhancedEnv
}

// headroomVenvBinaryCandidate returns the platform-specific headroom executable
// path inside the venv bin directory.
func headroomVenvBinaryCandidate(venvBinDir string) string {
	venvBinDir = strings.TrimSpace(venvBinDir)
	if venvBinDir == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(venvBinDir, "headroom.exe")
	}
	return filepath.Join(venvBinDir, "headroom")
}

// fileExists reports whether the named file exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// prependEnvPATH returns a copy of env with dir prepended to the PATH entry
// (or a new PATH entry added when absent). dir is placed first so it takes
// priority over every existing entry.
func prependEnvPATH(env []string, dir string) []string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(key, "PATH") {
			found = true
			newValue := dir
			if strings.TrimSpace(value) != "" {
				newValue += string(os.PathListSeparator) + value
			}
			out = append(out, "PATH="+newValue)
			continue
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PATH="+dir)
	}
	return out
}
