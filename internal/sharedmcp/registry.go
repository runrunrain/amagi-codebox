// Package sharedmcp 管理共用 MCP 登记文件（~/.agents/.mcp.json）的条目
// CRUD，并把登记条目分发到四个 Agent 引擎的真实 MCP 配置（Claude /
// Codex / OpenCode / Pi；omp 无 MCP 配置面，静态 excluded）。
//
// 托管安全（spec 裁定一）：目标配置文件中的条目名保持原名（不加前缀），
// 「只动自己写的」由 configDir 侧 shared-mcp-state.json 登记跟踪实现；
// 选择状态绝不写 ~/.agents 共享文件（spec 裁定三）。
package sharedmcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// SharedMcpEntry 是 ~/.agents/.mcp.json 中一个 server 条目的应用视图
// （spec §1.1）。值域来自本机实测：type ∈ {"stdio","http"}。
type SharedMcpEntry struct {
	Name        string            `json:"name"`              // 条目名（标识；字符集见 validName）
	Type        string            `json:"type"`              // "stdio" | "http"
	Command     string            `json:"command,omitempty"` // stdio 必填
	Args        []string          `json:"args,omitempty"`    // stdio
	Env         map[string]string `json:"env,omitempty"`     // stdio；字面值（明文存于登记文件，UI 脱敏展示）
	URL         string            `json:"url,omitempty"`     // http 必填
	Headers     map[string]string `json:"headers,omitempty"` // http；值同上
	Description string            `json:"description,omitempty"`
	// UnknownKeys 是信息性字段：登记文件中该条目所含、已知字段集之外的键名
	// 清单（只读展示「该条目含应用未识别的键 N 个」）。值不进 DTO——未知键
	// 的值由 CRUD 层在写入时从登记文件原字节合并保留，前端不回传也不参与
	// 持久化构造（禁止直接 json.Marshal 整个 DTO，否则本字段会落盘）。
	UnknownKeys []string `json:"unknownKeys,omitempty"`
}

// knownEntryKeys 是登记条目内应用可识别的字段集（spec §1.1 持久化纪律）。
var knownEntryKeys = map[string]bool{
	"type":        true,
	"command":     true,
	"args":        true,
	"env":         true,
	"url":         true,
	"headers":     true,
	"description": true,
}

// validName 校验条目名（spec §1.4 裁定）：^[A-Za-z0-9_-]+$。
// 「.」会破坏 Codex 段键 [mcp_servers.<name>]（按点分段），路径分隔符
// 有穿越风险，故一并拒绝。
func validName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// nameInvalidReason 是分发拒绝时给出的可读原因（§1.4）。
const nameInvalidReason = "条目名仅允许字母、数字、- 与 _（当前名字含分发不兼容字符，如「.」或路径分隔符）"

// validateEntry 校验条目字段（spec §1.1）：stdio → Command 非空；
// http → URL 非空且可 url.Parse 出 host；Type 非 {stdio,http} → 拒绝
// （既有条目的未知 type 只读展示 + 分发 unsupported）。
func validateEntry(entry SharedMcpEntry) error {
	switch entry.Type {
	case "stdio":
		if strings.TrimSpace(entry.Command) == "" {
			return NewErr(KindInvalidEntry, "stdio 形态的服务器必须填写 command（启动命令）")
		}
	case "http":
		host, perr := parseEntryURL(entry.URL)
		if perr != nil || host == "" {
			return NewErr(KindInvalidEntry, "http 形态的服务器必须填写合法 URL（需包含主机名），当前为 %q", entry.URL)
		}
	default:
		return NewErr(KindInvalidEntry, "服务器类型必须是 stdio 或 http，当前为 %q", entry.Type)
	}
	for k := range entry.Env {
		if strings.TrimSpace(k) == "" {
			return NewErr(KindInvalidEntry, "env 的键名不能为空")
		}
	}
	for k := range entry.Headers {
		if strings.TrimSpace(k) == "" {
			return NewErr(KindInvalidEntry, "headers 的键名不能为空")
		}
	}
	return nil
}

