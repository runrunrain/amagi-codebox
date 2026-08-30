//go:build windows

package pty

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/processcap"

	"github.com/UserExistsError/conpty"
)

const maxOutputHistorySize = 1024 * 1024 // 1MB 环形缓冲区上限，避免移动端回放只剩会话尾部

// PtySession 一个 ConPTY 会话
type PtySession struct {
	cpty           *conpty.ConPty
	cancel         context.CancelFunc
	done           chan struct{}
	ready          chan struct{}
	readArmed      chan struct{}
	waitArmed      chan struct{}
	exited         chan struct{}
	shellReady     chan struct{}
	shellReadyOnce sync.Once
	bootstrapMode  platform.LaunchBootstrapMode
	outputHistory  []byte     // 最近输出的环形缓冲区，供后加入的 WebSocket 客户端重放
	historyMu      sync.Mutex // 保护 outputHistory
	emitSeq        uint64     // monotonic counter incremented per PTY output chunk under historyMu
	currentCols    int        // 当前 PTY 列数
	currentRows    int        // 当前 PTY 行数
	runHandle      any        // opaque run identity; passed back to RunEventSink
	bindingID      processcap.BindingID
}

// outputCallback PTY 输出回调，供远程服务器的 WebSocket 使用
type outputCallback func(data []byte)

// exitCallback PTY 进程退出回调
type exitCallback func(exitCode uint32)

// resizeCallback PTY 尺寸变化回调，供远程 observer 同步 dimensions 帧
type resizeCallback func(cols, rows int)

// Service 管理所有嵌入式终端的 PTY 会话。
// raw PTY 的 read/wait loop 不再直接 EventsEmit（design §8.6 M-01）：
// 全部 output/exit 经注入的 RunEventSink 交 RunEventProjector 做 run-scoped 投影。
// 同时支持注册远程回调，供 WebSocket 转发使用。
type Service struct {
	sessions          map[string]*PtySession
	mu                sync.Mutex
	ownerID           uint64
	bindingGeneration uint64
	log               *logging.Service
	runSink           RunEventSink // sole output/exit sink; replaces direct EventsEmit (design §8.6 M-01)
	outputCBsMu       sync.RWMutex
	outputCBs         map[string]map[string]outputCallback // sessionID → {connID → cb}
	exitCBsMu         sync.RWMutex
	exitCBs           map[string]map[string]exitCallback // sessionID → {connID → cb}
	resizeCBsMu       sync.RWMutex
	resizeCBs         map[string]map[string]resizeCallback // sessionID → {connID → cb}
}

func NewService(log *logging.Service) *Service {
	ownerID, _ := processcap.NewOwnerID()
	return &Service{
		sessions:  make(map[string]*PtySession),
		ownerID:   ownerID,
		log:       log,
		outputCBs: make(map[string]map[string]outputCallback),
		exitCBs:   make(map[string]map[string]exitCallback),
		resizeCBs: make(map[string]map[string]resizeCallback),
	}
}

// RegisterOutputCallback 注册 PTY 输出回调（WebSocket 连接时调用）
func (s *Service) RegisterOutputCallback(sessionID string, id string, cb func(data []byte)) {
	s.outputCBsMu.Lock()
	defer s.outputCBsMu.Unlock()
	if s.outputCBs[sessionID] == nil {
		s.outputCBs[sessionID] = make(map[string]outputCallback)
	}
	s.outputCBs[sessionID][id] = cb
}

// UnregisterOutputCallback 注销 PTY 输出回调（WebSocket 断开时调用）
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

// RegisterExitCallback 注册 PTY 进程退出回调
func (s *Service) RegisterExitCallback(sessionID string, id string, cb func(exitCode uint32)) {
	s.exitCBsMu.Lock()
	defer s.exitCBsMu.Unlock()
	if s.exitCBs[sessionID] == nil {
		s.exitCBs[sessionID] = make(map[string]exitCallback)
	}
	s.exitCBs[sessionID][id] = cb
}

// UnregisterExitCallback 注销 PTY 进程退出回调（参数 id 对应 RegisterExitCallback 时传入的 id）
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

// RegisterResizeCallback 注册 PTY 尺寸变化回调
func (s *Service) RegisterResizeCallback(sessionID string, id string, cb func(cols, rows int)) {
	s.resizeCBsMu.Lock()
	defer s.resizeCBsMu.Unlock()
	if s.resizeCBs[sessionID] == nil {
		s.resizeCBs[sessionID] = make(map[string]resizeCallback)
	}
	s.resizeCBs[sessionID][id] = cb
}

// UnregisterResizeCallback 注销 PTY 尺寸变化回调
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

// AttachSessionObserver 以原子方式完成 observer attach：
// 先冻结 history / dimensions 快照，再注册 live output / dimensions 回调，避免 history 与 live 之间丢帧。
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

// DetachSessionObserver 注销通过 AttachSessionObserver 注册的 live 回调。
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

// SetRunEventSink injects the sole output/exit sink. Raw PTY goroutines call
// this sink instead of emitting Wails events directly (design §8.6 M-01).
func (s *Service) SetRunEventSink(sink RunEventSink) {
	s.runSink = sink
}

