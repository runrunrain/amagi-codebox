package session

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Manager 多终端会话管理器
type Manager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
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

// SetFirstOutput 设置会话首输出摘要（由 PTY 服务回调）
// 仅在首次设置时生效；空文本或会话不存在时无操作；已存在非空值时不覆盖。
func (m *Manager) SetFirstOutput(id string, text string) {
	if text == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return
	}
	if s.FirstOutput != "" {
		return
	}
	s.FirstOutput = text
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
func (m *Manager) List() []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
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

			FirstOutput: s.FirstOutput,
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
