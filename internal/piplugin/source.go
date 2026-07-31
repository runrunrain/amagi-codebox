package piplugin

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// 源类型常量。
const (
	sourceNPM   = "npm"
	sourceGit   = "git"
	sourceLocal = "local"
)

// 资源类型常量（与 pi 约定目录对应）。
const (
	resourceExtension = "extension"
	resourceSkill     = "skill"
	resourcePrompt    = "prompt"
	resourceTheme     = "theme"
)

// npmNamePattern 复刻 pi parseNpmSpec：^(@?[^@]+(?:\/[^@]+)?)(?:@(.+))?$
// 捕获组1=包名（可含 scope），组2=版本。
var npmNamePattern = regexp.MustCompile(`^(@?[^@]+(?:/[^@]+)?)(?:@(.+))?$`)

// parsedSource 是 parseSource 的结构化结果。
type parsedSource struct {
	// sourceType: npm / git / local
	sourceType string
	// npm 专属：包名（含 scope，不含版本）。
	name string
	// npm/git 专属：版本或 ref（@v1 / @1.0.0），精确即 pinned。
	ref string
	// git 专属：host（如 github.com）。
	host string
	// git 专属：path（user/project，已去 .git 与 @ref）。
	path string
	// local 专属：原始路径（原样保留，不解析）。
	localPath string
}

// parseSource 解析一个 pi 包源字符串，复刻 pi 的 parseSource 语义：
//  1. "npm:..." → npm；name 含 scope、去版本；
//  2. 本地路径（非 npm:/git:/github:/http(s):/ssh: 前缀）→ local；
//  3. 其余尝试按 git 解析（git: 前缀或显式协议 URL）。
func parseSource(raw string) parsedSource {
	s := strings.TrimSpace(raw)
	if s == "" {
		return parsedSource{}
	}
	if strings.HasPrefix(s, "npm:") {
		spec := strings.TrimSpace(strings.TrimPrefix(s, "npm:"))
		name, version := parseNpmSpec(spec)
		return parsedSource{
			sourceType: sourceNPM,
			name:       name,
			ref:        version,
		}
	}
	if isLocalPath(s) {
		return parsedSource{sourceType: sourceLocal, localPath: s}
	}
	if gs, ok := parseGitSource(s); ok {
		return gs
	}
	// 无法识别为 git，回退为 local（与 pi parseSource 兜底一致）。
	return parsedSource{sourceType: sourceLocal, localPath: s}
}

// parseNpmSpec 复刻 pi：从 npm spec（已去 "npm:" 前缀）拆出 name 与 version。
// 例："@foo/bar@1.0.0"→("@foo/bar","1.0.0")；"pkg"→("pkg","")。
func parseNpmSpec(spec string) (name, version string) {
	m := npmNamePattern.FindStringSubmatch(spec)
	if m == nil {
		return spec, ""
	}
	name = strings.TrimSpace(m[1])
	if name == "" {
		name = spec
	}
	version = strings.TrimSpace(m[2])
	return name, version
}

// isLocalPath 复刻 pi utils/paths.ts isLocalPath：
// 命中已知非本地前缀（npm:/git:/github:/http(s):/ssh:/git:）返回 false。
func isLocalPath(value string) bool {
	trimmed := strings.TrimSpace(value)
	for _, prefix := range []string{"npm:", "git:", "github:", "http:", "https:", "ssh:"} {
		if strings.HasPrefix(trimmed, prefix) {
			return false
		}
	}
	return true
}

// gitSourcePattern 捕获 scp-like 形式：^git@([^:]+):(.+)$
var gitScpPattern = regexp.MustCompile(`^git@([^:]+):(.+)$`)

// protocolPrefixes 是无 git: 前缀时才被接受的显式协议。
var protocolPrefixes = []string{"https://", "http://", "ssh://", "git://"}

