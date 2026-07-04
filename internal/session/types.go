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
	// Title 是会话标题（首条 user message 摘要），由方案 P 的轮询写入。
	// 方案 P 下可被多次覆盖：用户 /resume 切到历史会话并继续输入，
	// 标题会跟随切换后的会话；会话停止后冻结于最后跟踪值。
	Title string `json:"title,omitempty"`
	// ClaudeSessionID 是 tracker 动态跟踪到的 Claude session uuid
	// （方案 P：不通过启动命令注入 --session-id，靠 mtime 最新 jsonl 跟踪；
	// 会话停止时冻结于最后跟踪值）。
	// 用于拼 ~/.claude/projects/<encoded-cwd>/<sessionID>.jsonl 路径。
	ClaudeSessionID string `json:"claudeSessionId,omitempty"`
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
	// Title 为前端展示用会话标题（首条 user message 摘要）。
	Title string `json:"title"`
	// ClaudeSessionID 是 tracker 动态跟踪到的 Claude session uuid
	// （方案 P：不通过启动命令注入 --session-id，靠 mtime 最新 jsonl 跟踪；
	// 会话停止时冻结于最后跟踪值）。
	// 用于关联 jsonl 文件。
	ClaudeSessionID string `json:"claudeSessionId"`
}

// LaunchRequest 启动请求参数
type LaunchRequest struct {
	ProviderName string     `json:"providerName"`
	PresetName   string     `json:"presetName"`
	Mode         LaunchMode `json:"mode"`
	WorkDir      string     `json:"workDir"`
	UseProxy     bool       `json:"useProxy"`
}
