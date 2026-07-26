package opencodeplugin

import (
	"amagi-codebox/internal/platform"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

type testResolver struct{}

func (testResolver) Resolve(platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	return platform.ResolvedLaunchSpec{}, nil
}

func (testResolver) ResolveExecutable(name string, args []string, _ []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return platform.ResolvedCLI{Path: "/fake/" + name, Args: append([]string(nil), args...)}, platform.LaunchDiagnostics{}, nil
}

type testRunner struct {
	specs []platform.CommandSpec
	run   func(platform.CommandSpec) (*platform.ProcessResult, error)
}

func (r *testRunner) Start(platform.CommandSpec) (*exec.Cmd, error) {
	panic("not used")
}

func (r *testRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.specs = append(r.specs, spec)
	if r.run != nil {
		return r.run(spec)
	}
	return &platform.ProcessResult{Stdout: "Done"}, nil
}

type testHTTPDoer func(*http.Request) (*http.Response, error)

func (f testHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestListInstalledPluginsReadsConfigAndCache(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	spec := "github:owner/example#main"
	writeTestConfig(t, configDir, []any{spec, []any{"second-plugin@1.0.0", map[string]any{"mode": "safe"}}})
	writeTestCachedPackage(t, cacheDir, spec, "example-plugin", map[string]any{
		"name":        "example-plugin",
		"version":     "2.3.4",
		"description": "Example OpenCode plugin",
		"main":        "./index.js",
	})

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, &testRunner{})
	plugins, err := svc.ListInstalledPlugins()
	if err != nil {
		t.Fatalf("ListInstalledPlugins: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(plugins))
	}
	var found *Plugin
	for i := range plugins {
		if plugins[i].Spec == spec {
			found = &plugins[i]
		}
	}
	if found == nil {
		t.Fatalf("expected %s in plugin list: %#v", spec, plugins)
	}
	if found.Name != "example-plugin" || found.Version != "2.3.4" {
		t.Fatalf("unexpected package metadata: %#v", found)
	}
	if !reflect.DeepEqual(found.Targets, []string{"server"}) {
		t.Fatalf("unexpected targets: %#v", found.Targets)
	}
}

func TestListInstalledPluginsSupportsJSONC(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	writeTestFile(t, filepath.Join(configDir, "opencode.jsonc"), `{
  // OpenCode accepts comments and trailing commas.
  "plugin": [
    "example-plugin",
  ],
}`)

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, &testRunner{})
	plugins, err := svc.ListInstalledPlugins()
	if err != nil {
		t.Fatalf("ListInstalledPlugins: %v", err)
	}
	if len(plugins) != 1 || plugins[0].Spec != "example-plugin" {
		t.Fatalf("unexpected plugins: %#v", plugins)
	}
}

func TestGetPluginDetailsDiscoversAssets(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	spec := "example-plugin"
	writeTestConfig(t, configDir, []any{spec})
	root := writeTestCachedPackage(t, cacheDir, spec, "example-plugin", map[string]any{
		"name":    "example-plugin",
		"version": "1.0.0",
		"exports": map[string]any{"./server": "./index.js", "./tui": "./tui.js"},
	})
	writeTestFile(t, filepath.Join(root, "skills", "workflow", "SKILL.md"), "# Workflow")
	writeTestFile(t, filepath.Join(root, "commands", "deploy.md"), "# Deploy")
	writeTestFile(t, filepath.Join(root, "agents", "reviewer.md"), "# Reviewer")
	writeTestFile(t, filepath.Join(root, "mcp", "servers.json"), "{}")

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, &testRunner{})
	detail, err := svc.GetPluginDetails(spec)
	if err != nil {
		t.Fatalf("GetPluginDetails: %v", err)
	}
	if len(detail.Skills) != 1 || len(detail.Commands) != 1 || len(detail.Agents) != 1 || !detail.HasMCP {
		t.Fatalf("unexpected detail: %#v", detail)
	}
	if !reflect.DeepEqual(detail.Targets, []string{"server", "tui"}) {
		t.Fatalf("unexpected targets: %#v", detail.Targets)
	}
}

func TestInstallUsesOfficialGlobalCLI(t *testing.T) {
	runner := &testRunner{}
	svc := NewServiceWithDeps(t.TempDir(), t.TempDir(), nil, testResolver{}, runner)

	if _, err := svc.InstallPlugin("example-plugin"); err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}
	got := runner.specs[0].Args
	want := []string{"plugin", "example-plugin", "--global"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected CLI args: got %#v want %#v", got, want)
	}
}

