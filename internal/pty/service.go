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

	"github.com/UserExistsError/conpty"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const maxOutputHistorySize = 1024 * 1024 // 1MB 环形缓冲区上限，避免移动端回放只剩会话尾部

// PtySession 一个 ConPTY 会话
type PtySession struct {
	cpty          *conpty.ConPty
	cancel        context.CancelFunc
	done          chan struct{}
	outputHistory []byte     // 最近输出的环形缓冲区，供后加入的 WebSocket 客户端重放
	historyMu     sync.Mutex // 保护 outputHistory
	emitSeq       uint64     // monotonic counter incremented per PTY output chunk under historyMu
	currentCols   int        // 当前 PTY 列数
	currentRows   int        // 当前 PTY 行数

	// 首输出提取（readLoop goroutine 独占访问，无需额外锁）
	firstOutputBuf       []byte    // 累积的原始输出，达到阈值后提取首输出
	firstOutputDone      bool      // 是否已触发提取（仅触发一次）
	firstOutputStartedAt time.Time // 会话启动时刻，用于超时阈值判定
}

// outputCallback PTY 输出回调，供远程服务器的 WebSocket 使用
type outputCallback func(data []byte)

// exitCallback PTY 进程退出回调
type exitCallback func(exitCode uint32)

// resizeCallback PTY 尺寸变化回调，供远程 observer 同步 dimensions 帧
type resizeCallback func(cols, rows int)

// Service 管理所有嵌入式终端的 PTY 会话。
// 通过 Wails 事件双向传输数据：
//   - 后端→前端: EventsEmit("pty:data:<sessionID>", {s: emitSeq, d: base64Data})
//   - 前端→后端: PtyWrite(sessionID, base64Data)
//
// 同时支持注册远程回调，供 WebSocket 转发使用。
type Service struct {
	sessions    map[string]*PtySession
	mu          sync.Mutex
	ctx         context.Context // Wails app context (for EventsEmit)
	log         *logging.Service
	outputCBsMu sync.RWMutex
	outputCBs   map[string]map[string]outputCallback // sessionID → {connID → cb}
	exitCBsMu   sync.RWMutex
	exitCBs     map[string]map[string]exitCallback // sessionID → {connID → cb}
	resizeCBsMu sync.RWMutex
	resizeCBs   map[string]map[string]resizeCallback // sessionID → {connID → cb}

	// firstOutputSink 在 readLoop goroutine 中读取、主 goroutine 中通过 SetFirstOutputSink 写入，
	// 用 sinkMu 保护读写，确保并发安全。
	firstOutputSink func(sessionID, text string)
	sinkMu          sync.RWMutex
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

// SetFirstOutputSink 注入首输出回调，由 App 层在 NewApp 中调用以解耦 pty 包对 session 包的依赖。
// 回调签名为 (sessionID, text string)；nil 回调合法，readLoop 会安全跳过提取。
func (s *Service) SetFirstOutputSink(fn func(sessionID, text string)) {
	s.sinkMu.Lock()
	defer s.sinkMu.Unlock()
	s.firstOutputSink = fn
}

// deliverFirstOutput 在 readLoop 内累积 chunk，达到阈值后一次性提取首输出并投递给 sink。
// 调用方必须保证：本方法由 readLoop goroutine 独占调用（firstOutputBuf / firstOutputDone 无需额外锁），
// 且必须在 historyMu.Unlock() 之后、EventsEmit 之前调用，避免阻塞读取循环。
func (s *Service) deliverFirstOutput(sessionID string, ps *PtySession, chunk []byte) {
	if ps == nil || ps.firstOutputDone {
		return
	}
	ps.firstOutputBuf = append(ps.firstOutputBuf, chunk...)
	if len(ps.firstOutputBuf) < firstOutputByteThreshold &&
		time.Since(ps.firstOutputStartedAt) < firstOutputTimeout {
		return
	}
	text := ExtractFirstMeaningfulLine(ps.firstOutputBuf)
	ps.firstOutputDone = true
	if text == "" {
		return
	}
	s.sinkMu.RLock()
	sink := s.firstOutputSink
	s.sinkMu.RUnlock()
	if sink != nil {
		sink(sessionID, text)
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

// SetContext 设置 Wails 应用上下文（Startup 时调用）
func (s *Service) SetContext(ctx context.Context) {
	s.ctx = ctx
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
		return 0, fmt.Errorf("conpty start: %w", err)
	}

	pid := cpty.Pid()
	s.log.Info("pty", "ConPTY 已启动", fmt.Sprintf("id=%s pid=%d", sessionID, pid))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	ps := &PtySession{
		cpty:                cpty,
		cancel:              cancel,
		done:                done,
		currentCols:         cols,
		currentRows:         rows,
		firstOutputStartedAt: time.Now(),
	}
	s.sessions[sessionID] = ps

	// 启动读取协程：从 ConPTY 读取输出，通过 Wails 事件发送到前端
	go s.readLoop(sessionID, ps, ctx, done)

	// 启动等待协程：监控进程退出
	go s.waitLoop(sessionID, ps, ctx)

	// 如果指定了自动命令，延迟发送到 shell
	if sendAutoCommand != "" {
		go func() {
			time.Sleep(1000 * time.Millisecond) // 等待 shell 初始化完成
			cmd := sendAutoCommand + "\r\n"
			_, _ = ps.cpty.Write([]byte(cmd))
			s.log.Info("pty", "自动发送命令", fmt.Sprintf("id=%s cmd=%s", sessionID, redactAutoCommandForLog(sendAutoCommand)))
		}()
	}

	return pid, nil
}

func buildResolvedStartupPlan(spec platform.ResolvedLaunchSpec, log *logging.Service) (string, string) {
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

			// 首输出提取（在 historyMu 锁外执行，避免阻塞读取循环）。
			s.deliverFirstOutput(sessionID, ps, chunk)

			// Wails 前端事件
			if s.ctx != nil {
				data := base64.StdEncoding.EncodeToString(chunk)
				wailsRuntime.EventsEmit(s.ctx, "pty:data:"+sessionID, map[string]any{"s": seq, "d": data})
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
	exitCode, err := ps.cpty.Wait(ctx)
	s.log.Info("pty", "进程退出", fmt.Sprintf("id=%s exitCode=%d err=%v", sessionID, exitCode, err))

	// Wails 前端事件
	if s.ctx != nil {
		wailsRuntime.EventsEmit(s.ctx, "pty:exit:"+sessionID, map[string]interface{}{
			"exitCode": exitCode,
			"error":    fmt.Sprintf("%v", err),
		})
	}

	// 远程 WebSocket 退出回调
	for _, cb := range s.snapshotExitCallbacks(sessionID) {
		cb(exitCode)
	}
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

// Resize 调整 PTY 尺寸
func (s *Service) Resize(sessionID string, cols, rows int) error {
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
	<-ps.done // 等待读取协程退出
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
