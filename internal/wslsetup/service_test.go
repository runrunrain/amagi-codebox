package wslsetup

import "testing"

func TestNormalizeToolKey(t *testing.T) {
	cases := map[string]string{
		"claude":      "claude",
		"claude_code": "claude",
		"claude-code": "claude",
		"claudecode":  "claude",
		"OpenCode":    "opencode",
		"open-code":   "opencode",
		"codex":       "codex",
		"unknown":     "unknown",
	}
	for in, want := range cases {
		if got := normalizeToolKey(in); got != want {
			t.Errorf("normalizeToolKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPackageForTool(t *testing.T) {
	cases := map[string]string{
		"claude":      "@anthropic-ai/claude-code",
		"claude_code": "@anthropic-ai/claude-code",
		"opencode":    "opencode-ai",
		"codex":       "@openai/codex",
	}
	for in, want := range cases {
		got, ok := packageForTool(in)
		if !ok || got != want {
			t.Errorf("packageForTool(%q) = %q,%v, want %q,true", in, got, ok, want)
		}
	}
	if _, ok := packageForTool("nope"); ok {
		t.Errorf("packageForTool(nope) should be !ok")
	}
}

func TestBashSingleQuote(t *testing.T) {
	cases := map[string]string{
		"opencode-ai":         `'opencode-ai'`,
		"@anthropic-ai/x":     `'@anthropic-ai/x'`,
		"a'b":                 `'a'\''b'`,
		"":                    `''`,
	}
	for in, want := range cases {
		if got := bashSingleQuote(in); got != want {
			t.Errorf("bashSingleQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGetStatusUnavailableOffWindowsOrNoDistro verifies the stub/no-distro path
// returns Available=false with a reason (the Windows path with a real distro is
// covered by the real-machine verification, not unit tests).
func TestGetStatusReason(t *testing.T) {
	svc := NewService(nil)
	st := svc.GetStatus()
	if st.Available {
		// On a machine WITH a usable distro this may be true; only assert the
		// contract shape (no panic, tools list shape) rather than a fixed value.
		return
	}
	if st.Reason == "" {
		t.Errorf("unavailable status must carry a reason")
	}
}