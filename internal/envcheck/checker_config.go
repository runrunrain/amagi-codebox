package envcheck

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// predefinedConfigItems defines all Claude Code configuration items to check.
var predefinedConfigItems = []ClaudeConfigItem{
	{
		Key:          "env.ANTHROPIC_AUTH_TOKEN",
		FilePath:     "", // dynamically filled during check
		Category:     "api",
		Required:     true,
		Description:  "API 认证令牌（与 BASE_URL 至少配置一个）",
		DefaultValue: "",
	},
	{
		Key:          "env.ANTHROPIC_BASE_URL",
		FilePath:     "",
		Category:     "api",
		Required:     true,
		Description:  "API 端点地址",
		DefaultValue: "https://api.anthropic.com",
	},
	{
		Key:          "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
		FilePath:     "",
		Category:     "network",
		Required:     false,
		Description:  "禁用非必要网络流量（遥测、更新检查等），设为 1 启用",
		DefaultValue: "1",
	},
	{
		Key:          "DISABLE_AUTOUPDATER",
		FilePath:     "",
		Category:     "updates",
		Required:     false,
		Description:  "禁用自动更新，避免频繁升级打断工作",
		DefaultValue: "1",
	},
	{
		Key:          "CLAUDE_CODE_GIT_BASH_PATH",
		FilePath:     "",
		Category:     "windows",
		Required:     false,
		Description:  "Git Bash 可执行文件路径（Windows 平台推荐配置）",
		DefaultValue: `C:\Program Files\Git\bin\bash.exe`,
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

	// 2. Global (lowest priority)
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

		// Flatten nested JSON (e.g. env.ANTHROPIC_BASE_URL)
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
// Nested keys are joined with "." (e.g., env.ANTHROPIC_BASE_URL).
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