// Start 创建一个 ConPTY 会话。
// shellPath: shell 可执行文件路径（如 "C:\\Program Files\\PowerShell\\7\\pwsh.exe"），空则使用 autoCommand（如 "claude" 或 "opencode"）
// autoCommand: 启动 shell 后自动发送的命令（如 "claude" 或 "opencode"），空则不发送；如果 shellPath 为空，则直接作为启动命令
// workDir: 工作目录
// env: 环境变量
// cols, rows: 终端尺寸
func (s *Service) Start(sessionID, shellPath, autoCommand, workDir string, env []string, cols, rows int) (int, error) {
	cliPath := autoCommand
	cliArgs := []string{}
	if shellPath == "" {
		parts := strings.Fields(strings.TrimSpace(autoCommand))
		if len(parts) > 0 {
			cliPath = parts[0]
			cliArgs = append(cliArgs, parts[1:]...)
		}
	}
	if cliPath == "" {
		cliPath = "claude"
	}
	spec := platform.ResolvedLaunchSpec{
		WorkDir: workDir,
		CLI: platform.ResolvedCLI{
			Path: cliPath,
			Args: cliArgs,
		},
		Env:     platform.ResolvedEnv{Variables: env},
		PTYCols: cols,
		PTYRows: rows,
		Shell: func() *platform.ResolvedShell {
			if strings.TrimSpace(shellPath) == "" {
				return nil
			}
			return &platform.ResolvedShell{Path: shellPath}
		}(),
	}
	if strings.TrimSpace(shellPath) == "" {
		spec.BootstrapMode = platform.BootstrapDirectCommand
	} else {
		spec.BootstrapMode = platform.BootstrapShellInline
		spec.StartupCommand = autoCommand
	}
	return s.StartResolved(sessionID, spec)
}

func (s *Service) StartResolved(sessionID string, spec platform.ResolvedLaunchSpec) (int, error) {
	return s.StartResolvedWithRun(sessionID, spec, nil)
}

// StartResolvedWithRun starts a PTY session bound to an opaque run handle.
// The handle is passed back to the RunEventSink for run-scoped output/exit
// projection (design §8.6). A nil handle means output/exit is dropped
// (fail-closed: no ungated Wails emit).
func (s *Service) StartResolvedWithRun(sessionID string, spec platform.ResolvedLaunchSpec, runHandle any) (int, error) {
	evidence, err := s.StartResolvedWithRunEvidence(sessionID, spec, runHandle)
	return evidence.PID, err
}

// StartResolvedWithRunEvidence returns the concrete exact-close capability
// minted with the backend map insertion. Callers retain it in processcap.Registry.
func (s *Service) StartResolvedWithRunEvidence(sessionID string, spec platform.ResolvedLaunchSpec, runHandle any) (processcap.StartEvidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ownerID == 0 {
		return processcap.StartEvidence{}, fmt.Errorf("pty owner identity unavailable")
	}
	if _, exists := s.sessions[sessionID]; exists {
		return processcap.StartEvidence{}, fmt.Errorf("session %s already exists", sessionID)
	}
	if s.bindingGeneration == ^uint64(0) {
		return processcap.StartEvidence{}, fmt.Errorf("pty binding generation exhausted")
	}
	bindingGeneration := s.bindingGeneration + 1

	cols := spec.PTYCols
	rows := spec.PTYRows
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 40
	}

	workDir := spec.WorkDir
	commandLine, sendAutoCommand := buildResolvedStartupPlan(spec, s.log)

	s.log.Info("pty", "创建 ConPTY 会话", fmt.Sprintf("id=%s cmd=%s autoCmd=%s workDir=%s size=%dx%d", sessionID, commandLine, redactAutoCommandForLog(sendAutoCommand), workDir, cols, rows))

	opts := []conpty.ConPtyOption{
		conpty.ConPtyDimensions(cols, rows),
	}
	if workDir != "" {
		opts = append(opts, conpty.ConPtyWorkDir(workDir))
	}
	if spec.Env.Variables != nil {
		opts = append(opts, conpty.ConPtyEnv(spec.Env.Variables))
	}

	cpty, err := conpty.Start(commandLine, opts...)
	if err != nil {
		s.log.Error("pty", "ConPTY 启动失败", err.Error())
		return processcap.StartEvidence{}, fmt.Errorf("conpty start: %w", err)
	}

	pid := cpty.Pid()
	s.log.Info("pty", "ConPTY 已启动", fmt.Sprintf("id=%s pid=%d", sessionID, pid))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	ready := make(chan struct{})
	readArmed := make(chan struct{})
	waitArmed := make(chan struct{})
	exited := make(chan struct{})

	bindingID := processcap.BindingID{Kind: processcap.BackendPTY, Owner: s.ownerID, Generation: bindingGeneration}
	ps := &PtySession{
		cpty:          cpty,
		cancel:        cancel,
		done:          done,
		ready:         ready,
		readArmed:     readArmed,
		waitArmed:     waitArmed,
		exited:        exited,
		shellReady:    make(chan struct{}),
		bootstrapMode: spec.BootstrapMode,
		currentCols:   cols,
		currentRows:   rows,
		runHandle:     runHandle,
		bindingID:     bindingID,
	}
	s.bindingGeneration = bindingGeneration
	s.sessions[sessionID] = ps

	// 启动读取协程：从 ConPTY 读取输出，通过 Wails 事件发送到前端
	go s.readLoop(sessionID, ps, ctx, done)

	// 启动等待协程：监控进程退出
	go s.waitLoop(sessionID, ps, ctx)
	go func() {
		<-ps.readArmed
		<-ps.waitArmed
		close(ps.ready)
	}()

	// 如果指定了自动命令，延迟发送到 shell。控制托管会话（runHandle != nil）
	// 的 bootstrap 由 App 层经控制门 DoBootstrapPTY 发送（M-005：不裸写 cpty），
	// 这里只为非托管（legacy）启动保留原始延迟写入。
	if sendAutoCommand != "" && runHandle == nil {
		go func() {
			time.Sleep(1000 * time.Millisecond) // 等待 shell 初始化完成
			cmd := sendAutoCommand + "\r\n"
			_, _ = ps.cpty.Write([]byte(cmd))
			s.log.Info("pty", "自动发送命令", fmt.Sprintf("id=%s cmd=%s", sessionID, redactAutoCommandForLog(sendAutoCommand)))
		}()
	}

	binding := &ptyBinding{service: s, sessionID: sessionID, session: ps, id: bindingID}
	return processcap.StartEvidence{PID: pid, Binding: binding}, nil
}

