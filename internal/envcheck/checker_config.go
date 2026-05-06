package envcheck

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// predefinedConfigItems defines all Claude Code configuration items to check.
// Each item has a recommended default value that is written when the user
// clicks "一键配置" on a missing item. All items are non-required (Required=false)
// because Claude Code can run without them; they represent recommended defaults.
var predefinedConfigItems = []ClaudeConfigItem{
	{
		Key:          "hasCompletedOnboarding",
		FilePath:     "", // resolved to ~/.claude.json
		Category:     "onboarding",
		Required:     false,
		Description:  "标记首次引导已完成，跳过新手教程（配置于 ~/.claude.json）",
		DefaultValue: "true",
	},
	{
		Key:          "API_TIMEOUT_MS",
		FilePath:     "",
		Category:     "api",
		Required:     false,
		Description:  "API 请求超时时间（毫秒），默认 50 分钟适合长时间任务",
		DefaultValue: "3000000",
	},
	{
		Key:          "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
		FilePath:     "",
		Category:     "network",
		Required:     false,
		Description:  "禁用非必要网络流量（遥测、更新检查等）",
		DefaultValue: "1",
	},
	{
		Key:          "env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS",
		FilePath:     "",
		Category:     "experimental",
		Required:     false,
		Description:  "启用实验性 Agent Teams 协作功能",
		DefaultValue: "1",
	},
	{
		Key:          "env.CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS",
		FilePath:     "",
		Category:     "experimental",
		Required:     false,
		Description:  "禁用实验性 Beta 功能（设为 1 禁用 Beta，不设或设 0 启用）",
		DefaultValue: "1",
	},
	{
		Key:          "autoUpdatesChannel",
		FilePath:     "",
		Category:     "updates",
		Required:     false,
		Description:  "自动更新通道，latest 获取最新版本",
		DefaultValue: "latest",
	},
	{
		Key:          "effortLevel",
		FilePath:     "",
		Category:     "behavior",
		Required:     false,
		Description:  "推理深度级别，high 提供更深入的分析",
		DefaultValue: "high",
	},
	{
		Key:          "env.ENABLE_LSP_TOOL",
		FilePath:     "",
		Category:     "tools",
		Required:     false,
		Description:  "启用 LSP（语言服务器协议）工具，增强代码分析能力",
		DefaultValue: "1",
	},
	{
		Key:          "permissions.allow",
		FilePath:     "",
		Category:     "permissions",
		Required:     false,
		Description:  "权限白名单（允许的工具/命令列表）",
		DefaultValue: "[]",
	},
	{
		Key:          "permissions.deny",
		FilePath:     "",
		Category:     "permissions",
		Required:     false,
		Description:  "权限黑名单（拒绝的工具/命令列表）",
		DefaultValue: "[]",
	},
	{
		Key:          "permissions.ask",
		FilePath:     "",
		Category:     "permissions",
		Required:     false,
		Description:  "权限询问列表（使用前需用户确认的工具/命令）",
		DefaultValue: "[]",
	},
	{
		Key:          "permissions.defaultMode",
		FilePath:     "",
		Category:     "permissions",
		Required:     false,
		Description:  "默认权限模式，bypassPermissions 跳过权限检查",
		DefaultValue: "bypassPermissions",
	},
}

// configFilePaths returns the search order for Claude Code settings files.
// Earlier entries have higher priority.
func configFilePaths() []string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}

	var paths []string

	// 1. Project local (highest priority)
	cwd, _ := os.Getwd()
	if cwd != "" {
		paths = append(paths, filepath.Join(cwd, ".claude", "settings.local.json"))
		paths = append(paths, filepath.Join(cwd, ".claude", "settings.json"))
	}

	// 2. Global Claude state (separate from settings, for hasCompletedOnboarding etc.)
	if homeDir != "" {
		paths = append(paths, filepath.Join(homeDir, ".claude.json"))
	}

	// 3. Global settings (lowest priority among settings files)
	if homeDir != "" {
		paths = append(paths, filepath.Join(homeDir, ".claude", "settings.json"))
	}

	return paths
}

