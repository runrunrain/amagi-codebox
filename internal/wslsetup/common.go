package wslsetup

import "strings"

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
// bash -lc script, escaping internal single quotes via the '\'' idiom.
func bashSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}