// registryPath 返回登记文件绝对路径 <userHome>/.agents/.mcp.json。
func (s *Service) registryPath() string {
	return filepath.Join(s.userHome, ".agents", ".mcp.json")
}

// readRegistryStart 读取登记文件并裁定写入起点（spec §2.1 步骤 2）：
//   - 文件缺失 → (nil, false, nil)：起点为空文档 {}，由调用方新建；
//   - 文件存在但 json.Valid 失败或根非对象 → ErrRegistryCorrupt，绝不覆写；
//   - 文件存在且合法 → (原字节, true, nil)。
func (s *Service) readRegistryStart() (data []byte, exists bool, err error) {
	path := s.registryPath()
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		if errors.Is(rerr, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, NewErr(KindIO, "读取登记文件失败：%s（%v）", path, rerr)
	}
	if !gjson.ValidBytes(raw) {
		return nil, false, NewErr(KindRegistryCorrupt, "登记文件不是合法 JSON 对象，已拒绝读写（请先手动修复）：%s", path)
	}
	if !gjson.ParseBytes(raw).IsObject() {
		return nil, false, NewErr(KindRegistryCorrupt, "登记文件不是合法 JSON 对象，已拒绝读写（请先手动修复）：%s", path)
	}
	return raw, true, nil
}

// parseEntry 从登记条目对象解析出 DTO。非对象值按 UnknownKeys 形态只读
// 呈现（spec §2.1 List）；已知键下不可表达的值域（如 env 中非字符串值）
// 尽力取字符串形态，Update 以 DTO 序列化为准（已知字段集权威）。
func parseEntry(name string, v gjson.Result) SharedMcpEntry {
	e := SharedMcpEntry{Name: name}
	if !v.IsObject() {
		e.UnknownKeys = []string{"(值不是 JSON 对象，字段无法解析)"}
		return e
	}
	v.ForEach(func(k, val gjson.Result) bool {
		switch k.String() {
		case "type":
			e.Type = val.String()
		case "command":
			e.Command = val.String()
		case "url":
			e.URL = val.String()
		case "description":
			e.Description = val.String()
		case "args":
			if val.IsArray() {
				for _, item := range val.Array() {
					if item.Type == gjson.String {
						e.Args = append(e.Args, item.String())
					}
				}
			}
		case "env":
			if val.IsObject() {
				e.Env = stringMapOf(val)
			}
		case "headers":
			if val.IsObject() {
				e.Headers = stringMapOf(val)
			}
		default:
			e.UnknownKeys = append(e.UnknownKeys, k.String())
		}
		return true
	})
	sort.Strings(e.UnknownKeys)
	return e
}

