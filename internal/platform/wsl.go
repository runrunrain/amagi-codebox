package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
)

// WSL 发行版探测：`wsl.exe -l -q` 的输出是 UTF-16（通常带 BOM）。本模块负责解码、
// 过滤专用发行版（docker-desktop / docker-desktop-data），并缓存结果，避免每次会话
// 启动都 fork 一次 wsl.exe。仅在 Windows 且 shell key 为 wsl 时被调用；其他平台的
// capabilities 目录不含 wsl 候选，故不会触发探测。
//
// wslDistroLister runs `wsl.exe -l -q` and returns raw stdout bytes. It is a
// package var so tests can inject a fake list without invoking real WSL.
var wslDistroLister = func(env []string) ([]byte, error) {
	cmd := exec.Command("wsl.exe", "-l", "-q")
	SuppressConsoleWindow(cmd)
	if len(env) > 0 {
		cmd.Env = env
	}
	return cmd.Output()
}

var (
	wslDistroCacheMu   sync.Mutex
	wslDistroCacheDone bool
	wslDistroCache     []string
)

// wslReservedDistros are special-purpose distributions that cannot host a normal
// user shell / dev environment. They must never be selected as the WSL target.
var wslReservedDistros = map[string]struct{}{
	"docker-desktop":      {},
	"docker-desktop-data": {},
}

// wslDistroVersionLister runs `wsl.exe -l -v` (verbose: NAME / STATE / VERSION
// columns) and returns raw stdout bytes. The VERSION column is the WSL
// architecture generation (1 or 2) each distro is registered as — this is how
// we surface "WSL2" in the UI. Older inbox wsl.exe builds do not support -v;
// the error is swallowed by callers (version reported as 0 = unknown).
var wslDistroVersionLister = func(env []string) ([]byte, error) {
	cmd := exec.Command("wsl.exe", "-l", "-v")
	SuppressConsoleWindow(cmd)
	cmd.Env = env
	return cmd.Output()
}

var (
	wslDistroVersionCacheMu   sync.Mutex
	wslDistroVersionCache     map[string]int
	wslDistroVersionCacheDone bool
	wslDistroStateCache       map[string]string
)

// WSLDistroVersions returns a map of distro name → WSL architecture version
// (1 or 2), cached after the first probe. Empty map when the probe fails or no
// version info is available (older wsl.exe); callers must treat 0/absent as
// "unknown", never as WSL1.
func WSLDistroVersions(env []string) map[string]int {
	wslDistroVersionCacheMu.Lock()
	defer wslDistroVersionCacheMu.Unlock()
	if !wslDistroVersionCacheDone {
		wslDistroVersionCache, wslDistroStateCache = probeWSLDistroVersions(env)
		wslDistroVersionCacheDone = true
	}
	return wslDistroVersionCache
}

// WSLDistroStates returns a map of distro name → STATE ("Running" / "Stopped"),
// INCLUDING reserved distros (docker-desktop): the dockerwsl integration health
// check must observe the Docker Desktop tool distro. Shares the `wsl -l -v`
// probe and cache with WSLDistroVersions; empty map when the probe fails.
func WSLDistroStates(env []string) map[string]string {
	wslDistroVersionCacheMu.Lock()
	defer wslDistroVersionCacheMu.Unlock()
	if !wslDistroVersionCacheDone {
		wslDistroVersionCache, wslDistroStateCache = probeWSLDistroVersions(env)
		wslDistroVersionCacheDone = true
	}
	if wslDistroStateCache == nil {
		return map[string]string{}
	}
	return wslDistroStateCache
}

func probeWSLDistroVersions(env []string) (map[string]int, map[string]string) {
	out, err := wslDistroVersionLister(env)
	if err != nil {
		return map[string]int{}, map[string]string{}
	}
	decoded := decodeWSLListOutput(out)
	versions := map[string]int{}
	states := map[string]string{}
	for _, line := range strings.Split(decoded, "\n") {
		name, state, version, ok := parseWSLVerboseListLine(line)
		if !ok {
			continue
		}
		versions[name] = version
		states[name] = state
	}
	return versions, states
}

