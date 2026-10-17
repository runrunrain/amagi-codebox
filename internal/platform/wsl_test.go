package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMain pins the WSL distro lister to "no distro installed" for the whole
// package by default. This keeps every existing Windows resolver test running in
// its intended world (WSL absent → pwsh/cmd/attach paths), matching CI machines
// that have no WSL. Tests that specifically exercise the WSL branch override the
// lister locally via withFakeWSLDistros.
func TestMain(m *testing.M) {
	wslDistroLister = func(env []string) ([]byte, error) { return nil, nil }
	resetWSLDistroCacheForTest()
	os.Exit(m.Run())
}

// withFakeWSLDistros installs a fake lister returning the given distro names
// (UTF-16LE with BOM, mirroring real `wsl.exe -l -q` output) and clears the
// cache. It restores the default no-WSL lister on cleanup.
func withFakeWSLDistros(t *testing.T, names ...string) {
	t.Helper()
	raw := encodeUTF16LEWithBOM(strings.Join(names, "\r\n") + "\r\n")
	wslDistroLister = func(env []string) ([]byte, error) { return raw, nil }
	resetWSLDistroCacheForTest()
	t.Cleanup(func() {
		wslDistroLister = func(env []string) ([]byte, error) { return nil, nil }
		resetWSLDistroCacheForTest()
	})
}

