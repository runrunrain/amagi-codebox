package sharedext

// toml_mcp.go — Codex config.toml 的 MCP server TOML 保真编辑器。
//
// 思路复刻 internal/wslsupply/toml_edit.go（包私有不可 import）：行级定位
// 替换，只解析受管作用域 mcp_servers.* 的表头与作用域判定；段范围之外的
// 一切字节（非目标节、注释、空行、行尾风格 CRLF/LF）逐字节保留。编辑 =
// 整段替换/删除受管行区间、文末追加新段。受管作用域内出现不支持形态
//（数组表头、裸表、点引号段名、根级点键内联表、日期时间值）→ 返回错误
// 不修改原文件。

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	errTomlUnsupported = errors.New("受管 mcp_servers 作用域存在不支持的形态，拒绝编辑")
)

// tomlDoc 是 TOML 文档的行级编辑状态。
type tomlDoc struct {
	lines         []string // 含行尾符的原始行（未触碰行逐字节保留）
	crlf          bool     // 新增行采用的行尾风格（跟随原文主体）
	sectionRanges map[string][][2]int // 段名 → 行区间集合（按出现顺序）
	order         []string
	deleted       map[int]bool
}

// parseTOMLDoc 解析 data 中 mcp_servers 作用域的段结构。
// 只识别段边界（表头）与根级点键检测；段内键值不在此解析（整段替换/删除
// 不需要理解内容）。
func parseTOMLDoc(data []byte) (*tomlDoc, error) {
	text := string(data)
	doc := &tomlDoc{
		lines:         splitKeepEnds(text),
		crlf:          strings.Contains(text, "\r\n"),
		sectionRanges: map[string][][2]int{},
		deleted:       map[int]bool{},
	}
	if len(data) == 0 {
		return doc, nil
	}

	inRoot := true // 尚未进入任何表（根作用域）
	var curSection string // 当前正在累积的受管段（"" = 非受管上下文）
	var curEnd int        // 当前段范围的最新结束行（不含）
	curStart := 0         // 当前段范围的起始行

	flush := func(end int) {
		if curSection != "" {
			doc.addRange(curSection, curStart, end)
		}
		curSection = ""
	}

	for i := 0; i < len(doc.lines); i++ {
		line := strings.TrimSpace(stripLineEnd(doc.lines[i]))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			path, isArray, err := parseTOMLHeader(stripTOMLComment(line))
			if err != nil {
				// 畸形行（可能是多行值的续行）：不视为段边界，也不并入段范围
				//（防止带行尾注释的表头因解析失败被误吞）。
				continue
			}
			inRoot = false
			if len(path) > 0 && path[0] == "mcp_servers" {
				if isArray {
					return nil, fmt.Errorf("%w: 数组表头 %q", errTomlUnsupported, line)
				}
				if len(path) == 1 {
					return nil, fmt.Errorf("%w: 裸表 [mcp_servers]", errTomlUnsupported)
				}
				if strings.Contains(path[1], ".") {
					return nil, fmt.Errorf("%w: 点引号段名 %q", errTomlUnsupported, line)
				}
				key := "mcp_servers." + path[1]
				if key != curSection {
					flush(curEnd)
					curSection = key
					curStart = i
				}
				curEnd = i + 1
				continue
			}
			// 非受管表头：终止当前段范围。
			flush(curEnd)
			continue
		}
		if inRoot {
			// 根作用域键值行：检测 mcp_servers 点键/整键赋值形态。
			if eq := indexTOMLAssign(line); eq >= 0 {
				keyPart := strings.TrimSpace(line[:eq])
				if kp, kerr := splitTOMLKeyPath(keyPart); kerr == nil && len(kp) > 0 && kp[0] == "mcp_servers" {
					return nil, fmt.Errorf("%w: 根级键 %q", errTomlUnsupported, keyPart)
				}
			}
			continue
		}
		if curSection != "" {
			curEnd = i + 1
		}
	}
	flush(curEnd)

	// 段区间向后吸收段内尾随空行/注释（整体替换/删除时一并处理，
	// 避免残留孤立分隔；与 wslsupply 同款取舍）。
	for key, ranges := range doc.sectionRanges {
		for idx := range ranges {
			end := ranges[idx][1]
			for end < len(doc.lines) {
				t := strings.TrimSpace(stripLineEnd(doc.lines[end]))
				if t != "" && !strings.HasPrefix(t, "#") {
					break
				}
				end++
			}
			ranges[idx][1] = end
		}
		doc.sectionRanges[key] = ranges
	}
	return doc, nil
}

