package launcher

// wsl_managed_models.go — WSL 侧 distro models.json 的托管候选构建（P3-S1
// ManagedNamespace 分发）。复用 ReconcilePiAgentConfig 的严格解析与 amagi-*
// 命名空间语义，但从字节而非路径输入、输出候选字节而不直接落盘——提交由
// wslsupply 协调器的私有 staging 原语承担（唯一临时名 + 0600 + rename + distro
// 内 chmod 600 补偿）。
//
// 归属语义（design §三/§四）：
//   - amagi-* 命名空间归 CodeBox：期望集整体替换域内条目（含清理已删除的
//     provider）；
//   - 域外内容（非 amagi provider、未知顶层字段）逐字保留——不复制宿主整文件，
//     非 amagi provider 归 WSL 用户；
//   - 构建部分失败不以残缺集合 prune 旧项：retainIDs（本轮构建失败的 amagi-* id）
//     的既有条目保留原样，由调用方逐项报错（P3-S1 验收 3）；
//   - 两级损坏保护：JSON 语法损坏 / providers 嵌套形态错误 → 返回
//     ErrWSLManagedModelsCorrupt，绝不覆盖（P3-S1 验收 2）；
//   - 登录预设 contextWindow 是窄字段托管（补齐语义）：仅 upsert
//     providers.<provider>.modelOverrides.<model>.contextWindow，目标 provider
//     的其它字段与同键其它字段保留（复用 UpsertPiLoginModelOverride 的合并
//     语义，S1 不做清除——清除属 S2 接管框架）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrWSLManagedModelsCorrupt 表示既有 distro models.json 未通过严格解析/形态
// 校验（两级损坏保护）：调用方必须拒绝写入并保留原文，不得静默替换。
var ErrWSLManagedModelsCorrupt = errors.New("existing pi models.json is corrupt")

// PiLoginModelOverride 是登录预设 contextWindow 的窄字段托管描述（cw>0 才分发）。
type PiLoginModelOverride struct {
	Provider      string
	Model         string
	ContextWindow int
}

// BuildWSLPiManagedModelsCandidate 由既有 distro models.json 字节 + 期望 amagi-*
// provider 集 + 登录覆写构建目标候选字节（序列化格式与 WritePiAgentConfig 一致：
// MarshalIndent 两空格 + 尾换行）。existing 允许 nil/空（目标缺失，全新建）。
func BuildWSLPiManagedModelsCandidate(
	existing []byte,
	desiredProviders map[string]any,
	retainIDs []string,
	overrides []PiLoginModelOverride,
) ([]byte, error) {
	base := map[string]any{}
	if len(existing) > 0 {
		// 与 readStrictJSONObject 同语义：存在但空白/语法损坏/形态错误一律拒绝
		//（两级损坏保护），仅目标缺失（nil）走全新建。
		parsed, err := parseStrictJSONObject(existing)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrWSLManagedModelsCorrupt, err)
		}
		base = parsed
	}
	merged := mergeManagedNamespace(base, desiredProviders, retainIDs)
	if err := applyLoginModelOverrides(merged, overrides); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWSLManagedModelsCorrupt, err)
	}
	b, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal managed models.json: %w", err)
	}
	return append(b, '\n'), nil
}

// mergeManagedNamespace 是 mergeProviderConfig 的托管分发变体：prune amagi-*
// 命名空间（含清理已删除 provider），但 retainIDs（本轮构建失败者）的既有条目
// 保留——部分失败不以残缺集合 prune 旧项。域外内容逐字保留。
func mergeManagedNamespace(existing, desired map[string]any, retainIDs []string) map[string]any {
	retain := make(map[string]bool, len(retainIDs))
	for _, id := range retainIDs {
		retain[strings.TrimSpace(id)] = true
	}
	existingProviders := piProviderEntries(existing["providers"])
	desiredProviders := piProviderEntries(desired["providers"])
	providers := make(map[string]any, len(existingProviders)+len(desiredProviders))
	for key, value := range existingProviders {
		if strings.HasPrefix(key, ManagedProviderPrefix) && !retain[key] {
			continue
		}
		providers[key] = value
	}
	for key, value := range desiredProviders {
		providers[key] = value
	}

	merged := make(map[string]any, len(existing)+len(desired))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range desired {
		if key == "providers" {
			continue
		}
		merged[key] = value
	}
	_, existingHasProviders := existing["providers"]
	_, desiredHasProviders := desired["providers"]
	if existingHasProviders || desiredHasProviders || len(providers) > 0 {
		merged["providers"] = providers
	}
	return merged
}

// applyLoginModelOverrides 把登录预设 contextWindow 覆写深合并进 merged 的
// providers.<provider>.modelOverrides.<model>（窄字段：只增改 contextWindow，
// 保留同键其它字段与兄弟键；嵌套形态错误返回错误不覆盖——与
// UpsertPiLoginModelOverride 同一损坏保护）。仅 cw>0 参与（补齐语义）。
func applyLoginModelOverrides(merged map[string]any, overrides []PiLoginModelOverride) error {
	if len(overrides) == 0 {
		return nil
	}
	providers := piProviderEntries(merged["providers"])
	for _, ov := range overrides {
		if ov.ContextWindow <= 0 || strings.TrimSpace(ov.Provider) == "" || strings.TrimSpace(ov.Model) == "" {
			continue
		}
		rawEntry, hasEntry := providers[ov.Provider]
		var entry map[string]any
		if hasEntry {
			if entry, _ = rawEntry.(map[string]any); entry == nil {
				return fmt.Errorf("providers.%s is not an object", ov.Provider)
			}
		} else {
			entry = map[string]any{}
		}
		overrideMap, err := overrideMapAt(entry, "modelOverrides")
		if err != nil {
			return fmt.Errorf("providers.%s: %w", ov.Provider, err)
		}
		modelKey, err := overrideMapAt(overrideMap, ov.Model)
		if err != nil {
			return fmt.Errorf("providers.%s.modelOverrides: %w", ov.Provider, err)
		}
		modelKey["contextWindow"] = ov.ContextWindow
		overrideMap[ov.Model] = modelKey
		entry["modelOverrides"] = overrideMap
		providers[ov.Provider] = entry
	}
	merged["providers"] = providers
	return nil
}