// parseWSLVerboseListLine parses one line of `wsl.exe -l -v` output:
//
//	  NAME              STATE           VERSION
//	* Ubuntu-24.04      Running         2
//	  Ubuntu 22.04 LTS  Stopped         1
//
// The default distro is marked with a leading '*'. Distro names may contain
// spaces, so we take the LAST field as the version (when it is a pure digit)
// and the SECOND-TO-LAST as STATE; everything before (minus any '*' marker)
// joined back together is the name. The header line never ends in a digit and
// is rejected naturally.
func parseWSLVerboseListLine(line string) (name, state string, version int, ok bool) {
	fields := strings.Fields(strings.Trim(line, "\r\x00"))
	if len(fields) < 3 {
		return "", "", 0, false
	}
	version, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil || version < 1 {
		return "", "", 0, false
	}
	state = fields[len(fields)-2]
	// fields = [name-part...] STATE VERSION. Distro names may contain spaces;
	// re-join everything before the trailing STATE column.
	name = strings.TrimSpace(strings.TrimPrefix(strings.Join(fields[:len(fields)-2], " "), "*"))
	if name == "" {
		return "", "", 0, false
	}
	return name, state, version, true
}

// resetWSLDistroVersionCacheForTest clears the version-probe cache.
func resetWSLDistroVersionCacheForTest() {
	wslDistroVersionCacheMu.Lock()
	defer wslDistroVersionCacheMu.Unlock()
	wslDistroVersionCacheDone = false
	wslDistroVersionCache = nil
	wslDistroStateCache = nil
}

// availableWSLDistros returns the usable (non-reserved) WSL distributions, cached
// after the first successful probe. A probe error yields an empty list (treated
// as "no usable distro" so callers fall back to pwsh/cmd).
func availableWSLDistros(env []string) []string {
	wslDistroCacheMu.Lock()
	defer wslDistroCacheMu.Unlock()
	if wslDistroCacheDone {
		return wslDistroCache
	}
	wslDistroCacheDone = true
	wslDistroCache = probeWSLDistros(env)
	return wslDistroCache
}

