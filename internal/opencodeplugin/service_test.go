package opencodeplugin

import (
	"amagi-codebox/internal/platform"
	"context"
	"encoding/json"
	"fmt"
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

// fakeCLIFailure 仿照 opencode 1.18.10 对 github: spec 的「假失败」错误。
const fakeCLIFailure = "NpmInstallFailedError: failed to resolve entry point"

// TestUpdateGitHubPluginFallsBackWhenCLIFailsButCacheReady 模拟 opencode CLI「假失败」：
// git 解析正常（fake runner 返回预置 tags，无真实网络），opencode plugin 命令把包装入
// cache 但返回非零退出。断言 UpdatePlugin 走本地兜底成功、config 切到 target.Spec、旧 spec 移除。
func TestUpdateGitHubPluginFallsBackWhenCLIFailsButCacheReady(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	currentSpec := "github:owner/example#main"
	targetSpec := "github:owner/example#v1.5.164"
	writeTestConfig(t, configDir, []any{currentSpec})
	writeTestCachedPackage(t, cacheDir, currentSpec, "example-plugin", map[string]any{
		"name": "example-plugin", "version": "1.5.161", "main": "./index.js",
	})

	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		switch command.Path {
		case "/fake/git":
			return &platform.ProcessResult{Stdout: "bbb\trefs/tags/v1.5.164"}, nil
		case "/fake/opencode":
			// 假失败：包装入 cache 但 CLI 报错，且不写配置
			writeTestCachedPackage(t, cacheDir, targetSpec, "example-plugin", map[string]any{
				"name": "example-plugin", "version": "1.5.164", "main": "./index.js",
			})
			return nil, fmt.Errorf("%s", fakeCLIFailure)
		default:
			t.Fatalf("unexpected executable: %s", command.Path)
			return nil, nil
		}
	}}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	result, err := svc.UpdatePlugin(currentSpec)
	if err != nil {
		t.Fatalf("cache 已就绪时兜底应成功，得到错误: %v", err)
	}
	if !result.Success {
		t.Fatalf("期望兜底成功，得到 %#v", result)
	}
	if !strings.Contains(result.Output, "本地兜底") {
		t.Fatalf("输出应注明走了本地兜底: %#v", result)
	}
	if len(runner.specs) != 2 || !reflect.DeepEqual(runner.specs[1].Args,
		[]string{"plugin", targetSpec, "--global", "--force"}) {
		t.Fatalf("应执行 git 查询 + opencode 更新两步: %#v", runner.specs)
	}
	configured, err := svc.readConfiguredSpecs()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(configured, []string{targetSpec}) {
		t.Fatalf("配置应切换到 target spec 且旧 spec 已移除: %#v", configured)
	}
}

// TestUpdateGitHubPluginReturnsCLIErrWhenCacheMissing 模拟 CLI 失败且 cache 未装好：
// 断言返回原始 CLI 错误、配置不被改动。
func TestUpdateGitHubPluginReturnsCLIErrWhenCacheMissing(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	currentSpec := "github:owner/example#main"
	writeTestConfig(t, configDir, []any{currentSpec})

	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		if command.Path == "/fake/git" {
			return &platform.ProcessResult{Stdout: "bbb\trefs/tags/v1.5.164"}, nil
		}
		// 假失败且不写 cache
		return nil, fmt.Errorf("%s", fakeCLIFailure)
	}}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	result, err := svc.UpdatePlugin(currentSpec)
	if err == nil {
		t.Fatalf("cache 缺失时应返回原始 CLI 错误，得到成功: %#v", result)
	}
	if result == nil || result.Success {
		t.Fatalf("期望失败 result: %#v", result)
	}
	configured, _ := svc.readConfiguredSpecs()
	if !reflect.DeepEqual(configured, []string{currentSpec}) {
		t.Fatalf("配置不应被改动: %#v", configured)
	}
}

