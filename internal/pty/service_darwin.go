//go:build darwin

package pty

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"

	creackpty "github.com/creack/pty"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const maxOutputHistorySize = 1024 * 1024

type PtySession struct {
	cmd           *exec.Cmd
	ptmx          *os.File
	done          chan struct{}
	outputHistory []byte
	historyMu     sync.Mutex
	currentCols   int
	currentRows   int
	running       bool
	mu            sync.RWMutex
	exitCode      uint32
	waitErr       error
	waitOnce      sync.Once
}

type outputCallback func(data []byte)
type exitCallback func(exitCode uint32)
type resizeCallback func(cols, rows int)

type Service struct {
	sessions    map[string]*PtySession
	mu          sync.Mutex
	ctx         context.Context
	log         *logging.Service
	outputCBsMu sync.RWMutex
	outputCBs   map[string]map[string]outputCallback
	exitCBsMu   sync.RWMutex
	exitCBs     map[string]map[string]exitCallback
	resizeCBsMu sync.RWMutex
	resizeCBs   map[string]map[string]resizeCallback
}

func NewService(log *logging.Service) *Service {
	return &Service{
		sessions:  make(map[string]*PtySession),
		log:       log,
		outputCBs: make(map[string]map[string]outputCallback),
		exitCBs:   make(map[string]map[string]exitCallback),
		resizeCBs: make(map[string]map[string]resizeCallback),
	}
}

func (s *Service) RegisterOutputCallback(sessionID string, id string, cb func(data []byte)) {
	s.outputCBsMu.Lock()
	defer s.outputCBsMu.Unlock()
	if s.outputCBs[sessionID] == nil {
		s.outputCBs[sessionID] = make(map[string]outputCallback)
	}
	s.outputCBs[sessionID][id] = cb
}

func (s *Service) UnregisterOutputCallback(sessionID string, id string) {
	s.outputCBsMu.Lock()
	defer s.outputCBsMu.Unlock()
	if m, ok := s.outputCBs[sessionID]; ok {
		delete(m, id)
		if len(m) == 0 {
			delete(s.outputCBs, sessionID)
		}
	}
}

func (s *Service) RegisterExitCallback(sessionID string, id string, cb func(exitCode uint32)) {
	s.exitCBsMu.Lock()
	defer s.exitCBsMu.Unlock()
	if s.exitCBs[sessionID] == nil {
		s.exitCBs[sessionID] = make(map[string]exitCallback)
	}
	s.exitCBs[sessionID][id] = cb
}

func (s *Service) UnregisterExitCallback(sessionID string, id string) {
	s.exitCBsMu.Lock()
	defer s.exitCBsMu.Unlock()
	if m, ok := s.exitCBs[sessionID]; ok {
		delete(m, id)
		if len(m) == 0 {
			delete(s.exitCBs, sessionID)
		}
	}
}

func (s *Service) RegisterResizeCallback(sessionID string, id string, cb func(cols, rows int)) {
	s.resizeCBsMu.Lock()
	defer s.resizeCBsMu.Unlock()
	if s.resizeCBs[sessionID] == nil {
		s.resizeCBs[sessionID] = make(map[string]resizeCallback)
	}
	s.resizeCBs[sessionID][id] = cb
}

func (s *Service) UnregisterResizeCallback(sessionID string, id string) {
	s.resizeCBsMu.Lock()
	defer s.resizeCBsMu.Unlock()
	if m, ok := s.resizeCBs[sessionID]; ok {
		delete(m, id)
		if len(m) == 0 {
			delete(s.resizeCBs, sessionID)
		}
	}
}

