package session

import "time"

// AppType 应用类型
type AppType string

const (
	AppTypeClaudeCode AppType = "claudecode" // Claude Code 应用
	AppTypeOpenCode   AppType = "opencode"   // Open Code 应用
	AppTypeCodex      AppType = "codex"      // Codex CLI 应用
	// AppTypeAmagiCode is deprecated and retained only for reading legacy sessions.
	// New AmagiCode session creation and launch APIs have been removed.
	AppTypeAmagiCode AppType = "amagicode"
)

// LaunchMode 启动模式
type LaunchMode string

const (
	ModeTerminal LaunchMode = "terminal" // 独立终端窗口
	ModeEmbedded LaunchMode = "embedded" // 内嵌终端（ConPTY + xterm.js）
)

// SessionStatus 会话状态
type SessionStatus string

const (
	StatusRunning SessionStatus = "running"
	StatusStopped SessionStatus = "stopped"
	StatusExited  SessionStatus = "exited"
	StatusFailed  SessionStatus = "failed"
)

// Session 表示一个终端会话实例
type Session struct {
	ID           string        `json:"id"`
	AppType      AppType       `json:"appType"`
	Provider     string        `json:"provider"`
	Preset       string        `json:"preset"`
	Model        string        `json:"model"`
	Mode         LaunchMode    `json:"mode"`
	WorkDir      string        `json:"workDir"`
	Status       SessionStatus `json:"status"`
	PID          int           `json:"pid"`
	StartedAt    time.Time     `json:"startedAt"`
	StoppedAt    *time.Time    `json:"stoppedAt,omitempty"`
	UseProxy     bool          `json:"useProxy"`
	ErrorMessage string        `json:"errorMessage,omitempty"`
	FirstOutput  string        `json:"firstOutput,omitempty"`
}

// SessionInfo 返回给前端的会话摘要
type SessionInfo struct {
	ID        string        `json:"id"`
	AppType   AppType       `json:"appType"`
	Provider  string        `json:"provider"`
	Preset    string        `json:"preset"`
	Model     string        `json:"model"`
	Mode      LaunchMode    `json:"mode"`
	WorkDir   string        `json:"workDir"`
	Status    SessionStatus `json:"status"`
	PID       int           `json:"pid"`
	StartedAt   string        `json:"startedAt"`
	Duration    string        `json:"duration"`
	UseProxy    bool          `json:"useProxy"`
	FirstOutput string        `json:"firstOutput"`
}

// LaunchRequest 启动请求参数
type LaunchRequest struct {
	ProviderName string     `json:"providerName"`
	PresetName   string     `json:"presetName"`
	Mode         LaunchMode `json:"mode"`
	WorkDir      string     `json:"workDir"`
	UseProxy     bool       `json:"useProxy"`
}
