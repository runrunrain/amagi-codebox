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
	calls            []platform.CommandSpec
	pluginListOutput string
}

func (r *codexPluginTestRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.calls = append(r.calls, spec)
	switch strings.Join(spec.Args, " ") {
	case "plugin marketplace list":
		return &platform.ProcessResult{Stderr: "error: unrecognized subcommand 'list'\n\nUsage: codex plugin marketplace [OPTIONS] <COMMAND>"}, fmt.Errorf("exit status 2")
	case "plugin list":
		output := r.pluginListOutput
		if output == "" {
			output = "Marketplace `amagi-codex-marketplace`\n  amagi@amagi-codex-marketplace (installed, enabled)"
		}
		return &platform.ProcessResult{Stdout: output}, nil
	default:
		return &platform.ProcessResult{Stderr: "unexpected command: " + strings.Join(spec.Args, " ")}, fmt.Errorf("unexpected command")
	}
}

func TestRefreshPluginsDeduplicatesPlaceholderPluginRecordWithoutDeletingFiles(t *testing.T) {
	codexDir := t.TempDir()
	pluginRoot := filepath.Join(codexDir, "plugins", "cache", "amagi-codex-marketplace", "amagi", "1.5.116")
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0755); err != nil {
		t.Fatalf("mkdir plugin root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(`{"name":"amagi","version":"1.5.116"}`), 0644); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	runner := &codexPluginTestRunner{pluginListOutput: strings.Join([]string{
		"amagi@amagi-codex-marketplace installed enabled 1.5.116 " + pluginRoot,
		"PLUGIN@amagi-codex-marketplace installed enabled 1.5.116 " + pluginRoot,
	}, "\n")}
	s := NewServiceWithDeps(codexDir, nil, codexPluginTestResolver{}, runner)

	data, err := s.RefreshPlugins()
	if err != nil {
		t.Fatalf("RefreshPlugins: %v", err)
	}
	if len(data.Installed) != 1 {
		t.Fatalf("expected duplicate records to be deduplicated, got %+v", data.Installed)
	}
	installed := data.Installed[0]
	if installed.ID != "amagi@amagi-codex-marketplace" || installed.Warning == "" {
		t.Fatalf("expected canonical amagi plugin with duplicate warning, got %+v", installed)
	}
	if !containsWarning(data.Warnings, "PLUGIN@amagi-codex-marketplace") || !containsWarning(data.Warnings, "未删除任何用户文件") {
		t.Fatalf("expected duplicate warning without destructive cleanup, got %+v", data.Warnings)
	}
	if _, err := os.Stat(pluginRoot); err != nil {
		t.Fatalf("plugin root should not be deleted by duplicate diagnosis: %v", err)
	}
}

func TestGetPluginDetailsExposesCodexInterfaceDescriptions(t *testing.T) {
	codexDir := t.TempDir()
	pluginRoot := filepath.Join(codexDir, "plugins", "cache", "amagi-codex-marketplace", "amagi", "1.5.116")
	manifestDir := filepath.Join(pluginRoot, ".codex-plugin")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatalf("mkdir plugin root: %v", err)
	}
	manifest := []byte(`{
  "name": "amagi",
  "version": "1.5.116",
  "description": "Top-level fallback description",
  "interface": {
    "displayName": "Amagi Display",
    "shortDescription": "Short detail description",
    "longDescription": "Long detail description"
  }
}`)
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), manifest, 0644); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	runner := &codexPluginTestRunner{pluginListOutput: "amagi@amagi-codex-marketplace installed enabled 1.5.116 " + pluginRoot}
	s := NewServiceWithDeps(codexDir, nil, codexPluginTestResolver{}, runner)

	detail, err := s.GetPluginDetails(PluginSelector{PluginID: "amagi@amagi-codex-marketplace"})
	if err != nil {
		t.Fatalf("GetPluginDetails: %v", err)
	}
	if detail.Manifest.Interface == nil {
		t.Fatalf("expected manifest interface metadata in detail: %+v", detail.Manifest)
	}
	if detail.DisplayName != "Amagi Display" || detail.ShortDescription != "Short detail description" || detail.LongDescription != "Long detail description" {
		t.Fatalf("detail did not expose interface descriptions: %+v", detail)
	}
}

