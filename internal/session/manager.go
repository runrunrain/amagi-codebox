package session

import (
	"fmt"
	"sync"
	"time"

	"amagi-codebox/internal/appmeta/claude"

	"github.com/google/uuid"
)

// Manager 多终端会话管理器
type Manager struct {
	sessions map[string]*Session
	mu       sync.RWMutex

	// homeDir 用于 List 时回填已退出会话的标题（从 jsonl 直读）。
	// 由 SetHomeDir 注入；为零值时 List 跳过 jsonl 直读（保持纯内存读语义）。
	homeDir string
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}

// SetHomeDir 注入用户主目录，供 List 回填已退出会话的标题（方案 P 直读历史 jsonl）。
// 由 app.go startup 阶段注入一次；多次调用以最后一次为准。
func (m *Manager) SetHomeDir(homeDir string) {
	if homeDir == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.homeDir = homeDir
}

// Create 创建一个新的会话记录（尚未启动进程）
func (m *Manager) Create(appType AppType, provider, preset, model string, mode LaunchMode, workDir string, useProxy bool) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := &Session{
		ID:        uuid.New().String()[:8],
		AppType:   appType,
		Provider:  provider,
		Preset:    preset,
		Model:     model,
		Mode:      mode,
		WorkDir:   workDir,
		Status:    StatusRunning,
		StartedAt: time.Now(),
		UseProxy:  useProxy,
	}

	m.sessions[s.ID] = s
	return s
}

// SetPID 设置进程 PID
func (m *Manager) SetPID(id string, pid int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		s.PID = pid
	}
}

// SetTitle 设置会话标题（首条 user message 摘要）。
//
// 方案 P 行为说明：标题可被多次覆盖（用户 /resume 切到历史会话后继续输入，
// 标题应跟随切换后的会话）。空文本或会话不存在时无操作。
func (m *Manager) SetTitle(id string, text string) {
	if text == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return
	}
	s.Title = text
}

// SetClaudeSessionID 设置会话当前跟踪到的 Claude session uuid（方案 P 动态跟踪，可覆盖）。
// 会话停止时最后写入的值即冻结，供 List 直读历史 jsonl。
// 空字符串或会话不存在时无操作。
func (m *Manager) SetClaudeSessionID(id string, sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		s.ClaudeSessionID = sessionID
	}
}

// GetClaudeSessionID 返回会话当前锁定的 Claude session uuid。
//
// 方案 R 下：
//   - embedded 启动时由 app.go 注入的 --session-id 值（锁定值）；
//   - tracker 跟随 /resume 切换后写入的最新值。
//
// 空串表示未锁定（external 模式 / 注入失败 / 会话不存在），tracker 应降级方案 P
// （FindLatestActiveJSONL 取最新 mtime）。
func (m *Manager) GetClaudeSessionID(id string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.sessions[id]; ok {
		return s.ClaudeSessionID
	}
	return ""
}

// GetStatus 返回会话当前状态；会话不存在时返回空串。
// 供轮询 goroutine 在不持锁的情况下感知会话是否已停止（兜底退出信号）。
func (m *Manager) GetStatus(id string) SessionStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.sessions[id]; ok {
		return s.Status
	}
	return ""
}

// MarkStopped 标记会话为已停止
func (m *Manager) MarkStopped(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		now := time.Now()
		s.Status = StatusStopped
		s.StoppedAt = &now
	}
}

// MarkExited 标记会话为已退出（进程自行结束）
func (m *Manager) MarkExited(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		if s.Status == StatusRunning {
			now := time.Now()
			s.Status = StatusExited
			s.StoppedAt = &now
		}
	}
}

// MarkFailed 标记会话为失败
func (m *Manager) MarkFailed(id string, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		now := time.Now()
		s.Status = StatusFailed
		s.StoppedAt = &now
		s.ErrorMessage = errMsg
	}
}

// Get 获取单个会话
func (m *Manager) Get(id string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	copy := *s
	return &copy, nil
}

// List 返回所有会话的摘要信息
//
// 方案 P 直读：若 homeDir 已注入，对已退出（Status != Running）且 Title 空但
// ClaudeSessionID 非空的会话，从 ~/.claude/projects/<encoded-workDir>/<sid>.jsonl
// 直读首条 user message 填充 Title。读后写回 Session.Title 缓存，避免重复 IO。
// jsonl 不存在或读取失败时静默，Title 保持空（不报错）。
func (m *Manager) List() []SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	homeDir := m.homeDir

	result := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		// 方案 P 直读：仅当 homeDir 已注入、会话已退出、Title 空且 ClaudeSessionID 非空时尝试。
		if homeDir != "" && s.Title == "" && s.ClaudeSessionID != "" &&
			s.Status != StatusRunning && s.AppType == AppTypeClaudeCode {
			if title, ok := readTitleFromJSONL(homeDir, s.WorkDir, s.ClaudeSessionID); ok {
				s.Title = title // 写回缓存，后续 List 不再读盘
			}
		}

		info := SessionInfo{
			ID:        s.ID,
			AppType:   s.AppType,
			Provider:  s.Provider,
			Preset:    s.Preset,
			Model:     s.Model,
			Mode:      s.Mode,
			WorkDir:   s.WorkDir,
			Status:    s.Status,
			PID:       s.PID,
			StartedAt: s.StartedAt.Format(time.RFC3339),
			UseProxy:  s.UseProxy,

			Title:           s.Title,
			ClaudeSessionID: s.ClaudeSessionID,
		}

		if s.Status == StatusRunning {
			info.Duration = formatDuration(time.Since(s.StartedAt))
		} else if s.StoppedAt != nil {
			info.Duration = formatDuration(s.StoppedAt.Sub(s.StartedAt))
		}

		result = append(result, info)
	}

	// 按启动时间倒序排列
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].StartedAt < result[j].StartedAt {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result
}

// readTitleFromJSONL 从 homeDir/.claude/projects/<encoded-workDir>/<sid>.jsonl 直读首条 user message。
// 返回 (title, true) 表示成功；jsonl 不存在 / 无 user message / 读取失败 → ("", false)。
// 调用方负责持锁（本函数不接触 Manager.mu，仅做 IO）。
func readTitleFromJSONL(homeDir, workDir, claudeSessionID string) (string, bool) {
	jsonlPath := claude.SessionJSONLPath(homeDir, workDir, claudeSessionID)
	content, found, err := claude.ExtractFirstUserMessage(jsonlPath)
	if err != nil || !found {
		return "", false
	}
	return truncateFirstLine(content, titleMaxRunes, workDir), true
}

// RunningCount 返回运行中的会话数量
func (m *Manager) RunningCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, s := range m.sessions {
		if s.Status == StatusRunning {
			count++
		}
	}
	return count
}

// Remove 删除已结束的会话记录
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	if s.Status == StatusRunning {
		return fmt.Errorf("cannot remove running session: %s", id)
	}
	delete(m.sessions, id)
	return nil
}

// ClearStopped 清除所有非运行中的会话
func (m *Manager) ClearStopped() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for id, s := range m.sessions {
		if s.Status != StatusRunning {
			delete(m.sessions, id)
			count++
		}
	}
	return count
}

// GetRunning 返回所有运行中的会话 ID 列表
func (m *Manager) GetRunning() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var ids []string
	for id, s := range m.sessions {
		if s.Status == StatusRunning {
			ids = append(ids, id)
		}
	}
	return ids
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
