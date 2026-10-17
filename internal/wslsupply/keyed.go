package wslsupply

// keyed.go — 键级 TrackedProjection 引擎（design §三冲突判定表 + §四字段级/
// server 级最小登记单元，P3-S2）。
//
// 一个「键」是投影域内的最小登记单元：settings.json 的白名单字段、amagi.json
// 的扁平字段路径（profile、agents/<角色>、compaction、concurrency、mcp/<段>）、
// mcp.json 的 mcpServers/<server>。键路径以 "/" 分隔，只走对象层（不穿过数组）。
//
// 冲突判定（§三表，逐键）：
//   - 无记录 + 目标已有该键 → unmanaged-existing，保留原样（不因内容相同而
//     视为已托管；接管须显式确认）；
//   - 有记录 + 目标 == 上次写入 → 来源未变跳过 / 来源变才更新；
//   - 有记录 + 目标变化（或被删除）→ 本地修改/本地删除，冻结该键；
//   - 双侧同变 → 用 WSL 现状并列报告冲突，不以 mtime 仲裁；
//   - 来源删除 → 仅目标仍等于上次写入（托管且未手改）才移除；
//   - 转换失败 → 该键不新增不更新，目标现状保持（§五原子单元）。
// 比较基准是规范化 JSON（重序列化 map 键序确定），目标侧空白/格式编辑不算
// 语义修改；mtime 仅作展示辅助信息，不选胜者。

import (
	"encoding/json"
	"strings"
)

// SubHash 由 state.go 定义（来源/写入双基准）。此处补齐键级判定与合并原语。

// keyedDecisionKind 是单键动作分类。
type keyedDecisionKind uint8

const (
	keyNone keyedDecisionKind = iota
	keyAdd
	keyUpdate
	keyRemove
	keyFrozenLocalMod
	keyFrozenLocalDel
	keyFrozenBothChanged
	keyUnmanaged
)

// keyedDecision 是单键判定结果（Reason 为中文、脱敏）。
type keyedDecision struct {
	Key    string
	Kind   keyedDecisionKind
	Reason string
}

func (k keyedDecisionKind) writable() bool {
	return k == keyAdd || k == keyUpdate || k == keyRemove
}

func (k keyedDecisionKind) String() string {
	switch k {
	case keyNone:
		return "no-change"
	case keyAdd:
		return "add"
	case keyUpdate:
		return "update"
	case keyRemove:
		return "remove"
	case keyFrozenLocalMod:
		return "keep-frozen"
	case keyFrozenLocalDel:
		return "keep-frozen"
	case keyFrozenBothChanged:
		return "conflict"
	case keyUnmanaged:
		return "unmanaged-existing"
	}
	return "unknown"
}

// resolveKeyed 对「期望键集 × 目标现状 × 登记基准」做逐键判定。targetGet 返回
// 目标文档中该键路径上的当前值（含存在性）；subs 是登记的键级基准。
func resolveKeyed(desired map[string]any, targetGet func(string) (any, bool), subs map[string]SubHash) []keyedDecision {
	union := map[string]bool{}
	for k := range desired {
		union[k] = true
	}
	for k := range subs {
		union[k] = true
	}
	keys := make([]string, 0, len(union))
	for k := range union {
		keys = append(keys, k)
	}
	sortStrings(keys)
	out := make([]keyedDecision, 0, len(keys))
	for _, key := range keys {
		srcVal, srcOK := desired[key]
		tgtVal, tgtOK := targetGet(key)
		sub, inSub := subs[key]
		inSub = inSub && (sub.Written != "" || sub.Source != "")
		srcHash := ""
		if srcOK {
			srcHash = keyedValueHash(srcVal)
		}
		switch {
		case srcOK && inSub:
			switch {
			case !tgtOK:
				out = append(out, keyedDecision{Key: key, Kind: keyFrozenLocalDel,
					Reason: "WSL 侧已删除该条目，保留该状态（解除冻结见操作区）"})
			case keyedValueHash(tgtVal) != sub.Written:
				if sub.Source != srcHash {
					out = append(out, keyedDecision{Key: key, Kind: keyFrozenBothChanged,
						Reason: "双侧均有修改：保留 WSL 现状并列报告冲突，不自动合并"})
				} else {
					out = append(out, keyedDecision{Key: key, Kind: keyFrozenLocalMod,
						Reason: "WSL 侧已被手动修改，保留本地（预览后可重新接管）"})
				}
			case sub.Source == srcHash:
				out = append(out, keyedDecision{Key: key, Kind: keyNone, Reason: "无变化"})
			default:
				out = append(out, keyedDecision{Key: key, Kind: keyUpdate, Reason: "来源已变化，更新"})
			}
		case srcOK && !inSub:
			if tgtOK {
				out = append(out, keyedDecision{Key: key, Kind: keyUnmanaged,
					Reason: "WSL 侧已有同名条目（用户自有），保留原样待确认接管"})
			} else {
				out = append(out, keyedDecision{Key: key, Kind: keyAdd, Reason: "新增（来自 Windows）"})
			}
		case !srcOK && inSub:
			if tgtOK && keyedValueHash(tgtVal) == sub.Written {
				out = append(out, keyedDecision{Key: key, Kind: keyRemove, Reason: "来源已删除，移除托管条目"})
			} else if tgtOK {
				out = append(out, keyedDecision{Key: key, Kind: keyFrozenLocalMod,
					Reason: "来源已删除，但 WSL 侧已被手动修改，保留本地"})
			}
			// 目标亦缺失：登记基准刻意保留（不产生 decision、不进报告）——按 §三
			// 「有记录+目标被删→冻结」，用户删除的键若从来源回归则判
			// local-deletion-frozen（保留用户删除）；登记存续依赖 buildCandidate
			// 先整体复制 state.Subs 的既有行为（F-4：注释与行为对齐）。
		}
	}
	return out
}