// checkClaudeConfig scans all Claude Code configuration files and checks
// for the presence of predefined required/recommended configuration items.
func (s *Service) checkClaudeConfig() (*ClaudeConfigStatus, error) {
	// 1. Build config file list
	configPaths := configFilePaths()

	// 2. Read each file and merge. configFilePaths() returns paths in
	//    highest-priority-first order. We iterate in reverse (lowest first)
	//    so that higher-priority entries overwrite lower-priority ones.
	merged := make(map[string]string)
	var warnings []string
	for i := len(configPaths) - 1; i >= 0; i-- {
		path := configPaths[i]
		data, err := os.ReadFile(path)
		if err != nil {
			// File not existing is normal, skip
			continue
		}

		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			// JSON parse failure recorded as warning
			warnings = append(warnings, fmt.Sprintf("配置文件 %s JSON 格式错误: %v", path, err))
			continue
		}

		// Flatten nested JSON (e.g. env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS)
		flattenJSON(raw, "", merged)
	}

	// 3. Check each predefined item
	items := make([]ClaudeConfigItem, len(predefinedConfigItems))
	copy(items, predefinedConfigItems) // shallow copy

	missingRequired := 0
	allConfigured := true

	for i := range items {
		// Determine which file this item was read from (highest priority first)
		filePath := resolveConfigFilePath(configPaths, items[i].Key)
		items[i].FilePath = filePath

		// Check if key exists with a non-empty value
		if val, ok := merged[items[i].Key]; ok && strings.TrimSpace(val) != "" {
			items[i].Configured = true
			// Mask sensitive values: API Token only shows first/last chars
			items[i].CurrentValue = maskSensitiveValue(items[i].Key, val)
		} else {
			items[i].Configured = false
			items[i].CurrentValue = ""
		}

		// Count missing required items
		if items[i].Required && !items[i].Configured {
			missingRequired++
			allConfigured = false
		}
	}

	return &ClaudeConfigStatus{
		ConfigItems:     items,
		MissingRequired: missingRequired,
		AllConfigured:   allConfigured,
		Warnings:        warnings,
	}, nil
}

// flattenJSON recursively flattens a nested JSON object into a flat map.
// Nested keys are joined with "." (e.g., env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS).
func flattenJSON(data map[string]interface{}, prefix string, result map[string]string) {
	for key, val := range data {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		switch v := val.(type) {
		case map[string]interface{}:
			flattenJSON(v, fullKey, result)
		case string:
			result[fullKey] = v
		case float64:
			result[fullKey] = fmt.Sprintf("%v", v)
		case bool:
			result[fullKey] = fmt.Sprintf("%v", v)
		case []interface{}:
			// Arrays are serialized as JSON strings
			if jsonBytes, err := json.Marshal(v); err == nil {
				result[fullKey] = string(jsonBytes)
			}
		case nil:
			// nil values are treated as not configured
		default:
			result[fullKey] = fmt.Sprintf("%v", v)
		}
	}
}

// resolveConfigFilePath returns the highest-priority file path that contains
// the given configuration key.
func resolveConfigFilePath(configPaths []string, key string) string {
	// Scan from highest to lowest priority
	for _, path := range configPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}

		merged := make(map[string]string)
		flattenJSON(raw, "", merged)

		if _, ok := merged[key]; ok {
			return path
		}
	}

	// If no file contains this key, return the highest priority path
	// (for subsequent write operations)
	if len(configPaths) > 0 {
		return configPaths[0]
	}
	return ""
}

// maskSensitiveValue masks API keys/tokens to show only first 4 and last 4 characters.
func maskSensitiveValue(key string, value string) string {
	sensitiveKeys := map[string]bool{
		"env.ANTHROPIC_AUTH_TOKEN": true,
		"env.ANTHROPIC_API_KEY":    true,
	}
	if !sensitiveKeys[key] {
		return value
	}
	if len(value) <= 8 {
		return "****"
	}
	return value[:4] + "****" + value[len(value)-4:]
}
