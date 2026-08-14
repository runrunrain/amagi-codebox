package ompplugin

import (
	"amagi-codebox/internal/platform"
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 测试桩：CLI 解析器与进程执行器（复刻 piplugin/service_test.go 的模式）。

type testResolver struct{}

func (testResolver) Resolve(platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	return platform.ResolvedLaunchSpec{}, nil
}

func (testResolver) ResolveExecutable(name string, args []string, _ []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return platform.ResolvedCLI{Path: "/fake/" + name, Args: append([]string(nil), args...)}, platform.LaunchDiagnostics{}, nil
}

// failingResolver 模拟 CLI 未找到。
type failingResolver struct{}

func (failingResolver) Resolve(platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	return platform.ResolvedLaunchSpec{}, nil
}

func (failingResolver) ResolveExecutable(string, []string, []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return platform.ResolvedCLI{}, platform.LaunchDiagnostics{}, errors.New("executable not found")
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

// blockingRunner 阻塞到 ctx 截止，用于模拟超时。
type blockingRunner struct{}

func (blockingRunner) Start(platform.CommandSpec) (*exec.Cmd, error) {
	panic("not used")
}

func (blockingRunner) Run(ctx context.Context, _ platform.CommandSpec) (*platform.ProcessResult, error) {
	<-ctx.Done()
	return &platform.ProcessResult{}, ctx.Err()
}

// envValue reads a KEY=VALUE entry from an env slice; returns ("", false) if absent.
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, e := range env {
		if len(e) > len(prefix) && e[:len(prefix)] == prefix {
			return e[len(prefix):], true
		}
	}
	return "", false
}

// ============================================================================
// 解析（parsePluginList）
// ============================================================================

func TestParsePluginListEmpty(t *testing.T) {
	plugins, warnings, err := parsePluginList(`{"npm":[],"marketplace":[]}`)
	if err != nil {
		t.Fatalf("parsePluginList: %v", err)
	}
	if len(plugins) != 0 || len(warnings) != 0 {
		t.Fatalf("expected empty result, got plugins=%#v warnings=%#v", plugins, warnings)
	}
}

func TestParsePluginListEmptyOutput(t *testing.T) {
	plugins, warnings, err := parsePluginList("")
	if err != nil {
		t.Fatalf("parsePluginList: %v", err)
	}
	if len(plugins) != 0 || len(warnings) != 0 {
		t.Fatalf("expected empty result, got plugins=%#v warnings=%#v", plugins, warnings)
	}
}

func TestParsePluginListMixed(t *testing.T) {
	output := `{
  "npm": [
    {
      "name": "example-npm",
      "version": "1.2.3",
      "enabled": true,
      "enabledFeatures": ["search", "web"],
      "manifest": {"description": "Example npm plugin"},
      "path": "/home/u/.omp/plugins/node_modules/example-npm"
    },
    {
      "name": "minimal"
    }
  ],
  "marketplace": [
    {
      "id": "context7@claude-plugins-official",
      "scope": "user",
      "shadowedBy": null,
      "entries": [{"version": "2.0.0", "enabled": true}]
    },
    {
      "id": "tools@market",
      "scope": "project",
      "entries": []
    }
  ]
}`
	plugins, warnings, err := parsePluginList(output)
	if err != nil {
		t.Fatalf("parsePluginList: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if len(plugins) != 4 {
		t.Fatalf("expected 4 plugins, got %d: %#v", len(plugins), plugins)
	}

	// 排序：npm 在前 marketplace 在后，组内 name 升序。
	wantOrder := []string{"example-npm", "minimal", "context7@claude-plugins-official", "tools@market"}
	for i, want := range wantOrder {
		if plugins[i].Name != want {
			t.Fatalf("plugins[%d].Name = %q, want %q (order)", i, plugins[i].Name, want)
		}
	}

	// npm 完整条目。
	npm := plugins[0]
	if npm.Kind != pluginKindNPM || npm.ID != "example-npm" || npm.Version != "1.2.3" || !npm.Enabled {
		t.Fatalf("unexpected npm plugin: %#v", npm)
	}
	if !reflect.DeepEqual(npm.EnabledFeatures, []string{"search", "web"}) {
		t.Fatalf("unexpected enabledFeatures: %#v", npm.EnabledFeatures)
	}
	if npm.Description != "Example npm plugin" {
		t.Fatalf("unexpected description: %q", npm.Description)
	}
	if npm.InstallPath != "/home/u/.omp/plugins/node_modules/example-npm" {
		t.Fatalf("unexpected install path: %q", npm.InstallPath)
	}

	// npm 最小条目：缺省字段容忍（enabled 缺省 true，其余为空）。
	minimal := plugins[1]
	if minimal.Kind != pluginKindNPM || !minimal.Enabled || minimal.Version != "" || minimal.Description != "" {
		t.Fatalf("unexpected minimal npm plugin: %#v", minimal)
	}

	// marketplace 完整条目。
	mp := plugins[2]
	if mp.Kind != pluginKindMarketplace || mp.ID != "context7@claude-plugins-official" || mp.Version != "2.0.0" || !mp.Enabled {
		t.Fatalf("unexpected marketplace plugin: %#v", mp)
	}
	if mp.Scope != "user" {
		t.Fatalf("unexpected scope: %q", mp.Scope)
	}

	// marketplace entries 为空：version 空、enabled 缺省 true。
	empty := plugins[3]
	if empty.Kind != pluginKindMarketplace || empty.Version != "" || !empty.Enabled || empty.Scope != "project" {
		t.Fatalf("unexpected empty-entries marketplace plugin: %#v", empty)
	}
}

func TestParsePluginListLegacyHumanOutput(t *testing.T) {
	// 旧版 omp（或不支持 --json）输出人类文本："No plugins installed" 降级为空列表。
	output := "No plugins installed\n\nInstall plugins with: omp plugin install <package>"
	plugins, warnings, err := parsePluginList(output)
	if err != nil {
		t.Fatalf("parsePluginList: %v", err)
	}
	if len(plugins) != 0 {
		t.Fatalf("expected empty plugins, got %#v", plugins)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "omp 版本较旧") {
		t.Fatalf("expected legacy-version warning, got %#v", warnings)
	}
}

func TestParsePluginListGarbage(t *testing.T) {
	_, _, err := parsePluginList("garbage output without json")
	if err == nil {
		t.Fatal("expected error for garbage output, got nil")
	}
	if !strings.Contains(err.Error(), "非 JSON") {
		t.Fatalf("expected non-JSON error, got %v", err)
	}
}

func TestParsePluginListTruncatesLongOutput(t *testing.T) {
	long := strings.Repeat("x", 1200)
	_, _, err := parsePluginList(long)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(err.Error()) > 600 {
		t.Fatalf("error should carry truncated output, got %d chars", len(err.Error()))
	}
	if strings.Contains(err.Error(), strings.Repeat("x", 600)) {
		t.Fatal("error should not carry the full untruncated output")
	}
}

// ============================================================================
// 执行（CLI 拼装与错误路径）
// ============================================================================

func TestListPluginsInvokesOmpCLI(t *testing.T) {
	runner := &testRunner{run: func(platform.CommandSpec) (*platform.ProcessResult, error) {
		return &platform.ProcessResult{Stdout: `{"npm":[],"marketplace":[]}`}, nil
	}}
	svc := NewServiceWithDeps(nil, testResolver{}, runner)

	plugins, err := svc.ListPlugins()
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(plugins) != 0 {
		t.Fatalf("expected empty plugins, got %#v", plugins)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("expected 1 CLI call, got %d", len(runner.specs))
	}
	want := []string{"plugin", "list", "--json"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("unexpected CLI args: got %#v want %#v", runner.specs[0].Args, want)
	}
	if runner.specs[0].Path != "/fake/omp" {
		t.Fatalf("expected omp executable, got %s", runner.specs[0].Path)
	}
}

func TestRefreshPluginsReturnsWarnings(t *testing.T) {
	runner := &testRunner{run: func(platform.CommandSpec) (*platform.ProcessResult, error) {
		return &platform.ProcessResult{Stdout: "No plugins installed"}, nil
	}}
	svc := NewServiceWithDeps(nil, testResolver{}, runner)

	data, err := svc.RefreshPlugins()
	if err != nil {
		t.Fatalf("RefreshPlugins: %v", err)
	}
	if len(data.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %#v", data.Warnings)
	}
	if len(data.Installed) != 0 {
		t.Fatalf("expected empty installed, got %#v", data.Installed)
	}
}

func TestInstallInvokesOmpCLI(t *testing.T) {
	runner := &testRunner{}
	svc := NewServiceWithDeps(nil, testResolver{}, runner)

	if _, err := svc.InstallPlugin("github:user/repo"); err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("expected 1 CLI call, got %d", len(runner.specs))
	}
	want := []string{"install", "github:user/repo"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("unexpected CLI args: got %#v want %#v", runner.specs[0].Args, want)
	}
}

func TestUninstallInvokesOmpCLI(t *testing.T) {
	runner := &testRunner{}
	svc := NewServiceWithDeps(nil, testResolver{}, runner)

	if _, err := svc.UninstallPlugin("example-npm"); err != nil {
		t.Fatalf("UninstallPlugin: %v", err)
	}
	want := []string{"plugin", "uninstall", "example-npm"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("unexpected CLI args: got %#v want %#v", runner.specs[0].Args, want)
	}
}

func TestSetPluginEnabled(t *testing.T) {
	for _, tc := range []struct {
		enabled bool
		want    string
	}{
		{true, "enable"},
		{false, "disable"},
	} {
		runner := &testRunner{}
		svc := NewServiceWithDeps(nil, testResolver{}, runner)

		if _, err := svc.SetPluginEnabled("example-npm", tc.enabled); err != nil {
			t.Fatalf("SetPluginEnabled(%v): %v", tc.enabled, err)
		}
		want := []string{"plugin", tc.want, "example-npm"}
		if !reflect.DeepEqual(runner.specs[0].Args, want) {
			t.Fatalf("unexpected CLI args: got %#v want %#v", runner.specs[0].Args, want)
		}
	}
}

// pluginListRunner 按调用序号返回 list 输出（首次调用返回 JSON，其余返回 Done）。
type pluginListRunner struct {
	specs      []platform.CommandSpec
	listOutput string
}

func (r *pluginListRunner) Start(platform.CommandSpec) (*exec.Cmd, error) {
	panic("not used")
}

func (r *pluginListRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.specs = append(r.specs, spec)
	for _, a := range spec.Args {
		if a == "list" {
			return &platform.ProcessResult{Stdout: r.listOutput}, nil
		}
	}
	return &platform.ProcessResult{Stdout: "Done"}, nil
}

func TestUpgradeMarketplacePlugin(t *testing.T) {
	runner := &pluginListRunner{listOutput: `{"npm":[],"marketplace":[{"id":"context7@claude-plugins-official","scope":"user","entries":[{"version":"1.0.0"}]}]}`}
	svc := NewServiceWithDeps(nil, testResolver{}, runner)

	if _, err := svc.UpgradePlugin("context7@claude-plugins-official"); err != nil {
		t.Fatalf("UpgradePlugin: %v", err)
	}
	// 第一次调用是 list，第二次是 upgrade。
	if len(runner.specs) != 2 {
		t.Fatalf("expected 2 CLI calls, got %d", len(runner.specs))
	}
	want := []string{"plugin", "upgrade", "context7@claude-plugins-official"}
	if !reflect.DeepEqual(runner.specs[1].Args, want) {
		t.Fatalf("unexpected upgrade args: got %#v want %#v", runner.specs[1].Args, want)
	}
}

func TestUpgradeNpmPlugin(t *testing.T) {
	runner := &pluginListRunner{listOutput: `{"npm":[{"name":"example-npm","version":"1.2.3"}],"marketplace":[]}`}
	svc := NewServiceWithDeps(nil, testResolver{}, runner)

	if _, err := svc.UpgradePlugin("example-npm"); err != nil {
		t.Fatalf("UpgradePlugin: %v", err)
	}
	if len(runner.specs) != 2 {
		t.Fatalf("expected 2 CLI calls, got %d", len(runner.specs))
	}
	want := []string{"install", "example-npm", "--force"}
	if !reflect.DeepEqual(runner.specs[1].Args, want) {
		t.Fatalf("unexpected upgrade args: got %#v want %#v", runner.specs[1].Args, want)
	}
}

func TestUpgradeUnknownFallsBackToReinstall(t *testing.T) {
	runner := &pluginListRunner{listOutput: `{"npm":[],"marketplace":[]}`}
	svc := NewServiceWithDeps(nil, testResolver{}, runner)

	if _, err := svc.UpgradePlugin("not-installed"); err != nil {
		t.Fatalf("UpgradePlugin: %v", err)
	}
	if len(runner.specs) != 2 {
		t.Fatalf("expected 2 CLI calls, got %d", len(runner.specs))
	}
	want := []string{"install", "not-installed", "--force"}
	if !reflect.DeepEqual(runner.specs[1].Args, want) {
		t.Fatalf("unexpected fallback args: got %#v want %#v", runner.specs[1].Args, want)
	}
}

func TestUpgradeLegacyListDegrades(t *testing.T) {
	// 旧版 omp list 输出人类文本：升级仍按 npm 重装兜底，不因列表解析失败阻断。
	runner := &pluginListRunner{listOutput: "No plugins installed"}
	svc := NewServiceWithDeps(nil, testResolver{}, runner)

	if _, err := svc.UpgradePlugin("example-npm"); err != nil {
		t.Fatalf("UpgradePlugin: %v", err)
	}
	if len(runner.specs) != 2 {
		t.Fatalf("expected 2 CLI calls, got %d", len(runner.specs))
	}
	want := []string{"install", "example-npm", "--force"}
	if !reflect.DeepEqual(runner.specs[1].Args, want) {
		t.Fatalf("unexpected fallback args: got %#v want %#v", runner.specs[1].Args, want)
	}
}

func TestExecuteOmpCommandTimeout(t *testing.T) {
	svc := NewServiceWithDeps(nil, testResolver{}, blockingRunner{})

	start := time.Now()
	result, err := svc.executeOmpCommand(context.TODO(), 50*time.Millisecond, "install", "slow-pkg")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got %#v", result)
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

func TestExecuteOmpCommandCLINotFound(t *testing.T) {
	svc := NewServiceWithDeps(nil, failingResolver{}, &testRunner{})

	_, err := svc.ListPlugins()
	if err == nil {
		t.Fatal("expected CLI-not-found error, got nil")
	}
	if !strings.Contains(err.Error(), "未找到 omp CLI") {
		t.Fatalf("expected CLI-not-found error, got %v", err)
	}
}

// TestExecuteOmpCommandDoesNotRewriteAgentDirEnv 验证 omp 与 piplugin 的关键
// 差异：不注入/不改写 PI_CODING_AGENT_DIR（omp 不消费它，插件 CLI 天然操作
// ~/.omp/plugins）。父环境中的既有值原样透传，不做任何加工。
func TestExecuteOmpCommandDoesNotRewriteAgentDirEnv(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "/pre-existing/pi-agent-dir")
	runner := &testRunner{run: func(platform.CommandSpec) (*platform.ProcessResult, error) {
		return &platform.ProcessResult{Stdout: `{"npm":[],"marketplace":[]}`}, nil
	}}
	svc := NewServiceWithDeps(nil, testResolver{}, runner)

	if _, err := svc.ListPlugins(); err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("expected 1 CLI call, got %d", len(runner.specs))
	}
	got, ok := envValue(runner.specs[0].Env, "PI_CODING_AGENT_DIR")
	if !ok {
		t.Fatalf("PI_CODING_AGENT_DIR missing from omp CLI env: %v", runner.specs[0].Env)
	}
	if got != "/pre-existing/pi-agent-dir" {
		t.Fatalf("PI_CODING_AGENT_DIR was rewritten to %q; omp must not inject an agent dir", got)
	}
}