// stringMapOf 把 JSON 对象收敛为 map[string]string（仅取字符串值成员）。
func stringMapOf(v gjson.Result) map[string]string {
	out := make(map[string]string, len(v.Map()))
	v.ForEach(func(k, val gjson.Result) bool {
		if val.Type == gjson.String {
			out[k.String()] = val.String()
		}
		return true
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

// entryPath 构造 mcpServers.<name> 的 gjson/sjson 路径，转义路径特殊字符
// （\、.、"）——既有不合规名字（含点等）的 Delete/读取也必须精准命中
// （spec §1.4：不合规名字 Update/Delete 可用）。
func entryPath(name string) string {
	var b strings.Builder
	b.WriteString("mcpServers.")
	for i := 0; i < len(name); i++ {
		switch name[i] {
		case '\\', '.', '"':
			b.WriteByte('\\')
		}
		b.WriteByte(name[i])
	}
	return b.String()
}

// List 枚举登记条目（spec §2.1）：文件缺失 → 空列表 + exists=false，不报错
// 不创建；mcpServers 缺失 → 空列表 + exists=true；按名称排序。
func (s *Service) List() ([]SharedMcpEntry, bool, error) {
	data, exists, err := s.readRegistryStart()
	if err != nil {
		return nil, false, err
	}
	out := []SharedMcpEntry{}
	if !exists {
		return out, false, nil
	}
	servers := gjson.GetBytes(data, "mcpServers")
	if servers.Exists() && servers.IsObject() {
		servers.ForEach(func(k, v gjson.Result) bool {
			out = append(out, parseEntry(k.String(), v))
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, true, nil
}

// rawKV 是「未知键名 → 原样 raw 值」对（gjson.Result 无 Key 字段、Raw 为
// string，故用轻量结构传递）。
type rawKV struct {
	Key string
	Raw string
}

// buildEntryRaw 构造条目的紧凑 JSON 对象（spec §2.1 步骤 3）：已知字段
// （除 Name 外）显式序列化，固定键序（type/command/args/env/url/headers/
// description）；unknowns 是从当前文件原条目取出的未知键 raw 值（按原序）
// 原样合并保留。绝不 json.Marshal 整个 DTO（UnknownKeys 不落盘）。
func buildEntryRaw(entry SharedMcpEntry, unknowns []rawKV) ([]byte, error) {
	var buf bytes.Buffer
	add := func(key string, val any) error {
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(key)
		if err != nil {
			return err
		}
		vb, err := json.Marshal(val)
		if err != nil {
			return err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
		return nil
	}
	buf.WriteByte('{')
	if err := add("type", entry.Type); err != nil {
		return nil, err
	}
	if entry.Command != "" {
		if err := add("command", entry.Command); err != nil {
			return nil, err
		}
	}
	if len(entry.Args) > 0 {
		if err := add("args", entry.Args); err != nil {
			return nil, err
		}
	}
	if len(entry.Env) > 0 {
		if err := add("env", entry.Env); err != nil {
			return nil, err
		}
	}
	if entry.URL != "" {
		if err := add("url", entry.URL); err != nil {
			return nil, err
		}
	}
	if len(entry.Headers) > 0 {
		if err := add("headers", entry.Headers); err != nil {
			return nil, err
		}
	}
	if entry.Description != "" {
		if err := add("description", entry.Description); err != nil {
			return nil, err
		}
	}
	for _, u := range unknowns {
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(u.Key)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.WriteString(u.Raw)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// projectionWithoutEntry 返回「移除目标条目后的投影」（spec §2.1 步骤 5
// 保真断言语义，测试与实现共用）：CRLF、缩进、其它条目、未知顶层键的
// 逐字节保留经此投影比较证明（目标条目自身的形态不在保真范围内）。
func projectionWithoutEntry(data []byte, name string) []byte {
	out, err := sjson.DeleteBytes(data, entryPath(name))
	if err != nil {
		return append([]byte(nil), data...)
	}
	return out
}

// verifyRegistryCandidate 写前校验候选字节（spec §2.1 步骤 5）：
// json.Valid；回读断言目标条目在（wantPresent）；顶层键集断言（新增仅
// 允许 mcpServers 一个键）；投影字节相等（仅当写前已存在 mcpServers 键
// ——键/文件新建路径投影语义不适用，以键集断言替代）。
func verifyRegistryCandidate(before, after []byte, name string, wantPresent bool, beforeHadServersKey bool) error {
	if !gjson.ValidBytes(after) {
		return NewErr(KindIO, "登记文件写入候选不是合法 JSON，已拒绝写入")
	}
	got := gjson.GetBytes(after, entryPath(name))
	if wantPresent && !got.Exists() {
		return NewErr(KindIO, "登记文件写入校验失败：条目 %s 未出现在候选中", name)
	}
	if !wantPresent && got.Exists() {
		return NewErr(KindIO, "登记文件写入校验失败：条目 %s 仍存在于候选中", name)
	}
	bk, ak := topKeyNames(before), topKeyNames(after)
	for _, k := range ak {
		if k == "mcpServers" {
			continue
		}
		if !containsString(bk, k) {
			return NewErr(KindIO, "登记文件写入校验失败：顶层键 %s 意外新增", k)
		}
	}
	if beforeHadServersKey {
		if !bytes.Equal(projectionWithoutEntry(before, name), projectionWithoutEntry(after, name)) {
			return NewErr(KindIO, "登记文件写入校验失败：目标条目之外的字节发生了变化")
		}
	}
	return nil
}

// topKeyNames 列出 JSON 对象的顶层键名（排序稳定，claudeaccounts 先例）。
func topKeyNames(data []byte) []string {
	root := gjson.ParseBytes(data)
	if !root.IsObject() {
		return nil
	}
	names := make([]string, 0, len(root.Map()))
	for name := range root.Map() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// sameStringSet 报告两个有序键名切片是否为同一集合（claudeaccounts 先例）。
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Create 在登记文件新增条目（spec §2.1）。同名已存在 → 拒绝。
func (s *Service) Create(entry SharedMcpEntry) error {
	if err := validateEntry(entry); err != nil {
		return err
	}
	if !validName(entry.Name) {
		return NewErr(KindNameInvalid, "新增失败：%s", nameInvalidReason)
	}
	data, exists, err := s.readRegistryStart()
	if err != nil {
		return err
	}
	if exists && gjson.GetBytes(data, entryPath(entry.Name)).Exists() {
		return NewErr(KindExists, "条目 %s 已存在，请换一个名字或改用编辑", entry.Name)
	}
	next, err := s.applyRegistryWrite(data, entry.Name, func() ([]byte, error) {
		return buildEntryRaw(entry, nil) // Create 无未知键
	})
	if err != nil {
		return err
	}
	if s.log != nil {
		s.log.Info("sharedmcp", "登记文件新增条目", "name="+entry.Name, "type="+entry.Type, "path="+s.registryPath())
	}
	_ = next
	return nil
}

// Update 更新登记条目内容（name 是标识不可改，spec §2.1）。只改登记文件，
// 不自动重分发——已分发引擎随内容变化如实转 drift，由用户逐引擎 resync
// （spec §3）。未知键的 raw 值从当前文件原样合并保留。
func (s *Service) Update(name string, entry SharedMcpEntry) error {
	if entry.Name != "" && entry.Name != name {
		return NewErr(KindInvalidEntry, "条目名不可修改（标识为 %q，不接受 %q）", name, entry.Name)
	}
	if err := validateEntry(entry); err != nil {
		return err
	}
	if !validName(name) {
		return NewErr(KindNameInvalid, "更新失败：%s", nameInvalidReason)
	}
	data, exists, err := s.readRegistryStart()
	if err != nil {
		return err
	}
	cur := gjson.GetBytes(data, entryPath(name))
	if !exists || !cur.Exists() {
		return NewErr(KindNotFound, "条目 %s 不存在（可能已被删除，请刷新列表）", name)
	}
	var unknowns []rawKV
	if cur.IsObject() {
		cur.ForEach(func(k, v gjson.Result) bool {
			if !knownEntryKeys[k.String()] {
				unknowns = append(unknowns, rawKV{Key: k.String(), Raw: v.Raw})
			}
			return true
		})
	}
	if _, err := s.applyRegistryWrite(data, name, func() ([]byte, error) {
		return buildEntryRaw(entry, unknowns)
	}); err != nil {
		return err
	}
	if s.log != nil {
		s.log.Info("sharedmcp", "登记文件更新条目", "name="+name, "type="+entry.Type, "path="+s.registryPath())
	}
	return nil
}

// Delete 删除登记条目（spec §2.1）：sjson.DeleteBytes；mcpServers 清空后
// 保留空对象 {} 不删键（最小扰动，顶层键集恒不变）。与分发的联动（先逐
// 引擎还原）在 App 门面层编排（spec §3）。既有不合规名字同样可删。
func (s *Service) Delete(name string) error {
	data, exists, err := s.readRegistryStart()
	if err != nil {
		return err
	}
	if !exists || !gjson.GetBytes(data, entryPath(name)).Exists() {
		return NewErr(KindNotFound, "条目 %s 不存在（可能已被删除，请刷新列表）", name)
	}
	beforeHadServers := gjson.GetBytes(data, "mcpServers").Exists()
	next, derr := sjson.DeleteBytes(data, entryPath(name))
	if derr != nil {
		return NewErr(KindIO, "登记文件删除序列化失败：%s（%v）", name, derr)
	}
	if err := verifyRegistryCandidate(data, next, name, false, beforeHadServers); err != nil {
		return err
	}
	path := s.registryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return NewErr(KindIO, "创建登记目录失败：%s（%v）", filepath.Dir(path), err)
	}
	if err := writeAtomicPrivate(path, next); err != nil {
		return err
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil || gjson.GetBytes(after, entryPath(name)).Exists() {
		return NewErr(KindIO, "登记文件删除后回读校验失败：%s", path)
	}
	if s.log != nil {
		s.log.Info("sharedmcp", "登记文件删除条目", "name="+name, "path="+path)
	}
	return nil
}

// applyRegistryWrite 执行 Create/Update 共用的写入流水（spec §2.1 步骤
// 2b-6）：起点裁定（缺失 → 空文档 {} + MkdirAll ~/.agents；存在即原字节）
// → 构造紧凑条目 raw → sjson.SetRawBytes（顶层无 mcpServers 键时先建 {}）
// → 候选校验 → 原子私写（tmp 0600 + rename）→ 回读断言。
// 返回写入后的文件字节。
func (s *Service) applyRegistryWrite(data []byte, name string, buildRaw func() ([]byte, error)) ([]byte, error) {
	beforeHadServers := false
	if len(data) == 0 {
		data = []byte("{}")
	} else {
		beforeHadServers = gjson.GetBytes(data, "mcpServers").Exists()
	}
	raw, err := buildRaw()
	if err != nil {
		return nil, NewErr(KindIO, "条目 %s 序列化失败（%v）", name, err)
	}
	next := data
	if !beforeHadServers {
		if next, err = sjson.SetRawBytes(next, "mcpServers", []byte("{}")); err != nil {
			return nil, NewErr(KindIO, "登记文件初始化 mcpServers 键失败（%v）", err)
		}
	}
	next, err = sjson.SetRawBytes(next, entryPath(name), raw)
	if err != nil {
		return nil, NewErr(KindIO, "登记文件写入条目 %s 失败（%v）", name, err)
	}
	if err := verifyRegistryCandidate(data, next, name, true, beforeHadServers); err != nil {
		return nil, err
	}
	path := s.registryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, NewErr(KindIO, "创建登记目录失败：%s（%v）", filepath.Dir(path), err)
	}
	if err := writeAtomicPrivate(path, next); err != nil {
		return nil, err
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil {
		return nil, NewErr(KindIO, "登记文件写后回读失败：%s（%v）", path, rerr)
	}
	if !equalJSONValue([]byte(gjson.GetBytes(after, entryPath(name)).Raw), raw) {
		return nil, NewErr(KindIO, "登记文件写后回读与期望不符：%s", path)
	}
	return after, nil
}

// parseEntryURL 解析条目 URL（独立小函数便于测试）。
func parseEntryURL(raw string) (host string, err error) {
	u, err := parseURL(raw)
	if err != nil {
		return "", err
	}
	return u.Host, nil
}

// fmtEntry 用于日志的脱敏摘要（对齐 wslsupply.summarizeMCPServerEntry 纪律：
// 只含形态与数量，绝不含 env/headers 值）。
func fmtEntry(entry SharedMcpEntry) string {
	return fmt.Sprintf("name=%s type=%s args=%d envKeys=%d headerKeys=%d",
		entry.Name, entry.Type, len(entry.Args), len(entry.Env), len(entry.Headers))
}
