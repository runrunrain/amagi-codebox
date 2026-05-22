package codexplugin

import (
	"amagi-codebox/internal/platform"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestActualInstallPluginResolvesCodexFromLocalNodeBinWithGUIPATH(t *testing.T) {
	if os.Getenv("AMAGI_CODEBOX_ACTUAL_CODEX_INSTALL_TEST") != "1" {
		t.Skip("set AMAGI_CODEBOX_ACTUAL_CODEX_INSTALL_TEST=1 to run actual Codex install validation")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("actual GUI PATH validation is only defined for darwin")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	expectedCodexPath := filepath.Join(homeDir, ".local", "node", "bin", "codex")
	if info, err := os.Stat(expectedCodexPath); err != nil || info.IsDir() {
		t.Fatalf("expected codex executable at %q: info=%v err=%v", expectedCodexPath, info, err)
	}

	previousPath := os.Getenv("PATH")
	previousHome := os.Getenv("HOME")
	guiPath := "/usr/bin:/bin:/usr/sbin:/sbin"
	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", guiPath)

	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	cli, diagnostics, err := resolver.ResolveExecutable("codex", []string{"plugin", "add", "amagi@amagi-codex-marketplace"}, os.Environ())
	if err != nil {
		t.Fatalf("ResolveExecutable under GUI PATH %q: %v", guiPath, err)
	}
	if cli.Path != expectedCodexPath {
		t.Fatalf("resolved codex path = %q, want %q", cli.Path, expectedCodexPath)
	}
	if diagnostics.CLISource != "path-search" {
		t.Fatalf("cli source = %q, want path-search", diagnostics.CLISource)
	}

	configPath := filepath.Join(homeDir, ".codex", "config.toml")
	before, readErr := os.ReadFile(configPath)
	beforeExisted := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read Codex config before install: %v", readErr)
	}
	t.Cleanup(func() {
		_ = os.Setenv("PATH", previousPath)
		_ = os.Setenv("HOME", previousHome)
		if beforeExisted {
			_ = os.WriteFile(configPath, before, 0o600)
		} else {
			_ = os.Remove(configPath)
		}
	})

	svc := NewService("", nil)
	result, err := svc.InstallPlugin(PluginSelector{PluginID: "amagi@amagi-codex-marketplace"})
	if err != nil {
		encodedResult, _ := json.Marshal(result)
		t.Fatalf("InstallPlugin under GUI PATH failed: %v result=%s", err, encodedResult)
	}
	if result == nil || !result.Success {
		t.Fatalf("InstallPlugin returned unsuccessful result: %#v", result)
	}
	if out := strings.TrimSpace(result.Output); out == "" {
		t.Fatalf("InstallPlugin returned empty output: %#v", result)
	} else {
		fmt.Fprintln(os.Stderr, "actual Codex install output:", out)
	}
}