func (d *tomlDoc) addRange(key string, start, end int) {
	if end <= start {
		return
	}
	if _, ok := d.sectionRanges[key]; !ok {
		d.order = append(d.order, key)
	}
	ranges := d.sectionRanges[key]
	if n := len(ranges); n > 0 && ranges[n-1][1] == start {
		ranges[n-1][1] = end
		return
	}
	d.sectionRanges[key] = append(ranges, [2]int{start, end})
}

// ---------------------------------------------------------------------------
// 编辑操作
// ---------------------------------------------------------------------------

// SetMCPServerSection 在 TOML 数据中写入/替换 [mcp_servers.<name>] 段。
// data 为空（文件不存在）时输出仅含该段的新文件。值支持 string/bool/int/
// float/字符串数组/一层嵌套 map（env 表）；不支持形态返回错误不改文件。
func SetMCPServerSection(data []byte, name string, def map[string]any) ([]byte, error) {
	if err := validateTOMLSectionName(name); err != nil {
		return nil, err
	}
	doc, err := parseTOMLDoc(data)
	if err != nil {
		return nil, err
	}
	block, err := serializeTOMLSection("mcp_servers."+name, def, doc.crlf)
	if err != nil {
		return nil, err
	}
	ranges := doc.sectionRanges["mcp_servers."+name]
	if len(ranges) == 0 {
		doc.appendBlock(block)
		return doc.bytes(), nil
	}
	// 替换首个区间；若区间之后仍有内容，块尾补一个分隔空行。
	replaced := block
	if ranges[0][1] < len(doc.lines) {
		nl := "\n"
		if doc.crlf {
			nl = "\r\n"
		}
		replaced = append(append([]string(nil), block...), nl)
	}
	doc.replaceRange(ranges[0][0], ranges[0][1], replaced)
	for _, rg := range ranges[1:] {
		for i := rg[0]; i < rg[1] && i < len(doc.lines); i++ {
			doc.deleted[i] = true
		}
	}
	return doc.bytes(), nil
}

// RemoveMCPServerSection 从 TOML 数据中删除 [mcp_servers.<name>] 段
//（段不存在时原样返回，幂等）。删除保真：注释/空行/其他节逐字节保留。
func RemoveMCPServerSection(data []byte, name string) ([]byte, error) {
	if err := validateTOMLSectionName(name); err != nil {
		return nil, err
	}
	doc, err := parseTOMLDoc(data)
	if err != nil {
		return nil, err
	}
	for _, rg := range doc.sectionRanges["mcp_servers."+name] {
		for i := rg[0]; i < rg[1] && i < len(doc.lines); i++ {
			doc.deleted[i] = true
		}
	}
	return doc.bytes(), nil
}

// tomlSectionValue 读取 [mcp_servers.<name>] 段当前解析值（冲突检测用；
// 键解析失败宽容跳过）。段缺失返回 nil,false。
func tomlSectionValue(data []byte, name string) (map[string]any, bool) {
	doc, err := parseTOMLDoc(data)
	if err != nil {
		return nil, false
	}
	ranges := doc.sectionRanges["mcp_servers."+name]
	if len(ranges) == 0 {
		return nil, false
	}
	merged := map[string]any{}
	cur := merged // 当前写入目标（子表头切换到嵌套 map）
	for _, rg := range ranges {
		for i := rg[0]; i < rg[1] && i < len(doc.lines); i++ {
			line := strings.TrimSpace(stripLineEnd(doc.lines[i]))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "[") {
				path, _, err := parseTOMLHeader(stripTOMLComment(line))
				if err != nil || len(path) < 3 || path[0] != "mcp_servers" || path[1] != name {
					continue
				}
				// 子表头（如 [mcp_servers.x.env]）：写入目标切到嵌套 map。
				cur = merged
				for _, seg := range path[2:] {
					next, ok := cur[seg].(map[string]any)
					if !ok {
						next = map[string]any{}
						cur[seg] = next
					}
					cur = next
				}
				continue
			}
			eq := indexTOMLAssign(line)
			if eq < 0 {
				continue
			}
			keyPart := strings.TrimSpace(line[:eq])
			kp, kerr := splitTOMLKeyPath(keyPart)
			if kerr != nil || len(kp) != 1 {
				continue
			}
			v, _, verr := parseTOMLValue(strings.TrimSpace(line[eq+1:]))
			if verr != nil {
				continue
			}
			cur[kp[0]] = v
		}
	}
	return merged, true
}

// validateTOMLSectionName 校验 server 名可安全用作 TOML 段名。
func validateTOMLSectionName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("MCP server 名为空")
	}
	if strings.ContainsAny(name, ".\"'[]#\n\t") {
		return fmt.Errorf("MCP server 名 %q 含 TOML 段名不支持的字符（./引号/括号等）", name)
	}
	return nil
}