func (s *Service) AttachSessionObserver(sessionID string, id string, outputCB func(data []byte), resizeCB func(cols, rows int)) (history []byte, cols, rows int, err error) {
	s.mu.Lock()
	ps, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return nil, 0, 0, fmt.Errorf("session not found: %s", sessionID)
	}

	ps.historyMu.Lock()
	s.outputCBsMu.Lock()
	s.resizeCBsMu.Lock()

	history = make([]byte, len(ps.outputHistory))
	copy(history, ps.outputHistory)
	cols = ps.currentCols
	rows = ps.currentRows

	if outputCB != nil {
		if s.outputCBs[sessionID] == nil {
			s.outputCBs[sessionID] = make(map[string]outputCallback)
		}
		s.outputCBs[sessionID][id] = outputCB
	}
	if resizeCB != nil {
		if s.resizeCBs[sessionID] == nil {
			s.resizeCBs[sessionID] = make(map[string]resizeCallback)
		}
		s.resizeCBs[sessionID][id] = resizeCB
	}

	s.resizeCBsMu.Unlock()
	s.outputCBsMu.Unlock()
	ps.historyMu.Unlock()
	s.mu.Unlock()

	return history, cols, rows, nil
}

func (s *Service) DetachSessionObserver(sessionID string, id string) {
	s.UnregisterOutputCallback(sessionID, id)
	s.UnregisterResizeCallback(sessionID, id)
}

func (s *Service) snapshotOutputCallbacks(sessionID string) []outputCallback {
	s.outputCBsMu.RLock()
	defer s.outputCBsMu.RUnlock()
	callbacksByID := s.outputCBs[sessionID]
	callbacks := make([]outputCallback, 0, len(callbacksByID))
	for _, cb := range callbacksByID {
		callbacks = append(callbacks, cb)
	}
	return callbacks
}

func (s *Service) snapshotExitCallbacks(sessionID string) []exitCallback {
	s.exitCBsMu.RLock()
	defer s.exitCBsMu.RUnlock()
	callbacksByID := s.exitCBs[sessionID]
	callbacks := make([]exitCallback, 0, len(callbacksByID))
	for _, cb := range callbacksByID {
		callbacks = append(callbacks, cb)
	}
	return callbacks
}

func (s *Service) snapshotResizeCallbacks(sessionID string) []resizeCallback {
	s.resizeCBsMu.RLock()
	defer s.resizeCBsMu.RUnlock()
	callbacksByID := s.resizeCBs[sessionID]
	callbacks := make([]resizeCallback, 0, len(callbacksByID))
	for _, cb := range callbacksByID {
		callbacks = append(callbacks, cb)
	}
	return callbacks
}

func (s *Service) SetContext(ctx context.Context) { s.ctx = ctx }

func (s *Service) Start(sessionID, shellPath, autoCommand, workDir string, env []string, cols, rows int) (int, error) {
	spec := platform.ResolvedLaunchSpec{
		WorkDir: workDir,
		CLI: platform.ResolvedCLI{
			Path: autoCommand,
		},
		Env:     platform.ResolvedEnv{Variables: env},
		PTYCols: cols,
		PTYRows: rows,
	}
	if shellPath == "" {
		spec.BootstrapMode = platform.BootstrapDirectCommand
	} else {
		spec.BootstrapMode = platform.BootstrapShellInline
		spec.Shell = &platform.ResolvedShell{Path: shellPath, BootstrapArg: "-lc", LoginStyle: "login"}
		spec.StartupCommand = autoCommand
	}
	return s.StartResolved(sessionID, spec)
}

