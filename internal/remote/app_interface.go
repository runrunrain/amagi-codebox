package remote

import (
	"amagi-codebox/internal/config"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/settings"
)

// AppInterface 定义远程服务器需要调用的 App 方法集合，
// 避免 import cycle（remote → main）。
type AppInterface interface {
	// App info
	GetAppInfo() map[string]any

	// Session management
	GetSessions() []session.SessionInfo
	GetSession(sessionID string) (session.SessionInfo, error)
	LaunchSession(providerName, presetName string, mode string, workDir string, useHeadroom bool, shellPath string) (string, error)
	LaunchCodexSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error)
	LaunchOpenCode(providerName string, presetName string, mode string, workDir string, shellPath string) (string, error)
	LaunchPiSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error)
	LaunchOmpSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error)
	StopSession(sessionID string) error
	RemoveSession(sessionID string) error
	ClearStoppedSessions() int
	PtyResize(sessionID string, cols, rows int) error

	// Provider management
	GetProvidersByType(providerType string) map[string]config.Provider
	GetProviderExportJSON(providerName string) (string, error)
	SaveProviderFromJSON(providerName string, jsonStr string) error

	// Config
	SaveAllConfig() error

	// Secrets / diagnostics
	GetKeyDiagnostics() map[string]map[string]string

	// Logs
	GetLogs(level string, source string, keyword string, limit int) []logging.Entry

	// Settings (via embedded service references)
	GetSettingsService() *settings.Service
	GetPathsService() *paths.PathsService
	GetConfigService() *config.ConfigService

	// Remote port management
	SetRemotePort(port int) error

	// Web UI plane（remote-webui-plane 切片）：会话 pi webui 平面快照。
	// State 镜像 internal/webui.State（"probing"|"available"|"unavailable"|
	// "ended"|"unknown"）；Port/Token 仅在 State=="available" 时非零/非空。
	// Token 是 capability 凭据：调用方不得写日志或放入 URL query/path。
	GetSessionWebUI(sessionID string) (SessionWebUIInfo, bool)

	// ProbeSessionWebUI 先执行一轮 webui 探测（与桌面端轮询同一状态机推进
	// 路径）再返回快照；remote v1 状态端点按此节奏轮询。
	ProbeSessionWebUI(sessionID string) (SessionWebUIInfo, bool)
}

// SessionWebUIInfo 是 remote 侧可见的会话 webui 平面快照。第二个返回值
// （见上）报告宿主 webui 服务是否接线（未接线 → State 恒为 "unknown"）。
// 远程侧对未知 State 一律按 "unknown" 降级，不回显原始字符串。
type SessionWebUIInfo struct {
	State string
	Port  int
	Token string
}