// appendBlock 文末追加段（前置空行分隔；沿用主行尾风格；确保末行有行尾符）。
func (d *tomlDoc) appendBlock(block []string) {
	nl := "\n"
	if d.crlf {
		nl = "\r\n"
	}
	if len(d.lines) > 0 {
		last := d.lines[len(d.lines)-1]
		if !strings.HasSuffix(last, "\n") {
			d.lines[len(d.lines)-1] = last + nl
		}
		d.lines = append(d.lines, nl)
	}
	d.lines = append(d.lines, block...)
}

// replaceRange 以新行块替换行区间并平移行级删除标记。
func (d *tomlDoc) replaceRange(start, end int, newLines []string) {
	next := make([]string, 0, len(d.lines)-(end-start)+len(newLines))
	next = append(next, d.lines[:start]...)
	next = append(next, newLines...)
	next = append(next, d.lines[end:]...)
	delta := len(newLines) - (end - start)
	if delta != 0 {
		nd := map[int]bool{}
		for i, v := range d.deleted {
			if i < start {
				nd[i] = v
			} else if i >= end {
				nd[i+delta] = v
			}
		}
		d.deleted = nd
	}
	d.lines = next
}

// bytes 重建文档字节（跳过被删除行；未触碰行逐字节原样）。
func (d *tomlDoc) bytes() []byte {
	var sb strings.Builder
	for i, line := range d.lines {
		if d.deleted[i] {
			continue
		}
		sb.WriteString(line)
	}
	return []byte(sb.String())
}

// ---------------------------------------------------------------------------
// TOML 词法辅助（复刻 wslsupply/toml_edit.go 的包私有实现，本包独立维护）
// ---------------------------------------------------------------------------

// parseTOMLHeader 解析 `[a.b]` / `[[a.b]]` 表头。
func parseTOMLHeader(s string) (path []string, isArray bool, err error) {
	s = strings.TrimSpace(s)
	isArray = strings.HasPrefix(s, "[[")
	open, close := "[", "]"
	if isArray {
		open, close = "[[", "]]"
	}
	if !strings.HasPrefix(s, open) {
		return nil, false, errors.New("not a table header")
	}
	inner := strings.TrimSpace(s[len(open):])
	if !strings.HasSuffix(inner, close) {
		return nil, false, errors.New("unterminated table header")
	}
	inner = strings.TrimSpace(inner[:len(inner)-len(close)])
	if inner == "" {
		return nil, isArray, nil
	}
	path, err = splitTOMLKeyPath(inner)
	if err != nil {
		return nil, false, fmt.Errorf("table header: %w", err)
	}
	return path, isArray, nil
}

// splitTOMLKeyPath 按点分段（尊重引号段）。
func splitTOMLKeyPath(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	segStart := true
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '"' || c == '\'':
			seg, next, err := parseTOMLString(s[i:])
			if err != nil {
				return nil, err
			}
			cur.WriteString(seg)
			i += next // next 相对 s[i:]，必须叠加（引号在中段时 i=next 会倒退死循环）
			segStart = false
		case c == '.':
			if segStart {
				return nil, errors.New("empty key segment")
			}
			out = append(out, cur.String())
			cur.Reset()
			segStart = true
			i++
		case c == ' ' || c == '\t':
			i++
		default:
			cur.WriteByte(c)
			segStart = false
			i++
		}
	}
	if segStart && len(out) > 0 {
		return nil, errors.New("trailing dot in key path")
	}
	out = append(out, cur.String())
	for _, seg := range out {
		if seg == "" {
			return nil, errors.New("empty key segment")
		}
	}
	return out, nil
}

// indexTOMLAssign 找首个位于字符串外的 '='；遇注释先行返回 -1。
func indexTOMLAssign(s string) int {
	inStr := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr != 0 {
			if c == '\\' && inStr == '"' {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			inStr = c
		case '=':
			return i
		case '#':
			return -1
		}
	}
	return -1
}

// parseTOMLValue 解析一个 TOML 值：字符串/整数/浮点/布尔/数组/内联表。
// 日期时间等形态返回错误（受管段内视为不可编辑）。
func parseTOMLValue(s string) (any, string, error) {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return nil, "", errors.New("empty value")
	}
	switch s[0] {
	case '"', '\'':
		str, next, err := parseTOMLString(s)
		if err != nil {
			return nil, "", err
		}
		return str, s[next:], nil
	case '[':
		return parseTOMLArray(s)
	case '{':
		return parseTOMLInlineTable(s)
	case 't':
		if strings.HasPrefix(s, "true") {
			return true, s[4:], nil
		}
		return nil, "", fmt.Errorf("invalid token %q", trimTOMLShort(s))
	case 'f':
		if strings.HasPrefix(s, "false") {
			return false, s[5:], nil
		}
		return nil, "", fmt.Errorf("invalid token %q", trimTOMLShort(s))
	default:
		return parseTOMLNumber(s)
	}
}