// StartupAutoCommand returns the auto-command that StartResolvedWithRun would
// send after shell init for the given spec (M-005). The App layer uses this to
// route the delayed bootstrap write through the control gate (DoBootstrapPTY)
// instead of a raw cpty.Write goroutine for control-managed launches. Returns
// "" when no delayed command is needed (the command is either embedded in the
// shell invocation or absent).
func (s *Service) StartupAutoCommand(spec platform.ResolvedLaunchSpec) string {
	_, sendAutoCommand := buildResolvedStartupPlan(spec, s.log)
	return sendAutoCommand
}

func buildResolvedStartupPlan(spec platform.ResolvedLaunchSpec, log *logging.Service) (string, string) {
	// WSL sessions run the CLI inside a Linux distro. The command line is built
	// entirely here (wsl.exe -d ... --cd ... -- bash -lic '...'); it must not pass
	// through the pwsh/cmd/exe fallback logic below.
	if spec.BootstrapMode == platform.BootstrapWSL {
		return buildWSLCommandLine(spec, log), ""
	}

	shellPath := ""
	if spec.Shell != nil {
		shellPath = spec.Shell.Path
	}
	autoCommand := spec.StartupCommand
	if spec.BootstrapMode == platform.BootstrapDirectCommand {
		autoCommand = buildCommandLine(spec.CLI.Path, spec.CLI.Args)
	} else if spec.BootstrapMode == platform.BootstrapShellInline {
		autoCommand = normalizeWindowsShellWrapperCommand(autoCommand)
	}

	commandLine, sendAutoCommand := resolveStartupPlan(shellPath, autoCommand)

	// 验证 shell 路径是否存在，如果不存在则尝试回退
	if commandLine != "" && !isDirectCommand(commandLine) {
		resolvedPath := resolveShellPath(commandLine, log)
		if resolvedPath != commandLine {
			if log != nil {
				log.Info("pty", "Shell 路径回退", fmt.Sprintf("原路径=%s 回退到=%s", commandLine, resolvedPath))
			}
			commandLine = resolvedPath
		}
	}

	if shellPath != "" && autoCommand != "" && spec.BootstrapMode != platform.BootstrapShellAttach {
		commandLine, sendAutoCommand = buildStartupCommandLine(commandLine, autoCommand)
	}

	return commandLine, sendAutoCommand
}

// readLoop 持续读取 ConPTY 输出并发送给前端及所有注册的远程回调
func (s *Service) readLoop(sessionID string, ps *PtySession, ctx context.Context, done chan struct{}) {
	close(ps.readArmed)
	defer close(done)
	buf := make([]byte, 8192)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := ps.cpty.Read(buf)
		if n > 0 {
			ps.shellReadyOnce.Do(func() { close(ps.shellReady) })
			chunk := make([]byte, n)
			copy(chunk, buf[:n])

			// 追加到输出历史缓冲区（供后加入的 WebSocket 客户端重放）
			ps.historyMu.Lock()
			ps.emitSeq++
			seq := ps.emitSeq
			ps.outputHistory = append(ps.outputHistory, chunk...)
			if len(ps.outputHistory) > maxOutputHistorySize {
				ps.outputHistory = trimHistoryToFrontier(ps.outputHistory, maxOutputHistorySize)
			}
			ps.historyMu.Unlock()

			// Run-scoped output: raw PTY never emits Wails events directly
			// (design §8.6 M-01). The projector validates the run handle and
			// emits run-tagged pty:data if the run is still current.
			if s.runSink != nil {
				s.runSink.OfferOutput(ps.runHandle, seq, chunk)
			}

			// 远程 WebSocket 回调
			for _, cb := range s.snapshotOutputCallbacks(sessionID) {
				cb(chunk)
			}
		}
		if err != nil {
			s.log.Debug("pty", "读取结束", fmt.Sprintf("id=%s err=%v", sessionID, err))
			return
		}
	}
}

// waitLoop 等待进程退出并通知前端及所有注册的远程回调
func (s *Service) waitLoop(sessionID string, ps *PtySession, ctx context.Context) {
	close(ps.waitArmed)
	defer close(ps.exited)
	exitCode, err := ps.cpty.Wait(ctx)
	s.log.Info("pty", "进程退出", fmt.Sprintf("id=%s exitCode=%d err=%v", sessionID, exitCode, err))

	// Run-scoped exit: raw PTY never emits Wails events directly (design §8.6).
	failed := err != nil && err != context.Canceled
	if s.runSink != nil {
		s.runSink.OfferExit(ps.runHandle, exitCode, failed)
	}

	// 远程 WebSocket 退出回调
	for _, cb := range s.snapshotExitCallbacks(sessionID) {
		cb(exitCode)
	}
}