func probeWSLDistros(env []string) []string {
	out, err := wslDistroLister(env)
	if err != nil {
		return nil
	}
	decoded := decodeWSLListOutput(out)
	result := []string{}
	for _, line := range strings.Split(decoded, "\n") {
		name := strings.TrimSpace(strings.Trim(line, "\r\x00"))
		if name == "" {
			continue
		}
		if _, reserved := wslReservedDistros[strings.ToLower(name)]; reserved {
			continue
		}
		result = append(result, name)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// hasUsableWSLDistro reports whether at least one non-reserved WSL distro exists.
func hasUsableWSLDistro(env []string) bool {
	return len(availableWSLDistros(env)) > 0
}

// DefaultWSLDistro returns the first usable WSL distro name, or "" when none.
// Exported for the PTY layer, which must pass an explicit `-d <distro>` so the
// launch never lands on a reserved distro (e.g. docker-desktop mis-maps --cd).
func DefaultWSLDistro(env []string) string {
	distros := availableWSLDistros(env)
	if len(distros) == 0 {
		return ""
	}
	return distros[0]
}

// resetWSLDistroCacheForTest clears the probe cache so tests can re-run with a
// different injected lister.
func resetWSLDistroCacheForTest() {
	wslDistroCacheMu.Lock()
	defer wslDistroCacheMu.Unlock()
	wslDistroCacheDone = false
	wslDistroCache = nil
}

// --- WSL2 网络模式探测（.wslconfig，只读） ---
//
// AI 代理分流、Headroom、系统代理的统一入口都监听宿主回环（127.0.0.1）。WSL2
// 默认 NAT 模式下 distro 内的 127.0.0.1 指向发行版自身，宿主回环不可达；
// mirrored 模式（%USERPROFILE%\.wslconfig 的 [wsl2] 段 networkingMode=mirrored）
// 下 localhost 双向互通，回环入口对 WSL 会话自然可用。本节只读配置文件，不
// fork wsl.exe；纯文件解析使其在任何宿主 OS 上可单测（生产调用点——aiproxy
// 闸门与 envcheck 卡片——仅存在于 Windows 的 WSL 路径，非 Windows 宿主天然
// 不可达）。

// WSLNetworkingMode returns the WSL2 networking mode declared in the Windows
// user's .wslconfig: "mirrored", "nat", or "" (no file, no [wsl2]
// networkingMode key, or unparsable content). Callers must treat "" like
// "nat" — WSL's own default — so unknown states fail safe, the same
// assumption the AI-proxy WSL gate has always made.
//
// The Windows home is resolved from env (USERPROFILE first — the key wsl.exe
// itself uses to locate .wslconfig — then HOME), falling back to this
// process's home. 每次调用直读 .wslconfig（m-4：不设进程级缓存）——文件
// 极小、调用频率仅为会话启动/envcheck，直读成本可忽略；而缓存会让拒绝
// 文案「执行 wsl --shutdown 后重试」在同进程内失效（用户改完配置重试仍
// 被旧缓存拒绝）。测试以独立临时 HOME 隔离，不依赖缓存可重置性。
func WSLNetworkingMode(env []string) string {
	home := strings.TrimSpace(envValue(env, "USERPROFILE"))
	if home == "" {
		home = strings.TrimSpace(envValue(env, "HOME"))
	}
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return ""
		}
	}
	return probeWSLNetworkingMode(home)
}

// WSLFileMTime 返回 distro 内指定绝对 Linux 路径的 mtime（epoch 秒，经
// wsl.exe 内 `stat -c %Y`——distro 侧 ext4 时间戳的权威读法，9P/UNC stat
// 的翻译语义不可靠）。路径不存在、非 Windows 宿主或探测失败返回
// ok=false；调用方需按「时间戳不可判」保守处理，不得把 false 当作 0 值
// 参与新陈比较（A2 凭据双向同步 M-1 的判据画）。
func WSLFileMTime(distro, linuxPath string) (int64, bool) {
	distro = strings.TrimSpace(distro)
	if distro == "" || runtime.GOOS != "windows" || !strings.HasPrefix(linuxPath, "/") {
		return 0, false
	}
	out, err := wslScriptRunner(distro, "stat -c %Y "+wslShSingleQuote(linuxPath))
	if err != nil {
		return 0, false
	}
	sec, perr := strconv.ParseInt(strings.TrimSpace(strings.Trim(decodeWSLListOutput(out), "\r\x00 ")), 10, 64)
	if perr != nil || sec < 0 {
		return 0, false
	}
	return sec, true
}

// probeWSLNetworkingMode reads <home>\.wslconfig and parses the [wsl2]
// networkingMode key. Any read error returns "" (fail-safe as NAT).
func probeWSLNetworkingMode(home string) string {
	data, err := os.ReadFile(filepath.Join(home, ".wslconfig"))
	if err != nil {
		return ""
	}
	// .wslconfig 由编辑器保存，可能是带 BOM 的 UTF-8 或 UTF-16（旧版记事本）；
	// 复用 wsl.exe 输出的宽容解码（同包、decodeWSLListOutput 已有单测覆盖）。
	return parseWSLConfigNetworkingMode(decodeWSLListOutput(data))
}

