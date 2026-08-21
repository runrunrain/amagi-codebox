package platform

import (
	"os"
	"path/filepath"
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
	forward := []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "OPENAI_API_KEY", "CLAUDE_CODE_EFFORT_LEVEL", "PI_CODING_AGENT_DIR", "HTTPS_PROXY"}
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