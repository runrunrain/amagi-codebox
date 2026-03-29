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
	currentCols   int        // 当前 PTY 列数
	currentRows   int        // 当前 PTY 行数
}

// outputCallback PTY 输出回调，供远程服务器的 WebSocket 使用
type outputCallback func(data []byte)

// exitCallback PTY 进程退出回调
type exitCallback func(exitCode uint32)

// resizeCallback PTY 尺寸变化回调，供远程 observer 同步 dimensions 帧
type resizeCallback func(cols, rows int)

// Service 管理所有嵌入式终端的 PTY 会话。
// 通过 Wails 事件双向传输数据：
//   - 后端→前端: EventsEmit("pty:data:<sessionID>", base64Data)
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
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.sessions[sessionID]; exists {
		return 0, fmt.Errorf("session %s already exists", sessionID)
	}

	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 40
	}

	// 确定实际启动的命令行
	commandLine := shellPath
	if commandLine == "" {
		// shellPath 为空时，使用 autoCommand 作为直接启动命令（支持 "claude" 或 "opencode"）
		if autoCommand == "" {
			autoCommand = "claude" // 默认使用 claude
		}
		commandLine = autoCommand
		autoCommand = "" // 直接启动，不需要自动发送命令
	}

	// 验证 shell 路径是否存在，如果不存在则尝试回退
	if commandLine != "" && !isDirectCommand(commandLine) {
		resolvedPath := resolveShellPath(commandLine, s.log)
		if resolvedPath != commandLine {
			s.log.Info("pty", "Shell 路径回退", fmt.Sprintf("原路径=%s 回退到=%s", commandLine, resolvedPath))
			commandLine = resolvedPath
		}
	}

	// 如果是 pwsh/powershell，添加 -NoProfile -NoLogo 防止 Profile 脚本干扰环境变量
	if autoCommand != "" && (containsIgnoreCase(commandLine, "pwsh") || containsIgnoreCase(commandLine, "powershell")) {
		commandLine = fmt.Sprintf(`"%s" -NoProfile -NoLogo`, commandLine)
	} else if autoCommand != "" && containsIgnoreCase(commandLine, "cmd") {
		// cmd.exe：使用 /K 保持打开，设置 UTF-8 代码页
		commandLine = fmt.Sprintf(`"%s" /K chcp 65001 >nul`, commandLine)
	} else if autoCommand != "" && commandLine != "claude" && commandLine != "opencode" {
		// 其他 shell，直接启动
		commandLine = fmt.Sprintf(`"%s"`, commandLine)
	}

	s.log.Info("pty", "创建 ConPTY 会话", fmt.Sprintf("id=%s cmd=%s autoCmd=%s workDir=%s size=%dx%d", sessionID, commandLine, autoCommand, workDir, cols, rows))

	opts := []conpty.ConPtyOption{
		conpty.ConPtyDimensions(cols, rows),
	}
	if workDir != "" {
		opts = append(opts, conpty.ConPtyWorkDir(workDir))
	}
	if env != nil {
		opts = append(opts, conpty.ConPtyEnv(env))
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
		cpty:        cpty,
		cancel:      cancel,
		done:        done,
		currentCols: cols,
		currentRows: rows,
	}
	s.sessions[sessionID] = ps

	// 启动读取协程：从 ConPTY 读取输出，通过 Wails 事件发送到前端
	go s.readLoop(sessionID, ps, ctx, done)

	// 启动等待协程：监控进程退出
	go s.waitLoop(sessionID, ps, ctx)

	// 如果指定了自动命令，延迟发送到 shell
	if autoCommand != "" {
		go func() {
			time.Sleep(1000 * time.Millisecond) // 等待 shell 初始化完成
			cmd := autoCommand + "\r\n"
			_, _ = ps.cpty.Write([]byte(cmd))
			s.log.Info("pty", "自动发送命令", fmt.Sprintf("id=%s cmd=%s", sessionID, autoCommand))
		}()
	}

	return pid, nil
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
			ps.outputHistory = append(ps.outputHistory, chunk...)
			if len(ps.outputHistory) > maxOutputHistorySize {
				ps.outputHistory = ps.outputHistory[len(ps.outputHistory)-maxOutputHistorySize:]
			}
			ps.historyMu.Unlock()

			// Wails 前端事件
			if s.ctx != nil {
				data := base64.StdEncoding.EncodeToString(chunk)
				wailsRuntime.EventsEmit(s.ctx, "pty:data:"+sessionID, data)
			}

			// 远程 WebSocket 回调
			s.outputCBsMu.RLock()
			cbs := s.outputCBs[sessionID]
			s.outputCBsMu.RUnlock()
			for _, cb := range cbs {
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
	s.exitCBsMu.RLock()
	cbs := s.exitCBs[sessionID]
	s.exitCBsMu.RUnlock()
	for _, cb := range cbs {
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

	s.resizeCBsMu.RLock()
	callbacks := make([]resizeCallback, 0, len(s.resizeCBs[sessionID]))
	for _, cb := range s.resizeCBs[sessionID] {
		callbacks = append(callbacks, cb)
	}
	s.resizeCBsMu.RUnlock()
	for _, cb := range callbacks {
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

// RunningCount 返回运行中的 PTY 数量
func (s *Service) RunningCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
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
					log.Info("pty", "PowerShell 7 路径回退", fmt.Sprintf("找到替代路径=%s", p))
					return p
				}
			}
		}
		// PowerShell 7 不存在，回退到 Windows PowerShell
		log.Warn("pty", "PowerShell 7 未安装，回退到 Windows PowerShell", "原路径="+shellPath)
		return "powershell.exe"
	}

	// 如果是其他不存在的路径，返回原路径（后续启动会失败并报错）
	return shellPath
}
