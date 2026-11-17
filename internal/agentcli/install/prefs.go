package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// 用户清理偏好的键（shim + 三个技能目标）。文件中仅记录显式清理（值
// false）；键缺席 = 未表态（参与默认安装）；显式安装成功会移除该键。
const (
	prefShim   = "shim"
	prefAgents = "agents"
	prefClaude = "claude"
	prefCodex  = "codex"
)

var knownPreferenceKeys = []string{prefShim, prefAgents, prefClaude, prefCodex}

// preferencesPath 偏好文件路径：<home>/agent-cli-install.json。
// 记录用户显式清理的选择，避免应用下次启动又装回（同 feishu-sync 的
// cli-local-install.json 机制）。
func (m *Manager) preferencesPath() string {
	return filepath.Join(m.home, "agent-cli-install.json")
}

// declinedDefaults 读取用户显式清理的键集合。文件缺失视为无记录；已知键
// 的值必须是布尔，格式无效时如实报错——不静默忽略、不覆盖用户的文件。
// 未知键忽略（前向兼容）。
func (m *Manager) declinedDefaults() (map[string]bool, error) {
	data, err := os.ReadFile(m.preferencesPath())
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 CLI 安装偏好：%w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("CLI 安装偏好格式无效：%w", err)
	}
	declined := map[string]bool{}
	for _, key := range knownPreferenceKeys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		var b bool
		if err := json.Unmarshal(value, &b); err != nil {
			return nil, fmt.Errorf("CLI 安装偏好格式无效：%s 应为布尔", key)
		}
		if !b {
			declined[key] = true
		}
	}
	return declined, nil
}

// setDeclined 记录（declined=true）或清除（false）一个面的用户清理选择并
// 原子写回偏好文件；已是目标状态时不写盘。键必须是已知偏好键。
func (m *Manager) setDeclined(key string, declined bool) error {
	known := false
	for _, k := range knownPreferenceKeys {
		if k == key {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("未知偏好键：%s", key)
	}
	current, err := m.declinedDefaults()
	if err != nil {
		return err
	}
	if declined == current[key] {
		return nil
	}
	if declined {
		current[key] = true
	} else {
		delete(current, key)
	}
	raw := make(map[string]bool, len(current))
	for k := range current {
		raw[k] = false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.home, 0o755); err != nil {
		return fmt.Errorf("create home dir: %w", err)
	}
	return atomicWrite(m.preferencesPath(), string(data)+"\n", 0o600)
}