func TestDiagnoseAndDedupeCodexPluginsMarksSameInstallPathDuplicate(t *testing.T) {
	pluginRoot := filepath.Join("tmp", "codex", "plugins", "cache", "market", "amagi", "1.0.0")
	plugins, warnings := diagnoseAndDedupeCodexPlugins([]CodexPlugin{
		{ID: "PLUGIN@market", Name: "PLUGIN", Marketplace: "market", InstallPath: pluginRoot, Source: "cli"},
		{ID: "amagi@market", Name: "amagi", Marketplace: "market", InstallPath: pluginRoot, Source: "cli"},
	})
	if len(plugins) != 1 {
		t.Fatalf("expected one canonical plugin, got %+v", plugins)
	}
	if plugins[0].ID != "amagi@market" || plugins[0].Warning == "" {
		t.Fatalf("expected non-placeholder plugin to be canonical and warned, got %+v", plugins[0])
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "PLUGIN@market") {
		t.Fatalf("expected duplicate warning mentioning placeholder record, got %+v", warnings)
	}
}

func TestDiagnoseAndDedupeCodexPluginsMergesTmpMarketplaceAndCachePaths(t *testing.T) {
	codexDir := t.TempDir()
	tmpMarketplaceRoot := filepath.Join(codexDir, ".tmp", "marketplaces", "amagi-codex-marketplace", "plugins", "amagi")
	cacheRoot := filepath.Join(codexDir, "plugins", "cache", "amagi-codex-marketplace", "amagi", "1.5.116")

	plugins, warnings := diagnoseAndDedupeCodexPlugins([]CodexPlugin{
		{ID: "PLUGIN@amagi-codex-marketplace", Name: "PLUGIN", Marketplace: "amagi-codex-marketplace", InstallPath: tmpMarketplaceRoot, ManifestPath: filepath.Join(tmpMarketplaceRoot, ".codex-plugin", "plugin.json"), Source: "cli"},
		{ID: "amagi@amagi-codex-marketplace", Name: "amagi", Marketplace: "amagi-codex-marketplace", InstallPath: cacheRoot, ManifestPath: filepath.Join(cacheRoot, ".codex-plugin", "plugin.json"), Source: "configFallback"},
	})

	if len(plugins) != 1 {
		t.Fatalf("expected tmp marketplace and cache records to merge into one canonical plugin, got %+v", plugins)
	}
	canonical := plugins[0]
	if canonical.ID != "amagi@amagi-codex-marketplace" {
		t.Fatalf("expected real plugin ID to be canonical, got %+v", canonical)
	}
	if canonical.InstallPath != cacheRoot {
		t.Fatalf("expected canonical install path to prefer cache path %q, got %+v", cacheRoot, canonical)
	}
	if canonical.Warning != "" {
		t.Fatalf("expected tmp marketplace source duplicate to be merged without user-visible warning, got %q", canonical.Warning)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected refresh warnings to ignore tmp marketplace source duplicate, got %+v", warnings)
	}
}

func TestDiagnoseAndDedupeCodexPluginsWarnsForRealDuplicateOutsideTmpMarketplace(t *testing.T) {
	codexDir := t.TempDir()
	cacheRoot := filepath.Join(codexDir, "plugins", "cache", "market", "sample", "1.0.0")
	duplicateRoot := filepath.Join(codexDir, "plugins", "cache", "market", "sample", "0.9.0")

	plugins, warnings := diagnoseAndDedupeCodexPlugins([]CodexPlugin{
		{ID: "sample@market", Name: "sample", Marketplace: "market", InstallPath: cacheRoot, ManifestPath: filepath.Join(cacheRoot, ".codex-plugin", "plugin.json"), Source: "cli"},
		{ID: "PLUGIN@market", Name: "PLUGIN", Marketplace: "market", InstallPath: duplicateRoot, ManifestPath: filepath.Join(duplicateRoot, ".codex-plugin", "plugin.json"), Source: "cli"},
	})

	if len(plugins) != 1 {
		t.Fatalf("expected real duplicate records to merge into one canonical plugin, got %+v", plugins)
	}
	canonical := plugins[0]
	if canonical.ID != "sample@market" {
		t.Fatalf("expected real plugin ID to remain canonical, got %+v", canonical)
	}
	if canonical.InstallPath != cacheRoot {
		t.Fatalf("expected canonical install path to prefer cache path %q, got %+v", cacheRoot, canonical)
	}
	if canonical.Warning == "" || !strings.Contains(canonical.Warning, duplicateRoot) || !strings.Contains(canonical.Warning, cacheRoot) {
		t.Fatalf("expected real duplicate warning to explain duplicate/cache paths, got %q", canonical.Warning)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "PLUGIN@market") || !strings.Contains(warnings[0], "未删除任何用户文件") {
		t.Fatalf("expected non-destructive duplicate warning for real duplicate, got %+v", warnings)
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
