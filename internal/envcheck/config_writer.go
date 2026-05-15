package envcheck

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// IsConfigPathAllowed is the exported version of isConfigPathAllowed for use
// by app.go (defense-in-depth validation at the binding layer).
func IsConfigPathAllowed(targetPath string) bool {
	return isConfigPathAllowed(targetPath)
}

// ExpandTilde is the exported version of expandTilde for use by app.go.
func ExpandTilde(path string) string {
	return expandTilde(path)
}

// isConfigPathAllowed validates that the target file path is within the
// allowed set of Claude Code configuration files. This prevents arbitrary
// file writes via the frontend binding.
func isConfigPathAllowed(targetPath string) bool {
	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return false
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// File may not exist yet; use the absolute path as-is.
		realPath = filepath.Clean(absPath)
	} else {
		realPath = filepath.Clean(realPath)
	}
	if isProtectedConfigPath(realPath) {
		return false
	}

	// Allowed patterns:
	//   ~/.claude.json                     (global state, hasCompletedOnboarding)
	//   ~/.claude/settings.json            (global)
	//   <trusted-project-root>/.claude/settings.json        (project)
	//   <trusted-project-root>/.claude/settings.local.json  (project local)
	homeDir, _ := os.UserHomeDir()
	projectRoot := trustedClaudeProjectConfigRoot()

	allowedPatterns := []string{}
	if homeDir != "" {
		allowedPatterns = append(allowedPatterns,
			filepath.Clean(filepath.Join(homeDir, ".claude.json")),
			filepath.Clean(filepath.Join(homeDir, ".claude", "settings.json")),
		)
	}
	if projectRoot != "" {
		allowedPatterns = append(allowedPatterns,
			filepath.Clean(filepath.Join(projectRoot, ".claude", "settings.json")),
			filepath.Clean(filepath.Join(projectRoot, ".claude", "settings.local.json")),
		)
	}

	for _, pattern := range allowedPatterns {
		if realPath == pattern {
			return true
		}
	}
	return false
}

func configPathRejectionMessage(path string) string {
	return fmt.Sprintf("目标路径 %s 不在允许的配置文件列表中或位于系统/受保护目录，拒绝写入。请选择用户目录下的 ~/.claude/settings.json、~/.claude.json，或可信项目目录下的 .claude/settings.json", path)
}

func trustedClaudeProjectConfigRoot() string {
	cwd, err := os.Getwd()
	if err != nil || strings.TrimSpace(cwd) == "" {
		return ""
	}
	root, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	root = filepath.Clean(root)
	if isProtectedConfigPath(root) {
		return ""
	}
	if isLikelyProjectConfigRoot(root) || pathIsUnderUserHome(root) || pathIsUnderTemp(root) {
		return root
	}
	return ""
}

func isLikelyProjectConfigRoot(root string) bool {
	for _, marker := range []string{".git", ".claude", "CLAUDE.md", "go.mod", "package.json"} {
		if _, err := os.Stat(filepath.Join(root, marker)); err == nil {
			return true
		}
	}
	return false
}

func pathIsUnderUserHome(path string) bool {
	homeDir, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return false
	}
	return cleanPathHasPrefix(path, homeDir)
}

func pathIsUnderTemp(path string) bool {
	tempDir := os.TempDir()
	if strings.TrimSpace(tempDir) == "" {
		return false
	}
	return cleanPathHasPrefix(path, tempDir)
}