// WriteRaw writes raw (non-base64) bytes to the PTY. Used by the control gate
// closures which decode base64 at the App layer and checkpoint per chunk
// before each irreversible syscall (design §6.1, §9.4). M-009: the context
// carries the operation deadline; the write observes it non-blocking before the
// syscall so a gated/timeout-cancelled effect can bail. The underlying ConPTY
// write syscall cannot be interrupted, so the gate additionally bounds the
// effect and quarantines the backend on timeout.
func (s *Service) WriteRaw(ctx context.Context, sessionID string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err // gated/timeout-cancelled before the irreversible syscall
	}
	s.mu.Lock()
	ps, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	_, err := ps.cpty.Write(data)
	return err
}

// WaitReadyForBinding is the exact PTY ready/live barrier used by staged launch
// transactions. Ready means the exact map owner is still installed, both the
// read and wait pumps have been armed, and the process has not reported exit.
// Shell-attach mode additionally waits for the exact ConPTY's first output,
// proving that the interactive shell initialized before bootstrap input.
func (s *Service) WaitReadyForBinding(ctx context.Context, sessionID string, bindingID processcap.BindingID) error {
	s.mu.Lock()
	ps, ok := s.sessions[sessionID]
	if !ok || ps.bindingID != bindingID {
		s.mu.Unlock()
		return fmt.Errorf("session %s binding is not current", sessionID)
	}
	ready, exited := ps.ready, ps.exited
	s.mu.Unlock()

	if err := waitExactPTYReadiness(ctx, sessionID, ready, exited, ps.shellReady, ps.bootstrapMode == platform.BootstrapShellAttach); err != nil {
		return err
	}
	s.mu.Lock()
	current := s.sessions[sessionID] == ps && ps.bindingID == bindingID
	s.mu.Unlock()
	if !current {
		return fmt.Errorf("session %s binding changed at PTY ready barrier", sessionID)
	}
	return nil
}

// WriteRawForBinding writes only when the exact binding minted by Start is
// still the current live owner. It prevents a delayed bootstrap from reaching
// an ABA replacement that reused the same SessionID.
func (s *Service) WriteRawForBinding(ctx context.Context, sessionID string, bindingID processcap.BindingID, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	ps, ok := s.sessions[sessionID]
	if !ok || ps.bindingID != bindingID {
		s.mu.Unlock()
		return fmt.Errorf("session %s binding is not current", sessionID)
	}
	exited := ps.exited
	s.mu.Unlock()
	select {
	case <-exited:
		return fmt.Errorf("session %s exited before exact PTY write", sessionID)
	default:
	}
	_, err := ps.cpty.Write(data)
	return err
}

// Write 向 PTY 写入数据（前端用户输入）。data 为 base64 编码。
func (s *Service) Write(sessionID string, data string) error {
	s.mu.Lock()
	ps, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}

	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}
	_, err = ps.cpty.Write(raw)
	return err
}

// WriteLarge 向 PTY 分块写入大量数据（用于长文本粘贴）。data 为 base64 编码。
// 将数据拆分为多个小块逐步写入，避免 ConPTY 输入缓冲区溢出导致截断。
func (s *Service) WriteLarge(sessionID string, data string) error {
	s.mu.Lock()
	ps, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}

	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}

	const chunkSize = 1024 // 每次写入 1KB，避免缓冲区溢出
	for offset := 0; offset < len(raw); offset += chunkSize {
		end := offset + chunkSize
		if end > len(raw) {
			end = len(raw)
		}
		chunk := raw[offset:end]
		if _, err := ps.cpty.Write(chunk); err != nil {
			return fmt.Errorf("write chunk at offset %d: %w", offset, err)
		}
		// 如果不是最后一块，短暂等待让 ConPTY 消费缓冲区
		if end < len(raw) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	return nil
}

// Resize 调整 PTY 尺寸。M-009: ctx 携带操作 deadline，resize 在 syscall 前
// 非阻塞观察它；底层 syscall 不可中断，gate 侧另有 bounded+quarantine 兑底。
func (s *Service) Resize(ctx context.Context, sessionID string, cols, rows int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	ps, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("session %s not found", sessionID)
	}
	// 维度未变时跳过 ConPTY resize，避免触发不必要的屏幕缓冲区重绘
	if ps.currentCols == cols && ps.currentRows == rows {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	if err := ps.cpty.Resize(cols, rows); err != nil {
		return err
	}
	s.mu.Lock()
	ps.currentCols = cols
	ps.currentRows = rows
	s.mu.Unlock()

	for _, cb := range s.snapshotResizeCallbacks(sessionID) {
		cb(cols, rows)
	}

	return nil
}

// GetPtyDimensions 返回指定 PTY 会话的当前尺寸，供远程 WebSocket 客户端同步。
func (s *Service) GetPtyDimensions(sessionID string) (cols, rows int, err error) {
	s.mu.Lock()
	ps, exists := s.sessions[sessionID]
	if !exists {
		s.mu.Unlock()
		return 0, 0, fmt.Errorf("session not found: %s", sessionID)
	}
	cols = ps.currentCols
	rows = ps.currentRows
	s.mu.Unlock()
	return cols, rows, nil
}

