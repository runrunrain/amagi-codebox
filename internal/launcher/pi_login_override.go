package launcher

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Pi 登录预设的 contextWindow 覆写（app.go 装配回调与 LaunchPiSession 登录分支消费）。
//
// 背景：Pi 登录模型（原生 /login 认证的内置 provider）没有 amagi 预设参数面，
// 上下文窗口此前只能用 Pi 内置目录的固定值。Pi 的 models.json 支持对内置
// provider 的已注册模型做 modelOverrides 覆写（pi provider-composer：
// contextWindow = override.contextWindow ?? model.contextWindow，实测
// pi-1.1.0 加载生效）。本函数把登录预设的窗口值深合并进标准 agent root 的
// models.json——Leader 与 amagi 子 agent 共用该 root（登录会话
// PI_CODING_AGENT_DIR=canonical root 被 amagi 扩展按 PI_ 前缀透传给子进程），
// 一处写入两侧生效。
//
// 合并语义（区别于 mergeProviderConfig 的 provider 级整体替换）：
//   - 保留 models.json 其它顶层字段、其它 provider、目标 provider 的全部现有
//     字段（baseUrl/models/compat/modelOverrides 兄弟键……）；
//   - modelOverrides.<model> 只增改 contextWindow 字段，保留同键其它字段
//     （如用户手写的 compat 覆写，实战 anthropic supportsStrictTools 已在用）
//     与其它 model 键（同模型的多个登录预设共享同一覆写——窗口是模型级共享
//     值，保存侧全组对齐，见 ConfigService.alignPiLoginContextWindowLocked）；
//   - contextWindow<=0 表示目标态为「无覆写」：移除该键的 contextWindow 字段
//     （恢复 Pi 内置目录值）；键被删空则连带清理，provider 条目被清空则删除
//     该 provider 键（pi 对空 provider overlay 报 composition error，不留空壳）；
//   - 幂等：目标态与现状一致（含清除路径下本就无 contextWindow 字段）不落盘。
//
// 损坏保护（两级）：JSON 语法损坏经 readStrictJSONObject 报错不覆写；目标
// 路径上「已存在但类型错误」的嵌套结构（providers/provider/modelOverrides/
// model 任一为非对象）同样返回错误不覆写——无效内容留给用户诊断，不静默替换。
// 写入走 WritePiAgentConfig 原子范式。

// UpsertPiLoginModelOverride 把登录预设的 contextWindow 覆写合并进
// agentDir/models.json 的 providers.<provider>.modelOverrides.<model>。
// contextWindow>0 写入；<=0 移除该键的 contextWindow 字段（幂等恢复内置值）。
func UpsertPiLoginModelOverride(agentDir, provider, model string, contextWindow int) error {
	if strings.TrimSpace(agentDir) == "" {
		return fmt.Errorf("agentDir is required")
	}
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return fmt.Errorf("provider and model are required")
	}

	path := filepath.Join(agentDir, "models.json")
	existing, err := readStrictJSONObject(path)
	if err != nil {
		return fmt.Errorf("read existing pi models.json: %w", err)
	}

	providers := piProviderEntries(existing["providers"])
	rawEntry, hasEntry := providers[provider]
	var entry map[string]any
	if hasEntry {
		if entry, _ = rawEntry.(map[string]any); entry == nil {
			return fmt.Errorf("pi models.json providers.%s is not an object; refusing to overwrite (fix the file manually)", provider)
		}
	} else {
		entry = map[string]any{}
	}

	changed := false
	if contextWindow > 0 {
		overrides, err := overrideMapAt(entry, "modelOverrides")
		if err != nil {
			return fmt.Errorf("pi models.json providers.%s: %w", provider, err)
		}
		modelKey, err := overrideMapAt(overrides, model)
		if err != nil {
			return fmt.Errorf("pi models.json providers.%s.modelOverrides: %w", provider, err)
		}
		if cur, ok := modelKey["contextWindow"].(float64); !ok || int(cur) != contextWindow {
			changed = true // 仅值实际变化时写盘（幂等启动不刷新 mtime）
		}
		modelKey["contextWindow"] = contextWindow
		overrides[model] = modelKey
		entry["modelOverrides"] = overrides
	} else {
		overrides, hasOverrides := entry["modelOverrides"]
		if !hasOverrides {
			return nil // 本就无覆写面：目标态已达成，不动文件
		}
		overrideMap, _ := overrides.(map[string]any)
		if overrideMap == nil {
			return fmt.Errorf("pi models.json providers.%s.modelOverrides is not an object; refusing to overwrite (fix the file manually)", provider)
		}
		rawModelKey, hasModel := overrideMap[model]
		if !hasModel {
			return nil // 该 model 无覆写键：目标态已达成，不动文件
		}
		modelKey, _ := rawModelKey.(map[string]any)
		if modelKey == nil {
			return fmt.Errorf("pi models.json providers.%s.modelOverrides.%s is not an object; refusing to overwrite (fix the file manually)", provider, model)
		}
		if _, hasCW := modelKey["contextWindow"]; !hasCW {
			return nil // 键存在但无 contextWindow 字段：目标态已达成，不动文件
		}
		delete(modelKey, "contextWindow")
		if len(modelKey) == 0 {
			delete(overrideMap, model)
		} else {
			overrideMap[model] = modelKey
		}
		if len(overrideMap) == 0 {
			delete(entry, "modelOverrides")
			// provider 条目被清空 → 删除该 provider 键：pi 对空 provider
			// overlay 报 composition error（provider-composer「must specify
			// baseUrl/headers/compat/modelOverrides/models」），不留空壳。
			// providers 空时保留空对象（pi schema 合法），不删除顶层键。
			if len(entry) == 0 {
				delete(providers, provider)
			}
		} else {
			entry["modelOverrides"] = overrideMap
		}
		changed = true
	}
	if !changed {
		return nil
	}
	if _, stillPresent := providers[provider]; stillPresent || len(entry) > 0 {
		providers[provider] = entry // 未被制枝的 provider（含新建）回写；已制枝的不复活
	}
	existing["providers"] = providers
	return WritePiAgentConfig(agentDir, existing)
}

// overrideMapAt 取 parent[key] 为对象 map；key 不存在时新建空 map 返回（调用方
// 赋值回写）；key 存在但非对象时返回错误（损坏保护，不静默替换）。
func overrideMapAt(parent map[string]any, key string) (map[string]any, error) {
	raw, has := parent[key]
	if !has {
		return map[string]any{}, nil
	}
	m, _ := raw.(map[string]any)
	if m == nil {
		return nil, fmt.Errorf("entry %q is not an object; refusing to overwrite (fix the file manually)", key)
	}
	return m, nil
}
