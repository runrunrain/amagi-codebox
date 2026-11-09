package client

// args.go — argv 解析（DESIGN §2）：
//   - 全局旗标可出现在任意位置（--home/--json/--yes/-y/--timeout/--help/-h/--version）；
//   - 命令词最长匹配（{"config","get"} 优先于 {"config"}）；
//   - 参数旗标为 --kebab-case，位置参数按 Params 中 Positional 声明顺序分配；
//   - `--` 之后全为位置参数；同一参数不可同时以位置与旗标给出；
//   - 类型转换 string/int/bool/string[]/json；json 接受字面量/@文件/-(stdin)；
//     Stdin=true 的参数接受 `-`；bool 支持 --x / --no-x / --x=false；
//     string[] 收尾位位置参数全量收集且旗标可重复。

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
)

// ---- 全局旗标 ----

// globals 是全局旗标解析结果。
type globals struct {
	json       bool
	yes        bool
	help       bool
	version    bool
	home       string
	timeout    int
	timeoutSet bool
}

// extractGlobals 从 argv 提取全局旗标（支持 --flag=value 形态）；遇 `--`
// 停止提取（其后全为字面量）。返回剩余 token（保持原序，含 `--`）。
//
// 同名冲突消解（table-aware）：命令词之后与命令参数旗标同名的 token 优先
// 按参数旗标处理（留在 rest 交 parseCommandArgs 解析）——否则
// `provider save acme --json '{...}'` 的参数 --json 会被全局 --json 吞掉，
// JSON 字面量沦为多余位置参数。命令词之前没有命令上下文，同名 token 一律
// 按全局旗标处理（如 `--json status` 仍是 JSON 包络模式）。
func extractGlobals(argv []string, table []spec.Command) (globals, []string, error) {
	var g globals
	rest := make([]string, 0, len(argv))
	// 参数旗标集合：仅对“命令词之后”的同名 token 生效。
	paramFlags := map[string]bool{}
	i := 0
	// 阶段一：命令词之前的旗标一律按全局旗标处理。
	for ; i < len(argv); i++ {
		t := argv[i]
		if t == "--" {
			rest = append(rest, argv[i:]...)
			return g, rest, nil
		}
		if !strings.HasPrefix(t, "-") || t == "-" {
			break // 命令词（或位置参数）开始
		}
		if err := takeGlobal(&g, &i, argv, t, &rest); err != nil {
			return g, nil, err
		}
	}
	// 阶段二：收集命令词前缀（与 parseCommandArgs 的收集规则一致），
	// 最长匹配出命令后计算其参数旗标集合。
	words := make([]string, 0, 4)
	for ; i < len(argv); i++ {
		t := argv[i]
		if len(t) > 1 && strings.HasPrefix(t, "-") {
			break
		}
		words = append(words, t)
	}
	rest = append(rest, words...)
	if cmd, k := matchCommand(table, words); k > 0 {
		for _, p := range cmd.Params {
			paramFlags["--"+kebab(p.Name)] = true
		}
	}
	// 阶段三：命令词之后的旗标——同名参数旗标优先，其余按全局旗标。
	for ; i < len(argv); i++ {
		t := argv[i]
		if t == "--" {
			rest = append(rest, argv[i:]...)
			return g, rest, nil
		}
		if !strings.HasPrefix(t, "-") || t == "-" {
			rest = append(rest, t)
			continue
		}
		name, _, _ := strings.Cut(t, "=")
		if paramFlags[name] {
			rest = append(rest, t)
			continue
		}
		if err := takeGlobal(&g, &i, argv, t, &rest); err != nil {
			return g, nil, err
		}
	}
	return g, rest, nil
}

// takeGlobal 把一个已确认按全局旗标处理的 token（含其值消费）写入 g；
// 非全局旗标名则原样留在 rest（交命令参数解析）。
func takeGlobal(g *globals, i *int, argv []string, t string, rest *[]string) error {
	name, value, hasValue := strings.Cut(t, "=")
	needValue := func() (string, error) {
		if hasValue {
			return value, nil
		}
		if *i+1 < len(argv) && !strings.HasPrefix(argv[*i+1], "--") {
			*i++
			return argv[*i], nil
		}
		return "", fmt.Errorf("旗标 %s 缺少值", name)
	}
	switch name {
	case "--json", "--yes", "-y", "--help", "-h", "--version":
		if hasValue {
			return fmt.Errorf("旗标 %s 不接受值", name)
		}
		switch name {
		case "--json":
			g.json = true
		case "--yes", "-y":
			g.yes = true
		case "--help", "-h":
			g.help = true
		case "--version":
			g.version = true
		}
	case "--home":
		v, err := needValue()
		if err != nil {
			return err
		}
		g.home = v
	case "--timeout":
		v, err := needValue()
		if err != nil {
			return err
		}
		n, perr := strconv.Atoi(v)
		if perr != nil || n <= 0 {
			return fmt.Errorf("--timeout 需为正整数秒，得到 %q", v)
		}
		g.timeout, g.timeoutSet = n, true
	default:
		*rest = append(*rest, t)
	}
	return nil
}