// DetachSession forcibly detaches/closes the ConPTY backend for a session
// (R3-004). It is invoked by the control gate's quarantine path when a bounded
// raw Write/Resize times out mid-syscall: the underlying ConPTY write syscall
// cannot be interrupted, so closing the ConPTY handle releases the stuck
// overlapped I/O (the OS cancels outstanding I/O on handle close) and the
// locked resource. This unblocks the stuck goroutine and lets a trusted desktop
// recovery lifecycle (Stop/Restart) clean up. Safe to call on an already-closed
// session (idempotent no-op). It does NOT block on the read loop (a bounded
// Close already does that; Detach only needs to tear down the handle).
func (s *Service) DetachSession(sessionID string) (*DetachReceipt, error) {
	s.mu.Lock()
	ps, ok := s.sessions[sessionID]
	if ok {
		delete(s.sessions, sessionID)
	}
	s.mu.Unlock()
	if !ok {
		receipt := newDetachReceipt()
		_ = detachWithExactReaper(receipt, func() error { return nil }, nil)
		return receipt, nil
	}
	return s.detachExactSession(sessionID, ps)
}

func (s *Service) detachExactSession(sessionID string, ps *PtySession) (*DetachReceipt, error) {
	receipt := newDetachReceipt()
	s.mu.Lock()
	if s.sessions[sessionID] == ps {
		delete(s.sessions, sessionID)
	}
	s.mu.Unlock()
	if s.log != nil {
		s.log.Info("pty", "R4-004 强制 detach ConPTY 后端", "id="+sessionID)
	}
	ps.cancel()
	closeErr := detachWithExactReaper(receipt, func() error {
		return ps.cpty.Close()
	}, func(err error) {
		if s.log != nil {
			s.log.Warn("pty", "R4-004 exact ConPTY detach 重试失败", fmt.Sprintf("id=%s err=%v", sessionID, err))
		}
	})
	if closeErr != nil && s.log != nil {
		s.log.Warn("pty", "R4-004 ConPTY detach 首次关闭失败，已进入 exact reaper", fmt.Sprintf("id=%s err=%v", sessionID, closeErr))
	}
	return receipt, closeErr
}

type ptyBinding struct {
	service   *Service
	sessionID string
	session   *PtySession
	id        processcap.BindingID
	once      sync.Once
	evidence  processcap.ExactCloseEvidence
}

func (b *ptyBinding) BindingID() processcap.BindingID { return b.id }

func (b *ptyBinding) CloseExact(ctx context.Context) processcap.ExactCloseEvidence {
	b.once.Do(func() {
		receipt, closeErr := b.service.detachExactSession(b.sessionID, b.session)
		disposition := processcap.CloseConfirmed
		if closeErr != nil || !receipt.Confirmed() {
			disposition = processcap.CloseIndeterminate
		}
		evidence, err := processcap.NewExactCloseEvidence(b.id, receipt.Identity(), disposition, receipt)
		if err != nil {
			panic(err)
		}
		b.evidence = evidence
	})
	return b.evidence
}

// Close 关闭指定 PTY 会话
func (s *Service) Close(sessionID string) error {
	s.mu.Lock()
	ps, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	delete(s.sessions, sessionID)
	s.mu.Unlock()

	s.log.Info("pty", "关闭 PTY 会话", "id="+sessionID)
	ps.cancel()
	err := ps.cpty.Close()
	// M-009: bounded wait for the read loop to exit. A stuck read must not hold a
	// gate deadline indefinitely; after the deadline Close returns (the session
	// is already removed + canceled, so the goroutine will exit when its read
	// returns and produce no further committable observation — the committer's
	// backendEpoch check drops late output).
	select {
	case <-ps.done:
	case <-time.After(ptyCloseWaitTimeout):
		s.log.Warn("pty", "关闭等待读取协程退出超时", "id="+sessionID)
	}
	return err
}

// CloseAll 关闭所有 PTY 会话
func (s *Service) CloseAll() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	for _, id := range ids {
		s.Close(id)
	}
}

// IsRunning 检查会话是否存在
func (s *Service) IsRunning(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sessions[sessionID]
	return ok
}