func (s *Service) StartResolved(sessionID string, spec platform.ResolvedLaunchSpec) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.sessions[sessionID]; exists {
		return 0, fmt.Errorf("session %s already exists", sessionID)
	}

	cols := spec.PTYCols
	rows := spec.PTYRows
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 40
	}

	cmd, commandSummary, err := buildDarwinPTYCommand(spec)
	if err != nil {
		return 0, formatLaunchFailure(spec, "build-command", err)
	}
	if spec.WorkDir != "" {
		cmd.Dir = spec.WorkDir
	}
	cmd.Env = append([]string(nil), spec.Env.Variables...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	ptmx, err := creackpty.StartWithAttrs(cmd, &creackpty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}, cmd.SysProcAttr)
	if err != nil {
		return 0, formatLaunchFailure(spec, "spawn-pty", err)
	}

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	ps := &PtySession{cmd: cmd, ptmx: ptmx, done: make(chan struct{}), currentCols: cols, currentRows: rows, running: true}
	s.sessions[sessionID] = ps

	if s.log != nil {
		s.log.Info("pty", "启动 darwin PTY 会话", fmt.Sprintf("id=%s pid=%d mode=%s cmd=%s", sessionID, pid, spec.BootstrapMode, commandSummary))
	}

	go s.readLoop(sessionID, ps)
	go s.waitLoop(sessionID, ps)
	return pid, nil
}

func buildDarwinPTYCommand(spec platform.ResolvedLaunchSpec) (*exec.Cmd, string, error) {
	if spec.CLI.Path == "" {
		return nil, "", fmt.Errorf("resolved CLI path is empty")
	}
	if spec.BootstrapMode == platform.BootstrapDirectCommand || spec.Shell == nil || spec.Shell.Path == "" {
		cmd := exec.Command(spec.CLI.Path, spec.CLI.Args...)
		return cmd, platformCommandSummary(spec.CLI.Path, spec.CLI.Args), nil
	}

	shellPath := spec.Shell.Path
	startupCommand := spec.StartupCommand
	if startupCommand == "" {
		startupCommand = platformCommandSummary(spec.CLI.Path, spec.CLI.Args)
	}

	args := []string{}
	switch spec.Shell.Key {
	case "pwsh", "powershell":
		args = []string{"-NoLogo", "-NoProfile", "-Command", startupCommand}
	case "fish", "bash", "zsh", "sh", "":
		args = []string{"-lc", startupCommand}
	default:
		bootstrapArg := spec.Shell.BootstrapArg
		if bootstrapArg == "" {
			bootstrapArg = "-lc"
		}
		args = []string{bootstrapArg, startupCommand}
	}
	cmd := exec.Command(shellPath, args...)
	return cmd, platformCommandSummary(shellPath, args), nil
}

func platformCommandSummary(path string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, path)
	parts = append(parts, args...)
	return stringsJoin(parts, " ")
}

func stringsJoin(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += sep + parts[i]
	}
	return out
}

func formatLaunchFailure(spec platform.ResolvedLaunchSpec, stage string, err error) error {
	resolvedShell := ""
	if spec.Shell != nil {
		resolvedShell = spec.Shell.Path
	}
	return fmt.Errorf("pty launch failed at %s: %w (resolvedCLI=%s resolvedShell=%s effectivePATH=%s)", stage, err, spec.CLI.Path, resolvedShell, spec.Env.EffectivePATH)
}

func (s *Service) readLoop(sessionID string, ps *PtySession) {
	defer close(ps.done)
	buf := make([]byte, 8192)
	for {
		n, err := ps.ptmx.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			ps.historyMu.Lock()
			ps.outputHistory = append(ps.outputHistory, chunk...)
			if len(ps.outputHistory) > maxOutputHistorySize {
				ps.outputHistory = ps.outputHistory[len(ps.outputHistory)-maxOutputHistorySize:]
			}
			ps.historyMu.Unlock()
			if s.ctx != nil {
				data := base64.StdEncoding.EncodeToString(chunk)
				wailsRuntime.EventsEmit(s.ctx, "pty:data:"+sessionID, data)
			}
			for _, cb := range s.snapshotOutputCallbacks(sessionID) {
				cb(chunk)
			}
		}
		if err != nil {
			if err != io.EOF && s.log != nil {
				s.log.Debug("pty", "darwin PTY 读取结束", fmt.Sprintf("id=%s err=%v", sessionID, err))
			}
			return
		}
	}
}

