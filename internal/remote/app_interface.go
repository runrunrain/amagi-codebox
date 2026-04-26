package remote

import (
	"amagi-codebox/internal/amagi"
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
	LaunchSession(providerName, presetName string, mode string, workDir string, useProxy bool, shellPath string) (string, error)
	LaunchCodexSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error)
	LaunchOpenCode(providerName string, presetName string, mode string, workDir string, shellPath string) (string, error)
	LaunchAmagiCode(groupName string, providerName string, mode string, workDir string, shellPath string) (string, error)
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
	GetAmagiSettings() (*amagi.AmagiSettings, error)

	// Remote port management
	SetRemotePort(port int) error
}