// parseWSLConfigNetworkingMode parses .wslconfig INI content for the [wsl2]
// section's networkingMode key:
//   - sections match case-insensitively ([wsl2] / [WSL2]); keys before any
//     section header or inside other sections are ignored;
//   - full-line comments start with '#' or ';'; values may carry trailing
//     inline '#' / ';' comments, which are stripped;
//   - key matching is case-insensitive and tolerates spaces around '=';
//   - only "nat" and "mirrored" (case-insensitive) are recognized — any other
//     value (typos, experimental modes) returns "" so callers fail safe as
//     NAT;
//   - the first [wsl2] networkingMode key wins (INI first-wins convention;
//     later duplicates are ignored).
func parseWSLConfigNetworkingMode(content string) string {
	inSection := false
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			section := ""
			if end := strings.IndexByte(line, ']'); end > 0 {
				section = strings.TrimSpace(line[1:end])
			}
			inSection = strings.EqualFold(section, "wsl2")
			continue
		}
		if !inSection {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "networkingMode") {
			continue
		}
		if idx := strings.IndexAny(value, "#;"); idx >= 0 {
			value = value[:idx]
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "mirrored":
			return "mirrored"
		case "nat":
			return "nat"
		}
		return ""
	}
	return ""
}

// decodeWSLListOutput decodes the `wsl.exe -l -q` output, which is UTF-16 with a
// BOM on Windows. It handles UTF-16 LE/BE (with BOM), a UTF-8 BOM, and falls
// back to a NUL-byte heuristic (UTF-16LE without BOM) before treating the bytes
// as plain UTF-8/ASCII.
func decodeWSLListOutput(b []byte) string {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return string(b[3:])
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		return decodeUTF16(b[2:], false)
	}
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		return decodeUTF16(b[2:], true)
	}
	if bytesContainNUL(b) {
		return decodeUTF16(b, false)
	}
	return string(b)
}

func bytesContainNUL(b []byte) bool {
	for _, c := range b {
		if c == 0x00 {
			return true
		}
	}
	return false
}