func encodeUTF16LEWithBOM(s string) []byte {
	out := []byte{0xFF, 0xFE}
	for _, r := range s {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

func TestDecodeWSLListOutputUTF16LE(t *testing.T) {
	raw := encodeUTF16LEWithBOM("Ubuntu-24.04\r\ndocker-desktop\r\n")
	got := decodeWSLListOutput(raw)
	if !strings.Contains(got, "Ubuntu-24.04") || !strings.Contains(got, "docker-desktop") {
		t.Fatalf("decode failed: %q", got)
	}
}

func TestAvailableWSLDistrosFiltersReserved(t *testing.T) {
	withFakeWSLDistros(t, "docker-desktop", "Ubuntu-24.04", "docker-desktop-data")
	got := availableWSLDistros(nil)
	if len(got) != 1 || got[0] != "Ubuntu-24.04" {
		t.Fatalf("expected only Ubuntu-24.04, got %v", got)
	}
}

func TestAvailableWSLDistrosEmptyWhenOnlyReserved(t *testing.T) {
	withFakeWSLDistros(t, "docker-desktop", "docker-desktop-data")
	if hasUsableWSLDistro(nil) {
		t.Fatalf("expected no usable distro when only reserved present")
	}
}

func TestDefaultWSLDistroReturnsFirstUsable(t *testing.T) {
	withFakeWSLDistros(t, "docker-desktop", "Ubuntu-24.04", "Debian")
	if got := DefaultWSLDistro(nil); got != "Ubuntu-24.04" {
		t.Fatalf("expected Ubuntu-24.04, got %q", got)
	}
}

func TestShouldForwardToWSL(t *testing.T) {
	// m-5：ALL_PROXY/all_proxy 与 HTTP(S)_PROXY/NO_PROXY 同入精确白名单
	//（socks 类 scheme 与仅认 ALL_PROXY 的实现；aiproxy 分流注入携带两键）。
	forward := []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "OPENAI_API_KEY", "CLAUDE_CODE_EFFORT_LEVEL", "PI_CODING_AGENT_DIR", "HTTPS_PROXY", "ALL_PROXY", "all_proxy", "OPENCODE_CONFIG"}
	for _, k := range forward {
		if !shouldForwardToWSL(k) {
			t.Errorf("expected %q to be forwarded", k)
		}
	}
	skip := []string{"PATH", "SystemRoot", "WSLENV", "USERPROFILE", ""}
	for _, k := range skip {
		if shouldForwardToWSL(k) {
			t.Errorf("expected %q NOT to be forwarded", k)
		}
	}
}

// TestShouldForwardToWSLExtraKeys pins the exported forward-face predicate used
// by the launcher-side pre-forwarding strip (StripWSLHostPathEnv): extra custom
// keys (envvars.json) join the face case-insensitively, reserved keys never do,
// and absent extras leave unknown keys out of the face.
func TestShouldForwardToWSLExtraKeys(t *testing.T) {
	if !ShouldForwardToWSL("tavily_api_key", "TAVILY_API_KEY") {
		t.Error("extra key must match case-insensitively")
	}
	if !ShouldForwardToWSL("TAVILY_API_KEY", " tavily_api_key ") {
		t.Error("extra key must trim whitespace before matching")
	}
	if ShouldForwardToWSL("MY_TOOL_HOME") {
		t.Error("unknown key without extras must not forward")
	}
	for _, reserved := range []string{"PATH", "PATHEXT", "WSLENV"} {
		if ShouldForwardToWSL(reserved, reserved) {
			t.Errorf("reserved key %q must never forward, even when listed as extra", reserved)
		}
	}
	// 与 appendWSLENVForwarding 的判据一致性是剥离正确性的前提（剥离面 ⊆ 转发面）。
	for _, k := range []string{"ANTHROPIC_API_KEY", "CODEX_HOME", "PI_SESSION_FILE", "OPENCODE_CONFIG", "HTTPS_PROXY"} {
		if ShouldForwardToWSL(k) != shouldForwardToWSL(k) {
			t.Errorf("exported/internal predicate disagree on %q", k)
		}
	}
}

// TestAppendWSLENVForwardingOpenCodeConfig pins the T4 carrier: OPENCODE_CONFIG
// (pointing at the distro-side amagi-codebox.json written by the launcher) is
// listed in WSLENV so it crosses the boundary, while the content-carrier
// OPENCODE_CONFIG_CONTENT stays out (no OPENCODE_ prefix whitelist).
func TestAppendWSLENVForwardingOpenCodeConfig(t *testing.T) {
	env := []string{
		"PATH=/x",
		"OPENCODE_CONFIG=/home/u/.config/opencode/amagi-codebox.json",
		"OPENCODE_CONFIG_CONTENT={\"provider\":{}}",
	}
	out := appendWSLENVForwarding(env)
	wslenv := envValue(out, "WSLENV")
	if !strings.Contains(wslenv, "OPENCODE_CONFIG") {
		t.Fatalf("WSLENV missing OPENCODE_CONFIG: %q", wslenv)
	}
	if strings.Contains(wslenv, "OPENCODE_CONFIG_CONTENT") {
		t.Fatalf("WSLENV must not carry the content env var: %q", wslenv)
	}
	if v := envValue(out, "OPENCODE_CONFIG"); v != "/home/u/.config/opencode/amagi-codebox.json" {
		t.Fatalf("OPENCODE_CONFIG value must be forwarded verbatim (Linux path, no translation), got %q", v)
	}
}

func TestAppendWSLENVForwardingBuildsColonList(t *testing.T) {
	env := []string{
		"PATH=/x",
		"ANTHROPIC_API_KEY=sk",
		"ANTHROPIC_BASE_URL=http://x",
		"USERPROFILE=C:/Users/a",
	}
	out := appendWSLENVForwarding(env)
	wslenv := envValue(out, "WSLENV")
	if !strings.Contains(wslenv, "ANTHROPIC_API_KEY") || !strings.Contains(wslenv, "ANTHROPIC_BASE_URL") {
		t.Fatalf("WSLENV missing forwarded keys: %q", wslenv)
	}
	if strings.Contains(wslenv, "PATH") || strings.Contains(wslenv, "USERPROFILE") {
		t.Fatalf("WSLENV must not include Windows-only keys: %q", wslenv)
	}
}

// TestAppendWSLENVForwardingExtraKeys verifies user-configured custom env keys
// (envvars.json: TAVILY_API_KEY etc.) cross the boundary via the extra-key
// list: forwarded when present, absent keys never listed, reserved keys
// (WSLENV/PATH) rejected, matching case-insensitive.
func TestAppendWSLENVForwardingExtraKeys(t *testing.T) {
	env := []string{
		"PATH=/x",
		"TAVILY_API_KEY=tk",
		"FIRECRAWL_API_KEY=fk",
		"USERPROFILE=C:/Users/a",
	}
	out := appendWSLENVForwarding(env, "tavily_api_key", "FIRECRAWL_API_KEY", "MISSING_KEY", "PATH", "WSLENV", "  ")
	wslenv := envValue(out, "WSLENV")
	if !strings.Contains(wslenv, "TAVILY_API_KEY") || !strings.Contains(wslenv, "FIRECRAWL_API_KEY") {
		t.Fatalf("WSLENV missing extra custom keys: %q", wslenv)
	}
	if strings.Contains(wslenv, "MISSING_KEY") {
		t.Fatalf("WSLENV must not list keys absent from env: %q", wslenv)
	}
	if strings.Contains(wslenv, "PATH") {
		t.Fatalf("WSLENV must not forward reserved keys: %q", wslenv)
	}
}

// TestWindowsResolverDefaultsToWSLWhenDistroAvailable verifies the WSL branch:
// with a usable distro, an embedded Claude launch resolves to BootstrapWSL with
// the bare command name and a WSLENV that carries the injected key.
func TestWindowsResolverDefaultsToWSLWhenDistroAvailable(t *testing.T) {
	withFakeWSLDistros(t, "Ubuntu-24.04")
	// wsl.exe must resolve on PATH for the candidate to be selected.
	dir := t.TempDir()
	wslPath := dir + string(os.PathSeparator) + "wsl.exe"
	if err := os.WriteFile(wslPath, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:    "claudecode",
		LaunchMode: "embedded",
		WorkDir:    `D:\WorkPace`,
		Env:        []string{"PATH=" + dir, "ANTHROPIC_API_KEY=sk-test"},
		CLIArgs:    []string{"--session-id", "abc"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec.BootstrapMode != BootstrapWSL {
		t.Fatalf("bootstrap = %q, want %q", spec.BootstrapMode, BootstrapWSL)
	}
	if spec.Shell == nil || spec.Shell.Key != "wsl" {
		t.Fatalf("shell key not wsl: %+v", spec.Shell)
	}
	if spec.CLI.Path != "claude" {
		t.Fatalf("cli path = %q, want bare \"claude\"", spec.CLI.Path)
	}
	if wslenv := envValue(spec.Env.Variables, "WSLENV"); !strings.Contains(wslenv, "ANTHROPIC_API_KEY") {
		t.Fatalf("WSLENV missing injected key: %q", wslenv)
	}
}

// TestWindowsResolverWSLLaunchesWhenCLIAbsentFromWindowsPATH is the C2
// regression: the CLI is installed ONLY inside WSL (not on the Windows PATH), so
// the Windows CLI resolver would return cli_not_found. The WSL branch must run
// BEFORE that resolution and succeed with the bare command name.
func TestWindowsResolverWSLLaunchesWhenCLIAbsentFromWindowsPATH(t *testing.T) {
	withFakeWSLDistros(t, "Ubuntu-24.04")
	dir := t.TempDir()
	// Only wsl.exe exists on PATH; claude/opencode/codex do NOT.
	if err := os.WriteFile(dir+string(os.PathSeparator)+"wsl.exe", []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:    "opencode",
		LaunchMode: "embedded",
		WorkDir:    `D:\proj`,
		Env:        []string{"PATH=" + dir},
	})
	if err != nil {
		t.Fatalf("resolve must not fail when CLI lives only in WSL: %v", err)
	}
	if spec.BootstrapMode != BootstrapWSL {
		t.Fatalf("bootstrap = %q, want wsl", spec.BootstrapMode)
	}
	if spec.CLI.Path != "opencode" {
		t.Fatalf("cli path = %q, want bare \"opencode\"", spec.CLI.Path)
	}
}

// TestWindowsResolverExplicitWSLTerminalModeDoesNotEmitInline is the M2
// regression: a non-embedded (terminal) launch that resolves to WSL must not
// fall into the pwsh/cmd inline tail (which would emit a malformed wsl line). It
// should degrade to a direct command with a warning.
func TestWindowsResolverExplicitWSLTerminalModeDoesNotEmitInline(t *testing.T) {
	withFakeWSLDistros(t, "Ubuntu-24.04")
	dir := t.TempDir()
	if err := os.WriteFile(dir+string(os.PathSeparator)+"wsl.exe", []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// claude present on Windows PATH so CLI resolution succeeds and we reach the tail.
	if err := os.WriteFile(dir+string(os.PathSeparator)+"claude.exe", []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "claudecode",
		LaunchMode:         "terminal",
		RequestedShellPath: "wsl",
		Env:                []string{"PATH=" + dir},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec.BootstrapMode == BootstrapShellInline {
		t.Fatalf("terminal-mode wsl must not emit inline shell command")
	}
	if spec.BootstrapMode != BootstrapDirectCommand {
		t.Fatalf("bootstrap = %q, want direct-command fallback", spec.BootstrapMode)
	}
}

// TestWindowsResolverScriptWrapperTerminalModeUsesWindowsShellNotWSL is the M-1
// regression: on a machine WITH a usable WSL distro, a terminal-mode Claude
// launch whose CLI resolves to an npm script wrapper (.cmd) must still run inline
// via a Windows shell (pwsh/cmd) — NOT degrade to a bare direct exec of the
// script (which CreateProcess cannot launch) and NOT pick wsl.
func TestWindowsResolverScriptWrapperTerminalModeUsesWindowsShellNotWSL(t *testing.T) {
	withFakeWSLDistros(t, "Ubuntu-24.04")
	dir := t.TempDir()
	sep := string(os.PathSeparator)
	if err := os.WriteFile(dir+sep+"wsl.exe", []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Windows shells available so the non-wsl default can resolve.
	if err := os.WriteFile(dir+sep+"powershell.exe", []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+sep+"cmd.exe", []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Claude resolves to an npm .cmd script wrapper on PATH.
	if err := os.WriteFile(dir+sep+"claude.cmd", []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:    "claudecode",
		LaunchMode: "terminal",
		Env:        []string{"PATH=" + dir},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec.BootstrapMode != BootstrapShellInline {
		t.Fatalf("bootstrap = %q, want shell-inline (Windows shell runs the .cmd)", spec.BootstrapMode)
	}
	if spec.Shell == nil || strings.EqualFold(spec.Shell.Key, "wsl") {
		t.Fatalf("script wrapper must use a Windows shell, got %+v", spec.Shell)
	}
}

// TestWindowsResolverFallsBackWhenNoWSLDistro verifies that with wsl.exe present
// but NO usable distro, the resolver does not pick WSL (falls through to the
// existing pwsh/cmd/attach behavior). The stub dir also provides an opencode
// shim and cmd.exe so resolution succeeds on hosts that lack the real CLIs
// (the test would otherwise depend on the developer machine's PATH).
func TestWindowsResolverFallsBackWhenNoWSLDistro(t *testing.T) {
	// Default TestMain lister already returns no distro; be explicit.
	wslDistroLister = func(env []string) ([]byte, error) { return nil, nil }
	resetWSLDistroCacheForTest()
	dir := t.TempDir()
	for _, name := range []string{"wsl.exe", "cmd.exe", "opencode.cmd"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stub"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:    "opencode",
		LaunchMode: "embedded",
		Env:        []string{"PATH=" + dir},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec.BootstrapMode == BootstrapWSL {
		t.Fatalf("must not select WSL when no usable distro; got %q", spec.BootstrapMode)
	}
	if spec.Shell != nil && strings.EqualFold(spec.Shell.Key, "wsl") {
		t.Fatalf("resolved shell must not be wsl without a usable distro; got %+v", spec.Shell)
	}
}

// withFakeWSLVerboseList installs a fake `wsl -l -v` lister returning the given
// raw verbose output and clears the version cache. Restores a failing lister on
// cleanup (the package default for versions).
func withFakeWSLVerboseList(t *testing.T, raw []byte) {
	t.Helper()
	wslDistroVersionLister = func(env []string) ([]byte, error) { return raw, nil }
	resetWSLDistroVersionCacheForTest()
	t.Cleanup(func() {
		wslDistroVersionLister = func(env []string) ([]byte, error) { return nil, errors.New("not supported") }
		resetWSLDistroVersionCacheForTest()
	})
}

func TestParseWSLVerboseListLine(t *testing.T) {
	cases := []struct {
		line     string
		wantName string
		wantVer  int
		wantOK   bool
	}{
		{"* Ubuntu-24.04    Running    2", "Ubuntu-24.04", 2, true},
		{"  Debian          Stopped    1", "Debian", 1, true},
		// Distros whose names contain spaces: STATE is the second-to-last field.
		{"  Ubuntu 22.04 LTS  Stopped  1", "Ubuntu 22.04 LTS", 1, true},
		{"  NAME              STATE           VERSION", "", 0, false},
		{"", "", 0, false},
		{"  Ubuntu-24.04    Running", "", 0, false}, // missing VERSION column
	}
	for _, c := range cases {
		name, _, ver, ok := parseWSLVerboseListLine(c.line)
		if ok != c.wantOK || name != c.wantName || ver != c.wantVer {
			t.Errorf("parse(%q) = (%q,%d,%v), want (%q,%d,%v)", c.line, name, ver, ok, c.wantName, c.wantVer, c.wantOK)
		}
	}
}

func TestWSLDistroStatesIncludesReservedDistros(t *testing.T) {
	raw := encodeUTF16LEWithBOM("  NAME            STATE      VERSION\r\n* Ubuntu-24.04   Running    2\r\n  docker-desktop Running    2\r\n  Debian         Stopped    1\r\n")
	withFakeWSLVerboseList(t, raw)
	got := WSLDistroStates(nil)
	if got["Ubuntu-24.04"] != "Running" || got["docker-desktop"] != "Running" || got["Debian"] != "Stopped" || len(got) != 3 {
		t.Fatalf("states = %v, want {Ubuntu-24.04:Running, docker-desktop:Running, Debian:Stopped}", got)
	}
}

func TestWSLDistroVersionsFromVerboseOutput(t *testing.T) {
	raw := encodeUTF16LEWithBOM("  NAME            STATE      VERSION\r\n* Ubuntu-24.04   Running    2\r\n  Debian         Stopped    1\r\n")
	withFakeWSLVerboseList(t, raw)
	got := WSLDistroVersions(nil)
	if got["Ubuntu-24.04"] != 2 || got["Debian"] != 1 || len(got) != 2 {
		t.Fatalf("versions = %v, want {Ubuntu-24.04:2, Debian:1}", got)
	}
}

func TestWSLDistroVersionsEmptyWhenUnsupported(t *testing.T) {
	wslDistroVersionLister = func(env []string) ([]byte, error) { return nil, errors.New("flag -v not supported") }
	resetWSLDistroVersionCacheForTest()
	if got := WSLDistroVersions(nil); len(got) != 0 {
		t.Fatalf("versions = %v, want empty on probe error", got)
	}
}

// TestWindowsResolverUnresolvableRequestedShellStaysOutOfWSL 验证：WSL 可用的
// Windows 机器上，用户显式请求但解析失败的 shell（如从其他平台持久化来的
// "zsh"）回退到非 WSL shell（pwsh/cmd），而不是静默切进 WSL——"选了
// PowerShell 7 却启动进 WSL2"的根因回归测试。WSL 仅在用户未指定 shell 时
// 作为平台默认。
func TestWindowsResolverUnresolvableRequestedShellStaysOutOfWSL(t *testing.T) {
	withFakeWSLDistros(t, "Ubuntu-24.04")
	dir := t.TempDir()
	for _, name := range []string{"wsl.exe", "pwsh.exe", "opencode.cmd"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stub"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "opencode",
		LaunchMode:         "embedded",
		RequestedShellPath: "zsh",
		Env:                []string{"PATH=" + dir},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec.BootstrapMode == BootstrapWSL {
		t.Fatalf("bootstrap = %q, must not enter WSL for an unresolvable explicit shell request", spec.BootstrapMode)
	}
	if spec.Shell == nil {
		t.Fatal("expected a fallback shell")
	}
	if strings.EqualFold(spec.Shell.Key, "wsl") {
		t.Fatalf("fallback shell = wsl (%q), want a non-WSL Windows shell", spec.Shell.Path)
	}
	if !strings.EqualFold(spec.Shell.Key, "pwsh") {
		t.Fatalf("fallback shell key = %q, want pwsh (first non-WSL candidate)", spec.Shell.Key)
	}
	// 未指定 shell 时 WSL 默认保持不变（文档化的平台默认行为）。
	specNoShell, err := resolver.Resolve(ResolveRequest{
		AppType:    "opencode",
		LaunchMode: "embedded",
		Env:        []string{"PATH=" + dir},
	})
	if err != nil {
		t.Fatalf("resolve (no shell): %v", err)
	}
	if specNoShell.BootstrapMode != BootstrapWSL {
		t.Fatalf("no-shell bootstrap = %q, want %q (WSL stays the platform default)", specNoShell.BootstrapMode, BootstrapWSL)
	}
}

// writeWSLConfig writes a .wslconfig into dir (helper for WSLNetworkingMode
// tests; distinct temp dirs double as distinct cache keys).
func writeWSLConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".wslconfig"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseWSLConfigNetworkingMode(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"mirrored", "[wsl2]\nnetworkingMode=mirrored\n", "mirrored"},
		{"nat", "[wsl2]\nnetworkingMode=nat\n", "nat"},
		{"case-insensitive key and value", "[wsl2]\nNETWORKINGMODE = Mirrored\n", "mirrored"},
		{"spaces around equals", "[wsl2]\n networkingMode = mirrored \n", "mirrored"},
		{"hash full-line comment", "# wsl config\n[wsl2]\nnetworkingMode=mirrored\n", "mirrored"},
		{"semicolon full-line comment", "; comment\n[wsl2]\nnetworkingMode=nat\n", "nat"},
		{"inline trailing comment", "[wsl2]\nnetworkingMode=mirrored # loopback shared with host\n", "mirrored"},
		{"section case-insensitive", "[WSL2]\nnetworkingMode=mirrored\n", "mirrored"},
		{"key outside any section ignored", "networkingMode=mirrored\n[wsl2]\n", ""},
		{"other section ignored", "[automount]\nnetworkingMode=mirrored\n", ""},
		{"no key", "[wsl2]\nmemory=4GB\nprocessors=2\n", ""},
		{"unknown value fails safe", "[wsl2]\nnetworkingMode=bridged\n", ""},
		{"empty value fails safe", "[wsl2]\nnetworkingMode=\n", ""},
		{"first key wins", "[wsl2]\nnetworkingMode=nat\nnetworkingMode=mirrored\n", "nat"},
		{"second wsl2 section ignored after first key", "[wsl2]\nmemory=4GB\n[wsl2]\nnetworkingMode=mirrored\n", "mirrored"},
		{"garbage lines tolerated", "[wsl2]\nnot-a-key-value\nnetworkingMode=mirrored\n", "mirrored"},
		{"crlf line endings", "[wsl2]\r\nnetworkingMode=mirrored\r\n", "mirrored"},
		{"empty content", "", ""},
	}
	for _, tc := range cases {
		if got := parseWSLConfigNetworkingMode(tc.content); got != tc.want {
			t.Errorf("%s: parse = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWSLNetworkingModeFromEnv(t *testing.T) {
	// mirrored via USERPROFILE (the key wsl.exe itself uses).
	dir := t.TempDir()
	writeWSLConfig(t, dir, "[wsl2]\nnetworkingMode=mirrored\n")
	if got := WSLNetworkingMode([]string{"USERPROFILE=" + dir}); got != "mirrored" {
		t.Fatalf("USERPROFILE probe = %q, want mirrored", got)
	}

	// HOME fallback when USERPROFILE is absent.
	dirHome := t.TempDir()
	writeWSLConfig(t, dirHome, "[wsl2]\nnetworkingMode=nat\n")
	if got := WSLNetworkingMode([]string{"HOME=" + dirHome}); got != "nat" {
		t.Fatalf("HOME probe = %q, want nat", got)
	}

	// USERPROFILE wins over HOME (faithful to wsl.exe's profile resolution).
	if got := WSLNetworkingMode([]string{"USERPROFILE=" + dir, "HOME=" + dirHome}); got != "mirrored" {
		t.Fatalf("USERPROFILE must take precedence: %q", got)
	}

	// Missing file: "" (fail-safe as NAT).
	if got := WSLNetworkingMode([]string{"USERPROFILE=" + t.TempDir()}); got != "" {
		t.Fatalf("missing .wslconfig = %q, want \"\"", got)
	}

	// UTF-16LE with BOM (Notepad legacy save): decode path must still parse.
	dir16 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir16, ".wslconfig"), encodeUTF16LEWithBOM("[wsl2]\r\nnetworkingMode=mirrored\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := WSLNetworkingMode([]string{"USERPROFILE=" + dir16}); got != "mirrored" {
		t.Fatalf("UTF-16 probe = %q, want mirrored", got)
	}

	// UTF-8 BOM.
	dir8 := t.TempDir()
	bom := append([]byte{0xEF, 0xBB, 0xBF}, []byte("[wsl2]\nnetworkingMode=nat\n")...)
	if err := os.WriteFile(filepath.Join(dir8, ".wslconfig"), bom, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := WSLNetworkingMode([]string{"USERPROFILE=" + dir8}); got != "nat" {
		t.Fatalf("UTF-8 BOM probe = %q, want nat", got)
	}
}

// TestWSLNetworkingModeFreshRead pins the m-4 semantics: every call reads the
// .wslconfig directly (no process-level cache) — after the user edits the
// file and runs `wsl --shutdown`, a retry in the SAME process must observe the
// new mode. Distinct homes probe their own files independently.
func TestWSLNetworkingModeFreshRead(t *testing.T) {
	dir := t.TempDir()
	writeWSLConfig(t, dir, "[wsl2]\nnetworkingMode=mirrored\n")
	env := []string{"USERPROFILE=" + dir}
	if got := WSLNetworkingMode(env); got != "mirrored" {
		t.Fatalf("first probe = %q, want mirrored", got)
	}
	// Rewrite the file（用户按指引改配置 + wsl --shutdown）：下一次调用必须
	// 读到新值——旧进程级缓存会让拒绝文案的重试指引同进程失效（m-4）。
	writeWSLConfig(t, dir, "[wsl2]\nnetworkingMode=nat\n")
	if got := WSLNetworkingMode(env); got != "nat" {
		t.Fatalf("post-edit probe = %q, want nat (fresh read, no cache)", got)
	}
	// A different home (aiproxy/envcheck tests use distinct temp dirs) probes its own file.
	other := t.TempDir()
	writeWSLConfig(t, other, "[wsl2]\nnetworkingMode=mirrored\n")
	if got := WSLNetworkingMode([]string{"USERPROFILE=" + other}); got != "mirrored" {
		t.Fatalf("second home probe = %q, want mirrored", got)
	}
}

// TestWSLFileMTime pins the M-1 freshness probe: `stat -c %Y` seconds inside
// the distro, tolerant output decoding, and fail-as-false for missing paths /
// probe failures / unparsable output (callers must treat false as
// "indeterminable", never as 0). 经 wslFileMTimeForOS 可测核心验证（原生
// WSLFileMTime 在非 Windows 宿主提前返回，注入 runner 不可达；原生门行为
// 另行钉住）。
func TestWSLFileMTime(t *testing.T) {
	orig := wslScriptRunner
	t.Cleanup(func() { wslScriptRunner = orig })

	wslScriptRunner = func(_, script string) ([]byte, error) {
		if strings.Contains(script, "/home/u/.pi/agent/auth.json") {
			return []byte("1792992000\r\n"), nil
		}
		return nil, errors.New("stat failed")
	}
	if sec, ok := wslFileMTimeForOS("windows", "Ubuntu", "/home/u/.pi/agent/auth.json"); !ok || sec != 1792992000 {
		t.Fatalf("wslFileMTimeForOS = (%d, %v), want (1792992000, true)", sec, ok)
	}
	// 路径不存在（stat 非零退出）→ ok=false。
	if _, ok := wslFileMTimeForOS("windows", "Ubuntu", "/home/u/.pi/agent/missing.json"); ok {
		t.Fatal("missing path must report ok=false")
	}
	// 输出不可解析 → ok=false，不得当作 0 值。
	wslScriptRunner = func(_, _ string) ([]byte, error) { return []byte("not-a-number"), nil }
	if sec, ok := wslFileMTimeForOS("windows", "Ubuntu", "/home/u/.pi/agent/auth.json"); ok || sec != 0 {
		t.Fatalf("unparsable output = (%d, %v), want (0, false)", sec, ok)
	}
	// 非 Linux 绝对路径 / 空 distro → ok=false（runner 不被触达）。
	probeCalls := 0
	wslScriptRunner = func(_, _ string) ([]byte, error) { probeCalls++; return []byte("1"), nil }
	if _, ok := wslFileMTimeForOS("windows", "Ubuntu", "relative/path"); ok {
		t.Fatal("relative path must report ok=false")
	}
	if _, ok := wslFileMTimeForOS("windows", "", "/home/u/x"); ok {
		t.Fatal("empty distro must report ok=false")
	}
	// 非 Windows 宿主门：runner 可用也恒 (0,false)（原生行为，不扩大）。
	if sec, ok := wslFileMTimeForOS("darwin", "Ubuntu", "/home/u/.pi/agent/auth.json"); ok || sec != 0 {
		t.Fatal("non-windows host must fail closed")
	}
	if probeCalls != 0 {
		t.Fatalf("gated calls must not reach the runner, got %d calls", probeCalls)
	}
	// 包装层与核心同门：非 Windows 宿主上原生入口不可达（Windows 宿主上此
	// 断言天然跳过，交由 wsl_pi_login_sync_e2e_windows_test 真机覆盖）。
	if runtime.GOOS != "windows" {
		if sec, ok := WSLFileMTime("Ubuntu", "/home/u/.pi/agent/auth.json"); ok || sec != 0 {
			t.Fatal("WSLFileMTime must stay gated off on non-windows hosts")
		}
	}
}

// TestAppendWSLENVForwardingPathFlagKeys pins the P4-A2 per-key path gate:
// allowPathValue-marked custom keys (envvars.json) are forwarded with the
// WSLENV /p flag — the value crosses verbatim and WSL translates C:\... into
// /mnt/c/... inside the distro — while unmarked keys stay flag-less (default
// leak-safe baseline unchanged) and reserved keys never join the list even
// when marked.
func TestAppendWSLENVForwardingPathFlagKeys(t *testing.T) {
	env := []string{
		"PATH=/x",
		"UE_ROOT=" + `C:\Program Files\Epic Games\UE_5.4`,
		"TAVILY_API_KEY=tk",
	}
	out := appendWSLENVForwardingWithPaths(env, []string{"UE_ROOT", "TAVILY_API_KEY"}, []string{"UE_ROOT", "PATH", "NOPE"})
	wslenv := envValue(out, "WSLENV")
	// 放行键：/p 路径翻译标志。
	if !strings.Contains(wslenv, "UE_ROOT/p") {
		t.Fatalf("WSLENV missing /p-flagged allowed key: %q", wslenv)
	}
	// 未放行键：无标志转发（默认基线不变）。
	if !strings.Contains(wslenv, "TAVILY_API_KEY") || strings.Contains(wslenv, "TAVILY_API_KEY/") {
		t.Fatalf("unmarked key must forward without flags: %q", wslenv)
	}
	// 保留键：标记无效，永不入清单。
	if strings.Contains(wslenv, "PATH") {
		t.Fatalf("reserved key must never be listed: %q", wslenv)
	}
	// 不在 env 的放行键不入清单。
	if strings.Contains(wslenv, "NOPE") {
		t.Fatalf("absent key must not be listed: %q", wslenv)
	}
	// 值原样保留在进程 env：翻译由 WSL 按 /p 执行，不在进程 env 内改写。
	if v := envValue(out, "UE_ROOT"); v != `C:\Program Files\Epic Games\UE_5.4` {
		t.Fatalf("UE_ROOT value must stay verbatim for WSL-side /p translation, got %q", v)
	}
	// 大小写不敏感：标记 ue_root 同样命中 UE_ROOT。
	out2 := appendWSLENVForwardingWithPaths(env, []string{"UE_ROOT"}, []string{"ue_root"})
	if !strings.Contains(envValue(out2, "WSLENV"), "UE_ROOT/p") {
		t.Fatalf("case-insensitive allow marking failed: %q", envValue(out2, "WSLENV"))
	}
	// 兼容包装：无放行清单时与旧行为完全一致（无任何 /p 标志）。
	old := appendWSLENVForwarding(env, "UE_ROOT", "TAVILY_API_KEY")
	if wslenvOld := envValue(old, "WSLENV"); !strings.Contains(wslenvOld, "UE_ROOT") || strings.Contains(wslenvOld, "/p") {
		t.Fatalf("legacy wrapper behavior changed: %q", wslenvOld)
	}
	// envvars.json 键的真实链路：键在 extra 清单内 + 放行标记 → /p。
	out3 := appendWSLENVForwardingWithPaths([]string{"UE_ROOT=D:\\eng"}, []string{"UE_ROOT"}, []string{"UE_ROOT"})
	if !strings.Contains(envValue(out3, "WSLENV"), "UE_ROOT/p") {
		t.Fatalf("extra-key forwarding of marked key failed: %q", envValue(out3, "WSLENV"))
	}
	// 白名单前缀键（不经 extra 清单即转发）同样可被放行标记加 /p（per-key 同意门覆盖整个转发面）。
	out4 := appendWSLENVForwardingWithPaths([]string{"PI_SESSION_FILE=" + `C:\s`}, nil, []string{"PI_SESSION_FILE"})
	if !strings.Contains(envValue(out4, "WSLENV"), "PI_SESSION_FILE/p") {
		t.Fatalf("prefix-key /p marking failed: %q", envValue(out4, "WSLENV"))
	}
}

// qreview F-1：用户自带 WSLENV 条目命中放行键且无 /p 时升级补齐（防半开态）。
func TestAppendWSLENVForwardingPathFlagUpgradeExisting(t *testing.T) {
	env := []string{
		"UE_ROOT=C:\\Program Files\\Epic Games\\UE_5.4",
		"WSLENV=UE_ROOT:OTHER_KEY",
	}
	out := appendWSLENVForwardingWithPaths(env, []string{"UE_ROOT"}, []string{"UE_ROOT"})
	wslenv := envValue(out, "WSLENV")
	// 自带无 /p 条目升级为 UE_ROOT/p；其余自带条目原样。
	if !strings.Contains(wslenv, "UE_ROOT/p") {
		t.Fatalf("existing entry not upgraded with /p: %q", wslenv)
	}
	if !strings.Contains(wslenv, "OTHER_KEY") {
		t.Fatalf("unrelated existing entry lost: %q", wslenv)
	}
	// 已含 /p 的自带条目不重写（不出现 UE_ROOT/p/p）。
	env2 := []string{
		"UE_ROOT=D:\\UE",
		"WSLENV=UE_ROOT/p",
	}
	out2 := appendWSLENVForwardingWithPaths(env2, []string{"UE_ROOT"}, []string{"UE_ROOT"})
	if wslenv2 := envValue(out2, "WSLENV"); strings.Contains(wslenv2, "p/p") {
		t.Fatalf("double /p appended: %q", wslenv2)
	}
	// 含 p 的 flag 组合（如 /u/p）不重写。
	env3 := []string{
		"UE_ROOT=D:\\UE",
		"WSLENV=UE_ROOT/u/p",
	}
	out3 := appendWSLENVForwardingWithPaths(env3, []string{"UE_ROOT"}, []string{"UE_ROOT"})
	if wslenv3 := envValue(out3, "WSLENV"); strings.Contains(wslenv3, "p/p") {
		t.Fatalf("flag combo rewritten: %q", wslenv3)
	}
}

// TestParseWSLStatModeOutput pins the SA-06 staging-verification probe parser:
// octal modes 1-4 digits parse; CR/NUL/blank noise is tolerated; anything else
// (empty, non-octal, overlong) reports ok=false — callers must treat false as
// "isolation unverified" and fail closed, never as any concrete mode.
func TestParseWSLStatModeOutput(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"700", "700", true},
		{"600\r\n", "600", true},
		{" 644\x00", "644", true},
		{"0", "0", true},
		{"1777", "1777", true},
		{"", "", false},
		{"\r\n", "", false},
		{"abc", "", false},
		{"6a0", "", false},
		{"17777", "", false},
		{"-rw-------", "", false},
	} {
		got, ok := parseWSLStatModeOutput(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("parseWSLStatModeOutput(%q) = (%q,%v), want (%q,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
