package codexplugin

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var safePluginIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9._-]+$`)

func selectorPluginID(selector PluginSelector) (string, error) {
	id := strings.TrimSpace(selector.PluginID)
	if id == "" {
		id = strings.TrimSpace(selector.ID)
	}
	if id == "" && strings.TrimSpace(selector.Name) != "" && strings.TrimSpace(selector.Marketplace) != "" {
		id = strings.TrimSpace(selector.Name) + "@" + strings.TrimSpace(selector.Marketplace)
	}
	if err := validatePluginID(id); err != nil {
		return "", err
	}
	return id, nil
}

func validatePluginID(pluginID string) error {
	trimmed := strings.TrimSpace(pluginID)
	if trimmed == "" {
		return fmt.Errorf("插件 ID 不能为空")
	}
	if strings.Count(trimmed, "@") != 1 || !safePluginIDPattern.MatchString(trimmed) {
		return fmt.Errorf("无效的 Codex 插件 ID %q，应为 plugin@marketplace，且只能包含字母、数字、点、下划线和连字符", pluginID)
	}
	if strings.ContainsAny(trimmed, "\x00\r\n/\\;&|`$<>(){}[]!\"") {
		return fmt.Errorf("插件 ID 包含不安全字符")
	}
	return nil
}

func validateMarketplaceName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("市场源名称不能为空")
	}
	if strings.ContainsAny(trimmed, "\x00\r\n/\\;&|`$<>(){}[]!\"") {
		return fmt.Errorf("市场源名称包含不安全字符")
	}
	return nil
}

func validateSource(source string) error {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return fmt.Errorf("市场源地址不能为空")
	}
	if strings.ContainsAny(trimmed, "\x00\r\n") {
		return fmt.Errorf("市场源地址包含不安全控制字符")
	}
	return nil
}

func splitPluginID(pluginID string) (string, string) {
	idx := strings.LastIndex(pluginID, "@")
	if idx <= 0 || idx >= len(pluginID)-1 {
		return pluginID, ""
	}
	return pluginID[:idx], pluginID[idx+1:]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func relativePluginPath(installPath, filePath string) string {
	relPath, err := filepath.Rel(installPath, filePath)
	if err != nil {
		return filepath.ToSlash(filePath)
	}
	return filepath.ToSlash(relPath)
}
