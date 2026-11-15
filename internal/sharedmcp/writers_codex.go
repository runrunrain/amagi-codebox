package sharedmcp

// writers_codex.go — Codex 写入器（spec §2.2）：落点 ~/.codex/config.toml
// 的 [mcp_servers.<name>] 段。
//
// 流程：platform.CodexHomeDir(env) 解析（CODEX_HOME 优先）→ 读 config.toml
//（缺失 → 空文档新建，MkdirAll .codex）→ parseTOMLManaged(data,
// ["mcp_servers"])（失败 →「Codex 配置的 mcp_servers 段含无法解析的形态，
// 已拒绝改动」不动文件）→ UpsertSection/RemoveSection → 写后重解析断言
//（段存在且值一致 / 段不存在）→ 原子私写 0600。
//
// 条目映射（spec §2.2 Codex 节裁定）：
//   - stdio → command（string）+ args（数组）+ env 字面值写
//     [mcp_servers.<name>.env] 子表（嵌套 map → 子表段能力现成）；
//     **不写 env_vars**（实测其语义为 shell 环境变量名透传清单，与本功能
//     「字面值分发」不同义）；
//   - http → 仅当 headers 为空才 Supports=true（写 url）；含鉴权头 →
//     unsupported（Codex 远程鉴权仅 OAuth/bearer_token_env_var，不把登记
//     密钥散布到 shell 环境变量）；
//   - description 丢弃（Codex 段无此字段文档佐证）。
//
// 保真边界：手写段（如本机 node_repl，含 env_vars/startup_timeout_sec）
// 不在登记 → Upsert/Remove 的 key 不含它，逐字节不动；注释/空行/行尾风格
// 由 TOML 引擎保证。

import (
	"encoding/json"
	"os"
	"path/filepath"

	"amagi-codebox/internal/platform"
)

// codexWriter 是 Codex CLI 的 MCP 配置写入器。
type codexWriter struct {
	env      []string
	userHome string // CodexHomeDir 失败时的最终回退用
}

// ID 返回引擎标识。
func (w *codexWriter) ID() string { return engineIDCodex }

// TargetPath 返回目标文件路径（展示用）。
func (w *codexWriter) TargetPath() string { return w.path() }

// path 解析 config.toml 路径：platform.CodexHomeDir(env)（CODEX_HOME 优先，
// HOME/USERPROFILE 回退）。
func (w *codexWriter) path() string {
	home, err := platform.CodexHomeDir(w.env)
	if err != nil || home == "" {
		home = w.userHome
	}
	return filepath.Join(home, "config.toml")
}

// Supports：stdio 恒可；http 仅当 headers 为空（url 可达即写 url，鉴权
// 头无表达）；未知 type 不可。
func (w *codexWriter) Supports(entry SharedMcpEntry) (bool, string) {
	switch entry.Type {
	case "stdio":
		return true, ""
	case "http":
		if len(entry.Headers) > 0 {
			return false, "Codex 远程 MCP 鉴权仅支持 OAuth/bearer_token_env_var，无法表达登记文件中的自定义 headers（如 Authorization 头）"
		}
		return true, ""
	}
	return false, "条目形态未知（type 不是 stdio/http），无法分发"
}

// sectionKey 返回 mcp_servers.<name> 段键（name 已过 validName，无点）。
func sectionKey(name string) string { return "mcp_servers." + name }

// mappedSection 构造条目的 TOML 段值（map 形态；env 嵌套 map → 子表段）。
func mappedSection(entry SharedMcpEntry) map[string]any {
	sec := map[string]any{}
	switch entry.Type {
	case "stdio":
		sec["command"] = entry.Command
		if len(entry.Args) > 0 {
			args := make([]any, 0, len(entry.Args))
			for _, a := range entry.Args {
				args = append(args, a)
			}
			sec["args"] = args
		}
		if len(entry.Env) > 0 {
			env := make(map[string]any, len(entry.Env))
			for k, v := range entry.Env {
				env[k] = v
			}
			sec["env"] = env
		}
	case "http":
		sec["url"] = entry.URL
	}
	return sec
}

// Mapped 返回期望段值的 canonical JSON（与 SectionValue 重建的 JSON 同域
// 比较，值语义）。
func (w *codexWriter) Mapped(entry SharedMcpEntry) (json.RawMessage, error) {
	return json.Marshal(mappedSection(entry))
}