func TestValidatePluginSpecRejectsMetachars(t *testing.T) {
	for _, bad := range []string{"", "  ", "-flag", "foo&bar", "foo|bar", "foo<bar", "foo>bar", "foo^bar", "foo(bar)", "foo%bar", "foo\nbar", strings.Repeat("x", 2049)} {
		if _, err := validatePluginSpec(bad); err == nil {
			t.Fatalf("expected rejection for %q", bad)
		}
	}
}

func TestValidatePluginSpecAcceptsValidTargets(t *testing.T) {
	for _, good := range []string{"@oh-my-pi/exa", "name@marketplace", "github:user/repo", "https://github.com/user/repo#v1.0", "./local/path", "pkg[search,web]"} {
		got, err := validatePluginSpec(good)
		if err != nil {
			t.Fatalf("expected accept for %q, got %v", good, err)
		}
		if got != good {
			t.Fatalf("validatePluginSpec(%q) = %q", good, got)
		}
	}
}

func TestWriteOperationsRejectUnsafeSpec(t *testing.T) {
	svc := NewServiceWithDeps(nil, testResolver{}, &testRunner{})
	if _, err := svc.InstallPlugin("foo&bar"); err == nil {
		t.Fatal("expected rejection for InstallPlugin with metachars")
	}
	if _, err := svc.UninstallPlugin("foo|bar"); err == nil {
		t.Fatal("expected rejection for UninstallPlugin with metachars")
	}
	if _, err := svc.SetPluginEnabled("-flag", true); err == nil {
		t.Fatal("expected rejection for SetPluginEnabled with leading dash")
	}
	if _, err := svc.UpgradePlugin(""); err == nil {
		t.Fatal("expected rejection for UpgradePlugin with empty name")
	}
}
