package sharedmcp

// writers_claude.go — Claude 写入器（spec §2.2）：落点 ~/.claude.json 顶层
// mcpServers.<name>。
//
// 写入纪律：读（防御式，拒符号链接/非普通文件；缺失视为 {} 走新建）→
// sjson.SetRawBytes 字段级写入（顶层键集断言：写后键集 − 写前键集 ⊆
// {mcpServers}，仅首建允许新增这一个键；本机实测 54 顶层键保键集不变是
// R3 硬约束）→ 原子写（保留既有 mode——.claude.json 是既有用户资产，不
// 收紧其权限；新文件 0600）→ 回读断言条目值 canonical 相等。
//
// 条目映射：verbatim（type/command/args/env/url/headers/description 原样
//——登记形态与 Claude mcpServers 同构；description 保留，容忍度风险见
// spec §8 R-7 复核项）。

import (
	"encoding/json"
	"path/filepath"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// claudeWriter 是 Claude Code 的 MCP 配置写入器。
type claudeWriter struct {
	userHome string
}

// ID 返回引擎标识。
func (w *claudeWriter) ID() string { return engineIDClaude }

// TargetPath 返回目标文件路径（展示用）。
func (w *claudeWriter) TargetPath() string { return w.path() }

func (w *claudeWriter) path() string {
	return filepath.Join(w.userHome, ".claude.json")
}

// Supports：stdio/http 均可（verbatim 同构域）。
func (w *claudeWriter) Supports(entry SharedMcpEntry) (bool, string) {
	return supportsJSONServer(entry)
}

// Mapped 返回期望的条目 canonical JSON（verbatim 已知字段）。
func (w *claudeWriter) Mapped(entry SharedMcpEntry) (json.RawMessage, error) {
	return buildEntryRaw(entry, nil)
}

// Apply 写入/覆盖条目，返回写入的 canonical JSON。
func (w *claudeWriter) Apply(entry SharedMcpEntry) (json.RawMessage, error) {
	want, err := w.Mapped(entry)
	if err != nil {
		return nil, NewErr(KindIO, "条目 %s 序列化失败（%v）", entry.Name, err)
	}
	data, _, err := readJSONConfigDefensively(w.path(), "Claude 配置文件")
	if err != nil {
		return nil, err
	}
	if data == nil {
		data = []byte("{}")
	}
	next, err := setMcpServerEntry(data, entry.Name, want)
	if err != nil {
		return nil, err
	}
	if !gjson.ValidBytes(next) {
		return nil, NewErr(KindIO, "Claude 配置写入候选不是合法 JSON，已拒绝写入")
	}
	if err := assertTopKeysAllowMcpServers(data, next); err != nil {
		return nil, err
	}
	if err := writeAtomicPreservingMode(w.path(), next); err != nil {
		return nil, err
	}
	after, _, err := readJSONConfigDefensively(w.path(), "Claude 配置文件")
	if err != nil {
		return nil, err
	}
	if !equalJSONValue([]byte(gjson.GetBytes(after, entryPath(entry.Name)).Raw), want) {
		return nil, NewErr(KindIO, "Claude 配置写后回读与期望不符：%s", w.path())
	}
	return want, nil
}

// Remove 删除条目；mcpServers 清空后保留空对象 {} 不删键（顶层键集复原）。
func (w *claudeWriter) Remove(name string) error {
	data, exists, err := readJSONConfigDefensively(w.path(), "Claude 配置文件")
	if err != nil {
		return err
	}
	if !exists || !gjson.GetBytes(data, entryPath(name)).Exists() {
		return nil // 幂等
	}
	next, derr := sjson.DeleteBytes(data, entryPath(name))
	if derr != nil {
		return NewErr(KindIO, "Claude 配置删除序列化失败（%v）", derr)
	}
	if !gjson.ValidBytes(next) {
		return NewErr(KindIO, "Claude 配置删除候选不是合法 JSON，已拒绝写入")
	}
	if !sameStringSet(topKeyNames(data), topKeyNames(next)) {
		return NewErr(KindIO, "Claude 配置顶层键集合在删除后变化")
	}
	if err := writeAtomicPreservingMode(w.path(), next); err != nil {
		return err
	}
	after, _, err := readJSONConfigDefensively(w.path(), "Claude 配置文件")
	if err != nil {
		return err
	}
	if gjson.GetBytes(after, entryPath(name)).Exists() {
		return NewErr(KindIO, "Claude 配置删除后回读仍存在条目 %s", name)
	}
	return nil
}

// Has 报告目标当前是否存在该名字。
func (w *claudeWriter) Has(name string) (bool, error) {
	data, exists, err := readJSONConfigDefensively(w.path(), "Claude 配置文件")
	if err != nil || !exists {
		return false, err
	}
	return gjson.GetBytes(data, entryPath(name)).Exists(), nil
}

// Read 读回条目当前内容（canonical；不存在 → nil,nil）。
func (w *claudeWriter) Read(name string) (json.RawMessage, error) {
	data, exists, err := readJSONConfigDefensively(w.path(), "Claude 配置文件")
	if err != nil || !exists {
		return nil, err
	}
	got := gjson.GetBytes(data, entryPath(name))
	if !got.Exists() {
		return nil, nil
	}
	return []byte(got.Raw), nil
}