// ---- 命令词匹配 ----

func pathMatches(path []string, words []string) bool {
	if len(path) != len(words) {
		return false
	}
	for i := range path {
		if path[i] != words[i] {
			return false
		}
	}
	return true
}

// matchCommand 对 words 做最长前缀匹配，返回命中的命令与消费的词数（0 = 未命中）。
func matchCommand(table []spec.Command, words []string) (spec.Command, int) {
	maxLen := 0
	for _, c := range table {
		if len(c.Path) > maxLen {
			maxLen = len(c.Path)
		}
	}
	for k := min(len(words), maxLen); k >= 1; k-- {
		for _, c := range table {
			if pathMatches(c.Path, words[:k]) {
				return c, k
			}
		}
	}
	return spec.Command{}, 0
}

// ---- 参数解析 ----

// invocation 是一次成功解析的结果。
type invocation struct {
	cmd     spec.Command
	args    map[string]any
	confirm string // --yes 注入的确认值（= cmd.Confirm）
}

// unknownCommandError 表示命令词未命中命令表（Run 层转拼写建议/组帮助）。
type unknownCommandError struct{ words []string }

func (e *unknownCommandError) Error() string {
	return "未知命令 " + strconv.Quote(strings.Join(e.words, " "))
}

// stdinSource 惰性且至多读取一次 stdin（多个 `-` 参数共享同一份内容）。
type stdinSource struct {
	r    io.Reader
	buf  []byte
	done bool
}

func (s *stdinSource) all() ([]byte, error) {
	if !s.done {
		b, err := io.ReadAll(s.r)
		if err != nil {
			return nil, fmt.Errorf("读取 stdin 失败: %w", err)
		}
		s.buf, s.done = b, true
	}
	return s.buf, nil
}