// TestInstallPluginFallsBackWhenCLIFailsButCacheReady 模拟 Install 时 CLI 假失败但
// cache 已装好且版本匹配最新 tag：断言兜底成功且 spec 被写入配置。
//
// 收窄后（Major-2）兜底前置依赖 git ls-remote 成功解析 latest tag，故 fake runner
// 必须区分 /fake/git（返回预置 tags）与 /fake/opencode（写 cache + 假失败）。
func TestInstallPluginFallsBackWhenCLIFailsButCacheReady(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	spec := "github:owner/example#main"

	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		switch command.Path {
		case "/fake/git":
			return &platform.ProcessResult{Stdout: "bbb\trefs/tags/v1.5.164"}, nil
		case "/fake/opencode":
			writeTestCachedPackage(t, cacheDir, spec, "example-plugin", map[string]any{
				"name": "example-plugin", "version": "1.5.164", "main": "./index.js",
			})
			return nil, fmt.Errorf("%s", fakeCLIFailure)
		default:
			t.Fatalf("unexpected executable: %s", command.Path)
			return nil, nil
		}
	}}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	result, err := svc.InstallPlugin(spec)
	if err != nil {
		t.Fatalf("cache 已就绪时兜底应成功: %v", err)
	}
	if !result.Success || !strings.Contains(result.Output, "本地兜底") {
		t.Fatalf("期望兜底成功: %#v", result)
	}
	configured, _ := svc.readConfiguredSpecs()
	if !reflect.DeepEqual(configured, []string{spec}) {
		t.Fatalf("配置应包含 spec: %#v", configured)
	}
}

// TestInstallPluginReturnsCLIErrWhenCacheMissing 模拟 Install 时 CLI 失败且 cache 缺失
// （git ls-remote 成功解析 latest tag，但 opencode 假失败不写 cache）：断言返回原始
// CLI 错误、配置为空。
func TestInstallPluginReturnsCLIErrWhenCacheMissing(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	spec := "github:owner/example#main"

	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		switch command.Path {
		case "/fake/git":
			return &platform.ProcessResult{Stdout: "bbb\trefs/tags/v1.5.164"}, nil
		case "/fake/opencode":
			// 假失败且不写 cache
			return nil, fmt.Errorf("%s", fakeCLIFailure)
		default:
			t.Fatalf("unexpected executable: %s", command.Path)
			return nil, nil
		}
	}}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	result, err := svc.InstallPlugin(spec)
	if err == nil {
		t.Fatalf("cache 缺失时应返回原始 CLI 错误: %#v", result)
	}
	if result == nil || result.Success {
		t.Fatalf("期望失败 result: %#v", result)
	}
	configured, _ := svc.readConfiguredSpecs()
	if len(configured) != 0 {
		t.Fatalf("配置应为空: %#v", configured)
	}
}

// --- Major-2 收窄后的负向测试矩阵 ---
//
// 兜底现在严格收窄到「github spec + ls-remote 成功 + cache 版本匹配最新 tag」，
// 下列测试覆盖三个不达标分支：陈旧 cache、npm spec、ls-remote 失败。

// TestInstallPluginRejectsStaleCacheOnGithubFallback 模拟 cache 预存陈旧版本
// （1.5.161），latest tag 是 v1.5.164，CLI 假失败不刷新 cache：版本不符应返回
// 原始 CLI 错误且不写配置（防陈旧 cache 被误报成功）。
func TestInstallPluginRejectsStaleCacheOnGithubFallback(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	spec := "github:owner/example#main"
	// 预存陈旧 cache（版本 1.5.161），CLI 假失败时不会刷新它
	writeTestCachedPackage(t, cacheDir, spec, "example-plugin", map[string]any{
		"name": "example-plugin", "version": "1.5.161", "main": "./index.js",
	})

	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		switch command.Path {
		case "/fake/git":
			// latest tag 是 v1.5.164，cache 里是 1.5.161 → 版本不符
			return &platform.ProcessResult{Stdout: "bbb\trefs/tags/v1.5.164"}, nil
		case "/fake/opencode":
			// 假失败且不刷新 cache
			return nil, fmt.Errorf("%s", fakeCLIFailure)
		default:
			t.Fatalf("unexpected executable: %s", command.Path)
			return nil, nil
		}
	}}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	result, err := svc.InstallPlugin(spec)
	if err == nil {
		t.Fatalf("陈旧 cache 应返回原始 CLI 错误，得到成功: %#v", result)
	}
	if result == nil || result.Success {
		t.Fatalf("期望失败 result: %#v", result)
	}
	configured, _ := svc.readConfiguredSpecs()
	if len(configured) != 0 {
		t.Fatalf("陈旧 cache 时配置不应被写入: %#v", configured)
	}
}