func decodeUTF16(b []byte, bigEndian bool) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u16 := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if bigEndian {
			u16 = append(u16, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			u16 = append(u16, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	return string(utf16.Decode(u16))
}

// wslENVForwardPrefixes are the env-key prefixes whose values must cross the
// Windows→WSL boundary so the CLI running inside WSL sees the injected
// provider/auth configuration. Windows-only vars (PATH, SystemRoot, ...) are
// intentionally NOT forwarded to avoid polluting the Linux environment.
var wslENVForwardPrefixes = []string{
	"ANTHROPIC_",
	"OPENAI_",
	"CLAUDE_",
	"CODEX_",
	"PI_",
}

// wslENVForwardExact are additional non-prefixed keys to forward: the network
// proxy vars, plus OPENCODE_CONFIG — the path of an ADDITIONAL opencode config
// file. In WSL sessions CodeBox writes the runtime provider config into the
// distro (~/.config/opencode/amagi-codebox.json) and points this variable at
// its Linux path (forwarded verbatim; no /p path-translation flag — the value
// is already a distro-side path). opencode loads that file as a custom config
// layer on top of the user's global config (verified against the installed
// opencode v1.17.18 binary: load order global → OPENCODE_CONFIG → project →
// OPENCODE_CONFIG_CONTENT). The env-content carrier OPENCODE_CONFIG_CONTENT
// has no OPENCODE_ prefix in the whitelist and never crosses the boundary,
// which is exactly why the path carrier exists (evidence T4).
// ALL_PROXY/all_proxy（m-5）：socks 类 scheme 与仅认 ALL_PROXY 的实现依赖它；
// aiproxy 分流注入同时携带两键，缺它则 distro 内只见 HTTP(S)_PROXY/NO_PROXY。
// 其值是 URL，经 ShouldForwardToWSL 命中后路径值剥离判据自动生效。
var wslENVForwardExact = map[string]struct{}{
	"HTTP_PROXY":      {},
	"HTTPS_PROXY":     {},
	"NO_PROXY":        {},
	"http_proxy":      {},
	"https_proxy":     {},
	"no_proxy":        {},
	"ALL_PROXY":       {},
	"all_proxy":       {},
	"OPENCODE_CONFIG": {},
}

// ShouldForwardToWSL reports whether an env key would be listed by
// appendWSLENVForwarding given the same extraForwardKeys (user-configured
// envvars.json keys): whitelist prefix, exact key, or extra-key match
// (case-insensitive; reserved keys WSLENV/PATH/PATHEXT never forward).
// Exported so the launcher-side strip (StripWSLHostPathEnv) can identify
// entries that are ABOUT to cross the boundary; callers must strip BEFORE
// appendWSLENVForwarding computes the WSLENV list.
func ShouldForwardToWSL(key string, extraForwardKeys ...string) bool {
	if key == "" || strings.EqualFold(key, "WSLENV") {
		return false
	}
	if _, ok := wslENVForwardExact[key]; ok {
		return true
	}
	upper := strings.ToUpper(key)
	for _, prefix := range wslENVForwardPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	if IsWSLENVReservedKey(key) {
		return false
	}
	for _, k := range extraForwardKeys {
		if strings.EqualFold(strings.TrimSpace(k), key) {
			return true
		}
	}
	return false
}

func shouldForwardToWSL(key string) bool {
	return ShouldForwardToWSL(key)
}

// IsWSLENVReservedKey 报告不得经额外清单转发的保留键：WSLENV 自身（清单载体，
// 转发即自引用）与 PATH 家族（Windows 值进 WSL 会整体覆盖发行版自身的 PATH
// 解析，CLI 将无法在 distro 内定位）。导出供 envvars 保存校验（P4-A2 路径值
// 放行标记对保留键拒绝）与 launcher 剥离豁免面共用同一判据。
func IsWSLENVReservedKey(key string) bool {
	switch strings.ToUpper(strings.TrimSpace(key)) {
	case "WSLENV", "PATH", "PATHEXT":
		return true
	}
	return false
}

// appendWSLENVForwarding computes the WSLENV variable from the forwardable keys
// present in env and merges it with any existing WSLENV. The returned slice is a
// copy with WSLENV set; env is not mutated. WSLENV uses a colon-separated list
// of variable names; by default no path-translation flags are attached (the
// forwarded values are keys / URLs, not Windows paths) — the per-key /p gate
// lives in appendWSLENVForwardingWithPaths (P4-A2).
//
// extraForwardKeys lists additional env keys to forward verbatim across the
// Windows→WSL boundary even when they match no whitelist prefix — populated
// with user-configured custom env vars (envvars.json, e.g. TAVILY_API_KEY /
// FIRECRAWL_API_KEY) which are intentionally injected into CLI sessions and
// must therefore reach CLIs running inside the distro too. Keys are matched
// case-insensitively and only listed when actually present in env; reserved
// names (WSLENV / PATH / PATHEXT) are always excluded — forwarding a Windows
// PATH would clobber the distro's own PATH resolution.
// AppendWSLENVForwarding 把额外需要跨界转发的键并入 env 的 WSLENV 清单
// （仅收录通过 ShouldForwardToWSL 的键；既有 WSLENV 条目原样保留）。P3-S2：
// remote OpenCode 层 effect 在解析器之后注入 OPENCODE_CONFIG 时复用同一语义。
func AppendWSLENVForwarding(env []string, extraForwardKeys ...string) []string {
	return appendWSLENVForwarding(env, extraForwardKeys...)
}

// AppendWSLENVForwardingWithPaths 把额外需要跨界转发的键并入 env 的 WSLENV
// 清单，并为 pathFlagKeys 中的键附加 /p 路径翻译标志（P4-A2 per-key 放行门）。
// 导出供 launcher 侧链路一致性测试与潜在复用方使用；resolver 的 WSL 分支
// 即此调用形态。
func AppendWSLENVForwardingWithPaths(env []string, extraForwardKeys []string, pathFlagKeys []string) []string {
	return appendWSLENVForwardingWithPaths(env, extraForwardKeys, pathFlagKeys)
}

func appendWSLENVForwarding(env []string, extraForwardKeys ...string) []string {
	return appendWSLENVForwardingWithPaths(env, extraForwardKeys, nil)
}

// appendWSLENVForwardingWithPaths 是 appendWSLENVForwarding 的 per-key 放行扩展
// （P4-A2 路径值放行门，主上已批）：pathFlagKeys 中的键（envvars.json
// allowPathValue 标记，大小写不敏感）在 WSLENV 清单中获得 /p 路径翻译标志——
// 值跨界时由 WSL 把 C:\... 译为 /mnt/c/...，放行路径值的合法需求正是 WSL 内
// 工具要能按翻译后形态读 Windows 路径。值本身原样留在进程 env（翻译由 WSL
// 按 /p 执行，不在进程 env 内改写）。保留键（IsWSLENVReservedKey）不参与：
// 它们本来就不在转发面，放行标记对其无效。未列名的键行为与
// appendWSLENVForwarding 完全一致（无标志转发，默认防泄漏基线不变——
// StripWSLHostPathEnvWithAllow 侧对未标记的路径值键照旧剥离）。
func appendWSLENVForwardingWithPaths(env []string, extraForwardKeys []string, pathFlagKeys []string) []string {
	names := []string{}
	seen := map[string]struct{}{}
	addName := func(name, flagSuffix string) {
		key := strings.ToUpper(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		names = append(names, name+flagSuffix)
	}

	// 放行键名单提前构建（qreview F-1：保留段需要交叉判断）。
	pathFlags := make(map[string]struct{}, len(pathFlagKeys))
	for _, k := range pathFlagKeys {
		trimmed := strings.TrimSpace(k)
		if trimmed == "" || IsWSLENVReservedKey(trimmed) {
			continue
		}
		pathFlags[strings.ToUpper(trimmed)] = struct{}{}
	}

	// Preserve any pre-existing WSLENV entries first.
	if existing := envValue(env, "WSLENV"); existing != "" {
		for _, entry := range strings.Split(existing, ":") {
			trimmed := strings.TrimSpace(entry)
			if trimmed == "" {
				continue
			}
			// Strip any /flags suffix for dedup purposes but keep the raw entry.
			base := trimmed
			if idx := strings.Index(trimmed, "/"); idx >= 0 {
				base = trimmed[:idx]
			}
			if base == "" {
				continue
			}
			key := strings.ToUpper(base)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			// qreview F-1：用户自带条目命中放行键且未含 /p 时升级补齐，
			// 防半开态（剥离豁免但无路径翻译）；已有 /p 或含 p 的 flag
			// 组合（如 /u/p）不重写。
			if _, pf := pathFlags[key]; pf {
				flags := trimmed[len(base):]
				if !strings.Contains(flags, "p") {
					trimmed = trimmed + "/p"
				}
			}
			names = append(names, trimmed)
		}
	}

	extra := make(map[string]struct{}, len(extraForwardKeys))
	for _, k := range extraForwardKeys {
		trimmed := strings.TrimSpace(k)
		if trimmed == "" || IsWSLENVReservedKey(trimmed) {
			continue
		}
		extra[strings.ToUpper(trimmed)] = struct{}{}
	}

	for _, kv := range env {
		k := envEntryKey(kv)
		forwardable := shouldForwardToWSL(k)
		if !forwardable {
			if _, ok := extra[strings.ToUpper(k)]; ok {
				forwardable = true
			}
		}
		if !forwardable {
			continue
		}
		if _, ok := pathFlags[strings.ToUpper(k)]; ok {
			addName(k, "/p")
			continue
		}
		addName(k, "")
	}

	if len(names) == 0 {
		return env
	}
	return setEnvValue(env, "WSLENV", strings.Join(names, ":"))
}

func envEntryKey(kv string) string {
	idx := strings.IndexByte(kv, '=')
	if idx <= 0 {
		return ""
	}
	return kv[:idx]
}