// keyedValueHash 计算键值的规范化 hash：canonical JSON（map 键序确定），
// 空白/键序差异不产生伪修改。
func keyedValueHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return hexHash([]byte(err.Error()))
	}
	return hexHash(b)
}

// keyedPathSplit 拆分扁平键路径；空段视为非法（返回 nil）。
func keyedPathSplit(key string) []string {
	parts := strings.Split(key, "/")
	for _, p := range parts {
		if p == "" {
			return nil
		}
	}
	return parts
}

// keyedGet 沿对象层取值（中间段非对象或缺失 → 未命中）。
func keyedGet(obj map[string]any, key string) (any, bool) {
	parts := keyedPathSplit(key)
	if obj == nil || parts == nil {
		return nil, false
	}
	cur := obj
	for i, p := range parts {
		v, ok := cur[p]
		if !ok {
			return nil, false
		}
		if i == len(parts)-1 {
			return v, true
		}
		next, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return nil, false
}

// keyedSet 沿对象层写入（中间段缺失则建对象；中间段非对象 → false，调用方
// 把该键按不可合并处理：保留目标现状 + 诊断）。
func keyedSet(obj map[string]any, key string, value any) bool {
	parts := keyedPathSplit(key)
	if obj == nil || parts == nil {
		return false
	}
	cur := obj
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = value
			return true
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			if _, exists := cur[p]; exists {
				return false // 中间段被非对象占据：不可安全合并
			}
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	return false
}

// keyedDelete 删除叶子键（中间缺失 → 无操作成功；中间非对象 → 同样无操作，
// 由判定阶段决定语义）。
func keyedDelete(obj map[string]any, key string) {
	parts := keyedPathSplit(key)
	if obj == nil || parts == nil {
		return
	}
	cur := obj
	for i, p := range parts {
		v, ok := cur[p]
		if !ok {
			return
		}
		if i == len(parts)-1 {
			delete(cur, p)
			return
		}
		next, ok := v.(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
}

// sortStrings 是排序小助手（避免重复引入 sort 依赖处的样板）。
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// selectionMatchesKey 报告键 key（规范化全路径标识，如 "mcpServers/demo"、
// "profiles/demo"）是否在选择集 selected 内（SA-03 修复：UI 预览/勾选/导入
// 全链统一传全路径键，SetUnitSelection 原样保存——匹配以全路径为准，不能
// 全局截叶名，否则 mcp_servers 与 profiles 的同名叶会同时被误选）。
// 兼容历史登记的叶名形态：仅当叶名在 allKeys 内唯一对应时生效（同名双义
// 保守不选，不猜作用域）。allKeys 是本单元期望键集全集（含已从来源删除但
// 仍登记的键）。
func selectionMatchesKey(selected map[string]bool, key string, allKeys []string) bool {
	if len(selected) == 0 {
		return false
	}
	if selected[key] {
		return true
	}
	leaf := keyLeafName(key)
	if !selected[leaf] {
		return false
	}
	for _, k := range allKeys {
		if k != key && keyLeafName(k) == leaf {
			return false // 同名叶跨作用域双义：叶名不唯一，保守不选
		}
	}
	return true
}
