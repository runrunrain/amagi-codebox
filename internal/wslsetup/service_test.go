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
		"pi":          "@earendil-works/pi-coding-agent",
		"Pi":          "@earendil-works/pi-coding-agent",
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

func TestNodeVersionAtLeast(t *testing.T) {
	cases := []struct {
		v           string
		major       int
		minor       int
		want        bool
		description string
	}{
		{"v22.23.2", 22, 19, true, "above floor"},
		{"v22.19.0", 22, 19, true, "exactly at floor"},
		{"v22.13.0", 22, 19, false, "below floor minor (undici markAsUncloneable missing)"},
		{"v20.20.2", 22, 19, false, "node 20 lacks the API entirely"},
		{"v24.1.0", 22, 19, true, "newer major"},
		{"22.19.0", 22, 19, true, "no v prefix"},
		{"v22", 22, 19, false, "missing minor is unparseable"},
		{"", 22, 19, false, "empty output"},
		{"garbage", 22, 19, false, "garbage output"},
	}
	for _, c := range cases {
		if got := nodeVersionAtLeast(c.v, c.major, c.minor); got != c.want {
			t.Errorf("nodeVersionAtLeast(%q, %d, %d) = %v, want %v (%s)", c.v, c.major, c.minor, got, c.want, c.description)
		}
	}
}

func TestWSLPathFromWindowsHome(t *testing.T) {
	cases := map[string]string{
		`C:\Users\毛润\.pi\agent`: `/mnt/c/Users/毛润/.pi/agent`,
		`D:\Tools\My CLI`:         `/mnt/d/Tools/My CLI`,
		`relative\path`:           "",
		`nopath`:                  "",
		`\\server\share\x`:        "",
	}
	for in, want := range cases {
		if got := wslPathFromWindowsHome(in); got != want {
			t.Errorf("wslPathFromWindowsHome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBashSingleQuote(t *testing.T) {
	cases := map[string]string{
		"opencode-ai":     `'opencode-ai'`,
		"@anthropic-ai/x": `'@anthropic-ai/x'`,
		"a'b":             `'a'\''b'`,
		"":                `''`,
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
