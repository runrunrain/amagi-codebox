package sharedmcp

// writers_opencode.go — OpenCode 写入器（spec §2.2）：落点
// ~/.config/opencode/opencode.json 顶层 mcp。
//
// 形态检测式写入（本机 opencode 1.17.18 实测 = V1 `mcp.<name>` 直接嵌套
// 时代；仓库先例 app_wsl_supply_s4.go:923 同按 V1 消费）：
//   - mcp.servers 是对象 → V2 写 mcp.servers.<name>；
//   - 否则（含 mcp 键无/空）→ V1 写 mcp.<name>；
//   - 无 mcp 键的新文件默认 V1（spec §2.2 裁定 + §8 R-4 施工复核点）。
//   mcp 内其它键（V2 的 timeout、V1 其它 server）一律保真不动
//  （SyncManagedProviders 的「只替换自己拥有的键」合并语义）。
//
// 条目映射（字段翻译，非 verbatim）：stdio → {type:"local",
// command:[<command>,...args]}（command 数组、无 args 字段）+
// environment:{...env}（键名不是 env）；http → {type:"remote", url,
// headers}；不写 enabled/disabled（默认启用，最小写入面）；description
// 丢弃（schema 无此属性）。
//
// 写：json.MarshalIndent(2) + 尾 \n + 原子私写 0600（整体重排是
// SyncManagedProviders 既定先例语义；其它键做值级保留断言，不做字节级断言）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tidwall/gjson"
)

// opencodeWriter 是 OpenCode 的 MCP 配置写入器。
type opencodeWriter struct {
	userHome string
}

// ID 返回引擎标识。
func (w *opencodeWriter) ID() string { return engineIDOpenCode }

// TargetPath 返回目标文件路径（展示用）。
func (w *opencodeWriter) TargetPath() string { return w.path() }

func (w *opencodeWriter) path() string {
	return filepath.Join(w.userHome, ".config", "opencode", "opencode.json")
}

// Supports：stdio/http 均可（local/remote 翻译域全覆盖）；未知 type 不可。
func (w *opencodeWriter) Supports(entry SharedMcpEntry) (bool, string) {
	return supportsJSONServer(entry)
}

// mappedEntry 构造翻译后的条目对象。
func (w *opencodeWriter) mappedEntry(entry SharedMcpEntry) map[string]any {
	out := map[string]any{}
	switch entry.Type {
	case "stdio":
		out["type"] = "local"
		cmd := make([]any, 0, len(entry.Args)+1)
		cmd = append(cmd, entry.Command)
		cmd = append(cmd, toAnySlice(entry.Args)...)
		out["command"] = cmd
		if len(entry.Env) > 0 {
			env := make(map[string]any, len(entry.Env))
			for k, v := range entry.Env {
				env[k] = v
			}
			out["environment"] = env
		}
	case "http":
		out["type"] = "remote"
		out["url"] = entry.URL
		if len(entry.Headers) > 0 {
			hdrs := make(map[string]any, len(entry.Headers))
			for k, v := range entry.Headers {
				hdrs[k] = v
			}
			out["headers"] = hdrs
		}
	}
	return out
}

func toAnySlice(ss []string) []any {
	out := make([]any, 0, len(ss))
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}

// Mapped 返回期望条目的 canonical JSON。
func (w *opencodeWriter) Mapped(entry SharedMcpEntry) (json.RawMessage, error) {
	return json.Marshal(w.mappedEntry(entry))
}

// readObj 读取整文件为对象；缺失 → (nil,nil)；根非法 → ErrTargetCorrupt。
func (w *opencodeWriter) readObj() (map[string]any, error) {
	data, exists, err := readJSONConfigDefensively(w.path(), "OpenCode 配置文件")
	if err != nil || !exists {
		return nil, err
	}
	var obj map[string]any
	if uerr := json.Unmarshal(data, &obj); uerr != nil || obj == nil {
		return nil, NewErr(KindTargetCorrupt, "OpenCode 配置不是合法 JSON 对象，已拒绝改动（请先手动修复）：%s", w.path())
	}
	return obj, nil
}

