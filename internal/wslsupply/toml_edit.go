package wslsupply

// toml_edit.go — Codex config.toml 的 TOML 保真局部编辑器（P3-S4，design
// §四 Codex 表「TOML 编辑必须保留非目标节与注释」）。
//
// 取舍声明（任务书授权的最小侵入方案）：仓库未 vendored 任何 TOML 库，全量
// parse-rewrite 序列化无法保留注释/空行/键序。本编辑器采用「定位行级替换」：
// 只解析受管作用域（mcp_servers.* / profiles.*）内的表头与键值行；这些行段
// 之外的一切字节（非目标节、注释、空行、行尾风格）逐字节保留。编辑 = 整段
// 替换/删除受管行段、文末追加新段。受管作用域内出现不可解析/不支持形态
//（数组表、裸表直接键、顶层作用域赋值、点引号段名、日期时间值）→ 停止编辑
// 该文件（design：「解析失败停止该文件，不用 JSON 的整对象序列化替代 TOML
// 保真编辑」）。非受管节永不解析（合法性由 Codex 自行判断，我们只保留字节）。

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// errTomlUnsupportedForm 表示受管作用域出现本批不支持的形态。单元按受阻处理
// （不改写文件），非受管内容不受影响。
var errTomlUnsupportedForm = errors.New("toml managed scope uses an unsupported form")

// errTomlParse 表示受管作用域内值/键解析失败（来源侧归类 source-corrupt，
// 目标侧归类 target-corrupt——拒绝不覆盖）。
var errTomlParse = errors.New("toml managed scope parse failure")

// tomlManagedDoc 是一个 TOML 文档的受管作用域视图 + 行级编辑状态。
type tomlManagedDoc struct {
	lines         []string // 含行尾符的原始行（未触碰行逐字节保留）
	crlf          bool     // 新增行采用的行尾风格（跟随原文主体）
	sections      map[string]bool
	order         []string // 段发现顺序（确定性遍历）
	sectionRanges map[string]*tomlSectionRange
	deleted       map[int]bool
}

// tomlSectionRange 描述一个受管段占用的行区间集合（表头 + 其键值/子表行 +
// 段内尾随空行/注释；表头之前的注释属上文，不并入）。
type tomlSectionRange struct {
	Key    string
	Ranges [][2]int // [start, end) 行区间，按出现顺序
}