func (s *Service) waitLoop(sessionID string, ps *PtySession) {
	err := ps.cmd.Wait()
	exitCode := 0
	if ps.cmd.ProcessState != nil {
		exitCode = ps.cmd.ProcessState.ExitCode()
	}
	ps.mu.Lock()
	ps.running = false
	ps.exitCode = uint32(max(exitCode, 0))
	ps.waitErr = err
	ps.mu.Unlock()
	_ = ps.ptmx.Close()
	if s.log != nil {
		s.log.Info("pty", "darwin PTY 进程退出", fmt.Sprintf("id=%s exitCode=%d err=%v", sessionID, exitCode, err))
	}
	if s.ctx != nil {
		wailsRuntime.EventsEmit(s.ctx, "pty:exit:"+sessionID, map[string]any{"exitCode": exitCode, "error": fmt.Sprintf("%v", err)})
	}
	for _, cb := range s.snapshotExitCallbacks(sessionID) {
		cb(uint32(max(exitCode, 0)))
	}
}

func (s *Service) Write(sessionID string, data string) error {
	ps, err := s.session(sessionID)
	if err != nil {
		return err
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}
	_, err = ps.ptmx.Write(raw)
	return err
}

func (s *Service) WriteLarge(sessionID string, data string) error {
	ps, err := s.session(sessionID)
	if err != nil {
		return err
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}
	const chunkSize = 1024
	for offset := 0; offset < len(raw); offset += chunkSize {
		end := offset + chunkSize
		if end > len(raw) {
			end = len(raw)
		}
		if _, err := ps.ptmx.Write(raw[offset:end]); err != nil {
			return fmt.Errorf("write chunk at offset %d: %w", offset, err)
		}
		if end < len(raw) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	return nil
}

func (s *Service) Resize(sessionID string, cols, rows int) error {
	ps, err := s.session(sessionID)
	if err != nil {
		return err
	}
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("invalid pty dimensions: %dx%d", cols, rows)
	}
	ps.mu.RLock()
	unchanged := ps.currentCols == cols && ps.currentRows == rows
	ps.mu.RUnlock()
	if unchanged {
		return nil
	}
	if err := creackpty.Setsize(ps.ptmx, &creackpty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		return err
	}
	ps.mu.Lock()
	ps.currentCols = cols
	ps.currentRows = rows
	ps.mu.Unlock()
	for _, cb := range s.snapshotResizeCallbacks(sessionID) {
		cb(cols, rows)
	}
	return nil
}

func (s *Service) GetPtyDimensions(sessionID string) (cols, rows int, err error) {
	ps, err := s.session(sessionID)
	if err != nil {
		return 0, 0, err
	}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.currentCols, ps.currentRows, nil
}

func (s *Service) Close(sessionID string) error {
	ps, err := s.session(sessionID)
	if err != nil {
		return nil
	}
	ps.mu.RLock()
	running := ps.running
	ps.mu.RUnlock()
	if !running {
		return nil
	}
	if ps.cmd.Process != nil {
		_ = ps.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-ps.done:
		case <-time.After(2 * time.Second):
			_ = ps.cmd.Process.Kill()
			<-ps.done
		}
	}
	_ = ps.ptmx.Close()
	return nil
}

func (s *Service) CloseAll() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		_ = s.Close(id)
	}
}

func (s *Service) IsRunning(sessionID string) bool {
	ps, err := s.session(sessionID)
	if err != nil {
		return false
	}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.running
}

func (s *Service) GetOutputHistory(sessionID string) ([]byte, error) {
	ps, err := s.session(sessionID)
	if err != nil {
		return nil, err
	}
	ps.historyMu.Lock()
	defer ps.historyMu.Unlock()
	history := make([]byte, len(ps.outputHistory))
	copy(history, ps.outputHistory)
	return history, nil
}

func (s *Service) RunningCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, ps := range s.sessions {
		ps.mu.RLock()
		if ps.running {
			count++
		}
		ps.mu.RUnlock()
	}
	return count
}

func (s *Service) session(sessionID string) (*PtySession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	return ps, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