// parseGitSource 把 git 源解析为 host/path（用于实体目录定位）。
// 仅复刻 pi 取 host 与 path 的目录语义，不做 hosted-git-info 的快捷域名归一。
// 支持的输入形如（可带 git: 前缀）：
//   - github.com/user/repo[@ref]
//   - git@github.com:user/repo[@ref]
//   - https://github.com/user/repo[@ref]
//   - ssh://git@github.com/user/repo[@ref]
//
// 返回的 path 形如 "user/project"（已去 .git 与 @ref）。
func parseGitSource(raw string) (parsedSource, bool) {
	s := strings.TrimSpace(raw)
	hasGitPrefix := strings.HasPrefix(s, "git:")
	if hasGitPrefix {
		s = strings.TrimSpace(strings.TrimPrefix(s, "git:"))
	} else {
		ok := false
		for _, p := range protocolPrefixes {
			if strings.HasPrefix(s, p) {
				ok = true
				break
			}
		}
		if !ok {
			return parsedSource{}, false
		}
	}
	if s == "" {
		return parsedSource{}, false
	}

	repo, ref := splitGitRef(s)
	host, path := splitGitHostPath(repo)
	if host == "" || path == "" {
		return parsedSource{}, false
	}
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	if host == "" || path == "" || len(strings.Split(path, "/")) < 2 {
		return parsedSource{}, false
	}
	return parsedSource{
		sourceType: sourceGit,
		host:       host,
		path:       path,
		ref:        ref,
	}, true
}

// splitGitRef 从 git URL（已去 git: 前缀）中分离出 repo 与 @ref。
// 复刻 pi splitRef 对 scp-like / 协议 URL / 简写三种形态的处理。
func splitGitRef(input string) (repo, ref string) {
	// scp-like: git@host:path[@ref]
	if m := gitScpPattern.FindStringSubmatch(input); m != nil {
		rest := m[2]
		if at := strings.Index(rest, "@"); at >= 0 {
			repoPath := rest[:at]
			r := rest[at+1:]
			if repoPath != "" && r != "" {
				return "git@" + m[1] + ":" + repoPath, r
			}
		}
		return input, ""
	}
	// 协议 URL：ref 取 pathname 中首个 @ 之后部分。
	if strings.Contains(input, "://") {
		if u, err := url.Parse(input); err == nil {
			pathPart := strings.TrimPrefix(u.Path, "/")
			if at := strings.Index(pathPart, "@"); at >= 0 {
				repoPath := pathPart[:at]
				r := pathPart[at+1:]
				if repoPath != "" && r != "" {
					u.Path = "/" + repoPath
					return strings.TrimRight(u.String(), "/"), r
				}
			}
		}
		return input, ""
	}
	// 简写 host/path[@ref]：首个 / 前为 host，其后取首个 @ 分割 ref。
	if slash := strings.Index(input, "/"); slash >= 0 {
		host := input[:slash]
		rest := input[slash+1:]
		if at := strings.Index(rest, "@"); at >= 0 {
			repoPath := rest[:at]
			r := rest[at+1:]
			if repoPath != "" && r != "" {
				return host + "/" + repoPath, r
			}
		}
		return input, ""
	}
	return input, ""
}

// splitGitHostPath 从 repo（已去 @ref）中取 host 与 path。
func splitGitHostPath(repo string) (host, path string) {
	// scp-like: git@host:path
	if m := gitScpPattern.FindStringSubmatch(repo); m != nil {
		return m[1], m[2]
	}
	// 协议 URL：host=hostname，path=pathname（去前导 /）。
	if strings.Contains(repo, "://") {
		if u, err := url.Parse(repo); err == nil {
			return u.Hostname(), strings.TrimPrefix(u.Path, "/")
		}
		return "", ""
	}
	// 简写 host/path：首个 / 前为 host。
	if slash := strings.Index(repo, "/"); slash >= 0 {
		host = repo[:slash]
		path = repo[slash+1:]
		// pi 要求 host 含 "." 或为 localhost 才视为简写 git。
		if !strings.Contains(host, ".") && host != "localhost" {
			return "", ""
		}
		return host, path
	}
	return "", ""
}