// parseTOMLManaged 解析 data 中 scopes（如 ["mcp_servers","profiles"]）作用域
// 的段结构与值。受管作用域形态不支持或值不可解析时返回错误（调用方按单元
// 受阻处理，不写目标）。
func parseTOMLManaged(data []byte, scopes []string) (*tomlManagedDoc, map[string]*tomlSectionRange, error) {
	text := string(data)
	doc := &tomlManagedDoc{
		lines:         splitKeepEnds(text),
		crlf:          strings.Contains(text, "\r\n"),
		sections:      map[string]bool{},
		sectionRanges: map[string]*tomlSectionRange{},
		deleted:       map[int]bool{},
	}
	scopeSet := map[string]bool{}
	for _, s := range scopes {
		scopeSet[s] = true
	}
	ensure := func(key string) *tomlSectionRange {
		if r, ok := doc.sectionRanges[key]; ok {
			return r
		}
		r := &tomlSectionRange{Key: key}
		doc.sectionRanges[key] = r
		doc.sections[key] = true
		doc.order = append(doc.order, key)
		return r
	}
	addRange := func(key string, start, end int) {
		r := ensure(key)
		if n := len(r.Ranges); n > 0 && r.Ranges[n-1][1] == start {
			r.Ranges[n-1][1] = end
			return
		}
		r.Ranges = append(r.Ranges, [2]int{start, end})
	}

	currentPath := []string(nil)
	for i := 0; i < len(doc.lines); i++ {
		line := strings.TrimSpace(stripLineEnd(doc.lines[i]))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			path, isArray, err := parseTOMLHeader(line)
			if err != nil {
				// 非受管区的畸形行/数组续行：不解析、保留字节。受管区的
				// 续行不可能到达这里（多行值在 kv 解析内整体消费）。
				currentPath = []string(nil)
				continue
			}
			currentPath = path
			if len(path) > 0 && scopeSet[path[0]] {
				if isArray {
					return nil, nil, fmt.Errorf("line %d: %w: array-of-tables %q", i+1, errTomlUnsupportedForm, line)
				}
				if len(path) == 1 {
					return nil, nil, fmt.Errorf("line %d: %w: bare table [%s]", i+1, errTomlUnsupportedForm, path[0])
				}
				if strings.Contains(path[1], ".") {
					return nil, nil, fmt.Errorf("line %d: %w: dotted section name %q", i+1, errTomlUnsupportedForm, path[1])
				}
				addRange(path[0]+"."+path[1], i, i+1)
			}
			continue
		}
		kv, value, consumed, err := parseTOMLKV(doc.lines, i, currentPath, scopeSet)
		if err != nil {
			if !errors.Is(err, errTomlUnsupportedForm) {
				err = fmt.Errorf("%w: %v", errTomlParse, err)
			}
			return nil, nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		if kv != "" {
			kp, perr := splitTOMLKeyPath(kv)
			if perr != nil {
				return nil, nil, fmt.Errorf("line %d: key %q: %w", i+1, kv, errTomlParse)
			}
			full := append(append([]string{}, currentPath...), kp...)
			if key, depth, ok := managedSectionKey(scopeSet, full); ok && depth >= 2 {
				if depth == 2 && len(currentPath) == 0 {
					// 根级点键把整段赋给内联表（mcp_servers.x = { … }）：非表值是
					// 不支持形态（F3：明确拒绝，不产生空段误导诊断）。
					if _, isMap := value.(map[string]any); !isMap {
						return nil, nil, fmt.Errorf("line %d: %w: root key %q assigns a non-table to managed section", i+1, errTomlUnsupportedForm, kv)
					}
				}
				addRange(key, i, i+consumed+1)
			}
			i += consumed
		}
	}
	// 段区间向后吸收段内尾随空行/注释（整体替换/删除时一并处理）。
	for _, r := range doc.sectionRanges {
		for idx := range r.Ranges {
			end := r.Ranges[idx][1]
			for end < len(doc.lines) {
				t := strings.TrimSpace(stripLineEnd(doc.lines[end]))
				if t != "" && !strings.HasPrefix(t, "#") {
					break
				}
				end++
			}
			r.Ranges[idx][1] = end
		}
	}
	return doc, doc.sectionRanges, nil
}

// managedSectionKey 由表路径（或点键展开）求受管段键与深度。depth=2 段本体
// （mcp_servers.foo），depth≥3 子表/嵌套（仍属该段）。首段不在作用域 → false。
func managedSectionKey(scopeSet map[string]bool, path []string) (key string, depth int, ok bool) {
	if len(path) < 2 || !scopeSet[path[0]] {
		return "", 0, false
	}
	return path[0] + "." + path[1], len(path), true
}

