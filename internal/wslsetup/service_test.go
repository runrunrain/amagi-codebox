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
		"OMP":         "omp", // lowercase passthrough covers Oh My Pi
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
		"omp":         "@oh-my-pi/pi-coding-agent",
		"OMP":         "@oh-my-pi/pi-coding-agent",
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

// TestAllFiveCLIsMapped pins the五 CLI coverage requirement: every key in the
// canonical order resolves to a distinct npm package, and omp (the historical
// B7 exclusion, superseded by the Windows/WSL unified-management requirement)
// is present.
func TestAllFiveCLIsMapped(t *testing.T) {
	if len(cliToolOrder) != 5 {
		t.Fatalf("cliToolOrder has %d entries, want 5", len(cliToolOrder))
	}
	seen := map[string]bool{}
	for _, key := range cliToolOrder {
		pkg, ok := packageForTool(key)
		if !ok || pkg == "" {
			t.Errorf("cliToolOrder key %q has no npm package", key)
		}
		if seen[pkg] {
			t.Errorf("duplicate npm package %q across CLI keys", pkg)
		}
		seen[pkg] = true
	}
	if _, ok := packageForTool("omp"); !ok {
		t.Error("omp must be installable into WSL (five-CLI coverage)")
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

// TestUnsupportedPlatformContract pins the off-Windows behavior for every
// public method: a nil runner (explicitly injected so the test is
// deterministic on Windows hosts too) must yield structured failures — never
// a Go error for supported tools, and the plain error for unknown tools
// BEFORE the platform check, mirroring the historical stub order.
func TestUnsupportedPlatformContract(t *testing.T) {
	injectRunner(t, nil)
	svc := NewService(nil)

	if st := svc.GetStatus(); st.Available || st.Reason != errInstallUnsupported {
		t.Errorf("GetStatus() = %+v, want unavailable with %q", st, errInstallUnsupported)
	}

	res, err := svc.InstallTool("pi")
	if err != nil || res.Success || res.Error != errInstallUnsupported {
		t.Errorf("InstallTool(pi) = %+v, %v; want structured %q failure", res, err, errInstallUnsupported)
	}
	res, err = svc.UpdateTool("pi")
	if err != nil || res.Success || res.Error != errUpdateUnsupported {
		t.Errorf("UpdateTool(pi) = %+v, %v; want structured %q failure", res, err, errUpdateUnsupported)
	}
	sres, err := svc.InstallSearchTools()
	if err != nil || sres.Success || sres.Error != errInstallUnsupported || sres.Tool != SearchToolsKey {
		t.Errorf("InstallSearchTools() = %+v, %v; want structured unsupported failure", sres, err)
	}

	// Unknown tools are rejected with a Go error regardless of platform.
	for _, bad := range []string{"nope", ""} {
		if _, err := svc.InstallTool(bad); err == nil {
			t.Errorf("InstallTool(%q) must return an error for unknown tools", bad)
		}
		if _, err := svc.UpdateTool(bad); err == nil {
			t.Errorf("UpdateTool(%q) must return an error for unknown tools", bad)
		}
	}
}