func TestUpdateGitHubPluginSwitchesMovingRefToLatestImmutableTag(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	currentSpec := "github:owner/example#main"
	targetSpec := "github:owner/example#v1.5.164"
	writeTestConfig(t, configDir, []any{currentSpec})
	writeTestCachedPackage(t, cacheDir, currentSpec, "example-plugin", map[string]any{
		"name":    "example-plugin",
		"version": "1.5.161",
		"main":    "./index.js",
	})

	runner := &testRunner{}
	runner.run = func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		switch spec.Path {
		case "/fake/git":
			return &platform.ProcessResult{Stdout: strings.Join([]string{
				"aaa\trefs/tags/v1.5.163",
				"bbb\trefs/tags/v1.5.164",
				"ccc\trefs/tags/v2.0.0-rc.1",
			}, "\n")}, nil
		case "/fake/opencode":
			writeTestConfig(t, configDir, []any{targetSpec})
			writeTestCachedPackage(t, cacheDir, targetSpec, "example-plugin", map[string]any{
				"name":    "example-plugin",
				"version": "1.5.164",
				"main":    "./index.js",
			})
			return &platform.ProcessResult{Stdout: "Done"}, nil
		default:
			t.Fatalf("unexpected executable: %s", spec.Path)
			return nil, nil
		}
	}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	result, err := svc.UpdatePlugin(currentSpec)
	if err != nil {
		t.Fatalf("UpdatePlugin: %v", err)
	}
	if !result.Success || !strings.Contains(result.Output, targetSpec) {
		t.Fatalf("unexpected update result: %#v", result)
	}
	if len(runner.specs) != 2 {
		t.Fatalf("expected git lookup and opencode install, got %#v", runner.specs)
	}
	wantArgs := []string{"plugin", targetSpec, "--global", "--force"}
	if !reflect.DeepEqual(runner.specs[1].Args, wantArgs) {
		t.Fatalf("unexpected OpenCode args: got %#v want %#v", runner.specs[1].Args, wantArgs)
	}
	configured, err := svc.readConfiguredSpecs()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(configured, []string{targetSpec}) {
		t.Fatalf("unexpected configured specs: %#v", configured)
	}
}

func TestUpdateGitHubPluginSkipsAlreadyLatestImmutableTag(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	spec := "github:owner/example#v1.5.164"
	writeTestConfig(t, configDir, []any{spec})
	writeTestCachedPackage(t, cacheDir, spec, "example-plugin", map[string]any{
		"name":    "example-plugin",
		"version": "1.5.164",
		"main":    "./index.js",
	})
	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		if command.Path != "/fake/git" {
			t.Fatalf("already-current plugin should not run %s", command.Path)
		}
		return &platform.ProcessResult{Stdout: "bbb\trefs/tags/v1.5.164"}, nil
	}}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	result, err := svc.UpdatePlugin(spec)
	if err != nil {
		t.Fatalf("UpdatePlugin: %v", err)
	}
	if !result.Success || !strings.Contains(result.Output, "已是最新不可变版本") {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("expected only the git lookup, got %#v", runner.specs)
	}
}

func TestUpdateGitHubPluginMigratesMainEvenWhenCachedVersionMatches(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	currentSpec := "github:owner/example#main"
	targetSpec := "github:owner/example#v1.5.164"
	writeTestConfig(t, configDir, []any{currentSpec})
	writeTestCachedPackage(t, cacheDir, currentSpec, "example-plugin", map[string]any{
		"name":    "example-plugin",
		"version": "1.5.164",
		"main":    "./index.js",
	})
	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		if command.Path == "/fake/git" {
			return &platform.ProcessResult{Stdout: "bbb\trefs/tags/v1.5.164"}, nil
		}
		writeTestConfig(t, configDir, []any{targetSpec})
		writeTestCachedPackage(t, cacheDir, targetSpec, "example-plugin", map[string]any{
			"name":    "example-plugin",
			"version": "1.5.164",
			"main":    "./index.js",
		})
		return &platform.ProcessResult{Stdout: "Done"}, nil
	}}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	if _, err := svc.UpdatePlugin(currentSpec); err != nil {
		t.Fatalf("UpdatePlugin: %v", err)
	}
	if len(runner.specs) != 2 || runner.specs[1].Args[1] != targetSpec {
		t.Fatalf("moving ref was not migrated to immutable tag: %#v", runner.specs)
	}
}

