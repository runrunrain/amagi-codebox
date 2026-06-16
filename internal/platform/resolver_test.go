package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDarwinResolverAddsBaselinePATHAndResolvesCLI(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(cliPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake cli: %v", err)
	}

	resolver := NewCLIResolver(capabilitiesForTarget("darwin", "arm64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:    "claudecode",
		LaunchMode: "embedded",
		WorkDir:    "/tmp/demo",
		Env:        []string{"PATH=" + binDir},
		PTYCols:    120,
		PTYRows:    40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.CLI.Path != cliPath {
		t.Fatalf("resolved cli path = %q, want %q", spec.CLI.Path, cliPath)
	}
	if spec.BootstrapMode != BootstrapDirectCommand {
		t.Fatalf("bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapDirectCommand)
	}
	for _, required := range darwinBaselinePATH {
		if !strings.Contains(spec.Env.EffectivePATH, required) {
			t.Fatalf("effective PATH %q does not contain required baseline entry %q", spec.Env.EffectivePATH, required)
		}
	}
	if len(spec.Env.AddedPATHEntries) == 0 {
		t.Fatal("expected darwin baseline PATH entries to be recorded as controlled additions")
	}
	firstEntries := strings.Split(spec.Env.EffectivePATH, string(os.PathListSeparator))
	controlledEntries := append([]string{}, userLocalBinCandidatesForOS("darwin", []string{"PATH=" + binDir})...)
	controlledEntries = append(controlledEntries, darwinBaselinePATH...)
	if len(firstEntries) < 1+len(controlledEntries) {
		t.Fatalf("effective PATH has too few entries: %q", spec.Env.EffectivePATH)
	}
	if firstEntries[0] != binDir {
		t.Fatalf("effective PATH entry 0 = %q, want caller PATH %q", firstEntries[0], binDir)
	}
	for i, expected := range controlledEntries {
		if firstEntries[i+1] != expected {
			t.Fatalf("effective PATH entry %d = %q, want %q (caller PATH must precede controlled additions)", i+1, firstEntries[i+1], expected)
		}
	}
}

func TestDarwinResolverResolvesShellKeyAndInlineCommand(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "codex")
	shellPath := filepath.Join(binDir, "zsh")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write fake executable %s: %v", path, err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("darwin", "arm64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "codex",
		LaunchMode:         "embedded",
		RequestedShellPath: "zsh",
		WorkDir:            "/tmp/demo",
		Env:                []string{"PATH=" + binDir},
		CLIArgs:            []string{"-m", "gpt-5"},
		PTYCols:            80,
		PTYRows:            24,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.Shell == nil {
		t.Fatal("expected resolved shell")
	}
	if spec.Shell.Path != shellPath {
		t.Fatalf("resolved shell path = %q, want %q", spec.Shell.Path, shellPath)
	}
	if spec.Shell.BootstrapArg != "-ilc" {
		t.Fatalf("bootstrap arg = %q, want -ilc", spec.Shell.BootstrapArg)
	}
	if spec.BootstrapMode != BootstrapShellInline {
		t.Fatalf("bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapShellInline)
	}
	if !strings.Contains(spec.StartupCommand, cliPath) {
		t.Fatalf("startup command = %q, want resolved cli path included", spec.StartupCommand)
	}
}

func TestDarwinBaselinePATHIncludesUsrLocalBin(t *testing.T) {
	found := false
	for _, entry := range darwinBaselinePATH {
		if entry == "/usr/local/bin" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected darwin baseline PATH to include /usr/local/bin")
	}
}

func TestDarwinEffectivePATHIncludesHomeLocalBin(t *testing.T) {
	homeDir := t.TempDir()
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	npmLocalNodeDir := filepath.Join(homeDir, ".local", "node", "bin")
	npmGlobalDir := filepath.Join(homeDir, ".npm-global", "bin")

	_, effectivePATH, addedEntries, _ := buildEffectiveEnvForOS("darwin", []string{
		"HOME=" + homeDir,
		"PATH=",
	})

	if !pathListContains("darwin", effectivePATH, nativeDir) {
		t.Fatalf("effective PATH %q does not include native default dir %q", effectivePATH, nativeDir)
	}
	if len(addedEntries) == 0 || addedEntries[0] != nativeDir {
		t.Fatalf("added PATH entries = %#v, want native default dir first", addedEntries)
	}
	for _, required := range []string{npmLocalNodeDir, npmGlobalDir} {
		if !pathListContains("darwin", effectivePATH, required) {
			t.Fatalf("effective PATH %q does not include npm global bin candidate %q", effectivePATH, required)
		}
	}
}

func TestDarwinResolveExecutableFindsCodexInLocalNodeBin(t *testing.T) {
	homeDir := t.TempDir()
	npmLocalNodeDir := filepath.Join(homeDir, ".local", "node", "bin")
	codexPath := filepath.Join(npmLocalNodeDir, "codex")
	if err := os.MkdirAll(npmLocalNodeDir, 0o755); err != nil {
		t.Fatalf("mkdir npm local node dir: %v", err)
	}
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake codex cli: %v", err)
	}

	resolver := NewCLIResolver(capabilitiesForTarget("darwin", "arm64"))
	cli, diagnostics, err := resolver.ResolveExecutable("codex", []string{"plugin", "list"}, []string{
		"HOME=" + homeDir,
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
	})
	if err != nil {
		t.Fatalf("ResolveExecutable: %v", err)
	}
	if cli.Path != codexPath {
		t.Fatalf("resolved codex path = %q, want %q", cli.Path, codexPath)
	}
	if diagnostics.CLISource != "path-search" {
		t.Fatalf("cli source = %q, want path-search", diagnostics.CLISource)
	}
}

func TestBuildEffectiveEnvAddsLocalNodeBinToProcessPATH(t *testing.T) {
	if currentOS() != "darwin" {
		t.Skip("current process effective PATH assertion is only defined for darwin")
	}
	homeDir := t.TempDir()
	npmLocalNodeDir := filepath.Join(homeDir, ".local", "node", "bin")

	previousHomeDir := pathLookupUserHomeDir
	pathLookupUserHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { pathLookupUserHomeDir = previousHomeDir })

	vars := BuildEffectiveEnv([]string{
		"HOME=" + homeDir,
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
	})
	if !pathListContains(currentOS(), envValue(vars, "PATH"), npmLocalNodeDir) {
		t.Fatalf("effective process PATH %q does not include npm local node dir %q", envValue(vars, "PATH"), npmLocalNodeDir)
	}
}

func TestDarwinEffectivePATHIncludesUserHomeFallbackWhenEnvHomeMissing(t *testing.T) {
	homeDir := t.TempDir()
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	previousHomeDir := pathLookupUserHomeDir
	pathLookupUserHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { pathLookupUserHomeDir = previousHomeDir })

	_, effectivePATH, addedEntries, _ := buildEffectiveEnvForOS("darwin", []string{
		"PATH=",
	})

	if !pathListContains("darwin", effectivePATH, nativeDir) {
		t.Fatalf("effective PATH %q does not include user-home fallback native dir %q", effectivePATH, nativeDir)
	}
	if len(addedEntries) == 0 || addedEntries[0] != nativeDir {
		t.Fatalf("added PATH entries = %#v, want fallback native default dir first", addedEntries)
	}
}