func cleanPathHasPrefix(path string, root string) bool {
	cleanPath := filepath.Clean(path)
	cleanRoot := filepath.Clean(root)
	if strings.EqualFold(cleanPath, cleanRoot) {
		return true
	}
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

func isProtectedConfigPath(path string) bool {
	normalized := normalizeConfigSecurityPath(path)
	if normalized == "" {
		return false
	}
	protectedRoots := []string{
		normalizeConfigSecurityPath(os.Getenv("SystemRoot")),
		normalizeConfigSecurityPath(`C:\Windows`),
		normalizeConfigSecurityPath(`C:\Program Files`),
		normalizeConfigSecurityPath(`C:\Program Files (x86)`),
	}
	for _, root := range protectedRoots {
		if root != "" && normalizedPathHasPrefix(normalized, root) {
			return true
		}
	}
	return strings.Contains(normalized, "/windows/system32") || strings.Contains(normalized, "/windows/syswow64")
}

func normalizeConfigSecurityPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	cleaned := filepath.Clean(trimmed)
	cleaned = strings.ReplaceAll(cleaned, `\`, "/")
	return strings.ToLower(strings.TrimRight(cleaned, "/"))
}

func normalizedPathHasPrefix(path string, root string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, strings.TrimRight(root, "/")+"/")
}

// fixClaudeConfig writes a single configuration item to a Claude Code settings
// file. Only missing keys are added; existing keys are never modified.
// The write uses atomic file replacement (tmp file + os.Rename) with an
// automatic backup of the original file.
func (s *Service) fixClaudeConfig(req ConfigFixRequest) (*ConfigFixResult, error) {
	// 0. Validate key belongs to predefined items
	keyValid := false
	for _, item := range predefinedConfigItems {
		if item.Key == req.Key {
			keyValid = true
			break
		}
	}
	if !keyValid {
		return &ConfigFixResult{
			Success: false,
			Error:   fmt.Sprintf("不允许修改配置项 %s：不在预定义配置列表中", req.Key),
		}, nil
	}

	// 1. Expand ~ to user home directory
	filePath := expandTilde(req.FilePath)

	// 1b. Validate file path is within allowed set
	if !isConfigPathAllowed(filePath) {
		return &ConfigFixResult{
			Success: false,
			Error:   configPathRejectionMessage(filePath),
		}, nil
	}

	// 2. Determine the value to write
	value := req.Value
	if value == "" {
		// Look up default value from predefined config items
		for _, item := range predefinedConfigItems {
			if item.Key == req.Key {
				value = item.DefaultValue
				break
			}
		}
	}
	if value == "" {
		return &ConfigFixResult{
			Success: false,
			Message: fmt.Sprintf("未找到配置项 %s 的默认值，请手动指定", req.Key),
		}, nil
	}

	// 3. Read target file (create empty JSON if not exists)
	var current map[string]interface{}
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Ensure parent directory exists
			if dir := filepath.Dir(filePath); dir != "" && dir != "." {
				if mkdirErr := os.MkdirAll(dir, 0755); mkdirErr != nil {
					return &ConfigFixResult{
						Success: false,
						Error:   fmt.Sprintf("无法创建配置目录 %s: %v", dir, mkdirErr),
					}, nil
				}
			}
			current = make(map[string]interface{})
		} else {
			return &ConfigFixResult{
				Success: false,
				Error:   fmt.Sprintf("无法读取配置文件 %s: %v", filePath, err),
			}, nil
		}
	} else {
		if err := json.Unmarshal(data, &current); err != nil {
			return &ConfigFixResult{
				Success: false,
				Error:   fmt.Sprintf("配置文件 %s JSON 格式错误: %v", filePath, err),
			}, nil
		}
	}

	// 4. Check if target key already exists with a non-empty value
	keyParts := strings.Split(req.Key, ".")
	existingValue, keyExists := getNestedValue(current, keyParts)
	if keyExists && strings.TrimSpace(fmt.Sprintf("%v", existingValue)) != "" {
		previousValue := maskSensitiveValue(req.Key, fmt.Sprintf("%v", existingValue))
		return &ConfigFixResult{
			Success:       true,
			Message:       fmt.Sprintf("配置项 %s 已存在，跳过写入", req.Key),
			Changed:       false,
			PreviousValue: previousValue,
		}, nil
	}

	// 5. Backup original file (only if it existed and had content)
	var backupPath string
	if len(data) > 0 {
		timestamp := time.Now().Format("20060102-150405")
		backupPath = filePath + ".amagi-backup-" + timestamp
		if writeErr := os.WriteFile(backupPath, data, 0600); writeErr != nil {
			return &ConfigFixResult{
				Success: false,
				Error:   fmt.Sprintf("备份配置文件失败: %v", writeErr),
			}, nil
		}
	}

	// 6. Fill in the missing key
	if err := setNestedValue(current, keyParts, value); err != nil {
		return &ConfigFixResult{
			Success: false,
			Error:   fmt.Sprintf("写入配置失败: %v", err),
		}, nil
	}

	// 7. Serialize to formatted JSON
	newData, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return &ConfigFixResult{
			Success: false,
			Error:   fmt.Sprintf("序列化配置失败: %v", err),
		}, nil
	}

	// 8. Atomic write: temp file -> os.Rename
	tmpPath := filePath + ".tmp"
	if writeErr := os.WriteFile(tmpPath, newData, 0600); writeErr != nil {
		return &ConfigFixResult{
			Success: false,
			Error:   fmt.Sprintf("写入临时文件失败: %v", writeErr),
		}, nil
	}

	if renameErr := os.Rename(tmpPath, filePath); renameErr != nil {
		os.Remove(tmpPath) // Clean up temp file on failure
		return &ConfigFixResult{
			Success: false,
			Error:   fmt.Sprintf("原子替换配置文件失败: %v", renameErr),
		}, nil
	}

	return &ConfigFixResult{
		Success:    true,
		Message:    fmt.Sprintf("配置项 %s 已写入 %s", req.Key, filePath),
		BackupPath: backupPath,
		Changed:    true,
	}, nil
}

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return homeDir
	}
	return filepath.Join(homeDir, path[2:]) // "~/xxx" -> home/xxx
}

// getNestedValue retrieves a value from a nested map using key parts.
// Returns (value, true) if found, (nil, false) if not.
func getNestedValue(data map[string]interface{}, keyParts []string) (interface{}, bool) {
	if len(keyParts) == 0 {
		return nil, false
	}

	current := data
	for i, part := range keyParts {
		if i == len(keyParts)-1 {
			// Last part: return value
			val, ok := current[part]
			return val, ok
		}
		// Intermediate part: traverse deeper
		sub, ok := current[part]
		if !ok {
			return nil, false
		}
		subMap, ok := sub.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current = subMap
	}
	return nil, false
}

// setNestedValue sets a value in a nested map, creating intermediate maps as needed.
// When the value is a JSON array or object string, it is parsed into the typed Go value
// before being set. Returns an error if an intermediate node conflicts with a non-map value.
func setNestedValue(data map[string]interface{}, keyParts []string, value string) error {
	if len(keyParts) == 0 {
		return nil
	}

	// Try to parse value as typed JSON (arrays, objects, numbers, booleans).
	// If parsing succeeds, use the typed value; otherwise use the raw string.
	typedValue := parseConfigValue(value)

	current := data
	for i, part := range keyParts {
		if i == len(keyParts)-1 {
			// Last part: set value
			current[part] = typedValue
			return nil
		}
		// Intermediate part: ensure sub-map exists
		sub, ok := current[part]
		if !ok {
			newMap := make(map[string]interface{})
			current[part] = newMap
			current = newMap
		} else {
			subMap, ok := sub.(map[string]interface{})
			if !ok {
				// Existing value is not a map; type conflict -- refuse to overwrite
				return fmt.Errorf("配置路径 %s 中的 %s 已存在但不是嵌套对象，无法安全写入",
					strings.Join(keyParts, "."), part)
			}
			current = subMap
		}
	}
	return nil
}

// parseConfigValue attempts to parse a string value as typed JSON.
// Only arrays and objects are parsed into typed Go values, since they need to
// be valid JSON structures in the output file. Simple values (strings, numbers,
// booleans) are left as-is since Claude settings files expect string values.
func parseConfigValue(value string) interface{} {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return value
	}
	// Only attempt JSON parse for structured types (arrays and objects).
	// Simple scalar values are kept as strings for Claude settings compatibility.
	if (len(trimmed) > 0 && trimmed[0] == '[') || (len(trimmed) > 0 && trimmed[0] == '{') {
		var typed interface{}
		if err := json.Unmarshal([]byte(trimmed), &typed); err == nil {
			return typed
		}
	}
	return value
}