// GetOutputHistory 返回指定会话的输出历史（最多 maxOutputHistorySize 字节），
// 供新连接的 WebSocket 客户端重放，避免"后加入者看不到历史"的问题。
func (s *Service) GetOutputHistory(sessionID string) ([]byte, error) {
	s.mu.Lock()
	ps, exists := s.sessions[sessionID]
	s.mu.Unlock()
	if !exists {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	ps.historyMu.Lock()
	defer ps.historyMu.Unlock()
	history := make([]byte, len(ps.outputHistory))
	copy(history, ps.outputHistory)
	return history, nil
}

// GetOutputHistoryWithSeq returns a snapshot of the output history along with
// the emitSeq at the time of the snapshot. This allows frontend consumers to
// deduplicate: any live event with seq <= returned seq is already contained in
// the history snapshot.
func (s *Service) GetOutputHistoryWithSeq(sessionID string) ([]byte, uint64, error) {
	s.mu.Lock()
	ps, exists := s.sessions[sessionID]
	s.mu.Unlock()
	if !exists {
		return nil, 0, fmt.Errorf("session not found: %s", sessionID)
	}
	ps.historyMu.Lock()
	defer ps.historyMu.Unlock()
	history := make([]byte, len(ps.outputHistory))
	copy(history, ps.outputHistory)
	return history, ps.emitSeq, nil
}

// RunningCount 返回运行中的 PTY 数量
func (s *Service) RunningCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func resolveStartupPlan(shellPath, autoCommand string) (string, string) {
	if shellPath == "" {
		if autoCommand == "" {
			autoCommand = "claude"
		}
		return autoCommand, ""
	}
	return shellPath, autoCommand
}

func buildStartupCommandLine(commandLine, autoCommand string) (string, string) {
	if autoCommand == "" {
		return commandLine, ""
	}
	autoCommand = normalizeWindowsShellWrapperCommand(autoCommand)

	quotedShell := quoteCommandPath(commandLine)
	if containsIgnoreCase(commandLine, "pwsh") || containsIgnoreCase(commandLine, "powershell") {
		// -ExecutionPolicy Bypass 是进程级参数，仅对当前 PowerShell 会话生效，
		// 不改变系统执行策略。让 npm 全局安装的 .ps1 shim（如 opencode.ps1）
		// 在系统执行策略为 Restricted 的机器上也能正常运行。
		return fmt.Sprintf(`%s -NoProfile -NoLogo -NoExit -ExecutionPolicy Bypass -Command "%s"`, quotedShell, buildPowerShellCallCommand(autoCommand)), ""
	}
	if containsIgnoreCase(commandLine, "cmd") {
		return fmt.Sprintf(`%s /K "chcp 65001 >nul && %s"`, quotedShell, escapeCmdCommand(autoCommand)), ""
	}
	if commandLine != "claude" && commandLine != "opencode" {
		return quotedShell, autoCommand
	}
	return commandLine, autoCommand
}

// buildWSLCommandLine assembles the ConPTY command line for a WSL-backed
// session:
//
//	wsl.exe -d <distro> --cd "<winWorkDir>" -- bash -lic "<payload>"
//
// Two layers of quoting are involved and BOTH matter:
//
//  1. Windows layer: ConPTY passes this string to CreateProcess, where
//     CommandLineToArgvW splits it into argv. Windows does NOT treat single
//     quotes specially, so the bash payload must be wrapped in DOUBLE quotes
//     with internal double quotes / backslashes escaped, so the whole payload
//     arrives as ONE argv token to wsl.exe (which forwards it verbatim to bash).
//  2. bash layer: inside that single argv token, each CLI token is wrapped in
//     POSIX single quotes so spaces / $() / metachars in model names or args
//     cannot be re-interpreted by bash.
//
// The payload runs the CLI (bare name, resolved via the WSL PATH) then execs an
// interactive login shell so the terminal stays a Linux environment after the
// CLI exits. With no CLI (pure terminal) it just opens the interactive shell.
// --cd lets WSL do the /mnt mapping; WSLENV (already in spec.Env.Variables)
// carries injected auth/provider env across the boundary.
func buildWSLCommandLine(spec platform.ResolvedLaunchSpec, log *logging.Service) string {
	distro := platform.DefaultWSLDistro(spec.Env.Variables)
	if distro == "" {
		// Defensive: the resolver only selects WSL when a usable distro exists.
		// If we somehow reach here without one, fall back to a bare wsl.exe so the
		// failure is visible rather than launching a wrong shell silently.
		if log != nil {
			log.Warn("pty", "WSL 会话无可用发行版，回退裸 wsl.exe", "")
		}
		return "wsl.exe"
	}

	// wsl.exe does NOT accept a double-quoted distro name after -d: it treats the
	// surrounding quotes as part of the name and fails with WSL_E_DISTRO_NOT_FOUND
	// (verified on real Windows CommandLineToArgvW parsing). Distro names from
	// `wsl -l -q` are plain tokens without spaces, so pass the name bare.
	parts := []string{"wsl.exe", "-d", distro}
	if strings.TrimSpace(spec.WorkDir) != "" {
		parts = append(parts, "--cd", windowsQuoteArg(spec.WorkDir))
		// C8: a Windows drive-letter workdir lands on the /mnt/<drive> DrvFS/9P
		// mount, whose I/O is dramatically slower than the distro's ext4 for
		// small-file / git-heavy workloads. Advisory log only — the session still
		// starts. (No session-start advisory UI mechanism exists; see
		// docs/user/usage.md 工作目录选型 for user-facing guidance.)
		if workDirMapsToDrvFS(spec.WorkDir) && log != nil {
			log.Warn("pty", "DrvFS 工作区 I/O 显著慢于 ext4",
				fmt.Sprintf("workDir=%s 挂载为 %s；建议将工作区放在 WSL ext4（如 ~/）内", spec.WorkDir, drvfsMountPath(spec.WorkDir)))
		}
	}
	parts = append(parts, "--", "bash", "-lic")

	payload := buildWSLInnerCommand(spec.CLI.Path, spec.CLI.Args)
	parts = append(parts, windowsQuoteArg(payload))
	return strings.Join(parts, " ")
}

// buildWSLInnerCommand builds the bash -lic payload: run the CLI (if any) with
// each token POSIX-single-quoted, then hand off to an interactive login shell so
// the session stays in Linux.
func buildWSLInnerCommand(cliName string, args []string) string {
	cli := strings.TrimSpace(cliName)
	if cli == "" {
		return "exec bash -li"
	}
	tokens := make([]string, 0, 1+len(args))
	tokens = append(tokens, bashSingleQuote(cli))
	for _, a := range args {
		tokens = append(tokens, bashSingleQuote(a))
	}
	return strings.Join(tokens, " ") + "; exec bash -li"
}

// bashSingleQuote wraps a token in POSIX single quotes, escaping internal single
// quotes via the standard '\'' idiom. This neutralises every bash metacharacter.
func bashSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// workDirMapsToDrvFS reports whether workDir is a Windows drive-letter path
// (e.g. X:\repo): wsl.exe --cd maps those onto the /mnt/<drive> DrvFS/9P mount
// inside the distro. Linux-style paths (~/... or /home/...) stay on ext4.
func workDirMapsToDrvFS(workDir string) bool {
	w := strings.TrimSpace(workDir)
	return len(w) >= 2 && w[1] == ':' && isDriveLetter(w[0])
}

// drvfsMountPath converts a Windows drive-letter path to its WSL DrvFS mount
// point (X:\a\b → /mnt/x/a/b). Best-effort, used for log detail only.
func drvfsMountPath(winPath string) string {
	w := strings.TrimSpace(winPath)
	if !workDirMapsToDrvFS(w) {
		return ""
	}
	return "/mnt/" + strings.ToLower(w[:1]) + strings.ReplaceAll(w[2:], `\`, "/")
}

func isDriveLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// windowsQuoteArg wraps a value so CommandLineToArgvW parses it as a single
// argv token. It always double-quotes, escapes internal double quotes as \", and
// doubles any run of backslashes that immediately precedes the closing quote so
// a trailing backslash (e.g. a drive-root workdir "D:\") cannot escape it.
func windowsQuoteArg(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch c {
		case '\\':
			backslashes++
		case '"':
			// Escape all pending backslashes (they precede a quote), then the quote.
			for j := 0; j < backslashes*2+1; j++ {
				b.WriteByte('\\')
			}
			b.WriteByte('"')
			backslashes = 0
		default:
			for j := 0; j < backslashes; j++ {
				b.WriteByte('\\')
			}
			backslashes = 0
			b.WriteByte(c)
		}
	}
	// Double any trailing backslashes so they don't escape the closing quote.
	for j := 0; j < backslashes*2; j++ {
		b.WriteByte('\\')
	}
	b.WriteByte('"')
	return b.String()
}

func quoteCommandPath(commandLine string) string {
	if strings.HasPrefix(commandLine, `"`) || strings.ContainsRune(commandLine, ' ') {
		return fmt.Sprintf(`"%s"`, strings.Trim(commandLine, `"`))
	}
	return commandLine
}

func buildCommandLine(command string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, quoteCommandPath(command))
	for _, arg := range args {
		parts = append(parts, quoteCommandPath(arg))
	}
	return strings.Join(parts, " ")
}

