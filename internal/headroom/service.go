package headroom

import (
	"fmt"
	"os"
	"os/exec"
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

// Start launches the headroom proxy child process. realBackendURL is the
// real upstream API base URL (e.g. the provider's EffectiveBaseURL); headroom
// uses it via the ANTHROPIC_TARGET_API_URL environment variable to know where
// to forward compressed traffic.
//
// Death-loop prevention (critical): the child environment is derived from the
// process environment with any inherited ANTHROPIC_BASE_URL removed. If
// headroom inherited an ANTHROPIC_BASE_URL pointing at itself (DefaultPort) or
// at the codebox injection proxy (:5280), it would forward requests back into
// itself / the injection proxy that calls back into headroom, creating an
// infinite request loop. Only ANTHROPIC_TARGET_API_URL is set so headroom
// always reaches the real upstream.
func (s *HeadroomService) Start(realBackendURL string) error {
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
	binPath, enhancedEnv := resolveHeadroomBinWithEnv()
	env := buildChildEnvWithBase(realBackendURL, port, enhancedEnv)

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
		s.log.Info("headroom", "代理子进程已启动", fmt.Sprintf("pid=%d port=%d upstream=%s", pid, port, s.backendURL))
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
	s.running = false
	s.cmd = nil
	s.mu.Unlock()

	err := cmd.Process.Kill()
	if s.log != nil {
		s.log.Info("headroom", "上下文压缩已停用", fmt.Sprintf("port=%d err=%v", s.port, err))
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
// process from the given base environment.
//
// It strips any inherited ANTHROPIC_BASE_URL (death-loop prevention), and
// injects the variables headroom needs to locate its listen port and real
// upstream:
//   - ANTHROPIC_TARGET_API_URL: the real upstream base URL
//   - HEADROOM_PORT: the listen port
//   - HEADROOM_HOST: the bind address (always loopback)
//
// baseEnv is expected to carry the augmented PATH produced by
// resolveHeadroomBinWithEnv so the child process can locate headroom (and any
// interpreter it depends on) even when the GUI process inherited a minimal PATH.
func buildChildEnvWithBase(realBackendURL string, port int, baseEnv []string) []string {
	base := append([]string(nil), baseEnv...)
	out := make([]string, 0, len(base)+3)
	for _, kv := range base {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			out = append(out, kv)
			continue
		}
		// Strip inherited ANTHROPIC_BASE_URL regardless of case. Windows
		// environment variable names are case-insensitive; on POSIX the
		// canonical name is uppercase, so EqualFold covers both.
		if strings.EqualFold(key, "ANTHROPIC_BASE_URL") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out,
		"ANTHROPIC_TARGET_API_URL="+strings.TrimRight(realBackendURL, "/"),
		fmt.Sprintf("HEADROOM_PORT=%d", port),
		"HEADROOM_HOST="+headroomHost,
	)
	return out
}

// resolveHeadroomBinWithEnv resolves the absolute path to the headroom
// executable AND returns the enhanced environment used for resolution.
//
// Why both: the resolver augments PATH with common install locations
// (~/.local/bin, /opt/homebrew/bin, login-shell discovery, ...) so it can find
// headroom even when the GUI process inherited a minimal PATH. But the headroom
// child process must run with that SAME augmented PATH, otherwise two failure
// modes arise on macOS (where GUI apps launched from Dock get a PATH of little
// more than /usr/bin:/bin):
//
//  1. When resolution falls back to the bare "headroom" name, exec.Command
//     looks it up in the child PATH -- which is the minimal GUI PATH, not the
//     augmented one -- and fails with "executable file not found in $PATH".
//  2. Even when resolution succeeds with an absolute path, headroom may be a
//     Python/script shim that itself needs the augmented PATH (e.g. to locate
//     its interpreter).
//
// Returning the enhanced env alongside the path lets Start() pass it through to
// the child process so resolution and execution share one consistent PATH.
// Mirrors the buildEnhancedEnv() pattern used throughout the envcheck package.
func resolveHeadroomBinWithEnv() (binPath string, enhancedEnv []string) {
	enhancedEnv = platform.BuildEffectiveEnv(os.Environ())
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	resolved, _, err := resolver.ResolveExecutable("headroom", nil, enhancedEnv)
	if err == nil && strings.TrimSpace(resolved.Path) != "" {
		return resolved.Path, enhancedEnv
	}
	return "headroom", enhancedEnv
}
