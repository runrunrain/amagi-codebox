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

	env := buildChildEnv(realBackendURL, port)

	spec := platform.CommandSpec{
		Path:   resolveHeadroomBin(),
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
		s.log.Info("headroom", "代理子进程已停止", fmt.Sprintf("err=%v", err))
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

// buildChildEnv constructs the environment for the headroom child process.
//
// It starts from the process environment, strips any inherited
// ANTHROPIC_BASE_URL (death-loop prevention), and injects the variables
// headroom needs to locate its listen port and real upstream:
//   - ANTHROPIC_TARGET_API_URL: the real upstream base URL
//   - HEADROOM_PORT: the listen port
//   - HEADROOM_HOST: the bind address (always loopback)
func buildChildEnv(realBackendURL string, port int) []string {
	base := os.Environ()
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

// resolveHeadroomBin resolves the absolute path to the headroom executable
// using the platform CLI resolver (which augments PATH with common install
// locations), falling back to the bare "headroom" name so the OS can resolve
// it. Mirrors installer.go resolveNPMPath.
func resolveHeadroomBin() string {
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	resolved, _, err := resolver.ResolveExecutable("headroom", nil, os.Environ())
	if err == nil && strings.TrimSpace(resolved.Path) != "" {
		return resolved.Path
	}
	return "headroom"
}