func TestDarwinResolverPrefersClaudeNativeDefaultOverNPMShim(t *testing.T) {
	homeDir := t.TempDir()
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	nativePath := filepath.Join(nativeDir, "claude")
	if err := os.MkdirAll(nativeDir, 0o755); err != nil {
		t.Fatalf("mkdir native dir: %v", err)
	}
	if err := os.WriteFile(nativePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write native cli: %v", err)
	}

	npmDir := t.TempDir()
	npmShim := filepath.Join(npmDir, "claude")
	if err := os.WriteFile(npmShim, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write npm shim: %v", err)
	}

	resolver := NewCLIResolver(capabilitiesForTarget("darwin", "arm64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:    "claudecode",
		LaunchMode: "embedded",
		WorkDir:    "/tmp/demo",
		Env: []string{
			"HOME=" + homeDir,
			"PATH=" + npmDir,
		},
		PTYCols: 120,
		PTYRows: 40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.CLI.Path != nativePath {
		t.Fatalf("resolved cli path = %q, want native default %q instead of npm shim %q", spec.CLI.Path, nativePath, npmShim)
	}
	if spec.Diagnostics.CLISource != "path-search" {
		t.Fatalf("cli source = %q, want path-search", spec.Diagnostics.CLISource)
	}
}

func TestDarwinResolverUsesUserHomeFallbackForClaudeNativeDefault(t *testing.T) {
	homeDir := t.TempDir()
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	nativePath := filepath.Join(nativeDir, "claude")
	if err := os.MkdirAll(nativeDir, 0o755); err != nil {
		t.Fatalf("mkdir native dir: %v", err)
	}
	if err := os.WriteFile(nativePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write native cli: %v", err)
	}

	npmDir := t.TempDir()
	npmShim := filepath.Join(npmDir, "claude")
	if err := os.WriteFile(npmShim, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write npm shim: %v", err)
	}

	previousHomeDir := pathLookupUserHomeDir
	pathLookupUserHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { pathLookupUserHomeDir = previousHomeDir })

	resolver := NewCLIResolver(capabilitiesForTarget("darwin", "arm64"))
	cli, diagnostics, err := resolver.ResolveExecutable("claude", nil, []string{
		"PATH=" + npmDir,
	})
	if err != nil {
		t.Fatalf("ResolveExecutable: %v", err)
	}
	if cli.Path != nativePath {
		t.Fatalf("resolved cli path = %q, want native default %q instead of npm shim %q", cli.Path, nativePath, npmShim)
	}
	if diagnostics.CLISource != "path-search" {
		t.Fatalf("cli source = %q, want path-search", diagnostics.CLISource)
	}
}

func pathListContains(osName string, pathValue string, want string) bool {
	for _, entry := range splitPathListForOS(osName, pathValue) {
		if filepath.Clean(entry) == filepath.Clean(want) {
			return true
		}
	}
	return false
}

func TestBuildShellResolveArgsUsesInteractiveLoginForZsh(t *testing.T) {
	args := buildShellResolveArgs(ResolvedShell{Key: "zsh", Path: "/bin/zsh"}, "claude")
	if len(args) != 2 {
		t.Fatalf("unexpected arg length: %d", len(args))
	}
	if args[0] != "-ilc" {
		t.Fatalf("shell resolve bootstrap = %q, want -ilc", args[0])
	}
	if !strings.Contains(args[1], "command -v -- 'claude'") {
		t.Fatalf("unexpected shell resolve command: %q", args[1])
	}
}

func TestResolveExecutableUsesTargetOSInsteadOfHostRuntime(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(cliPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake cli: %v", err)
	}

	resolver := NewCLIResolver(capabilitiesForTarget("darwin", "arm64"))
	cli, diagnostics, err := resolver.ResolveExecutable("claude", nil, []string{"PATH=" + binDir})
	if err != nil {
		t.Fatalf("ResolveExecutable: %v", err)
	}
	if cli.Path != cliPath {
		t.Fatalf("resolved cli path = %q, want %q", cli.Path, cliPath)
	}
	if diagnostics.CLISource != "path-search" {
		t.Fatalf("cli source = %q, want path-search", diagnostics.CLISource)
	}
}

func TestResolveExecutableDoesNotUseHostAmbientPathForForeignTargetOS(t *testing.T) {
	resolver := NewCLIResolver(capabilitiesForTarget("darwin", "arm64"))
	cli, diagnostics, err := resolver.ResolveExecutable("definitely-missing-amagi-cli", nil, []string{"PATH="})
	if err == nil {
		t.Fatalf("expected foreign target resolution to ignore host ambient PATH, got cli=%+v diagnostics=%+v", cli, diagnostics)
	}
	if diagnostics.CLISource == "ambient-path" {
		t.Fatalf("foreign target resolution should not fall back to host ambient PATH")
	}
}

func TestWindowsResolverOpenCodeEmbeddedAttachEvenForCmdWrapper(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "opencode.cmd")
	shellPath := filepath.Join(binDir, "cmd.exe")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("@echo off\r\n"), 0o755); err != nil {
			t.Fatalf("write fake executable %s: %v", path, err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:    "opencode",
		LaunchMode: "embedded",
		WorkDir:    `C:\work`,
		Env:        []string{"PATH=" + binDir},
		PTYCols:    120,
		PTYRows:    40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.BootstrapMode != BootstrapShellAttach {
		t.Fatalf("bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapShellAttach)
	}
	if spec.Shell == nil || spec.Shell.Path == "" {
		t.Fatalf("expected non-empty resolved shell, got %+v", spec.Shell)
	}
	// Startup command uses the resolved default shell. It must type the command
	// name, never the .cmd path, so npm shims run inside the attached PTY shell.
	switch spec.Shell.Key {
	case "pwsh", "powershell":
		if spec.StartupCommand != "& 'opencode'" {
			t.Fatalf("startup command = %q, want %q", spec.StartupCommand, "& 'opencode'")
		}
	case "cmd":
		if spec.StartupCommand != "opencode" {
			t.Fatalf("startup command = %q, want %q", spec.StartupCommand, "opencode")
		}
	default:
		if spec.StartupCommand != "opencode" {
			t.Fatalf("startup command for shell %q = %q, want %q", spec.Shell.Key, spec.StartupCommand, "opencode")
		}
	}
	if strings.Contains(spec.StartupCommand, "opencode.cmd") {
		t.Fatalf("startup command should not contain .cmd path: %q", spec.StartupCommand)
	}
}

func TestWindowsResolverCodexExeEmbeddedUsesShellAttach(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "codex.exe")
	shellPath := filepath.Join(binDir, "pwsh.exe")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("MZ"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:    "codex",
		LaunchMode: "embedded",
		WorkDir:    `C:\work`,
		Env:        []string{"PATH=" + binDir},
		PTYCols:    80,
		PTYRows:    24,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Codex on Windows embedded now uses BootstrapShellAttach instead of direct command
	if spec.BootstrapMode != BootstrapShellAttach {
		t.Fatalf("bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapShellAttach)
	}
	if spec.Shell == nil {
		t.Fatalf("expected shell to be auto-assigned for codex attach mode, got nil")
	}
	// Default Windows shell is pwsh (found in PATH), so PowerShell-safe format
	if spec.StartupCommand != "& 'codex'" {
		t.Fatalf("startup command = %q, want %q", spec.StartupCommand, "& 'codex'")
	}
}

func TestBuildEffectiveEnvForWindowsAddsNPMGlobalPathWithoutDuplicates(t *testing.T) {
	appData := `C:\Users\demo\AppData\Roaming`
	appDataNPM := appData + `\npm`
	_, effectivePATH, addedEntries, _ := buildEffectiveEnvForOS("windows", []string{
		"PATH=" + appDataNPM + `;C:\Tools`,
		"APPDATA=" + appData,
		`LOCALAPPDATA=C:\Users\demo\AppData\Local`,
	})

	entries := strings.Split(effectivePATH, ";")
	count := 0
	for _, entry := range entries {
		if strings.EqualFold(entry, appDataNPM) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("APPDATA npm path count = %d, want 1 in PATH %q", count, effectivePATH)
	}
	for _, entry := range addedEntries {
		if strings.EqualFold(entry, appDataNPM) {
			t.Fatalf("duplicate APPDATA npm path should not be recorded as added: %+v", addedEntries)
		}
	}
}

func TestWindowsResolverFindsClaudePackageBinaryFallback(t *testing.T) {
	appData := filepath.Join(t.TempDir(), "AppData", "Roaming")
	npmPrefix := strings.TrimRight(appData, `/\`) + `\npm`
	fallbackPath := filepath.Join(npmPrefix, "node_modules", "@anthropic-ai", ".claude-code-nDGSeslo", "node_modules", "@anthropic-ai", "claude-code-win32-x64", "claude.exe")
	if err := os.MkdirAll(filepath.Dir(fallbackPath), 0o755); err != nil {
		t.Fatalf("mkdir fallback dir: %v", err)
	}
	if err := os.WriteFile(fallbackPath, []byte("MZ"), 0o755); err != nil {
		t.Fatalf("write fallback claude exe: %v", err)
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	cli, diagnostics, err := resolver.ResolveExecutable("claude", nil, []string{
		"PATH=",
		"APPDATA=" + appData,
		"LOCALAPPDATA=",
	})
	if err != nil {
		t.Fatalf("ResolveExecutable: %v", err)
	}
	if filepath.Clean(cli.Path) != filepath.Clean(fallbackPath) {
		t.Fatalf("resolved claude path = %q, want package binary fallback %q", cli.Path, fallbackPath)
	}
	if diagnostics.CLISource != "path-search" {
		t.Fatalf("cli source = %q, want path-search", diagnostics.CLISource)
	}
}

func TestWindowsResolverOpenCodeEmbeddedUsesShellAttach(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "opencode.cmd")
	shellPath := filepath.Join(binDir, "pwsh.exe")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "opencode",
		LaunchMode:         "embedded",
		RequestedShellPath: "pwsh",
		WorkDir:            `C:\work`,
		Env:                []string{"PATH=" + binDir},
		PTYCols:            120,
		PTYRows:            40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.BootstrapMode != BootstrapShellAttach {
		t.Fatalf("bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapShellAttach)
	}
	if spec.Shell == nil {
		t.Fatal("expected resolved shell for attach mode")
	}
	// PowerShell attach: & 'opencode'
	if spec.StartupCommand != "& 'opencode'" {
		t.Fatalf("startup command = %q, want %q", spec.StartupCommand, "& 'opencode'")
	}
	// Even though CLI resolved to .cmd, startup command must NOT contain .cmd path
	if strings.Contains(spec.StartupCommand, ".cmd") {
		t.Fatalf("startup command should not contain .cmd path: %q", spec.StartupCommand)
	}
}

func TestWindowsResolverCodexEmbeddedWithArgsUsesShellAttach(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "codex.cmd")
	shellPath := filepath.Join(binDir, "pwsh.exe")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "codex",
		LaunchMode:         "embedded",
		RequestedShellPath: "pwsh",
		WorkDir:            `C:\work`,
		Env:                []string{"PATH=" + binDir},
		CLIArgs:            []string{"-m", "gpt-5"},
		PTYCols:            80,
		PTYRows:            24,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.BootstrapMode != BootstrapShellAttach {
		t.Fatalf("bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapShellAttach)
	}
	if spec.Shell == nil {
		t.Fatal("expected resolved shell for attach mode")
	}
	// PowerShell attach: & 'codex' '-m' 'gpt-5'
	if spec.StartupCommand != "& 'codex' '-m' 'gpt-5'" {
		t.Fatalf("startup command = %q, want %q", spec.StartupCommand, "& 'codex' '-m' 'gpt-5'")
	}
}

func TestWindowsResolverOpenCodeNoShellDefaultsToShellAttach(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "opencode.cmd")
	shellPath := filepath.Join(binDir, "pwsh.exe")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:    "opencode",
		LaunchMode: "embedded",
		WorkDir:    `C:\work`,
		Env:        []string{"PATH=" + binDir},
		PTYCols:    120,
		PTYRows:    40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.BootstrapMode != BootstrapShellAttach {
		t.Fatalf("bootstrap mode = %q, want %q (auto-assigned default shell)", spec.BootstrapMode, BootstrapShellAttach)
	}
	if spec.Shell == nil {
		t.Fatal("expected default shell to be auto-assigned for attach mode")
	}
	if spec.StartupCommand != "& 'opencode'" {
		t.Fatalf("startup command = %q, want %q", spec.StartupCommand, "& 'opencode'")
	}
}

func TestWindowsResolverClaudeCodeNPMCmdEmbeddedForcesCmdWhenPowerShellRequested(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "claude.cmd")
	shellPath := filepath.Join(binDir, "pwsh.exe")
	fakeCmdPath := filepath.Join(binDir, "cmd.exe")
	for _, path := range []string{cliPath, shellPath, fakeCmdPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "claudecode",
		LaunchMode:         "embedded",
		RequestedShellPath: "pwsh",
		WorkDir:            `C:\work`,
		Env:                []string{"PATH=" + binDir, "ComSpec=" + fakeCmdPath},
		PTYCols:            120,
		PTYRows:            40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.BootstrapMode != BootstrapShellAttach {
		t.Fatalf("bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapShellAttach)
	}
	if spec.Shell == nil || spec.Shell.Key != "cmd" || spec.Shell.Path != fakeCmdPath {
		t.Fatalf("expected Claude npm shim to force trusted cmd attach shell, got %+v", spec.Shell)
	}
	if spec.StartupCommand != "claude" {
		t.Fatalf("startup command = %q, want claude", spec.StartupCommand)
	}
	assertNoExternalTerminalLauncherTokens(t, spec.StartupCommand)
	if strings.Contains(strings.ToLower(spec.StartupCommand), ".cmd") || strings.Contains(strings.ToLower(spec.StartupCommand), ".ps1") {
		t.Fatalf("startup command must not contain wrapper path: %q", spec.StartupCommand)
	}
	if spec.Diagnostics.ShellSource != "forced-cmd-attach-comspec" {
		t.Fatalf("shell source = %q, want forced-cmd-attach-comspec", spec.Diagnostics.ShellSource)
	}
	foundOverrideWarning := false
	for _, warning := range spec.Diagnostics.Warnings {
		if strings.Contains(warning, "overridden with cmd.exe") {
			foundOverrideWarning = true
			break
		}
	}
	if !foundOverrideWarning {
		t.Fatalf("expected override warning for PowerShell request, warnings=%#v", spec.Diagnostics.Warnings)
	}
}

func TestWindowsResolverClaudeCodeNPMAliasEmbeddedUsesAliasCommandName(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "claudecode.cmd")
	shellPath := filepath.Join(binDir, "pwsh.exe")
	cmdPath := filepath.Join(binDir, "cmd.exe")
	for _, path := range []string{cliPath, shellPath, cmdPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:    "claude_code",
		LaunchMode: "embedded",
		WorkDir:    `C:\work`,
		Env:        []string{"PATH=" + binDir},
		PTYCols:    120,
		PTYRows:    40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.CLI.Name != "claudecode" {
		t.Fatalf("CLI name = %q, want claudecode alias", spec.CLI.Name)
	}
	if spec.BootstrapMode != BootstrapShellAttach {
		t.Fatalf("bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapShellAttach)
	}
	if spec.Shell == nil || spec.Shell.Key != "cmd" || spec.Shell.Path != cmdPath {
		t.Fatalf("expected Claude npm alias to force cmd attach shell, got %+v", spec.Shell)
	}
	if spec.StartupCommand != "claudecode" {
		t.Fatalf("startup command = %q, want claudecode", spec.StartupCommand)
	}
	assertNoExternalTerminalLauncherTokens(t, spec.StartupCommand)
	if strings.Contains(strings.ToLower(spec.StartupCommand), ".cmd") {
		t.Fatalf("startup command must type command alias, not wrapper path: %q", spec.StartupCommand)
	}
}

func TestWindowsResolverClaudeCodeCommandNameAliases(t *testing.T) {
	for _, tt := range []struct {
		appType string
		file    string
		want    string
	}{
		{appType: "claudecode", file: "claude.cmd", want: "claude"},
		{appType: "claude-code", file: "claude-code.cmd", want: "claude-code"},
		{appType: "claude", file: "claude.cmd", want: "claude"},
	} {
		t.Run(tt.appType+"/"+tt.file, func(t *testing.T) {
			binDir := t.TempDir()
			for _, path := range []string{filepath.Join(binDir, tt.file), filepath.Join(binDir, "pwsh.exe"), filepath.Join(binDir, "cmd.exe")} {
				if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
					t.Fatalf("write fake: %v", err)
				}
			}
			resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
			spec, err := resolver.Resolve(ResolveRequest{
				AppType:    tt.appType,
				LaunchMode: "embedded",
				WorkDir:    `C:\work`,
				Env:        []string{"PATH=" + binDir},
				PTYCols:    120,
				PTYRows:    40,
			})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if spec.StartupCommand != tt.want {
				t.Fatalf("startup command = %q, want %q", spec.StartupCommand, tt.want)
			}
			assertNoExternalTerminalLauncherTokens(t, spec.StartupCommand)
			if strings.Contains(strings.ToLower(spec.StartupCommand), ".cmd") {
				t.Fatalf("startup command must not contain wrapper path: %q", spec.StartupCommand)
			}
		})
	}
}

func TestWindowsResolverClaudeCodeNPMCmdNoRequestedShellForcesCmdAttach(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "claude.cmd")
	pwshPath := filepath.Join(binDir, "pwsh.exe")
	fakeCmdPath := filepath.Join(binDir, "cmd.exe")
	for _, path := range []string{cliPath, pwshPath, fakeCmdPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:    "claudecode",
		LaunchMode: "embedded",
		WorkDir:    `C:\work`,
		Env:        []string{"PATH=" + binDir},
		PTYCols:    120,
		PTYRows:    40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.BootstrapMode != BootstrapShellAttach {
		t.Fatalf("bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapShellAttach)
	}
	if spec.Shell == nil || spec.Shell.Key != "cmd" || spec.Shell.Path != fakeCmdPath {
		t.Fatalf("Claude Code npm shim should force cmd attach shell, got %+v", spec.Shell)
	}
	if spec.StartupCommand != "claude" {
		t.Fatalf("startup command = %q, want claude", spec.StartupCommand)
	}
	assertNoExternalTerminalLauncherTokens(t, spec.StartupCommand)
}

func TestWindowsResolverClaudeCodeNPMShimWithExtensionlessAndPs1ForcesCmd(t *testing.T) {
	binDir := t.TempDir()
	extensionlessShim := filepath.Join(binDir, "claude")
	cmdShim := filepath.Join(binDir, "claude.cmd")
	psShim := filepath.Join(binDir, "claude.ps1")
	pwshPath := filepath.Join(binDir, "pwsh.exe")
	cmdPath := filepath.Join(binDir, "cmd.exe")
	for _, path := range []string{extensionlessShim, cmdShim, psShim, pwshPath, cmdPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "claudecode",
		LaunchMode:         "embedded",
		RequestedShellPath: "pwsh",
		WorkDir:            `C:\work`,
		Env:                []string{"PATH=" + binDir, "ComSpec=" + cmdPath},
		PTYCols:            120,
		PTYRows:            40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Windows path resolution must prefer .exe/.cmd/.bat over the extensionless
	// POSIX shell script that npm creates alongside the .cmd wrapper. The
	// extensionless file is not a valid Win32 executable.
	if spec.CLI.Path != cmdShim {
		t.Fatalf("resolved cli path = %q, want .cmd shim %q (extensionless npm POSIX shim %q must not shadow it)", spec.CLI.Path, cmdShim, extensionlessShim)
	}
	if spec.Shell == nil || spec.Shell.Key != "cmd" || spec.Shell.Path != cmdPath {
		t.Fatalf("expected requested pwsh to be overridden by cmd for Claude npm shim, got %+v", spec.Shell)
	}
	if spec.StartupCommand != "claude" {
		t.Fatalf("startup command = %q, want claude", spec.StartupCommand)
	}
	for _, forbidden := range []string{"& 'claude'", "claude.ps1", "claude.cmd", "Start-Process", "wt.exe", "explorer"} {
		if strings.Contains(strings.ToLower(spec.StartupCommand), strings.ToLower(forbidden)) {
			t.Fatalf("startup command %q contains forbidden PowerShell/external token %q", spec.StartupCommand, forbidden)
		}
	}
	assertNoExternalTerminalLauncherTokens(t, spec.StartupCommand)
}

func TestWindowsResolverClaudeCodeNPMCmdExplicitCmdAttachRemainsSupported(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "claude.cmd")
	cmdPath := filepath.Join(binDir, "cmd.exe")
	for _, path := range []string{cliPath, cmdPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "claudecode",
		LaunchMode:         "embedded",
		RequestedShellPath: "cmd",
		WorkDir:            `C:\work`,
		Env:                []string{"PATH=" + binDir},
		PTYCols:            120,
		PTYRows:            40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.BootstrapMode != BootstrapShellAttach {
		t.Fatalf("bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapShellAttach)
	}
	if spec.Shell == nil || spec.Shell.Key != "cmd" || spec.Shell.Path != cmdPath {
		t.Fatalf("expected explicit cmd attach shell, got %+v", spec.Shell)
	}
	if spec.StartupCommand != "claude" {
		t.Fatalf("startup command = %q, want claude", spec.StartupCommand)
	}
	assertNoExternalTerminalLauncherTokens(t, spec.StartupCommand)
}

func TestResolveWindowsCmdAttachShellIgnoresPathHijackWhenSystemRootAvailable(t *testing.T) {
	pathDir := t.TempDir()
	fakeCmdPath := filepath.Join(pathDir, "cmd.exe")
	systemRoot := t.TempDir()
	trustedCmdPath := filepath.Join(systemRoot, "System32", "cmd.exe")
	if err := os.MkdirAll(filepath.Dir(trustedCmdPath), 0o755); err != nil {
		t.Fatalf("mkdir trusted cmd dir: %v", err)
	}
	for _, path := range []string{fakeCmdPath, trustedCmdPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake cmd %s: %v", path, err)
		}
	}

	shell, source, warnings := resolveWindowsCmdAttachShell([]string{
		"PATH=" + pathDir,
		"SystemRoot=" + systemRoot,
	})

	if shell.Path != trustedCmdPath {
		t.Fatalf("shell path = %q, want trusted SystemRoot cmd %q", shell.Path, trustedCmdPath)
	}
	if shell.Path == fakeCmdPath {
		t.Fatalf("shell path selected PATH-controlled fake cmd.exe: %q", fakeCmdPath)
	}
	if source != "forced-cmd-attach-systemroot" {
		t.Fatalf("source = %q, want forced-cmd-attach-systemroot", source)
	}
	for _, warning := range warnings {
		if strings.Contains(warning, "PATH-resolved") {
			t.Fatalf("trusted SystemRoot resolution should not emit PATH fallback warning: %#v", warnings)
		}
	}
}

func TestResolveWindowsCmdAttachShellIgnoresSuspiciousComSpec(t *testing.T) {
	pathDir := t.TempDir()
	fakeCmdPath := filepath.Join(pathDir, "cmd.exe")
	systemRoot := t.TempDir()
	trustedCmdPath := filepath.Join(systemRoot, "System32", "cmd.exe")
	if err := os.MkdirAll(filepath.Dir(trustedCmdPath), 0o755); err != nil {
		t.Fatalf("mkdir trusted cmd dir: %v", err)
	}
	for _, path := range []string{fakeCmdPath, trustedCmdPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake cmd %s: %v", path, err)
		}
	}

	shell, source, warnings := resolveWindowsCmdAttachShell([]string{
		"PATH=" + pathDir,
		"ComSpec=" + trustedCmdPath + " /d /c calc.exe",
		"SystemRoot=" + systemRoot,
	})

	if shell.Path != trustedCmdPath {
		t.Fatalf("shell path = %q, want suspicious ComSpec ignored and SystemRoot cmd selected %q", shell.Path, trustedCmdPath)
	}
	if source != "forced-cmd-attach-systemroot" {
		t.Fatalf("source = %q, want forced-cmd-attach-systemroot", source)
	}
	foundComSpecWarning := false
	for _, warning := range warnings {
		if strings.Contains(warning, "ComSpec was ignored") {
			foundComSpecWarning = true
			break
		}
	}
	if !foundComSpecWarning {
		t.Fatalf("expected suspicious ComSpec warning, got %#v", warnings)
	}
}

func TestResolveWindowsCmdAttachShellRejectsNonCmdComSpec(t *testing.T) {
	pathDir := t.TempDir()
	fakeCmdPath := filepath.Join(pathDir, "cmd.exe")
	fakeCmdShim := filepath.Join(pathDir, "cmd.cmd")
	systemRoot := t.TempDir()
	trustedCmdPath := filepath.Join(systemRoot, "System32", "cmd.exe")
	if err := os.MkdirAll(filepath.Dir(trustedCmdPath), 0o755); err != nil {
		t.Fatalf("mkdir trusted cmd dir: %v", err)
	}
	for _, path := range []string{fakeCmdPath, fakeCmdShim, trustedCmdPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake cmd %s: %v", path, err)
		}
	}

	shell, source, warnings := resolveWindowsCmdAttachShell([]string{
		"PATH=" + pathDir,
		"ComSpec=" + fakeCmdShim,
		"SystemRoot=" + systemRoot,
	})

	if shell.Path != trustedCmdPath {
		t.Fatalf("shell path = %q, want cmd.cmd ComSpec ignored and SystemRoot cmd selected %q", shell.Path, trustedCmdPath)
	}
	if source != "forced-cmd-attach-systemroot" {
		t.Fatalf("source = %q, want forced-cmd-attach-systemroot", source)
	}
	foundComSpecWarning := false
	for _, warning := range warnings {
		if strings.Contains(warning, "ComSpec was ignored") {
			foundComSpecWarning = true
			break
		}
	}
	if !foundComSpecWarning {
		t.Fatalf("expected non-cmd.exe ComSpec warning, got %#v", warnings)
	}
}

func assertNoExternalTerminalLauncherTokens(t *testing.T, command string) {
	t.Helper()
	lower := strings.ToLower(command)
	for _, forbidden := range []string{"cmd /c start", "cmd.exe /c start", "start-process", "wt ", "wt.exe", "explorer", "claude.cmd", "claude.bat"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("startup command %q contains external terminal launcher token %q", command, forbidden)
		}
	}
}

func TestWindowsResolverClaudeCodeOfficialExeEmbeddedUsesDirectCommand(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "claude.exe")
	if err := os.WriteFile(cliPath, []byte("MZ"), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:    "claudecode",
		LaunchMode: "embedded",
		WorkDir:    `C:\work`,
		Env:        []string{"PATH=" + binDir},
		PTYCols:    120,
		PTYRows:    40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.BootstrapMode == BootstrapShellAttach {
		t.Fatalf("claudecode should not use shell-attach, got %q", spec.BootstrapMode)
	}
	if spec.BootstrapMode != BootstrapDirectCommand {
		t.Fatalf("official claude.exe bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapDirectCommand)
	}
}

func TestDarwinOpenCodeDoesNotUseShellAttach(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "opencode")
	shellPath := filepath.Join(binDir, "zsh")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("darwin", "arm64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "opencode",
		LaunchMode:         "embedded",
		RequestedShellPath: "zsh",
		WorkDir:            "/tmp/demo",
		Env:                []string{"PATH=" + binDir},
		PTYCols:            120,
		PTYRows:            40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.BootstrapMode == BootstrapShellAttach {
		t.Fatalf("darwin opencode should not use shell-attach, got %q", spec.BootstrapMode)
	}
}

func TestDarwinCodexDoesNotUseShellAttach(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "codex")
	shellPath := filepath.Join(binDir, "zsh")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("darwin", "arm64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "codex",
		LaunchMode:         "embedded",
		RequestedShellPath: "zsh",
		WorkDir:            "/tmp/demo",
		Env:                []string{"PATH=" + binDir},
		CLIArgs:            []string{"-m", "gpt-5"},
		PTYCols:            80,
		PTYRows:            24,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.BootstrapMode == BootstrapShellAttach {
		t.Fatalf("darwin codex should not use shell-attach, got %q", spec.BootstrapMode)
	}
}

func TestWindowsOpenCodeTerminalModeDoesNotUseShellAttach(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "opencode.cmd")
	shellPath := filepath.Join(binDir, "pwsh.exe")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "opencode",
		LaunchMode:         "terminal",
		RequestedShellPath: "pwsh",
		WorkDir:            `C:\work`,
		Env:                []string{"PATH=" + binDir},
		PTYCols:            120,
		PTYRows:            40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.BootstrapMode == BootstrapShellAttach {
		t.Fatalf("terminal mode should not use shell-attach, got %q", spec.BootstrapMode)
	}
}

// --- Shell-safe attach command escaping tests ---

func TestBuildAttachStartupCommandForShell_PowerShell_SingleQuotesBlockExpansion(t *testing.T) {
	shell := &ResolvedShell{Key: "pwsh", Path: "pwsh.exe"}

	tests := []struct {
		name string
		cli  string
		args []string
		want string
	}{
		{
			name: "bare opencode no args",
			cli:  "opencode",
			args: nil,
			want: "& 'opencode'",
		},
		{
			name: "codex with safe model arg",
			cli:  "codex",
			args: []string{"-m", "gpt-5"},
			want: "& 'codex' '-m' 'gpt-5'",
		},
		{
			name: "codex model with ampersand",
			cli:  "codex",
			args: []string{"-m", "gpt&5"},
			want: "& 'codex' '-m' 'gpt&5'",
		},
		{
			name: "codex model with pipe",
			cli:  "codex",
			args: []string{"-m", "a|b"},
			want: "& 'codex' '-m' 'a|b'",
		},
		{
			name: `codex model with angle brackets`,
			cli:  "codex",
			args: []string{"-m", "x>y"},
			want: "& 'codex' '-m' 'x>y'",
		},
		{
			name: "codex model with single quote",
			cli:  "codex",
			args: []string{"-m", "O'Brien"},
			want: "& 'codex' '-m' 'O''Brien'",
		},
		{
			name: "codex model with PowerShell subexpression",
			cli:  "codex",
			args: []string{"-m", "$(Get-Process)"},
			want: "& 'codex' '-m' '$(Get-Process)'",
		},
		{
			name: "codex model with dollar variable",
			cli:  "codex",
			args: []string{"-m", "$HOME"},
			want: "& 'codex' '-m' '$HOME'",
		},
		{
			name: "codex model with backtick",
			cli:  "codex",
			args: []string{"-m", "a`b"},
			want: "& 'codex' '-m' 'a`b'",
		},
		{
			name: "codex model with double quotes",
			cli:  "codex",
			args: []string{"-m", `he said "hello"`},
			want: `& 'codex' '-m' 'he said "hello"'`,
		},
		{
			name: "powershell allows percent sign",
			cli:  "codex",
			args: []string{"-m", "100%"},
			want: "& 'codex' '-m' '100%'",
		},
		{
			name: "powershell allows exclamation mark",
			cli:  "codex",
			args: []string{"-m", "yes!"},
			want: "& 'codex' '-m' 'yes!'",
		},
		{
			name: "powershell key alias",
			cli:  "opencode",
			args: []string{"-m", "test&model"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildAttachStartupCommandForShell(shell, tt.cli, tt.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.want != "" && got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			// Verify no bare metacharacters outside single quotes for pwsh
			assertNoBareShellMetacharsInPwshTokens(t, got)
		})
	}
}

// The last test case uses the "powershell" key alias instead of "pwsh".
func TestBuildAttachStartupCommandForShell_PowerShellAlias_SameOutput(t *testing.T) {
	pwshShell := &ResolvedShell{Key: "pwsh", Path: "pwsh.exe"}
	psShell := &ResolvedShell{Key: "powershell", Path: "powershell.exe"}

	gotPwsh, err := buildAttachStartupCommandForShell(pwshShell, "codex", []string{"-m", "gpt&5"})
	if err != nil {
		t.Fatalf("pwsh: %v", err)
	}
	gotPs, err := buildAttachStartupCommandForShell(psShell, "codex", []string{"-m", "gpt&5"})
	if err != nil {
		t.Fatalf("powershell: %v", err)
	}

	if gotPwsh != gotPs {
		t.Fatalf("pwsh=%q != powershell=%q", gotPwsh, gotPs)
	}
}

func TestBuildAttachStartupCommandForShell_PowerShell_RejectsNewlines(t *testing.T) {
	shell := &ResolvedShell{Key: "pwsh", Path: "pwsh.exe"}

	_, err := buildAttachStartupCommandForShell(shell, "codex", []string{"-m", "line1\nline2"})
	if err == nil {
		t.Fatal("expected error for LF in arg, got nil")
	}

	_, err = buildAttachStartupCommandForShell(shell, "codex", []string{"-m", "line1\rline2"})
	if err == nil {
		t.Fatal("expected error for CR in arg, got nil")
	}
}

func assertNoBareShellMetacharsInPwshTokens(t *testing.T, cmd string) {
	t.Helper()
	// The leading "& " is the PowerShell call operator -- intentionally safe.
	// Skip it before checking for bare metacharacters.
	rest := cmd
	if strings.HasPrefix(rest, "& ") {
		rest = rest[2:]
	}
	// Parse tokens between single quotes. Everything inside '...' is safe.
	// Check the non-quoted parts for dangerous characters.
	dangerous := "|<>(){};`$"
	inQuote := false
	for i := 0; i < len(rest); i++ {
		ch := rest[i]
		if ch == '\'' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && strings.ContainsRune(dangerous, rune(ch)) {
			t.Fatalf("bare shell metachar %q outside single quotes in: %q", string(ch), cmd)
		}
	}
}

func TestBuildAttachStartupCommandForShell_Cmd_SafeEscaping(t *testing.T) {
	shell := &ResolvedShell{Key: "cmd", Path: "cmd.exe"}

	tests := []struct {
		name string
		cli  string
		args []string
		want string
	}{
		{
			name: "bare opencode no args",
			cli:  "opencode",
			args: nil,
			want: "opencode",
		},
		{
			name: "codex with safe model arg",
			cli:  "codex",
			args: []string{"-m", "gpt-5"},
			want: "codex -m gpt-5",
		},
		{
			name: "codex model with ampersand",
			cli:  "codex",
			args: []string{"-m", "gpt&5"},
			want: `codex -m "gpt&5"`,
		},
		{
			name: "codex model with pipe",
			cli:  "codex",
			args: []string{"-m", "a|b"},
			want: `codex -m "a|b"`,
		},
		{
			name: "codex model with redirect",
			cli:  "codex",
			args: []string{"-m", "x>y"},
			want: `codex -m "x>y"`,
		},
		{
			name: "codex model with multiple dangerous chars",
			cli:  "codex",
			args: []string{"-m", "a&b|c>d"},
			want: `codex -m "a&b|c>d"`,
		},
		{
			name: "codex model with spaces",
			cli:  "codex",
			args: []string{"-m", "gpt 5"},
			want: `codex -m "gpt 5"`,
		},
		{
			name: "empty arg",
			cli:  "codex",
			args: []string{"-m", ""},
			want: `codex -m ""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildAttachStartupCommandForShell(shell, tt.cli, tt.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildAttachStartupCommandForShell_Cmd_RejectsPercentSign(t *testing.T) {
	shell := &ResolvedShell{Key: "cmd", Path: "cmd.exe"}
	_, err := buildAttachStartupCommandForShell(shell, "codex", []string{"-m", "%EVIL%"})
	if err == nil {
		t.Fatal("expected error for % in cmd arg, got nil")
	}
}

func TestBuildAttachStartupCommandForShell_Cmd_RejectsExclamationMark(t *testing.T) {
	shell := &ResolvedShell{Key: "cmd", Path: "cmd.exe"}
	_, err := buildAttachStartupCommandForShell(shell, "codex", []string{"-m", "!EVIL!"})
	if err == nil {
		t.Fatal("expected error for ! in cmd arg, got nil")
	}
}

func TestBuildAttachStartupCommandForShell_Cmd_RejectsNewlines(t *testing.T) {
	shell := &ResolvedShell{Key: "cmd", Path: "cmd.exe"}
	_, err := buildAttachStartupCommandForShell(shell, "codex", []string{"-m", "line1\nline2"})
	if err == nil {
		t.Fatal("expected error for LF in cmd arg, got nil")
	}
	_, err = buildAttachStartupCommandForShell(shell, "codex", []string{"-m", "line1\rline2"})
	if err == nil {
		t.Fatal("expected error for CR in cmd arg, got nil")
	}
}

func TestBuildAttachStartupCommandForShell_CmdQuotingPreventsCommandInjection(t *testing.T) {
	shell := &ResolvedShell{Key: "cmd", Path: "cmd.exe"}

	// Verify that dangerous characters are inside double quotes, not bare
	dangerous := "&|<>"
	for _, ch := range dangerous {
		arg := "a" + string(ch) + "b"
		got, err := buildAttachStartupCommandForShell(shell, "codex", []string{"-m", arg})
		if err != nil {
			t.Fatalf("unexpected error for char %q: %v", string(ch), err)
		}
		// The arg must be double-quoted, containing the dangerous char inside
		expected := `codex -m "a` + string(ch) + `b"`
		if got != expected {
			t.Errorf("for char %q: got %q, want %q", string(ch), got, expected)
		}
	}
}

func TestBuildAttachStartupCommandForShell_FallbackUsesBuildCommandString(t *testing.T) {
	shell := &ResolvedShell{Key: "bash", Path: "/bin/bash"}
	got, err := buildAttachStartupCommandForShell(shell, "opencode", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fallback uses buildCommandString which just joins tokens
	if got != "opencode" {
		t.Fatalf("fallback for unknown shell: got %q, want %q", got, "opencode")
	}
}

func TestBuildAttachStartupCommandForShell_NilShellFallback(t *testing.T) {
	got, err := buildAttachStartupCommandForShell(nil, "opencode", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "opencode" {
		t.Fatalf("nil shell fallback: got %q, want %q", got, "opencode")
	}
}

func TestWindowsResolverAttachWithCmdShell_SafeCmdFormat(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "opencode.cmd")
	shellPath := filepath.Join(binDir, "cmd.exe")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("@echo off\r\n"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "opencode",
		LaunchMode:         "embedded",
		RequestedShellPath: "cmd",
		WorkDir:            `C:\work`,
		Env:                []string{"PATH=" + binDir},
		PTYCols:            120,
		PTYRows:            40,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.BootstrapMode != BootstrapShellAttach {
		t.Fatalf("bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapShellAttach)
	}
	if spec.Shell == nil || spec.Shell.Key != "cmd" {
		t.Fatalf("expected cmd shell, got %+v", spec.Shell)
	}
	// cmd.exe attach: bare command name (safe cmd token)
	if spec.StartupCommand != "opencode" {
		t.Fatalf("startup command = %q, want %q", spec.StartupCommand, "opencode")
	}
}

func TestWindowsResolverCodexAttachWithCmdShell_SafeCmdEscaping(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "codex.cmd")
	shellPath := filepath.Join(binDir, "cmd.exe")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("@echo off\r\n"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "codex",
		LaunchMode:         "embedded",
		RequestedShellPath: "cmd",
		WorkDir:            `C:\work`,
		Env:                []string{"PATH=" + binDir},
		CLIArgs:            []string{"-m", "gpt&5"},
		PTYCols:            80,
		PTYRows:            24,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.BootstrapMode != BootstrapShellAttach {
		t.Fatalf("bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapShellAttach)
	}
	// cmd.exe attach: dangerous chars in double-quoted args
	if spec.StartupCommand != `codex -m "gpt&5"` {
		t.Fatalf("startup command = %q, want %q", spec.StartupCommand, `codex -m "gpt&5"`)
	}
}

func TestWindowsResolverCodexAttachWithCmdShell_RejectsPercentArg(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "codex.cmd")
	shellPath := filepath.Join(binDir, "cmd.exe")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("@echo off\r\n"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	_, err := resolver.Resolve(ResolveRequest{
		AppType:            "codex",
		LaunchMode:         "embedded",
		RequestedShellPath: "cmd",
		WorkDir:            `C:\work`,
		Env:                []string{"PATH=" + binDir},
		CLIArgs:            []string{"-m", "%EVIL%"},
		PTYCols:            80,
		PTYRows:            24,
	})
	if err == nil {
		t.Fatal("expected error for percent-EVIL-percent in cmd args, got nil")
	}
}

func TestWindowsResolverCodexAttachWithCmdShell_RejectsExclamationArg(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "codex.cmd")
	shellPath := filepath.Join(binDir, "cmd.exe")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("@echo off\r\n"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	_, err := resolver.Resolve(ResolveRequest{
		AppType:            "codex",
		LaunchMode:         "embedded",
		RequestedShellPath: "cmd",
		WorkDir:            `C:\work`,
		Env:                []string{"PATH=" + binDir},
		CLIArgs:            []string{"-m", "!EVIL!"},
		PTYCols:            80,
		PTYRows:            24,
	})
	if err == nil {
		t.Fatal("expected error for !EVIL! in cmd args, got nil")
	}
}

func TestWindowsResolverCodexAttachWithCmdShell_RejectsNewlineArg(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "codex.cmd")
	shellPath := filepath.Join(binDir, "cmd.exe")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("@echo off\r\n"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	_, err := resolver.Resolve(ResolveRequest{
		AppType:            "codex",
		LaunchMode:         "embedded",
		RequestedShellPath: "cmd",
		WorkDir:            `C:\work`,
		Env:                []string{"PATH=" + binDir},
		CLIArgs:            []string{"-m", "line1\nline2"},
		PTYCols:            80,
		PTYRows:            24,
	})
	if err == nil {
		t.Fatal("expected error for newline in cmd args, got nil")
	}
}

func TestWindowsResolverCodexAttachWithPwshShell_AllowsPercentAndBang(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "codex.cmd")
	shellPath := filepath.Join(binDir, "pwsh.exe")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	spec, err := resolver.Resolve(ResolveRequest{
		AppType:            "codex",
		LaunchMode:         "embedded",
		RequestedShellPath: "pwsh",
		WorkDir:            `C:\work`,
		Env:                []string{"PATH=" + binDir},
		CLIArgs:            []string{"-m", "%EVIL%", "-p", "!DANGER!"},
		PTYCols:            80,
		PTYRows:            24,
	})
	if err != nil {
		t.Fatalf("pwsh should allow %% and ! in single-quoted args, got error: %v", err)
	}
	// PowerShell single-quoted tokens are literal
	if spec.StartupCommand != "& 'codex' '-m' '%EVIL%' '-p' '!DANGER!'" {
		t.Fatalf("startup command = %q, want & 'codex' '-m' '%%EVIL%%' '-p' '!DANGER!'", spec.StartupCommand)
	}
}

func TestWindowsResolverCodexAttachWithPwshShell_RejectsNewlineArg(t *testing.T) {
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "codex.cmd")
	shellPath := filepath.Join(binDir, "pwsh.exe")
	for _, path := range []string{cliPath, shellPath} {
		if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
	}

	resolver := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	_, err := resolver.Resolve(ResolveRequest{
		AppType:            "codex",
		LaunchMode:         "embedded",
		RequestedShellPath: "pwsh",
		WorkDir:            `C:\work`,
		Env:                []string{"PATH=" + binDir},
		CLIArgs:            []string{"-m", "line1\nline2"},
		PTYCols:            80,
		PTYRows:            24,
	})
	if err == nil {
		t.Fatal("expected error for newline in pwsh args, got nil")
	}
}

func TestIsSafeCmdToken(t *testing.T) {
	tests := []struct {
		value string
		safe  bool
	}{
		{"opencode", true},
		{"codex", true},
		{"-m", true},
		{"gpt-5", true},
		{"gpt_5.4", true},
		{"model@v2", true},
		{"/path/to/bin", true},
		{`\windows\path`, true},
		{"C:", true},
		{"gpt&5", false},
		{"a|b", false},
		{"x>y", false},
		{"has space", false},
		{`has"quote`, false},
		{"$(cmd)", false},
		{"", true}, // empty handled separately by escapeCmdArg
	}
	for _, tt := range tests {
		got := isSafeCmdToken(tt.value)
		if got != tt.safe {
			t.Errorf("isSafeCmdToken(%q) = %v, want %v", tt.value, got, tt.safe)
		}
	}
}