// TestInstallPluginDoesNotFallbackForNpmSpec 模拟 npm spec CLI 失败（即便 cache
// 已存在）：收窄后仅 github spec 走兜底，npm spec 原样报错。
func TestInstallPluginDoesNotFallbackForNpmSpec(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	spec := "example-plugin"
	// 预存 cache（即便有，npm spec 也不应兜底）
	writeTestCachedPackage(t, cacheDir, spec, "example-plugin", map[string]any{
		"name": "example-plugin", "version": "1.0.0", "main": "./index.js",
	})

	runner := &testRunner{run: func(platform.CommandSpec) (*platform.ProcessResult, error) {
		return nil, fmt.Errorf("%s", fakeCLIFailure)
	}}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	result, err := svc.InstallPlugin(spec)
	if err == nil {
		t.Fatalf("npm spec CLI 失败应原样报错，得到成功: %#v", result)
	}
	if result == nil || result.Success {
		t.Fatalf("期望失败 result: %#v", result)
	}
	configured, _ := svc.readConfiguredSpecs()
	if len(configured) != 0 {
		t.Fatalf("npm spec 不兜底，配置应为空: %#v", configured)
	}
}

// TestInstallPluginReturnsCLIErrWhenLsRemoteFails 模拟 cache 已就绪但 git ls-remote
// 失败：无法证明 cache 是本次 latest tag 产物，应返回原始 CLI 错误不写配置。
func TestInstallPluginReturnsCLIErrWhenLsRemoteFails(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	spec := "github:owner/example#main"
	// cache 已就绪，但 ls-remote 失败时无法校验版本，仍不应兜底
	writeTestCachedPackage(t, cacheDir, spec, "example-plugin", map[string]any{
		"name": "example-plugin", "version": "1.5.164", "main": "./index.js",
	})

	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		switch command.Path {
		case "/fake/git":
			return nil, fmt.Errorf("ls-remote network down")
		case "/fake/opencode":
			return nil, fmt.Errorf("%s", fakeCLIFailure)
		default:
			t.Fatalf("unexpected executable: %s", command.Path)
			return nil, nil
		}
	}}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	result, err := svc.InstallPlugin(spec)
	if err == nil {
		t.Fatalf("ls-remote 失败应返回原始 CLI 错误，得到成功: %#v", result)
	}
	if result == nil || result.Success {
		t.Fatalf("期望失败 result: %#v", result)
	}
	configured, _ := svc.readConfiguredSpecs()
	if len(configured) != 0 {
		t.Fatalf("ls-remote 失败时配置不应被写入: %#v", configured)
	}
}

// TestUpdateGitHubPluginRejectsStaleCacheOnFallback 模拟 Update 时 CLI 假失败且
// target.Spec 的 cache 缺失/陈旧（版本不符）：应返回原始 CLI 错误、配置不变。
func TestUpdateGitHubPluginRejectsStaleCacheOnFallback(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	currentSpec := "github:owner/example#main"
	writeTestConfig(t, configDir, []any{currentSpec})
	// 当前 cache 是陈旧版本 1.5.161（target.Spec 的 cache 不存在）
	writeTestCachedPackage(t, cacheDir, currentSpec, "example-plugin", map[string]any{
		"name": "example-plugin", "version": "1.5.161", "main": "./index.js",
	})

	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		switch command.Path {
		case "/fake/git":
			return &platform.ProcessResult{Stdout: "bbb\trefs/tags/v1.5.164"}, nil
		case "/fake/opencode":
			// 假失败且不刷新 cache（target.Spec 的 cache 缺失）
			return nil, fmt.Errorf("%s", fakeCLIFailure)
		default:
			t.Fatalf("unexpected executable: %s", command.Path)
			return nil, nil
		}
	}}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	result, err := svc.UpdatePlugin(currentSpec)
	if err == nil {
		t.Fatalf("陈旧 cache 应返回原始 CLI 错误，得到成功: %#v", result)
	}
	if result == nil || result.Success {
		t.Fatalf("期望失败 result: %#v", result)
	}
	configured, _ := svc.readConfiguredSpecs()
	if !reflect.DeepEqual(configured, []string{currentSpec}) {
		t.Fatalf("配置不应被改动: %#v", configured)
	}
}