func TestUpdateNPMPluginPinsLatestExactVersion(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	currentSpec := "example-plugin"
	targetSpec := "example-plugin@2.3.4"
	writeTestConfig(t, configDir, []any{currentSpec})
	writeTestCachedPackage(t, cacheDir, currentSpec, "example-plugin", map[string]any{
		"name":    "example-plugin",
		"version": "2.3.3",
		"main":    "./index.js",
	})

	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		if command.Path != "/fake/opencode" {
			t.Fatalf("unexpected executable: %s", command.Path)
		}
		writeTestConfig(t, configDir, []any{targetSpec})
		writeTestCachedPackage(t, cacheDir, targetSpec, "example-plugin", map[string]any{
			"name":    "example-plugin",
			"version": "2.3.4",
			"main":    "./index.js",
		})
		return &platform.ProcessResult{Stdout: "Done"}, nil
	}}
	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	svc.http = testHTTPDoer(func(req *http.Request) (*http.Response, error) {
		if req.URL.EscapedPath() != "/example-plugin/latest" {
			t.Fatalf("unexpected npm URL: %s", req.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"version":"2.3.4"}`)),
			Header:     make(http.Header),
		}, nil
	})

	result, err := svc.UpdatePlugin(currentSpec)
	if err != nil {
		t.Fatalf("UpdatePlugin: %v", err)
	}
	if !result.Success || !strings.Contains(result.Output, targetSpec) {
		t.Fatalf("unexpected result: %#v", result)
	}
	wantArgs := []string{"plugin", targetSpec, "--global", "--force"}
	if len(runner.specs) != 1 || !reflect.DeepEqual(runner.specs[0].Args, wantArgs) {
		t.Fatalf("unexpected OpenCode calls: %#v", runner.specs)
	}
}

func TestUpdateRejectsFilePlugin(t *testing.T) {
	svc := NewServiceWithDeps(t.TempDir(), t.TempDir(), nil, testResolver{}, &testRunner{})
	if _, err := svc.UpdatePlugin("file:///tmp/example-plugin"); err == nil || !strings.Contains(err.Error(), "直接从原路径加载") {
		t.Fatalf("expected local plugin update error, got %v", err)
	}
}

func TestUpdateSpecParsers(t *testing.T) {
	githubCases := map[string]githubRepository{
		"github:owner/repo#main":                   {Owner: "owner", Name: "repo", Ref: "main"},
		"https://github.com/owner/repo.git#v1.2.3": {Owner: "owner", Name: "repo", Ref: "v1.2.3"},
		"git@github.com:owner/repo.git#deadbeef":   {Owner: "owner", Name: "repo", Ref: "deadbeef"},
	}
	for spec, want := range githubCases {
		got, err := parseGitHubRepository(spec)
		if err != nil {
			t.Fatalf("parseGitHubRepository(%q): %v", spec, err)
		}
		if got != want {
			t.Fatalf("parseGitHubRepository(%q): got %#v want %#v", spec, got, want)
		}
	}

	npmCases := map[string]string{
		"example-plugin":        "example-plugin",
		"example-plugin@1.2.3":  "example-plugin",
		"@scope/example-plugin": "@scope/example-plugin",
		"@scope/example@v1.2.3": "@scope/example",
		"@scope/example@latest": "@scope/example",
	}
	for spec, want := range npmCases {
		got, err := npmPackageName(spec)
		if err != nil {
			t.Fatalf("npmPackageName(%q): %v", spec, err)
		}
		if got != want {
			t.Fatalf("npmPackageName(%q): got %q want %q", spec, got, want)
		}
	}
}

func TestUninstallRemovesOnlySelectedPlugin(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	writeTestConfig(t, configDir, []any{
		"keep-plugin",
		[]any{"remove-plugin", map[string]any{"mode": "safe"}},
	})
	path := filepath.Join(configDir, "opencode.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["model"] = "provider/model"
	encoded, _ := json.Marshal(document)
	if err := os.WriteFile(path, encoded, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, &testRunner{})
	result, err := svc.UninstallPlugin("remove-plugin")
	if err != nil {
		t.Fatalf("UninstallPlugin: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success: %#v", result)
	}
	plugins, err := svc.readConfiguredSpecs()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plugins, []string{"keep-plugin"}) {
		t.Fatalf("unexpected plugins after uninstall: %#v", plugins)
	}
	after, _ := os.ReadFile(path)
	if gjson.GetBytes(after, "model").String() != "provider/model" {
		t.Fatalf("uninstall changed unrelated config: %s", after)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("config permissions were not preserved: info=%v err=%v", info, err)
	}
}

func writeTestConfig(t *testing.T, configDir string, plugins []any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"plugin": plugins})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(configDir, "opencode.json"), string(data))
}

func writeTestCachedPackage(t *testing.T, cacheDir string, spec string, packageName string, manifest map[string]any) string {
	t.Helper()
	wrapper := filepath.Join(cacheDir, "packages", filepath.FromSlash(spec))
	root := filepath.Join(wrapper, "node_modules", filepath.FromSlash(packageName))
	wrapperData, _ := json.Marshal(map[string]any{"dependencies": map[string]string{packageName: spec}})
	manifestData, _ := json.Marshal(manifest)
	writeTestFile(t, filepath.Join(wrapper, "package.json"), string(wrapperData))
	writeTestFile(t, filepath.Join(root, "package.json"), string(manifestData))
	return root
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
