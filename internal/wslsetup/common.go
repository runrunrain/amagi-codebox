package wslsetup

import (
	"strconv"
	"strings"
)

// normalizeToolKey canonicalizes a tool identifier from the frontend/session
// layer (which uses variants like "claude_code", "claude-code") to the keys used
// in cliPackages.
func normalizeToolKey(tool string) string {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "claude", "claude_code", "claude-code", "claudecode":
		return "claude"
	case "opencode", "open-code", "open_code":
		return "opencode"
	case "codex":
		return "codex"
	default:
		return strings.ToLower(strings.TrimSpace(tool))
	}
}

// bashSingleQuote wraps a token in POSIX single quotes for safe embedding in a
// bash -lc script, escaping internal single quotes with the classic sequence
// (single quote becomes: quote, backslash-quote, quote).
func bashSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// nodeVersionAtLeast reports whether a `node -v` output (e.g. "v22.19.0") is at
// least major.minor. Unparseable input reports false so the caller installs.
func nodeVersionAtLeast(v string, major, minor int) bool {
	parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".", 3)
	if len(parts) < 2 {
		return false
	}
	maj, errMaj := strconv.Atoi(parts[0])
	min, errMin := strconv.Atoi(parts[1])
	if errMaj != nil || errMin != nil {
		return false
	}
	return maj > major || (maj == major && min >= minor)
}

// wslPathFromWindowsHome（Windows 路径→/mnt DrvFS 映射）已随 ensurePiConfig
// 整树播种（P3-S0 停用）一并删除：仓库内不再有消费方（旧用途是把 Windows
// ~/.pi/agent 喂给 distro 内 cp -r 播种）。