// TestUpdateNPMPluginDoesNotFallbackWhenCLIFails 模拟 npm spec Update 时 CLI 失败：
// 收窄后仅 github spec 走兜底，npm spec 原样报错、配置不变。
func TestUpdateNPMPluginDoesNotFallbackWhenCLIFails(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	currentSpec := "example-plugin"
	writeTestConfig(t, configDir, []any{currentSpec})
	writeTestCachedPackage(t, cacheDir, currentSpec, "example-plugin", map[string]any{
		"name": "example-plugin", "version": "2.3.3", "main": "./index.js",
	})

	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		if command.Path != "/fake/opencode" {
			t.Fatalf("unexpected executable: %s", command.Path)
		}
		// npm CLI 假失败（实证不会，此处验证收窄：即便 cache 存在也不兜底）
		return nil, fmt.Errorf("%s", fakeCLIFailure)
	}}
	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	svc.http = testHTTPDoer(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"version":"2.3.4"}`)),
			Header:     make(http.Header),
		}, nil
	})

	result, err := svc.UpdatePlugin(currentSpec)
	if err == nil {
		t.Fatalf("npm spec CLI 失败应原样报错，得到成功: %#v", result)
	}
	if result == nil || result.Success {
		t.Fatalf("期望失败 result: %#v", result)
	}
	configured, _ := svc.readConfiguredSpecs()
	if !reflect.DeepEqual(configured, []string{currentSpec}) {
		t.Fatalf("npm spec 不兜底，配置不应被改动: %#v", configured)
	}
}

// --- Minor-2：ensurePluginSpecInConfig 边界测试矩阵 ---
//
// 围绕配置兜底 helper 的真实调用补最小边界，避免未来回归在主测试全绿时破坏
// 配置安全（JSONC 不写、不重复、保 mode）。

// TestEnsurePluginSpecInConfigRejectsJSONCAndKeepsFile 断言 JSONC 含注释时报错
// 且文件内容字节级不变。
func TestEnsurePluginSpecInConfigRejectsJSONCAndKeepsFile(t *testing.T) {
	configDir := t.TempDir()
	path := filepath.Join(configDir, "opencode.jsonc")
	original := `{
  // comment
  "plugin": ["existing"]
}`
	writeTestFile(t, path, original)
	svc := NewServiceWithDeps(configDir, t.TempDir(), nil, testResolver{}, &testRunner{})
	err := svc.ensurePluginSpecInConfig("", "new-plugin")
	if err == nil {
		t.Fatalf("JSONC 配置应报错")
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != original {
		t.Fatalf("JSONC 报错时文件内容不应改变:\n got=%q\nwant=%q", string(after), original)
	}
}

// TestEnsurePluginSpecInConfigIdempotentWhenSpecExists 断言 spec 已在配置中时不重复添加。
func TestEnsurePluginSpecInConfigIdempotentWhenSpecExists(t *testing.T) {
	configDir := t.TempDir()
	svc := NewServiceWithDeps(configDir, t.TempDir(), nil, testResolver{}, &testRunner{})
	writeTestConfig(t, configDir, []any{"existing", "other"})
	if err := svc.ensurePluginSpecInConfig("", "existing"); err != nil {
		t.Fatalf("已存在 spec 应幂等成功: %v", err)
	}
	configured, _ := svc.readConfiguredSpecs()
	if !reflect.DeepEqual(configured, []string{"existing", "other"}) {
		t.Fatalf("不应重复添加: %#v", configured)
	}
}

// TestEnsurePluginSpecInConfigPreservesFileMode 断言 helper 写后文件 mode 与原文件一致
// （writeAtomic 在 POSIX 保 mode，此处覆盖新 helper 路径）。
func TestEnsurePluginSpecInConfigPreservesFileMode(t *testing.T) {
	configDir := t.TempDir()
	path := filepath.Join(configDir, "opencode.json")
	writeTestConfig(t, configDir, []any{"existing"})
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithDeps(configDir, t.TempDir(), nil, testResolver{}, &testRunner{})
	if err := svc.ensurePluginSpecInConfig("", "new-plugin"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("文件 mode 应保持 0o600，得到 %v", info.Mode().Perm())
	}
}

// --- Major-1 签名守卫 + Minor-1 配置写失败边界 ---
//
// 兜底现在在最外层增加 isKnownFalseInstallFailure 签名守卫：仅当 CLI 失败文本
// （result.Error 与 err 两者）包含 NpmInstallFailedError 才进入三重校验。下列
// 测试覆盖签名不匹配（超时/权限等无关失败）与配置写失败两个 fallback 安全边界。

// TestInstallPluginReturnsCLIErrWhenFailureSignatureMismatch 模拟 cache 已就绪
// （版本匹配 latest tag）但 CLI 失败签名不匹配 NpmInstallFailedError（如权限错误）：
// 签名守卫应阻止兜底，返回原始 CLI 错误、不写配置，且 git ls-remote 不被调用
// （避免给无关失败附加最长 45s 的网络成本）。
func TestInstallPluginReturnsCLIErrWhenFailureSignatureMismatch(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	spec := "github:owner/example#main"
	// cache 已就绪且版本匹配 latest tag：若无签名守卫，旧代码会兜底成功
	writeTestCachedPackage(t, cacheDir, spec, "example-plugin", map[string]any{
		"name": "example-plugin", "version": "1.5.164", "main": "./index.js",
	})

	var gitCalled int
	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		switch command.Path {
		case "/fake/git":
			gitCalled++
			return &platform.ProcessResult{Stdout: "bbb\trefs/tags/v1.5.164"}, nil
		case "/fake/opencode":
			// 非 NpmInstallFailedError 的无关失败（stderr 携带权限错误，验证
			// 签名守卫检查 result.Error 的路径）
			return &platform.ProcessResult{Stderr: "permission denied: cannot write to cache"}, fmt.Errorf("exit status 1")
		default:
			t.Fatalf("unexpected executable: %s", command.Path)
			return nil, nil
		}
	}}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	result, err := svc.InstallPlugin(spec)
	if err == nil {
		t.Fatalf("签名不匹配应返回原始 CLI 错误，得到成功: %#v", result)
	}
	if result == nil || result.Success {
		t.Fatalf("期望失败 result: %#v", result)
	}
	if gitCalled != 0 {
		t.Fatalf("签名不匹配时不应发起 git ls-remote，实际调用 %d 次", gitCalled)
	}
	configured, _ := svc.readConfiguredSpecs()
	if len(configured) != 0 {
		t.Fatalf("签名不匹配时配置不应被写入: %#v", configured)
	}
}

// TestUpdateGitHubPluginReturnsCLIErrWhenFailureSignatureMismatch 模拟 Update 时
// target.Spec 的 cache 已就绪（版本匹配 latest tag）但 CLI 失败签名不匹配
// NpmInstallFailedError（如超时/锁等待）：签名守卫应阻止兜底，返回原始 CLI 错误、
// 配置不变。
//
// 限度披露：Update 的 resolveUpdateTarget 在 CLI 之前执行（Update 语义前置，用于
// 确定 target.Spec），故 git ls-remote 必然被调用一次；签名守卫在此阻止的是
// fallback 内的 cache 校验与配置写入，而非前置 ls-remote（与 Install 不同，Install
// 的 ls-remote 在 fallback 内、可被签名守卫阻止）。
func TestUpdateGitHubPluginReturnsCLIErrWhenFailureSignatureMismatch(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	currentSpec := "github:owner/example#main"
	targetSpec := "github:owner/example#v1.5.164"
	writeTestConfig(t, configDir, []any{currentSpec})
	// target.Spec 的 cache 已就绪且版本匹配：若无签名守卫，旧代码会兜底成功
	writeTestCachedPackage(t, cacheDir, targetSpec, "example-plugin", map[string]any{
		"name": "example-plugin", "version": "1.5.164", "main": "./index.js",
	})

	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		switch command.Path {
		case "/fake/git":
			// Update 前置 resolveUpdateTarget 必然调用，无法被签名守卫阻止
			return &platform.ProcessResult{Stdout: "bbb\trefs/tags/v1.5.164"}, nil
		case "/fake/opencode":
			// 非 NpmInstallFailedError 的无关失败
			return &platform.ProcessResult{Stderr: "command timed out waiting for npm lock"}, fmt.Errorf("exit status 1")
		default:
			t.Fatalf("unexpected executable: %s", command.Path)
			return nil, nil
		}
	}}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	result, err := svc.UpdatePlugin(currentSpec)
	if err == nil {
		t.Fatalf("签名不匹配应返回原始 CLI 错误，得到成功: %#v", result)
	}
	if result == nil || result.Success {
		t.Fatalf("期望失败 result: %#v", result)
	}
	configured, _ := svc.readConfiguredSpecs()
	if !reflect.DeepEqual(configured, []string{currentSpec}) {
		t.Fatalf("签名不匹配时配置不应被改动: %#v", configured)
	}
}

// TestInstallPluginFallbackReturnsCLIErrWhenConfigWriteFails 覆盖 Minor-1：cache
// 已就绪 + 签名匹配，但 ensurePluginSpecInConfig 写配置失败。通过把 opencode.json
// 设为目录触发 ReadFile is-a-directory 错误（跨平台稳定），断言兜底不误报成功、
// 返回原始 CLI 错误语义、config 未被改动（目录保持原状）。
func TestInstallPluginFallbackReturnsCLIErrWhenConfigWriteFails(t *testing.T) {
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	spec := "github:owner/example#main"
	writeTestCachedPackage(t, cacheDir, spec, "example-plugin", map[string]any{
		"name": "example-plugin", "version": "1.5.164", "main": "./index.js",
	})
	// 把 opencode.json 设为目录：configFilePath() 因 IsDir 跳过它并返回同名默认
	// 路径，ensurePluginSpecInConfig 的 ReadFile 命中目录而失败（跨平台稳定触发，
	// 无需依赖只读权限或注入 writer）。
	configPath := filepath.Join(configDir, "opencode.json")
	if err := os.Mkdir(configPath, 0o755); err != nil {
		t.Fatal(err)
	}

	runner := &testRunner{run: func(command platform.CommandSpec) (*platform.ProcessResult, error) {
		switch command.Path {
		case "/fake/git":
			return &platform.ProcessResult{Stdout: "bbb\trefs/tags/v1.5.164"}, nil
		case "/fake/opencode":
			// 签名匹配（stderr 含 NpmInstallFailedError），进入 fallback 后
			// 由 ensurePluginSpecInConfig 写配置失败
			return &platform.ProcessResult{Stderr: fakeCLIFailure}, fmt.Errorf("exit status 1")
		default:
			t.Fatalf("unexpected executable: %s", command.Path)
			return nil, nil
		}
	}}

	svc := NewServiceWithDeps(configDir, cacheDir, nil, testResolver{}, runner)
	result, err := svc.InstallPlugin(spec)
	if err == nil {
		t.Fatalf("配置写失败时应返回原始 CLI 错误，得到成功: %#v", result)
	}
	if result == nil || result.Success {
		t.Fatalf("期望失败 result（保留原始 CLI 错误语义）: %#v", result)
	}
	// config 应保持目录原状（未被 writeAtomic 替换为文件）
	if info, statErr := os.Stat(configPath); statErr != nil || !info.IsDir() {
		t.Fatalf("配置写失败时 opencode.json 应保持目录原状: info=%v err=%v", info, statErr)
	}
}