// parseTOMLString 解析基本/字面字符串（多行串不支持）。
func parseTOMLString(s string) (string, int, error) {
	if strings.HasPrefix(s, `"""`) || strings.HasPrefix(s, "'''") {
		return "", 0, errors.New("multi-line strings unsupported in managed scope")
	}
	q := s[0]
	if q == '\'' {
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", 0, errors.New("unterminated literal string")
		}
		return s[1 : 1+end], end + 2, nil
	}
	var out strings.Builder
	i := 1
	for i < len(s) {
		c := s[i]
		if c == '"' {
			return out.String(), i + 1, nil
		}
		if c == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				out.WriteByte('\n')
			case 't':
				out.WriteByte('\t')
			case 'r':
				out.WriteByte('\r')
			case '"':
				out.WriteByte('"')
			case '\\':
				out.WriteByte('\\')
			case 'u', 'U':
				width := 4
				if s[i+1] == 'U' {
					width = 8
				}
				if i+2+width > len(s) {
					return "", 0, errors.New("bad unicode escape")
				}
				var r rune
				if _, err := fmt.Sscanf(s[i+2:i+2+width], "%x", &r); err != nil {
					return "", 0, errors.New("bad unicode escape")
				}
				out.WriteRune(r)
				i += 2 + width
				continue
			default:
				return "", 0, errors.New("unsupported escape in string")
			}
			i += 2
			continue
		}
		out.WriteByte(c)
		i++
	}
	return "", 0, errors.New("unterminated basic string")
}

// parseTOMLNumber 解析整数/浮点（含下划线分隔）。
func parseTOMLNumber(s string) (any, string, error) {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits, dots, esign := 0, 0, false
	for i < len(s) {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c == '_':
		case c == '.':
			dots++
		case (c == 'e' || c == 'E') && digits > 0:
			esign = true
		case esign && (c == '+' || c == '-'):
		default:
			goto done
		}
		i++
	}
done:
	if digits == 0 {
		return nil, "", fmt.Errorf("not a number: %q", trimTOMLShort(s))
	}
	tok := strings.ReplaceAll(s[:i], "_", "")
	rest := s[i:]
	if dots > 0 || esign {
		var f float64
		if _, err := fmt.Sscanf(tok, "%g", &f); err != nil {
			return nil, "", fmt.Errorf("bad float %q", tok)
		}
		return f, rest, nil
	}
	var n int64
	if _, err := fmt.Sscanf(tok, "%d", &n); err != nil {
		return nil, "", fmt.Errorf("bad integer %q", tok)
	}
	return n, rest, nil
}

// parseTOMLArray 解析数组（单行语义；跨行数组在本包编辑器中不需要——
// tomlSectionValue 只做宽容冲突检测）。
func parseTOMLArray(s string) (any, string, error) {
	i := 1
	var out []any
	for {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			return nil, "", errors.New("unterminated array")
		}
		if s[i] == ']' {
			return out, s[i+1:], nil
		}
		v, next, err := parseTOMLValue(s[i:])
		if err != nil {
			return nil, "", err
		}
		out = append(out, v)
		i = len(s) - len(next)
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i < len(s) && s[i] == ',' {
			i++
			continue
		}
		if i < len(s) && s[i] == ']' {
			return out, s[i+1:], nil
		}
		return nil, "", errors.New("unterminated array")
	}
}

// parseTOMLInlineTable 解析内联表。
func parseTOMLInlineTable(s string) (any, string, error) {
	i := 1
	out := map[string]any{}
	for {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			return nil, "", errors.New("unterminated inline table")
		}
		if s[i] == '}' {
			return out, s[i+1:], nil
		}
		rest := s[i:]
		eq := indexTOMLAssign(rest)
		if eq < 0 {
			return nil, "", errors.New("inline table entry missing '='")
		}
		keyPart := strings.TrimSpace(rest[:eq])
		path, err := splitTOMLKeyPath(keyPart)
		if err != nil {
			return nil, "", err
		}
		v, next, err := parseTOMLValue(rest[eq+1:])
		if err != nil {
			return nil, "", err
		}
		if !setNestedTOML(out, path, v) {
			return nil, "", fmt.Errorf("inline table key conflict at %q", keyPart)
		}
		i = len(s) - len(next)
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i < len(s) && s[i] == ',' {
			i++
			continue
		}
		if i < len(s) && s[i] == '}' {
			return out, s[i+1:], nil
		}
		return nil, "", errors.New("unterminated inline table")
	}
}