// readDoc 读取并解析 TOML 受管作用域；文件缺失 → 空文档。解析失败 →
// ErrTargetCorrupt（不动文件）。
func (w *codexWriter) readDoc() (*tomlManagedDoc, []byte, error) {
	data, err := os.ReadFile(w.path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, NewErr(KindIO, "读取 Codex 配置失败：%s（%v）", w.path(), err)
	}
	doc, _, perr := parseTOMLManaged(data, []string{"mcp_servers"})
	if perr != nil {
		return nil, nil, NewErr(KindTargetCorrupt, "Codex 配置的 mcp_servers 段含无法解析的形态，已拒绝改动：%s（%v）", w.path(), perr)
	}
	return doc, data, nil
}

// Apply 写入/覆盖条目段，返回期望段值的 canonical JSON。
func (w *codexWriter) Apply(entry SharedMcpEntry) (json.RawMessage, error) {
	want, err := w.Mapped(entry)
	if err != nil {
		return nil, NewErr(KindIO, "条目 %s 序列化失败（%v）", entry.Name, err)
	}
	doc, _, err := w.readDoc()
	if err != nil {
		return nil, err
	}
	if doc == nil {
		// 文件缺失 → 空文档新建（MkdirAll 在写前执行）。
		doc, _, _ = parseTOMLManaged([]byte(""), []string{"mcp_servers"})
	}
	key := sectionKey(entry.Name)
	if uerr := doc.UpsertSection(key, mappedSection(entry)); uerr != nil {
		return nil, NewErr(KindIO, "Codex 段序列化失败（%v）", uerr)
	}
	candidate := doc.Bytes()
	// 写后重解析断言（verifyTOMLCandidate 同款）：段存在且值一致。
	vdoc, _, verr := parseTOMLManaged(candidate, []string{"mcp_servers"})
	if verr != nil {
		return nil, NewErr(KindIO, "Codex 配置写入候选重解析失败，已拒绝写入（%v）", verr)
	}
	got, ok := vdoc.SectionValue(key)
	if !ok || !equalJSONValue(mustJSON(got), want) {
		return nil, NewErr(KindIO, "Codex 配置写入候选值与期望不符，已拒绝写入")
	}
	if err := os.MkdirAll(filepath.Dir(w.path()), 0o700); err != nil {
		return nil, NewErr(KindIO, "创建 Codex 配置目录失败：%s（%v）", filepath.Dir(w.path()), err)
	}
	if err := writeAtomicPrivate(w.path(), candidate); err != nil {
		return nil, err
	}
	return want, nil
}

// Remove 删除条目段；不存在 → 幂等成功。手写段不在登记 key 内，逐字节不动。
func (w *codexWriter) Remove(name string) error {
	doc, _, err := w.readDoc()
	if err != nil {
		return err
	}
	if doc == nil || !doc.Has(sectionKey(name)) {
		return nil // 幂等
	}
	doc.RemoveSection(sectionKey(name))
	candidate := doc.Bytes()
	// 写后重解析断言：段不存在。
	vdoc, _, verr := parseTOMLManaged(candidate, []string{"mcp_servers"})
	if verr != nil {
		return NewErr(KindIO, "Codex 配置删除候选重解析失败，已拒绝写入（%v）", verr)
	}
	if vdoc.Has(sectionKey(name)) {
		return NewErr(KindIO, "Codex 配置删除候选仍含段 %s，已拒绝写入", sectionKey(name))
	}
	if err := os.MkdirAll(filepath.Dir(w.path()), 0o700); err != nil {
		return NewErr(KindIO, "创建 Codex 配置目录失败：%s（%v）", filepath.Dir(w.path()), err)
	}
	return writeAtomicPrivate(w.path(), candidate)
}

// Has 报告 mcp_servers.<name> 段是否存在。
func (w *codexWriter) Has(name string) (bool, error) {
	doc, _, err := w.readDoc()
	if err != nil || doc == nil {
		return false, err
	}
	return doc.Has(sectionKey(name)), nil
}

// Read 把段当前值重建为 canonical JSON（段缺失 → nil,nil）。
func (w *codexWriter) Read(name string) (json.RawMessage, error) {
	doc, _, err := w.readDoc()
	if err != nil || doc == nil {
		return nil, err
	}
	val, ok := doc.SectionValue(sectionKey(name))
	if !ok {
		return nil, nil
	}
	return mustJSON(val), nil
}

// mustJSON 把 map 序列化为 JSON（已解析值域，失败属编程错误 → 空对象）。
func mustJSON(v map[string]any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}