// parseTOMLHeader 解析 `[a.b."c"]` / `[[a.b]]` 表头。
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
	i, segStart := 0, true
	for i < len(s) {
		c := s[i]
		switch {
		case c == '"' || c == '\'':
			seg, next, err := parseTOMLString(s[i:])
			if err != nil {
				return nil, err
			}
			if segStart && cur.Len() == 0 {
				cur.WriteString(seg)
			} else {
				cur.WriteString(seg)
			}
			i = next
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

// parseTOMLKV 解析 idx 起（可能跨行多行数组）的键值行。返回：
//   - key 非空：该行属受管作用域且已完整解析（key 为原始键部，value 由
//     SectionValue 按需重建；consumed 为附加消费的续行数）；
//   - key 空 + err 空：非受管行（不解析、不消费，原样保留）；
//   - err 非空：受管作用域内不可解析（含不支持形态）→ 停止编辑该文件。
func parseTOMLKV(lines []string, idx int, currentPath []string, scopeSet map[string]bool) (key string, value any, consumed int, err error) {
	line := strings.TrimSpace(stripLineEnd(lines[idx]))
	eq := indexTOMLAssign(line)
	if eq < 0 {
		if len(currentPath) > 0 && scopeSet[currentPath[0]] {
			return "", nil, 0, errors.New("managed scope has an unparseable line")
		}
		return "", nil, 0, nil
	}
	keyPart := strings.TrimSpace(line[:eq])
	var path []string
	if len(currentPath) == 0 {
		kp, kerr := splitTOMLKeyPath(keyPart)
		if kerr != nil {
			return "", nil, 0, nil // 非受管根键：不解析
		}
		path = kp
		if !scopeSet[path[0]] {
			return "", nil, 0, nil
		}
		if len(path) == 1 {
			return "", nil, 0, fmt.Errorf("%w: top-level %q assignment", errTomlUnsupportedForm, path[0])
		}
		if strings.Contains(path[1], ".") {
			return "", nil, 0, fmt.Errorf("%w: dotted segment in %q", errTomlUnsupportedForm, keyPart)
		}
	} else {
		if !scopeSet[currentPath[0]] {
			return "", nil, 0, nil // 非受管表内的键值：不解析
		}
		kp, kerr := splitTOMLKeyPath(keyPart)
		if kerr != nil {
			return "", nil, 0, fmt.Errorf("key in managed table: %w", kerr)
		}
		path = append(append([]string{}, currentPath...), kp...)
	}
	// 值解析（多行数组逐行拼接直到可完整解析或耗尽）。
	joined := strings.TrimSpace(line[eq+1:])
	for {
		v, rem, perr := parseTOMLValue(joined)
		if perr == nil {
			if t := strings.TrimSpace(rem); t != "" && !strings.HasPrefix(t, "#") {
				return "", nil, 0, fmt.Errorf("trailing content after value in managed scope: %q", trimShort(t))
			}
			return keyPart, v, consumed, nil
		}
		if idx+consumed+1 >= len(lines) {
			return "", nil, 0, fmt.Errorf("value in managed scope: %v", perr)
		}
		consumed++
		joined = joined + "\n" + strings.TrimSpace(stripLineEnd(lines[idx+consumed]))
	}
}

// indexTOMLAssign 找到首个位于字符串外的 '='；遇注释先行返回 -1。
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

// parseTOMLValue 解析一个 TOML 值；返回值与剩余串。支持字符串/整数/浮点/
// 布尔/数组/内联表；日期时间等形态返回错误（受管段内视为不可编辑）。
func parseTOMLValue(s string) (any, string, error) {
	s = skipTOMLBlank(s)
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
	case 't', 'f':
		if strings.HasPrefix(s, "true") {
			return true, s[4:], nil
		}
		if strings.HasPrefix(s, "false") {
			return false, s[5:], nil
		}
		return nil, "", fmt.Errorf("invalid token %q", trimShort(s))
	default:
		return parseTOMLNumber(s)
	}
}

// skipTOMLBlank 跳过空白与整行注释（多行数组拼接后 '#'-至-'\n' 均为空白）。
func skipTOMLBlank(s string) string {
	for {
		s = s[countTOMLWhitespace(s):]
		if strings.HasPrefix(s, "#") {
			if nl := strings.IndexByte(s, '\n'); nl >= 0 {
				s = s[nl+1:]
				continue
			}
			return ""
		}
		return s
	}
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
		return nil, "", fmt.Errorf("not a number: %q", trimShort(s))
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

// parseTOMLArray 解析数组（可含跨行拼接的 '\n'，按空白处理）。
func parseTOMLArray(s string) (any, string, error) {
	i := 1
	var out []any
	for {
		rest := skipTOMLBlank(s[i:])
		i = len(s) - len(rest)
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
		rest = skipTOMLBlank(s[i:])
		i = len(s) - len(rest)
		if i < len(s) && s[i] == ',' {
			i++
			continue
		}
		if i < len(s) && s[i] == ']' {
			return out, s[i+1:], nil
		}
		if i >= len(s) {
			return nil, "", errors.New("unterminated array")
		}
		return nil, "", fmt.Errorf("unexpected content in array: %q", trimShort(s[i:]))
	}
}

// parseTOMLInlineTable 解析内联表（单行语义；跨行拼接后亦按空白容许）。
func parseTOMLInlineTable(s string) (any, string, error) {
	i := 1
	out := map[string]any{}
	for {
		rest := skipTOMLBlank(s[i:])
		i = len(s) - len(rest)
		if i >= len(s) {
			return nil, "", errors.New("unterminated inline table")
		}
		if s[i] == '}' {
			return out, s[i+1:], nil
		}
		eq := indexTOMLAssign(s[i:])
		if eq < 0 {
			return nil, "", errors.New("inline table entry missing '='")
		}
		keyPart := strings.TrimSpace(s[i : i+eq])
		path, err := splitTOMLKeyPath(keyPart)
		if err != nil {
			return nil, "", err
		}
		v, next, err := parseTOMLValue(s[i+eq+1:])
		if err != nil {
			return nil, "", err
		}
		if !setNestedTOML(out, path, v) {
			return nil, "", fmt.Errorf("inline table key conflict at %q", keyPart)
		}
		i = len(s) - len(next)
		rest = skipTOMLBlank(s[i:])
		i = len(s) - len(rest)
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

// parseTOMLString 解析基本/字面字符串（含多行基本串 """ 的常见形态不支持——
// 受管段内报错）。
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

func countTOMLWhitespace(s string) int {
	i := 0
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

func trimShort(s string) string {
	if len(s) > 24 {
		s = s[:24] + "…"
	}
	return strings.TrimSpace(strings.ReplaceAll(s, "\n", "\\n"))
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
// 编辑操作（行级替换；非受管行逐字节保留）
// ---------------------------------------------------------------------------

// RemoveSection 删除一个受管段的全部行区间。
func (d *tomlManagedDoc) RemoveSection(key string) {
	r := d.sectionRanges[key]
	if r == nil {
		return
	}
	for _, rg := range r.Ranges {
		for i := rg[0]; i < rg[1] && i < len(d.lines); i++ {
			d.deleted[i] = true
		}
	}
	delete(d.sections, key)
}

// UpsertSection 以确定性序列化写入/替换一个受管段；段已存在则原位替换首个
// 行区间并删除其余区间，不存在则文末追加（前置空行分隔）。值仅支持标量/
// 数组/一层嵌套 map（env 子表）；其它形态返回错误（调用方保持目标现状）。
func (d *tomlManagedDoc) UpsertSection(key string, entry map[string]any) error {
	block, err := serializeTOMLSection(key, entry, d.crlf)
	if err != nil {
		return err
	}
	r := d.sectionRanges[key]
	if r == nil || len(r.Ranges) == 0 {
		d.appendSection(key, block)
		return nil
	}
	first := r.Ranges[0]
	d.replaceRange(first[0], first[1], block)
	for _, rg := range r.Ranges[1:] {
		for i := rg[0]; i < rg[1] && i < len(d.lines); i++ {
			d.deleted[i] = true
		}
	}
	return nil
}

// replaceRange 以新行块替换行区间并平移全部行级标记。
func (d *tomlManagedDoc) replaceRange(start, end int, newLines []string) {
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
		for _, r := range d.sectionRanges {
			for idx := range r.Ranges {
				rg := r.Ranges[idx]
				if rg[0] >= end {
					r.Ranges[idx] = [2]int{rg[0] + delta, rg[1] + delta}
				} else if rg[1] > end {
					r.Ranges[idx] = [2]int{rg[0], rg[1] + delta}
				}
			}
		}
	}
	d.lines = next
}

// appendSection 文末追加段（前置空行；沿用主行尾风格）。
func (d *tomlManagedDoc) appendSection(key string, block []string) {
	nl := "\n"
	if d.crlf {
		nl = "\r\n"
	}
	if len(d.lines) > 0 {
		last := d.lines[len(d.lines)-1]
		if last != "" && !strings.HasSuffix(last, "\n") {
			d.lines[len(d.lines)-1] = last + nl
		}
		d.lines = append(d.lines, nl)
	}
	start := len(d.lines)
	d.lines = append(d.lines, block...)
	d.sectionRanges[key] = &tomlSectionRange{Key: key, Ranges: [][2]int{{start, len(d.lines)}}}
	d.sections[key] = true
	d.order = append(d.order, key)
}

// Bytes 重建文档字节（跳过被删除行；未触碰行逐字节原样）。
func (d *tomlManagedDoc) Bytes() []byte {
	var sb strings.Builder
	for i, line := range d.lines {
		if d.deleted[i] {
			continue
		}
		sb.WriteString(line)
	}
	return []byte(sb.String())
}

// SectionValue 求一个受管段当前的解析值（供冲突判定：与期望值的 canonical
// hash 比较；keyedValueHash 语义一致）。段缺失返回 nil,false。
func (d *tomlManagedDoc) SectionValue(key string) (map[string]any, bool) {
	r := d.sectionRanges[key]
	if r == nil {
		return nil, false
	}
	scopeRoot := strings.SplitN(key, ".", 2)[0]
	scopes := map[string]bool{scopeRoot: true}
	merged := map[string]any{}
	for _, rg := range r.Ranges {
		currentPath := []string(nil)
		for i := rg[0]; i < rg[1] && i < len(d.lines); i++ {
			if d.deleted[i] {
				continue
			}
			line := strings.TrimSpace(stripLineEnd(d.lines[i]))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "[") {
				if path, _, err := parseTOMLHeader(line); err == nil {
					currentPath = path
				}
				continue
			}
			kv, v, consumed, err := parseTOMLKV(d.lines, i, currentPath, scopes)
			if err != nil || kv == "" {
				continue
			}
			kp, perr := splitTOMLKeyPath(kv)
			if perr != nil {
				continue
			}
			full := append(append([]string{}, currentPath...), kp...)
			if len(full) == 2 {
				// 根级点键内联表（mcp_servers.x = { … }）：值即段本体（F3）。
				if body, isMap := v.(map[string]any); isMap {
					for k, val := range body {
						if _, exists := merged[k]; !exists {
							merged[k] = val
						}
					}
				}
				i += consumed
				continue
			}
			if len(full) < 3 {
				continue
			}
			setNestedTOML(merged, full[2:], v)
			i += consumed
		}
	}
	return merged, true
}

// Has 报告段是否存在。
func (d *tomlManagedDoc) Has(key string) bool { return d.sections[key] }

// Keys 返回受管段键（发现顺序副本）。
func (d *tomlManagedDoc) Keys() []string { return append([]string(nil), d.order...) }

// serializeTOMLSection 把段键（"mcp_servers.foo"）+ entry 序列化为行块。
// 顺序确定：固定首序（command、args）→ 其余标量/数组键按字典序 → 嵌套 map
// （如 env）作为子表段（空行分隔、键按字典序）。嵌套 map 内再嵌 map 不支持。
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
				return nil, fmt.Errorf("key %q: %w", k, err)
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
			return nil, fmt.Errorf("key %q: %w", k, err)
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
				return nil, fmt.Errorf("nested table under %q.%q unsupported", key, k)
			}
			vs, err := serializeTOMLValue(child[ck])
			if err != nil {
				return nil, fmt.Errorf("key %q.%q: %w", k, ck, err)
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

// serializeTOMLValue 序列化标量/数组值（字符串按基本串转义）。
func serializeTOMLValue(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return quoteTOMLBasicString(t), nil
	case bool:
		if t {
			return "true", nil
		}
		return "false", nil
	case int64:
		return fmt.Sprintf("%d", t), nil
	case int:
		return fmt.Sprintf("%d", t), nil
	case float64:
		return fmt.Sprintf("%v", t), nil
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
		return "", fmt.Errorf("unsupported toml value type %T", v)
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
