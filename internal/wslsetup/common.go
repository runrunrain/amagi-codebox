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

// wslPathFromWindowsHome converts an absolute Windows path (C:\Users\...) to its
// WSL drvfs mount path (/mnt/c/Users/...). Returns "" for paths it cannot map.
// Parsing is hand-rolled (not filepath.VolumeName) so behavior is identical on
// every host OS — the package builds on macOS/Linux and the unit tests cover it.
func wslPathFromWindowsHome(winPath string) string {
	if len(winPath) < 3 || winPath[1] != ':' {
		return ""
	}
	c := winPath[0]
	isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	if !isLetter {
		return ""
	}
	drive := strings.ToLower(winPath[0:1])
	rest := strings.ReplaceAll(winPath[2:], `\`, "/")
	return "/mnt/" + drive + rest
}