// v2path 报告是否按 V2（mcp.servers.<name>）写入：mcp 是对象且
// mcp.servers 是对象 → true。
func opencodeUseV2(obj map[string]any) bool {
	mcp, ok := obj["mcp"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = mcp["servers"].(map[string]any)
	return ok
}

// entryKeyPath 返回条目在对象内的取值/写入路径（V2 → "mcp.servers.<n>"，
// V1 → "mcp.<n>"；name 已过 validName）。
func opencodeEntryPath(v2 bool, name string) string {
	if v2 {
		return fmt.Sprintf("mcp.servers.%s", name)
	}
	return fmt.Sprintf("mcp.%s", name)
}

// Apply 写入条目，返回写入的 canonical JSON。
func (w *opencodeWriter) Apply(entry SharedMcpEntry) (json.RawMessage, error) {
	want, err := w.Mapped(entry)
	if err != nil {
		return nil, NewErr(KindIO, "条目 %s 序列化失败（%v）", entry.Name, err)
	}
	obj, err := w.readObj()
	if err != nil {
		return nil, err
	}
	if obj == nil {
		obj = map[string]any{}
	}
	v2 := opencodeUseV2(obj)
	// 值级保真断言基准：除目标键所在的顶层键外，其余顶层键值不变（R6 ③
	// 只断言 theme 等其它键值级保留，不做字节级断言）。
	beforeEcho := echoTopLevel(obj, opencodeEntryPath(v2, entry.Name))
	setNestedPath(obj, opencodeEntryPath(v2, entry.Name), w.mappedEntry(entry))
	if !echoTopLevel(obj, opencodeEntryPath(v2, entry.Name)).equalWithin(beforeEcho) {
		return nil, NewErr(KindIO, "OpenCode 配置写入候选意外改动其它键，已拒绝写入")
	}
	next, merr := json.MarshalIndent(obj, "", "  ")
	if merr != nil {
		return nil, NewErr(KindIO, "OpenCode 配置序列化失败（%v）", merr)
	}
	next = append(next, '\n')
	if err := os.MkdirAll(filepath.Dir(w.path()), 0o700); err != nil {
		return nil, NewErr(KindIO, "创建 OpenCode 配置目录失败：%s（%v）", filepath.Dir(w.path()), err)
	}
	if err := writeAtomicPrivate(w.path(), next); err != nil {
		return nil, err
	}
	// 写后回读断言（值级）。
	after, err := w.readObj()
	if err != nil {
		return nil, err
	}
	got := gjson.GetBytes(mustJSON(after), opencodeEntryPath(v2, entry.Name))
	if !got.Exists() || !equalJSONValue([]byte(got.Raw), want) {
		return nil, NewErr(KindIO, "OpenCode 配置写后回读与期望不符：%s", w.path())
	}
	return want, nil
}

// Remove 删除条目（V1/V2 同检测）；不存在 → 幂等成功。空容器保留（只动
// 我方键，不删 mcp / mcp.servers 键）。
func (w *opencodeWriter) Remove(name string) error {
	obj, err := w.readObj()
	if err != nil {
		return err
	}
	if obj == nil {
		return nil
	}
	v2 := opencodeUseV2(obj)
	if !removeNestedPath(obj, opencodeEntryPath(v2, name)) {
		return nil // 幂等：键不存在
	}
	next, merr := json.MarshalIndent(obj, "", "  ")
	if merr != nil {
		return NewErr(KindIO, "OpenCode 配置序列化失败（%v）", merr)
	}
	next = append(next, '\n')
	if err := os.MkdirAll(filepath.Dir(w.path()), 0o700); err != nil {
		return NewErr(KindIO, "创建 OpenCode 配置目录失败：%s（%v）", filepath.Dir(w.path()), err)
	}
	if err := writeAtomicPrivate(w.path(), next); err != nil {
		return err
	}
	return nil
}

// Has 报告条目当前是否存在（V1/V2 同检测）。
func (w *opencodeWriter) Has(name string) (bool, error) {
	obj, err := w.readObj()
	if err != nil || obj == nil {
		return false, err
	}
	cur, ok := nestedPath(obj, opencodeEntryPath(opencodeUseV2(obj), name))
	return ok && cur != nil, nil
}

// Read 读回条目当前内容（canonical；不存在 → nil,nil）。
func (w *opencodeWriter) Read(name string) (json.RawMessage, error) {
	obj, err := w.readObj()
	if err != nil || obj == nil {
		return nil, err
	}
	cur, ok := nestedPath(obj, opencodeEntryPath(opencodeUseV2(obj), name))
	if !ok || cur == nil {
		return nil, nil
	}
	b, merr := json.Marshal(cur)
	if merr != nil {
		return nil, NewErr(KindIO, "OpenCode 条目序列化失败（%v）", merr)
	}
	return b, nil
}

// ---- 通用嵌套路径工具（对象图上的 get/set/remove，路径段均为 validName
// 或固定字面量，无转义需求） ----

func splitDotPath(p string) []string {
	var out []string
	cur := ""
	for i := 0; i < len(p); i++ {
		if p[i] == '.' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(p[i])
	}
	return append(out, cur)
}

func nestedPath(obj map[string]any, path string) (any, bool) {
	segs := splitDotPath(path)
	var cur any = obj
	for _, seg := range segs {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func setNestedPath(obj map[string]any, path string, val any) {
	segs := splitDotPath(path)
	cur := obj
	for _, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
	cur[segs[len(segs)-1]] = val
}

func removeNestedPath(obj map[string]any, path string) bool {
	segs := splitDotPath(path)
	cur := obj
	for _, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			return false
		}
		cur = next
	}
	last := segs[len(segs)-1]
	if _, ok := cur[last]; !ok {
		return false
	}
	delete(cur, last)
	return true
}

// echoSnapshot 是值级保真断言的快照（顶层键 → canonical JSON；目标键除外）。
type echoSnapshot map[string]json.RawMessage

func echoTopLevel(obj map[string]any, excludePath string) echoSnapshot {
	exclude := splitDotPath(excludePath)[0]
	out := echoSnapshot{}
	for k, v := range obj {
		if k == exclude {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			out[k] = nil
			continue
		}
		out[k] = b
	}
	return out
}

// equalWithin 比较两份快照（目标键所在顶层键已排除——V1 首建时 mcp 键
// 整体新增属预期，仅断言其余顶层键逐一 canonical 相等）。
func (e echoSnapshot) equalWithin(other echoSnapshot) bool {
	if len(e) != len(other) {
		return false
	}
	for k, v := range e {
		ov, ok := other[k]
		if !ok || !equalJSONValue(v, ov) {
			return false
		}
	}
	return true
}
