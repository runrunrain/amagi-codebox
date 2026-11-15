package sharedmcp

// writers_pi.go — Pi 写入器（spec §2.2）：落点 <piAgentDir>/mcp.json 顶层
// mcpServers.<name>。
//
// agentDir 复刻 internal/piconfig.agentDir 语义（该包未导出且不在本契约
// 白名单）：$PI_CODING_AGENT_DIR 优先，否则 ~/.pi/agent。文件不存在 →
// MkdirAll 后新建 {}（本机已有含 2 个手写 server 的文件——不触碰）。
//
// 条目映射：verbatim（本机实测登记文件与 pi mcp.json 条目结构同构）；
// description 保留。写：sjson 字段级 + 顶层键集断言（不变）→ 原子私写
// 0600（有意收紧，同登记文件纪律）。

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// piWriter 是 Pi 的 MCP 配置写入器。
type piWriter struct {
	env      []string
	userHome string
}

// ID 返回引擎标识。
func (w *piWriter) ID() string { return engineIDPi }

// TargetPath 返回目标文件路径（展示用）。
func (w *piWriter) TargetPath() string { return w.path() }

// agentDir 复刻 piconfig.agentDir：$PI_CODING_AGENT_DIR 优先，否则
// ~/.pi/agent（userHome 为构造时解析的用户主目录）。
func (w *piWriter) agentDir() string {
	if v := envValueOf(w.env, "PI_CODING_AGENT_DIR"); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return filepath.Join(w.userHome, ".pi", "agent")
}

func (w *piWriter) path() string {
	return filepath.Join(w.agentDir(), "mcp.json")
}

// envValueOf 从注入的 env 切片取键值（大小写不敏感，platform.envValue
// 同语义；本包内实现避免导出依赖）。
func envValueOf(env []string, key string) string {
	for _, entry := range env {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(parts[0], key) {
			return parts[1]
		}
	}
	return ""
}

// Supports：stdio/http 均可（verbatim 同构域）。
func (w *piWriter) Supports(entry SharedMcpEntry) (bool, string) {
	return supportsJSONServer(entry)
}

// Mapped 返回期望的条目 canonical JSON（verbatim 已知字段）。
func (w *piWriter) Mapped(entry SharedMcpEntry) (json.RawMessage, error) {
	return buildEntryRaw(entry, nil)
}

// Apply 写入/覆盖条目，返回写入的 canonical JSON。
func (w *piWriter) Apply(entry SharedMcpEntry) (json.RawMessage, error) {
	want, err := w.Mapped(entry)
	if err != nil {
		return nil, NewErr(KindIO, "条目 %s 序列化失败（%v）", entry.Name, err)
	}
	data, _, err := readJSONConfigDefensively(w.path(), "Pi MCP 配置文件")
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
		return nil, NewErr(KindIO, "Pi MCP 配置写入候选不是合法 JSON，已拒绝写入")
	}
	if err := assertTopKeysAllowMcpServers(data, next); err != nil {
		return nil, err
	}
	if err := writeAtomicPrivate(w.path(), next); err != nil {
		return nil, err
	}
	after, _, err := readJSONConfigDefensively(w.path(), "Pi MCP 配置文件")
	if err != nil {
		return nil, err
	}
	if !equalJSONValue([]byte(gjson.GetBytes(after, entryPath(entry.Name)).Raw), want) {
		return nil, NewErr(KindIO, "Pi MCP 配置写后回读与期望不符：%s", w.path())
	}
	return want, nil
}

// Remove 删除条目；mcpServers 清空后保留空对象 {} 不删键。
func (w *piWriter) Remove(name string) error {
	data, exists, err := readJSONConfigDefensively(w.path(), "Pi MCP 配置文件")
	if err != nil {
		return err
	}
	if !exists || !gjson.GetBytes(data, entryPath(name)).Exists() {
		return nil // 幂等
	}
	next, derr := sjson.DeleteBytes(data, entryPath(name))
	if derr != nil {
		return NewErr(KindIO, "Pi MCP 配置删除序列化失败（%v）", derr)
	}
	if !gjson.ValidBytes(next) {
		return NewErr(KindIO, "Pi MCP 配置删除候选不是合法 JSON，已拒绝写入")
	}
	if !sameStringSet(topKeyNames(data), topKeyNames(next)) {
		return NewErr(KindIO, "Pi MCP 配置顶层键集合在删除后变化")
	}
	if err := writeAtomicPrivate(w.path(), next); err != nil {
		return err
	}
	after, _, err := readJSONConfigDefensively(w.path(), "Pi MCP 配置文件")
	if err != nil {
		return err
	}
	if gjson.GetBytes(after, entryPath(name)).Exists() {
		return NewErr(KindIO, "Pi MCP 配置删除后回读仍存在条目 %s", name)
	}
	return nil
}

// Has 报告目标当前是否存在该名字。
func (w *piWriter) Has(name string) (bool, error) {
	data, exists, err := readJSONConfigDefensively(w.path(), "Pi MCP 配置文件")
	if err != nil || !exists {
		return false, err
	}
	return gjson.GetBytes(data, entryPath(name)).Exists(), nil
}

// Read 读回条目当前内容（canonical；不存在 → nil,nil）。
func (w *piWriter) Read(name string) (json.RawMessage, error) {
	data, exists, err := readJSONConfigDefensively(w.path(), "Pi MCP 配置文件")
	if err != nil || !exists {
		return nil, err
	}
	got := gjson.GetBytes(data, entryPath(name))
	if !got.Exists() {
		return nil, nil
	}
	return []byte(got.Raw), nil
}