func setNestedTOML(obj map[string]any, path []string, v any) bool {
	cur := obj
	for i, seg := range path {
		if i == len(path)-1 {
			if _, exists := cur[seg]; exists {
				return false
			}
			cur[seg] = v
			return true
		}
		next, ok := cur[seg].(map[string]any)
		if !ok {
			if _, exists := cur[seg]; exists {
				return false
			}
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
	return false
}

func trimTOMLShort(s string) string {
	if len(s) > 24 {
		s = s[:24] + "…"
	}
	return strings.TrimSpace(strings.ReplaceAll(s, "\n", "\\n"))
}

// stripTOMLComment 剥离行尾注释（首个字符串外的 # 起）。
func stripTOMLComment(s string) string {
	inStr := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr != 0 {
			if c == '\\' && inStr == '"' {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			inStr = c
		case '#':
			return s[:i]
		}
	}
	return s
}

func stripLineEnd(s string) string { return strings.TrimRight(s, "\r\n") }

func splitKeepEnds(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// ---------------------------------------------------------------------------
// 序列化（确定性输出：command/args 首序 → 字典序标量 → 字典序嵌套子表）
// ---------------------------------------------------------------------------

// serializeTOMLSection 把段键（"mcp_servers.foo"）+ entry 序列化为行块。
// 嵌套 map（如 env）作为子表段输出；嵌套 map 内再嵌 map 不支持。
func serializeTOMLSection(key string, entry map[string]any, crlf bool) ([]string, error) {
	nl := "\n"
	if crlf {
		nl = "\r\n"
	}
	var out []string
	emit := func(format string, args ...any) {
		out = append(out, fmt.Sprintf(format, args...)+nl)
	}
	emit("[%s]", key)
	done := map[string]bool{}
	for _, k := range []string{"command", "args"} {
		if v, ok := entry[k]; ok && !isNestedMap(v) {
			vs, err := serializeTOMLValue(v)
			if err != nil {
				return nil, fmt.Errorf("键 %q: %w", k, err)
			}
			emit("%s = %s", k, vs)
			done[k] = true
		}
	}
	var scalars, nested []string
	for k, v := range entry {
		if done[k] {
			continue
		}
		if isNestedMap(v) {
			nested = append(nested, k)
		} else {
			scalars = append(scalars, k)
		}
	}
	sort.Strings(scalars)
	for _, k := range scalars {
		vs, err := serializeTOMLValue(entry[k])
		if err != nil {
			return nil, fmt.Errorf("键 %q: %w", k, err)
		}
		emit("%s = %s", k, vs)
	}
	sort.Strings(nested)
	for _, k := range nested {
		child, _ := entry[k].(map[string]any)
		emit("")
		emit("[%s.%s]", key, k)
		keys := make([]string, 0, len(child))
		for ck := range child {
			keys = append(keys, ck)
		}
		sort.Strings(keys)
		for _, ck := range keys {
			if isNestedMap(child[ck]) {
				return nil, fmt.Errorf("键 %q.%q 下不支持再嵌套表", key, k)
			}
			vs, err := serializeTOMLValue(child[ck])
			if err != nil {
				return nil, fmt.Errorf("键 %q.%q: %w", k, ck, err)
			}
			emit("%s = %s", ck, vs)
		}
	}
	return out, nil
}

func isNestedMap(v any) bool {
	_, ok := v.(map[string]any)
	return ok
}

// serializeTOMLValue 序列化标量/数组值（字符串按基本串转义；支持
// string/bool/int/int64/float64/[]any/[]string）。
func serializeTOMLValue(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return quoteTOMLBasicString(t), nil
	case bool:
		if t {
			return "true", nil
		}
		return "false", nil
	case int:
		return fmt.Sprintf("%d", t), nil
	case int64:
		return fmt.Sprintf("%d", t), nil
	case float64:
		return fmt.Sprintf("%v", t), nil
	case []string:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			parts = append(parts, quoteTOMLBasicString(item))
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			s, err := serializeTOMLValue(item)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	default:
		return "", fmt.Errorf("不支持的 TOML 值类型 %T", v)
	}
}

func quoteTOMLBasicString(s string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\t':
			out.WriteString(`\t`)
		case '\r':
			out.WriteString(`\r`)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}
