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
	if len(firstEntries) < 1+len(darwinBaselinePATH) {
		t.Fatalf("effective PATH has too few entries: %q", spec.Env.EffectivePATH)
	}
	if firstEntries[0] != binDir {
		t.Fatalf("effective PATH entry 0 = %q, want caller PATH %q", firstEntries[0], binDir)
	}
	for i, expected := range darwinBaselinePATH {
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
	if spec.Shell.BootstrapArg != "-lc" {
		t.Fatalf("bootstrap arg = %q, want -lc", spec.Shell.BootstrapArg)
	}
	if spec.BootstrapMode != BootstrapShellInline {
		t.Fatalf("bootstrap mode = %q, want %q", spec.BootstrapMode, BootstrapShellInline)
	}
	if !strings.Contains(spec.StartupCommand, cliPath) {
		t.Fatalf("startup command = %q, want resolved cli path included", spec.StartupCommand)
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
	cli, diagnostics, err := resolver.ResolveExecutable("go", nil, []string{"PATH="})
	if err == nil {
		t.Fatalf("expected foreign target resolution to ignore host ambient PATH, got cli=%+v diagnostics=%+v", cli, diagnostics)
	}
	if diagnostics.CLISource == "ambient-path" {
		t.Fatalf("foreign target resolution should not fall back to host ambient PATH")
	}
}