// fallbackName 从源字符串推导一个显示名（package.json 缺失时用）。
func fallbackName(source string) string {
	s := strings.TrimSpace(source)
	s = strings.TrimPrefix(s, "npm:")
	s = strings.TrimPrefix(s, "git:")
	// 去协议前缀。
	for _, p := range protocolPrefixes {
		s = strings.TrimPrefix(s, p)
	}
	// 去 scp-like 的 git@host: 前缀。
	if m := gitScpPattern.FindStringSubmatch(s); m != nil {
		s = m[2]
	}
	// 去 @ref / @version。
	if at := strings.LastIndex(s, "@"); at > 0 {
		// 保留 scoped 名（@scope/name）开头的 @。
		if !(at == 0) {
			s = s[:at]
		}
	}
	// 取最后一段路径。
	s = strings.TrimSuffix(s, "/")
	if slash := strings.LastIndex(s, "/"); slash >= 0 {
		s = s[slash+1:]
	}
	s = strings.TrimSuffix(s, ".git")
	if s == "" {
		return source
	}
	return s
}

// isExactSemver 判断 npm 版本表达式是否为严格语义化精确版本（对标 pi 使用的
// npm semver.valid()）：可选 `v` 前缀；MAJOR.MINOR.PATCH 数字段不允许前导零；
// prerelease 段为点分隔的 [0-9A-Za-z-] 标识符（数字标识符同样禁前导零），
// build 段（+ 后）为点分隔的 [0-9A-Za-z-] 标识符（允许前导零）。
// 区间/^/~/*/latest/非法 exact-looking 值（如 01.2.3、1.2.3-..）均不算 pinned。
// 对齐 npm semver 的两条附加限制（R3 复审 Minor-3）：整体长度 ≤ 256；
// core/prerelease 的数字标识符 ≤ MAX_SAFE_INTEGER（防止大整数精度丢失）。
const (
	semverMaxLength = 256
	maxSafeInteger  = 9007199254740991
)

func isExactSemver(ref string) bool {
	s := strings.TrimSpace(ref)
	if len(s) == 0 || len(s) > semverMaxLength {
		return false
	}
	s = strings.TrimPrefix(s, "v")
	// 拆 build 元数据
	if i := strings.Index(s, "+"); i >= 0 {
		build := s[i+1:]
		s = s[:i]
		if !validSemverIdents(build, true) {
			return false
		}
	}
	// 拆 prerelease
	if i := strings.Index(s, "-"); i >= 0 {
		pre := s[i+1:]
		s = s[:i]
		if !validSemverIdents(pre, false) {
			return false
		}
	}
	// 核心三段
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if !isNumericNoLeadingZero(p) || !isSafeIntegerPart(p) {
			return false
		}
	}
	return true
}

// validSemverIdents 校验点分隔的标识符序列。allowNumericLeadingZero 对应 build
// 段语义（build 允许 01 等数字前导零，prerelease 不允许）。
func validSemverIdents(s string, allowNumericLeadingZero bool) bool {
	if s == "" {
		return false
	}
	for _, ident := range strings.Split(s, ".") {
		if ident == "" {
			return false // 连续点 / 尾点产生空标识符
		}
		for _, r := range ident {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-') {
				return false
			}
		}
		if !allowNumericLeadingZero && isAllDigits(ident) && !isNumericNoLeadingZero(ident) {
			return false
		}
		if isAllDigits(ident) && !isSafeIntegerPart(ident) {
			return false
		}
	}
	return true
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// isNumericNoLeadingZero 校验 SemVer 数字段：纯数字且不允许前导零（"0" 合法，"01" 非法）。
func isNumericNoLeadingZero(s string) bool {
	if !isAllDigits(s) {
		return false
	}
	return len(s) == 1 || s[0] != '0'
}

// isSafeIntegerPart 校验数字标识符不超过 MAX_SAFE_INTEGER（npm semver 同款限制）。
func isSafeIntegerPart(s string) bool {
	n, err := strconv.ParseInt(s, 10, 64)
	return err == nil && n <= maxSafeInteger
}

// stringField safely reads a trimmed string field from a map, falling back.
func stringField(data map[string]any, key, fallback string) string {
	if v, ok := data[key].(string); ok {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return fallback
}

// authorField reads package.json author (string or {name}).
func authorField(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		if name, ok := v["name"].(string); ok {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

// repositoryField reads package.json repository (string or {url}).
func repositoryField(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		if raw, ok := v["url"].(string); ok {
			return strings.TrimSpace(raw)
		}
	}
	return ""
}
