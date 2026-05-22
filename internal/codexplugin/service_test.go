package codexplugin

import (
	"amagi-codebox/internal/platform"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type codexPluginTestResolver struct{}

func (codexPluginTestResolver) Resolve(request platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	return platform.ResolvedLaunchSpec{}, fmt.Errorf("not used")
}

func (codexPluginTestResolver) ResolveExecutable(command string, args []string, env []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return platform.ResolvedCLI{Name: command, Path: command, Args: append([]string(nil), args...)}, platform.LaunchDiagnostics{}, nil
}

type codexPluginTestRunner struct {
	calls []platform.CommandSpec
}

func (r *codexPluginTestRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.calls = append(r.calls, spec)
	switch strings.Join(spec.Args, " ") {
	case "plugin marketplace list":
		return &platform.ProcessResult{Stderr: "error: unrecognized subcommand 'list'\n\nUsage: codex plugin marketplace [OPTIONS] <COMMAND>"}, fmt.Errorf("exit status 2")
	case "plugin list":
		return &platform.ProcessResult{Stdout: "Marketplace `amagi-codex-marketplace`\n  amagi@amagi-codex-marketplace (installed, enabled)"}, nil
	default:
		return &platform.ProcessResult{Stderr: "unexpected command: " + strings.Join(spec.Args, " ")}, fmt.Errorf("unexpected command")
	}
}

func (r *codexPluginTestRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, fmt.Errorf("not used")
}

func TestRefreshPluginsFallsBackWhenMarketplaceListSubcommandUnsupported(t *testing.T) {
	codexDir := t.TempDir()
	pluginRoot := filepath.Join(codexDir, "plugins", "cache", "amagi-codex-marketplace", "amagi", "1.5.116")
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0755); err != nil {
		t.Fatalf("mkdir plugin root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(`{"name":"amagi","version":"1.5.116"}`), 0644); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	runner := &codexPluginTestRunner{}
	s := NewServiceWithDeps(codexDir, nil, codexPluginTestResolver{}, runner)

	data, err := s.RefreshPlugins()
	if err != nil {
		t.Fatalf("RefreshPlugins should return data with warning instead of error: %v", err)
	}
	if data == nil {
		t.Fatalf("RefreshPlugins returned nil data")
	}
	if len(data.Installed) != 1 {
		t.Fatalf("expected installed plugin from plugin list fallback path, got %+v", data.Installed)
	}
	installed := data.Installed[0]
	if installed.ID != "amagi@amagi-codex-marketplace" || installed.Name != "amagi" || installed.Marketplace != "amagi-codex-marketplace" || !installed.Enabled {
		t.Fatalf("unexpected installed plugin: %+v", installed)
	}
	if installed.InstallPath != pluginRoot {
		t.Fatalf("expected installed plugin root resolved from cache, got %+v", installed)
	}
	if !hasMarketplace(data.Marketplaces, "amagi-codex-marketplace") {
		t.Fatalf("expected marketplace inferred from installed/cache fallback, got %+v", data.Marketplaces)
	}
	if data.Available == nil {
		t.Fatalf("expected non-nil available list")
	}
	if !containsWarning(data.Warnings, "unrecognized subcommand 'list'") {
		t.Fatalf("expected marketplace list failure warning, got %+v", data.Warnings)
	}
	if !runnerCalled(runner.calls, "plugin", "marketplace", "list") || !runnerCalled(runner.calls, "plugin", "list") {
		t.Fatalf("expected marketplace list and plugin list calls, got %+v", runner.calls)
	}
}

func hasMarketplace(marketplaces []CodexMarketplace, name string) bool {
	for _, marketplace := range marketplaces {
		if marketplace.Name == name {
			return true
		}
	}
	return false
}

func containsWarning(warnings []string, needle string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, needle) {
			return true
		}
	}
	return false
}

func runnerCalled(calls []platform.CommandSpec, args ...string) bool {
	for _, call := range calls {
		if reflect.DeepEqual(call.Args, args) {
			return true
		}
	}
	return false
}