func normalizeWindowsShellWrapperCommand(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return command
	}

	firstToken, rest := splitFirstStartupToken(trimmed)
	if firstToken == "" || !isWindowsShellWrapperPath(firstToken) {
		return command
	}

	base := windowsPathBase(firstToken)
	name := base[:len(base)-len(wrapperExtension(base))]
	if rest == "" {
		return name
	}
	return name + " " + rest
}

func splitFirstStartupToken(command string) (string, string) {
	if command == "" {
		return "", ""
	}
	if command[0] == '"' {
		var token strings.Builder
		for i := 1; i < len(command); i++ {
			if command[i] == '"' {
				return token.String(), strings.TrimSpace(command[i+1:])
			}
			token.WriteByte(command[i])
		}
		return token.String(), ""
	}

	for i := 0; i < len(command); i++ {
		switch command[i] {
		case ' ', '\t', '\r', '\n':
			return command[:i], strings.TrimSpace(command[i+1:])
		}
	}
	return command, ""
}

func isWindowsShellWrapperPath(token string) bool {
	if wrapperExtension(token) == "" {
		return false
	}
	return strings.Contains(token, `\`) || strings.Contains(token, `/`)
}

func wrapperExtension(path string) string {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".cmd") {
		return path[len(path)-len(".cmd"):]
	}
	if strings.HasSuffix(lower, ".bat") {
		return path[len(path)-len(".bat"):]
	}
	return ""
}

func windowsPathBase(path string) string {
	lastBackslash := strings.LastIndex(path, `\`)
	lastSlash := strings.LastIndex(path, `/`)
	idx := lastBackslash
	if lastSlash > idx {
		idx = lastSlash
	}
	if idx < 0 || idx+1 >= len(path) {
		return path
	}
	return path[idx+1:]
}

func buildPowerShellCallCommand(command string) string {
	parts := splitStartupCommand(command)
	quotedParts := make([]string, 0, len(parts)+1)
	quotedParts = append(quotedParts, "&")
	for _, part := range parts {
		quotedParts = append(quotedParts, quotePowerShellSingleQuotedToken(part))
	}
	return strings.Join(quotedParts, " ")
}

func splitStartupCommand(command string) []string {
	var parts []string
	var current strings.Builder
	inDoubleQuotes := false

	flush := func() {
		if current.Len() == 0 {
			return
		}
		parts = append(parts, current.String())
		current.Reset()
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]
		switch ch {
		case '"':
			inDoubleQuotes = !inDoubleQuotes
		case ' ', '\t', '\r', '\n':
			if inDoubleQuotes {
				current.WriteByte(ch)
			} else {
				flush()
			}
		default:
			current.WriteByte(ch)
		}
	}
	flush()

	if len(parts) == 0 {
		return []string{command}
	}
	return parts
}

func quotePowerShellSingleQuotedToken(token string) string {
	return "'" + strings.ReplaceAll(token, "'", "''") + "'"
}

func escapePowerShellCommand(command string) string {
	replacer := strings.NewReplacer(
		"`", "``",
		"\"", "`\"",
	)
	return replacer.Replace(command)
}