// kebab 把 camelCase 参数名转为旗标形态（appType → app-type）。
func kebab(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('-')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isFlagValue 判断 token 能否作为旗标的值：`-`（stdin）、普通值与负数可以，
// 其余旗标形态（--x / -x）不可以。
func isFlagValue(t string) bool {
	if t == "-" || !strings.HasPrefix(t, "-") {
		return true
	}
	body := t[1:]
	if body == "" {
		return false
	}
	for _, r := range body {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func checkEnum(p spec.Param, v string) error {
	if len(p.Enum) == 0 {
		return nil
	}
	for _, e := range p.Enum {
		if e == v {
			return nil
		}
	}
	return fmt.Errorf("参数 --%s 取值必须是 %s 之一，得到 %q", kebab(p.Name), strings.Join(p.Enum, "|"), v)
}

// stdinString 处理 Stdin 参数的 `-` 展开（去尾部换行）；其余原样返回。
func stdinString(p spec.Param, raw string, stdin *stdinSource) (string, error) {
	if p.Stdin && raw == "-" {
		b, err := stdin.all()
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return raw, nil
}

// jsonValue 处理 json 参数：字面量 / @文件 / -(stdin)，并校验合法后返回解析值。
func jsonValue(p spec.Param, raw string, stdin *stdinSource) (any, error) {
	text := raw
	switch {
	case raw == "-":
		b, err := stdin.all()
		if err != nil {
			return nil, err
		}
		text = string(b)
	case strings.HasPrefix(raw, "@"):
		b, err := os.ReadFile(raw[1:])
		if err != nil {
			return nil, fmt.Errorf("参数 --%s 读取文件 %s 失败: %v", kebab(p.Name), raw, err)
		}
		text = string(b)
	}
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil, fmt.Errorf("参数 --%s 不是合法 JSON: %v", kebab(p.Name), err)
	}
	return v, nil
}

// splitStdinLines 把 stdin 内容按行拆为 string[] 元素（去尾部空行，忽略空行）。
func splitStdinLines(b []byte) []string {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// parseCommandArgs 解析命令词 + 参数。所有失败均为用法错误（退出码 2）；
// 错误信息绝不包含参数原始值（防止 Secret 值回显）。helpMode=true 时
// （`<命令> --help`）只做命令词匹配，不解析/校验参数——帮助不应被参数问题拦截。
func parseCommandArgs(table []spec.Command, tokens []string, stdin *stdinSource, helpMode bool) (invocation, error) {
	if len(tokens) > 0 && tokens[0] == "--" {
		tokens = tokens[1:]
	}
	// 命令词：从头部收集连续的非旗标 token（全局旗标已被 extractGlobals 摘除）。
	var words []string
	i := 0
	for i < len(tokens) && !(len(tokens[i]) > 1 && strings.HasPrefix(tokens[i], "-")) {
		words = append(words, tokens[i])
		i++
	}
	if len(words) == 0 {
		if i < len(tokens) {
			return invocation{}, fmt.Errorf("未知旗标 %s：旗标前缺少命令词", tokens[i])
		}
		return invocation{}, fmt.Errorf("缺少命令词")
	}
	cmd, k := matchCommand(table, words)
	if k == 0 {
		return invocation{}, &unknownCommandError{words: words}
	}
	if helpMode {
		return invocation{cmd: cmd}, nil
	}

	byFlag := make(map[string]spec.Param, len(cmd.Params))
	for _, p := range cmd.Params {
		byFlag[kebab(p.Name)] = p
	}

	args := make(map[string]any, len(cmd.Params))
	flagGiven := make(map[string]bool, len(cmd.Params))

	setValue := func(p spec.Param, raw string) error {
		if _, dup := args[p.Name]; dup && p.Type != spec.TypeStrings {
			return fmt.Errorf("参数 --%s 重复给出", kebab(p.Name))
		}
		// Secret+Stdin 参数只能经 stdin 传入（评审 D-07）：明文 argv 值会进入
		// shell 历史与进程参数，本地直接拒绝（用法错误 exit 2），不发出请求。
		// 错误不回显值，并给出 `-`（stdin）用法示例。
		if p.Secret && p.Stdin && raw != "-" {
			usage := progName + " " + strings.Join(cmd.Path, " ")
			if p.Positional {
				return fmt.Errorf("参数 <%s> 是机密参数，只能以 `-`（stdin）传入，不能给明文值；例如：printf '%%s' \"$SECRET\" | %s … -", p.Name, usage)
			}
			return fmt.Errorf("参数 --%s 是机密参数，只能以 `-`（stdin）传入，不能给明文值；例如：printf '%%s' \"$SECRET\" | %s --%s -", kebab(p.Name), usage, kebab(p.Name))
		}
		switch p.Type {
		case spec.TypeString:
			v, err := stdinString(p, raw, stdin)
			if err != nil {
				return err
			}
			if err := checkEnum(p, v); err != nil {
				return err
			}
			args[p.Name] = v
		case spec.TypeInt:
			n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err != nil {
				return fmt.Errorf("参数 --%s 必须是整数，得到 %q", kebab(p.Name), raw)
			}
			args[p.Name] = n
		case spec.TypeBool:
			b, err := strconv.ParseBool(strings.TrimSpace(raw))
			if err != nil {
				return fmt.Errorf("参数 --%s 必须是布尔值（true/false），得到 %q", kebab(p.Name), raw)
			}
			args[p.Name] = b
		case spec.TypeStrings:
			items := []string{raw}
			if p.Stdin && raw == "-" {
				b, err := stdin.all()
				if err != nil {
					return err
				}
				items = splitStdinLines(b)
				if len(items) == 0 {
					return fmt.Errorf("参数 --%s 从 stdin 读到的内容为空", kebab(p.Name))
				}
			}
			for _, it := range items {
				v, err := stdinString(p, it, stdin)
				if err != nil {
					return err
				}
				if err := checkEnum(p, v); err != nil {
					return err
				}
				if old, ok := args[p.Name].([]string); ok {
					args[p.Name] = append(old, v)
				} else {
					args[p.Name] = []string{v}
				}
			}
		case spec.TypeJSON:
			v, err := jsonValue(p, raw, stdin)
			if err != nil {
				return err
			}
			args[p.Name] = v
		default:
			return fmt.Errorf("参数 --%s 声明了未知类型 %s", kebab(p.Name), p.Type)
		}
		flagGiven[p.Name] = true
		return nil
	}

	// 剩余 token：命令词匹配后多出的非旗标词仍是位置参数（按原序在前）。
	positional := append([]string{}, words[k:]...)
	literal := false
	rest := tokens[i:]
	for j := 0; j < len(rest); j++ {
		t := rest[j]
		if literal {
			positional = append(positional, t)
			continue
		}
		if t == "--" {
			literal = true
			continue
		}
		if len(t) > 2 && strings.HasPrefix(t, "--") {
			body := t[2:]
			name, value, hasEq := strings.Cut(body, "=")
			p, found := byFlag[name]
			negate := false
			if !found && strings.HasPrefix(name, "no-") {
				if p2, ok := byFlag[name[len("no-"):]]; ok {
					if p2.Type != spec.TypeBool {
						return invocation{}, fmt.Errorf("--no-%s 仅适用于 bool 参数（%s 是 %s）", name, name[len("no-"):], p2.Type)
					}
					p, found, negate, name = p2, true, true, name[len("no-"):]
				}
			}
			if !found {
				return invocation{}, fmt.Errorf("未知旗标 --%s（命令 %s）%s", name, cmd.Name(), suggestFlagSuffix(cmd, name))
			}
			if p.Type == spec.TypeBool {
				val := !negate
				if hasEq {
					v, err := strconv.ParseBool(value)
					if err != nil {
						return invocation{}, fmt.Errorf("--%s=%q 不是布尔值（可用 true/false）", name, value)
					}
					val = v
				}
				if _, dup := args[p.Name]; dup {
					return invocation{}, fmt.Errorf("参数 --%s 重复给出", name)
				}
				args[p.Name] = val
				flagGiven[p.Name] = true
				continue
			}
			if negate {
				return invocation{}, fmt.Errorf("--no-%s 仅适用于 bool 参数", name)
			}
			var raw string
			if hasEq {
				raw = value
			} else {
				if j+1 >= len(rest) || !isFlagValue(rest[j+1]) {
					return invocation{}, fmt.Errorf("旗标 --%s 缺少值", name)
				}
				j++
				raw = rest[j]
			}
			if err := setValue(p, raw); err != nil {
				return invocation{}, err
			}
			continue
		}
		if len(t) > 1 && strings.HasPrefix(t, "-") {
			return invocation{}, fmt.Errorf("未知旗标 %s（命令 %s）", t, cmd.Name())
		}
		positional = append(positional, t)
	}

	// 位置参数按 Positional 声明顺序分配；string[] 全量收集收尾位。
	pi := 0
	for _, p := range cmd.Params {
		if !p.Positional {
			continue
		}
		if p.Type == spec.TypeStrings {
			var items []string
			for pi < len(positional) {
				raw := positional[pi]
				pi++
				v, err := stdinString(p, raw, stdin)
				if err != nil {
					return invocation{}, err
				}
				if err := checkEnum(p, v); err != nil {
					return invocation{}, err
				}
				items = append(items, v)
			}
			if len(items) > 0 {
				if flagGiven[p.Name] {
					return invocation{}, fmt.Errorf("参数 <%s> 不能同时以位置参数和旗标给出", p.Name)
				}
				args[p.Name] = items
			}
			continue
		}
		if pi >= len(positional) {
			break
		}
		raw := positional[pi]
		pi++
		if flagGiven[p.Name] {
			return invocation{}, fmt.Errorf("参数 <%s> 不能同时以位置参数和旗标给出", p.Name)
		}
		if err := setValue(p, raw); err != nil {
			return invocation{}, err
		}
	}
	if pi < len(positional) {
		return invocation{}, fmt.Errorf("多余的参数 %q：命令 %s 不接受这么多位置参数", positional[pi], cmd.Name())
	}

	// 必填检查（枚举/类型已在转换时校验）。
	paramHint := func(p spec.Param) string {
		if p.Desc != "" {
			return "（" + p.Desc + "）"
		}
		return ""
	}
	for _, p := range cmd.Params {
		if p.Required {
			if _, ok := args[p.Name]; !ok {
				if p.Positional {
					return invocation{}, fmt.Errorf("缺少必填位置参数 <%s>%s", p.Name, paramHint(p))
				}
				return invocation{}, fmt.Errorf("缺少必填参数 --%s%s", kebab(p.Name), paramHint(p))
			}
		}
	}
	return invocation{cmd: cmd, args: args}, nil
}

// suggestFlagSuffix 为未知参数旗标给出就近建议（前缀命中或编辑距离 ≤2）。
func suggestFlagSuffix(cmd spec.Command, name string) string {
	var cands []string
	for _, p := range cmd.Params {
		k := kebab(p.Name)
		if k == name {
			continue
		}
		if strings.HasPrefix(k, name) || levenshtein(name, k) <= 2 {
			cands = append(cands, "--"+k)
		}
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Strings(cands)
	return "。你是不是想输入：" + strings.Join(cands, " / ")
}
