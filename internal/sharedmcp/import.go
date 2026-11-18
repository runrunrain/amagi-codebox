package sharedmcp

// import.go — 共用 MCP「粘贴 JSON / 选择 JSON 文件」导入纵切：纯解析
// （格式兼容 sharedext.ParseMCPJSON 的三格式语义，但不复用其实现——它对
// 名称非法/形态不明的条目在预览阶段直接丢弃，与「严禁无声丢弃」冲突）→
// 预览（只含名称/类型/状态，绝不回传配置值）→ 显式提交后批量登记到
// ~/.agents/.mcp.json（条目 def 原样写入——未知配置值零丢弃；同名跳过并
// 逐条报告；默认无分发：不写状态库、不写任何引擎目标文件）。

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// MaxImportBytes 是导入内容（粘贴文本与所选文件）的统一大小上限。
const MaxImportBytes = 256 * 1024

// 导入预览条目状态（ImportPreviewEntry.Status 值域）。
const (
	ImportStatusOk      = "ok"      // 可导入
	ImportStatusExists  = "exists"  // 同名冲突（登记已有或本批重复），提交时跳过
	ImportStatusInvalid = "invalid" // 名称/形态无法登记，不导入（Note 含原因）
)

// ImportPreviewEntry 是导入预览的一行：只含名称/归一形态/状态与附注，
// 绝不含配置值（command/url/env/headers 等一律不回传，spec 预览纪律）。
type ImportPreviewEntry struct {
	Name   string `json:"name"`
	Type   string `json:"type"` // 归一形态 "stdio"|"http"；无法归一时为原始 type 值或空
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// ImportPreview 是解析预览结果（纯解析 + 与现行登记的同名比对，不落盘）。
type ImportPreview struct {
	Entries []ImportPreviewEntry `json:"entries"`
}

// ImportSkipped 是批量提交时被跳过条目的逐条报告（部分冲突报告）。
type ImportSkipped struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// ImportOutcome 是批量导入的结果报告：成功登记名单 + 跳过清单（同名冲突
// 与无法登记的条目均如实逐条列出，绝不无声丢弃）。
type ImportOutcome struct {
	Imported []string        `json:"imported"`
	Skipped  []ImportSkipped `json:"skipped"`
}

// importCandidate 是解析出的一个候选条目。Raw 是 def 的 canonical JSON
// （全部键原样保留，仅必要时归一 type 键）；InvalidReason 非空表示该条目
// 无法登记（预览标 invalid、提交进 skipped，均显式报告）。
type importCandidate struct {
	Name          string
	Type          string // 归一形态（""=无法归一）
	RawType       string // def 原始 type 字符串值（缺失/非字符串为 ""）
	Raw           []byte
	Note          string // 可导入条目的附注（如 sse 归一说明）
	InvalidReason string
}

// displayType 返回预览展示形态：归一结果优先，无法归一时回退原始 type 值。
func (c importCandidate) displayType() string {
	return c.Type
}

// importFormatHint 是解析失败时说明支持格式的固定文案。
const importFormatHint = "支持三种格式：1) {\"mcpServers\": {\"名称\": {\"command\"|\"url\": ...}}}（宽容 opencode 的 \"mcp\" 包裹键）；2) 裸对象 {\"名称\": {\"command\"|\"url\": ...}}；3) 单条粘贴（对象或数组，数组元素递归按前两种格式解释）"

// ParseImportText 纯解析导入文本为候选条目（按名称排序，不落盘、不读登记
// 文件）。整体不可解析（空/超限/非法 JSON/未识别出任何 server）→ 错误。
func ParseImportText(text string) ([]importCandidate, error) {
	if len(text) > MaxImportBytes {
		return nil, NewErr(KindInvalidEntry, "导入内容超过大小上限 %d KB（当前约 %d KB）", MaxImportBytes/1024, (len(text)+1023)/1024)
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, NewErr(KindInvalidEntry, "导入内容为空；%s", importFormatHint)
	}
	var root any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if !json.Valid([]byte(trimmed)) {
		return nil, NewErr(KindInvalidEntry, "不是合法 JSON；%s", importFormatHint)
	}
	if err := checkImportJSON(decoder, 0); err != nil {
		return nil, err
	}
	decoder = json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber() // preserve arbitrary JSON numbers in unknown fields
	if err := decoder.Decode(&root); err != nil {
		return nil, NewErr(KindInvalidEntry, "不是合法 JSON；%s", importFormatHint)
	}
	candidates := parseImportValue(root)
	if len(candidates) == 0 {
		return nil, NewErr(KindInvalidEntry, "未能从内容中识别出任何 MCP server；%s", importFormatHint)
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
	return candidates, nil
}

// checkImportJSON visits each object before decoding it into a map. Without this,
// a duplicated server or configuration key silently replaces its first value.
func checkImportJSON(decoder *json.Decoder, depth int) error {
	if depth > 100 {
		return NewErr(KindInvalidEntry, "导入 JSON 嵌套过深")
	}
	token, err := decoder.Token()
	if err != nil {
		return NewErr(KindInvalidEntry, "不是合法 JSON")
	}
	switch token {
	case json.Delim('{'):
		seen := map[string]struct{}{}
		for decoder.More() {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok {
				return NewErr(KindInvalidEntry, "不是合法 JSON")
			}
			if _, exists := seen[name]; exists {
				return NewErr(KindInvalidEntry, "导入 JSON 包含重复键，请移除重复定义后重试")
			}
			seen[name] = struct{}{}
			if err := checkImportJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	case json.Delim('['):
		for decoder.More() {
			if err := checkImportJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	if _, err := decoder.Token(); err != nil {
		return NewErr(KindInvalidEntry, "不是合法 JSON")
	}
	return nil
}

// parseImportValue 递归解释已解析的 JSON 值（数组元素递归；对象按包裹/裸
// 对象两种格式解释——语义对齐 sharedext.parseMCPValue）。
func parseImportValue(v any) []importCandidate {
	switch t := v.(type) {
	case []any:
		var out []importCandidate
		for _, item := range t {
			out = append(out, parseImportValue(item)...)
		}
		return out
	case map[string]any:
		return parseImportObject(t)
	}
	return nil
}

// parseImportObject 解释一个 JSON 对象：优先识别 mcpServers（宽容 opencode
// 的 "mcp"）包裹键——包裹内所有条目逐名产出（无法登记的形态显式标 invalid，
// 绝不无声丢弃）；否则按裸对象格式（进入条件与 sharedext 相同：至少一个
// def 是含 command 或 url 的对象，防止把任意 JSON 误当 server 列表；进入
// 后其余条目同样显式报告）。
func parseImportObject(obj map[string]any) []importCandidate {
	for _, wrapper := range []string{"mcpServers", "mcp"} {
		if inner, ok := obj[wrapper].(map[string]any); ok && len(inner) > 0 {
			out := make([]importCandidate, 0, len(inner))
			for name, def := range inner {
				out = append(out, makeImportCandidate(name, def))
			}
			return out
		}
	}
	recognized := false
	for _, def := range obj {
		if d, ok := def.(map[string]any); ok {
			if _, hasCommand := d["command"]; hasCommand {
				recognized = true
			}
			if _, hasURL := d["url"]; hasURL {
				recognized = true
			}
		}
	}
	if !recognized {
		return nil
	}
	out := make([]importCandidate, 0, len(obj))
	for name, def := range obj {
		out = append(out, makeImportCandidate(name, def))
	}
	return out
}

// makeImportCandidate 解释单个 name→def：形态归一（显式 stdio/http 优先；
// sse/streamable-http 视作 http；缺失时按 command/url 推断——登记条目需要
// 标准 type 才能进入既有校验与分发矩阵）、名称/字段校验（与手工 Create 同
// 一 validateEntry 语义），并生成「def 全部键原样保留」的 canonical Raw
// （仅归一 type 键，未知配置值零丢弃）。
func makeImportCandidate(name string, def any) importCandidate {
	c := importCandidate{Name: name}
	d, ok := def.(map[string]any)
	if !ok {
		c.InvalidReason = "条目定义不是 JSON 对象，无法导入"
		return c
	}
	if tv, exists := d["type"]; exists {
		s, isStr := tv.(string)
		if !isStr {
			c.InvalidReason = "type 字段必须是字符串（\"stdio\" 或 \"http\"）"
			return c
		}
		c.RawType = s
	}
	command, _ := d["command"].(string)
	entryURL, _ := d["url"].(string)
	_, hasCommand := d["command"]
	_, hasURL := d["url"]

	normalized := ""
	note := ""
	switch {
	case c.RawType == "stdio" || c.RawType == "http":
		normalized = c.RawType
	case c.RawType == "sse" || c.RawType == "streamable-http" || c.RawType == "streamableHttp":
		normalized = "http"
		note = fmt.Sprintf("type %q 将按远程（http）形态登记", c.RawType)
	case c.RawType == "" && hasCommand:
		normalized = "stdio"
	case c.RawType == "" && hasURL:
		normalized = "http"
	default:
		c.InvalidReason = "无法判定服务器形态（仅支持 stdio/http 或可推断形态的 command/url）"
		return c
	}
	c.Type = normalized
	c.Note = note

	if !validName(name) {
		c.InvalidReason = nameInvalidReason
		return c
	}
	entry := SharedMcpEntry{Name: name, Type: normalized, Command: command, URL: entryURL}
	if args, ok := d["args"].([]any); ok {
		for _, item := range args {
			if s, ok := item.(string); ok {
				entry.Args = append(entry.Args, s)
			}
		}
	}
	if env, ok := d["env"].(map[string]any); ok {
		for k, v := range env {
			if strings.TrimSpace(k) == "" {
				c.InvalidReason = "env 的键名不能为空"
				return c
			}
			if s, ok := v.(string); ok {
				if entry.Env == nil {
					entry.Env = map[string]string{}
				}
				entry.Env[k] = s
			}
		}
	}
	if headers, ok := d["headers"].(map[string]any); ok {
		for k, v := range headers {
			if strings.TrimSpace(k) == "" {
				c.InvalidReason = "headers 的键名不能为空"
				return c
			}
			if s, ok := v.(string); ok {
				if entry.Headers == nil {
					entry.Headers = map[string]string{}
				}
				entry.Headers[k] = s
			}
		}
	}
	if err := validateEntry(entry); err != nil {
		c.InvalidReason = err.Error()
		return c
	}

	raw, err := json.Marshal(d)
	if err != nil {
		c.InvalidReason = fmt.Sprintf("条目序列化失败：%v", err)
		return c
	}
	if c.RawType != normalized {
		if raw, err = sjson.SetBytes(raw, "type", normalized); err != nil {
			c.InvalidReason = fmt.Sprintf("条目 type 归一失败：%v", err)
			return c
		}
	}
	c.Raw = raw
	return c
}

// BuildImportPreview 纯解析 + 与现行登记比对，产出预览（不落盘）。预览行
// 只含名称/类型/状态/附注，不含任何配置值；同名（登记已有或本批重复）标
// exists，无法登记标 invalid 并给原因。
func (s *Service) BuildImportPreview(text string) (ImportPreview, error) {
	candidates, err := ParseImportText(text)
	if err != nil {
		return ImportPreview{}, err
	}
	entries, _, lerr := s.List()
	if lerr != nil {
		return ImportPreview{}, lerr
	}
	existing := make(map[string]bool, len(entries))
	for _, e := range entries {
		existing[e.Name] = true
	}
	seen := map[string]bool{}
	out := ImportPreview{Entries: make([]ImportPreviewEntry, 0, len(candidates))}
	for _, c := range candidates {
		row := ImportPreviewEntry{Name: c.Name, Type: c.displayType()}
		switch {
		case c.InvalidReason != "":
			row.Status = ImportStatusInvalid
			row.Note = c.InvalidReason
		case seen[c.Name]:
			row.Status = ImportStatusExists
			row.Note = "本批内容中同名重复，仅首个会被导入"
		case existing[c.Name]:
			row.Status = ImportStatusExists
			row.Note = "登记文件已有同名条目，提交时将跳过（不覆盖既有条目）"
		default:
			row.Status = ImportStatusOk
			row.Note = c.Note
		}
		if c.InvalidReason == "" {
			seen[c.Name] = true
		}
		out.Entries = append(out.Entries, row)
	}
	return out, nil
}

// Import 显式提交：把解析出的可登记条目逐条写入 ~/.agents/.mcp.json（原样
// def，未知配置值零丢弃；复用 applyRegistryWrite 的写前/写后校验与原子
// 私写）。同名条目（登记已有或本批已写）跳过并逐条报告；无法登记的条目
// 进 skipped 报告；单条写入失败如实记入 skipped 后继续（部分成功语义）。
// 默认无分发：不写状态库、不写任何引擎目标文件——分发仍由用户在矩阵显式
// 勾选（SetSelected）。整体不可解析或登记文件损坏 → 错误（不做部分写入）。
func (s *Service) Import(text string) (ImportOutcome, error) {
	candidates, err := ParseImportText(text)
	if err != nil {
		return ImportOutcome{}, err
	}
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	data, exists, rerr := s.readRegistryStart()
	if rerr != nil {
		return ImportOutcome{}, rerr
	}
	var outcome ImportOutcome
	seen := map[string]bool{}
	for _, c := range candidates {
		if c.InvalidReason != "" {
			outcome.Skipped = append(outcome.Skipped, ImportSkipped{Name: c.Name, Reason: c.InvalidReason})
			continue
		}
		if seen[c.Name] {
			outcome.Skipped = append(outcome.Skipped, ImportSkipped{Name: c.Name, Reason: "本批内容中同名重复，已跳过（仅导入首个）"})
			continue
		}
		if exists && gjson.GetBytes(data, entryPath(c.Name)).Exists() {
			outcome.Skipped = append(outcome.Skipped, ImportSkipped{Name: c.Name, Reason: "登记文件已有同名条目，已跳过（不覆盖既有条目）"})
			continue
		}
		raw := c.Raw
		next, werr := s.applyRegistryWrite(data, c.Name, func() ([]byte, error) { return raw, nil })
		if werr != nil {
			outcome.Skipped = append(outcome.Skipped, ImportSkipped{Name: c.Name, Reason: werr.Error()})
			continue
		}
		data = next
		exists = true
		seen[c.Name] = true
		outcome.Imported = append(outcome.Imported, c.Name)
	}
	if outcome.Imported == nil {
		outcome.Imported = []string{}
	}
	if outcome.Skipped == nil {
		outcome.Skipped = []ImportSkipped{}
	}
	if s.log != nil && len(outcome.Imported) > 0 {
		s.log.Info("sharedmcp", "导入登记条目", fmt.Sprintf("成功 %d 个，跳过 %d 个", len(outcome.Imported), len(outcome.Skipped)), "path="+s.registryPath())
	}
	return outcome, nil
}
