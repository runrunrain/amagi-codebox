package platform

import (
	"os/exec"
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
)

// WSLDistroVersions returns a map of distro name → WSL architecture version
// (1 or 2), cached after the first probe. Empty map when the probe fails or no
// version info is available (older wsl.exe); callers must treat 0/absent as
// "unknown", never as WSL1.
func WSLDistroVersions(env []string) map[string]int {
	wslDistroVersionCacheMu.Lock()
	defer wslDistroVersionCacheMu.Unlock()
	if wslDistroVersionCacheDone {
		return wslDistroVersionCache
	}
	wslDistroVersionCacheDone = true
	wslDistroVersionCache = probeWSLDistroVersions(env)
	return wslDistroVersionCache
}

func probeWSLDistroVersions(env []string) map[string]int {
	out, err := wslDistroVersionLister(env)
	if err != nil {
		return map[string]int{}
	}
	decoded := decodeWSLListOutput(out)
	result := map[string]int{}
	for _, line := range strings.Split(decoded, "\n") {
		name, version, ok := parseWSLVerboseListLine(line)
		if !ok {
			continue
		}
		result[name] = version
	}
	return result
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
func parseWSLVerboseListLine(line string) (string, int, bool) {
	fields := strings.Fields(strings.Trim(line, "\r\x00"))
	if len(fields) < 3 {
		return "", 0, false
	}
	version, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil || version < 1 {
		return "", 0, false
	}
	// fields = [name-part...] STATE VERSION. Distro names may contain spaces;
	// re-join everything before the trailing STATE column.
	name := strings.TrimSpace(strings.TrimPrefix(strings.Join(fields[:len(fields)-2], " "), "*"))
	if name == "" {
		return "", 0, false
	}
	return name, version, true
}

// resetWSLDistroVersionCacheForTest clears the version-probe cache.
func resetWSLDistroVersionCacheForTest() {
	wslDistroVersionCacheMu.Lock()
	defer wslDistroVersionCacheMu.Unlock()
	wslDistroVersionCacheDone = false
	wslDistroVersionCache = nil
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

// wslENVForwardExact are additional non-prefixed keys to forward (network proxy).
var wslENVForwardExact = map[string]struct{}{
	"HTTP_PROXY":  {},
	"HTTPS_PROXY": {},
	"NO_PROXY":    {},
	"http_proxy":  {},
	"https_proxy": {},
	"no_proxy":    {},
}

func shouldForwardToWSL(key string) bool {
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
	return false
}

// appendWSLENVForwarding computes the WSLENV variable from the forwardable keys
// present in env and merges it with any existing WSLENV. The returned slice is a
// copy with WSLENV set; env is not mutated. WSLENV uses a colon-separated list
// of variable names (no path-translation flags: the forwarded values are keys /
// URLs, not Windows paths).
func appendWSLENVForwarding(env []string) []string {
	names := []string{}
	seen := map[string]struct{}{}
	addName := func(name string) {
		key := strings.ToUpper(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		names = append(names, name)
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
			names = append(names, trimmed)
		}
	}

	for _, kv := range env {
		k := envEntryKey(kv)
		if shouldForwardToWSL(k) {
			addName(k)
		}
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