func escapeCmdCommand(command string) string {
	replacer := strings.NewReplacer(
		"^", "^^",
		"&", "^&",
		"|", "^|",
		"<", "^<",
		">", "^>",
	)
	return replacer.Replace(command)
}

func redactAutoCommandForLog(autoCommand string) string {
	if autoCommand == "" {
		return ""
	}
	return "[embedded-startup-command]"
}

// isDirectCommand 检查是否是直接命令（如 claude、opencode）而不是 shell 路径
func isDirectCommand(cmd string) bool {
	// 不包含路径分隔符且不是 .exe 结尾的视为直接命令
	lowerCmd := strings.ToLower(cmd)
	return !strings.Contains(cmd, "\\") && !strings.Contains(cmd, "/") && !strings.HasSuffix(lowerCmd, ".exe")
}

// resolveShellPath 验证 shell 路径是否存在，如果不存在则尝试回退到可用的 shell
func resolveShellPath(shellPath string, log *logging.Service) string {
	// 如果文件存在，直接返回
	if _, err := os.Stat(shellPath); err == nil {
		return shellPath
	}

	// PowerShell 7 回退逻辑
	if containsIgnoreCase(shellPath, "pwsh") || containsIgnoreCase(shellPath, "PowerShell\\7") {
		// 尝试其他常见的 PowerShell 7 安装路径
		altPaths := []string{
			"C:\\Program Files\\PowerShell\\7\\pwsh.exe",
			os.Getenv("ProgramFiles") + "\\PowerShell\\7\\pwsh.exe",
		}
		for _, p := range altPaths {
			if p != shellPath {
				if _, err := os.Stat(p); err == nil {
					if log != nil {
						log.Info("pty", "PowerShell 7 路径回退", fmt.Sprintf("找到替代路径=%s", p))
					}
					return p
				}
			}
		}
		// PowerShell 7 不存在，回退到 Windows PowerShell
		if log != nil {
			log.Warn("pty", "PowerShell 7 未安装，回退到 Windows PowerShell", "原路径="+shellPath)
		}
		return "powershell.exe"
	}

	// 如果是其他不存在的路径，返回原路径（后续启动会失败并报错）
	return shellPath
}

// trimHistoryToFrontier trims the history buffer to at most maxSize bytes,
// starting from a safe UTF-8 and ANSI escape boundary. This prevents
// replaying a partial multi-byte UTF-8 character or a truncated ANSI
// escape sequence, which would cause garbled output on history replay.
func trimHistoryToFrontier(history []byte, maxSize int) []byte {
	if len(history) <= maxSize {
		return history
	}
	start := len(history) - maxSize
	// Step 1: avoid splitting a multi-byte UTF-8 sequence.
	for start < len(history) && !isUTF8LeadingByte(history[start]) {
		start++
	}
	// Step 2: avoid starting inside an ANSI escape sequence.
	if idx := findTruncatedEscape(history, start); idx > start {
		start = idx
	}
	return history[start:]
}

func isUTF8LeadingByte(b byte) bool {
	return b&0xC0 != 0x80
}

func findTruncatedEscape(history []byte, start int) int {
	const scanLimit = 128
	lower := start - scanLimit
	if lower < 0 {
		lower = 0
	}
	for i := start - 1; i >= lower; i-- {
		if history[i] == 0x1B {
			if i+1 < len(history) && history[i+1] == 0x5B {
				// CSI sequence
				foundTerminator := false
				for j := i + 2; j < len(history); j++ {
					b := history[j]
					if b >= 0x40 && b <= 0x7E {
						if j < start {
							foundTerminator = true
						}
						break
					}
					if !((b >= 0x30 && b <= 0x3F) || (b >= 0x20 && b <= 0x2F)) {
						foundTerminator = true
						break
					}
				}
				if !foundTerminator {
					for j := start; j < len(history) && j < start+scanLimit; j++ {
						if history[j] >= 0x40 && history[j] <= 0x7E {
							return j + 1
						}
					}
					return start + 1
				}
			} else if i+1 < len(history) && history[i+1] == 0x5D {
				// OSC sequence
				foundTerminator := false
				for j := i + 2; j < len(history); j++ {
					b := history[j]
					if b == 0x07 {
						if j < start {
							foundTerminator = true
						}
						break
					}
					if b == 0x1B && j+1 < len(history) && history[j+1] == 0x5C {
						if j+1 < start {
							foundTerminator = true
						}
						break
					}
				}
				if !foundTerminator {
					for j := start; j < len(history) && j < start+scanLimit; j++ {
						if history[j] == 0x07 {
							return j + 1
						}
						if history[j] == 0x1B && j+1 < len(history) && history[j+1] == 0x5C {
							return j + 2
						}
					}
					return start + 1
				}
			} else if i+1 < len(history) && history[i+1] >= 0x40 && history[i+1] <= 0x7E {
				if i+1 >= start {
					return i + 2
				}
			}
			return start
		}
	}
	return start
}
