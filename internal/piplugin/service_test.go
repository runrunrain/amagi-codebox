package piplugin

import (
	"amagi-codebox/internal/platform"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// 测试桩：CLI 解析器与进程执行器（复刻 opencodeplugin/service_test.go 的模式）。

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

func writeTestSettings(t *testing.T, agentDir string, packages []any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"packages": packages})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(agentDir, "settings.json"), string(data))
}

// writeTestNpmEntity 写一个 npm 实体：<agentDir>/npm/node_modules/<name>/package.json
func writeTestNpmEntity(t *testing.T, agentDir, name string, manifest map[string]any) string {
	t.Helper()
	root := filepath.Join(agentDir, "npm", "node_modules", filepath.FromSlash(name))
	manifestData, _ := json.Marshal(manifest)
	writeTestFile(t, filepath.Join(root, "package.json"), string(manifestData))
	return root
}

// writeTestGitEntity 写一个 git 实体：<agentDir>/git/<host>/<user>/<project>/package.json
func writeTestGitEntity(t *testing.T, agentDir, host, userRepo string, manifest map[string]any) string {
	t.Helper()
	root := filepath.Join(agentDir, "git", host, filepath.FromSlash(userRepo))
	manifestData, _ := json.Marshal(manifest)
	writeTestFile(t, filepath.Join(root, "package.json"), string(manifestData))
	return root
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListInstalledPluginsReadsSettingsAndNpmEntity(t *testing.T) {
	agentDir := t.TempDir()
	source := "npm:example-pi-pkg@1.2.3"
	writeTestSettings(t, agentDir, []any{source, "npm:second"})
	writeTestNpmEntity(t, agentDir, "example-pi-pkg", map[string]any{
		"name":        "example-pi-pkg",
		"version":     "1.2.3",
		"description": "Example pi package",
	})

	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, &testRunner{})
	packages, err := svc.ListInstalledPackages()
	if err != nil {
		t.Fatalf("ListInstalledPackages: %v", err)
	}
	if len(packages) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(packages))
	}
	var found *Package
	for i := range packages {
		if packages[i].Source == source {
			found = &packages[i]
		}
	}
	if found == nil {
		t.Fatalf("expected %s in package list: %#v", source, packages)
	}
	if found.Name != "example-pi-pkg" || found.Version != "1.2.3" || found.Description != "Example pi package" {
		t.Fatalf("unexpected package metadata: %#v", found)
	}
	if found.SourceType != sourceNPM || !found.Pinned {
		t.Fatalf("expected npm+pinlooked, got sourceType=%s pinned=%v", found.SourceType, found.Pinned)
	}
	wantPath := filepath.Join(agentDir, "npm", "node_modules", "example-pi-pkg")
	if found.InstallPath != wantPath {
		t.Fatalf("unexpected install path: got %s want %s", found.InstallPath, wantPath)
	}
}

func TestListInstalledPluginsReadsGitEntity(t *testing.T) {
	agentDir := t.TempDir()
	source := "git:github.com/owner/repo@v1"
	writeTestSettings(t, agentDir, []any{source})
	writeTestGitEntity(t, agentDir, "github.com", "owner/repo", map[string]any{
		"name":    "repo-extension",
		"version": "0.4.1",
	})

	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, &testRunner{})
	packages, err := svc.ListInstalledPackages()
	if err != nil {
		t.Fatalf("ListInstalledPackages: %v", err)
	}
	if len(packages) != 1 {
		t.Fatalf("expected 1 package, got %d", len(packages))
	}
	p := packages[0]
	if p.SourceType != sourceGit || !p.Pinned {
		t.Fatalf("expected git+pinned, got %#v", p)
	}
	wantPath := filepath.Join(agentDir, "git", "github.com", "owner", "repo")
	if p.InstallPath != wantPath {
		t.Fatalf("unexpected git install path: got %s want %s", p.InstallPath, wantPath)
	}
	if p.Version != "0.4.1" {
		t.Fatalf("unexpected version: %s", p.Version)
	}
}

func TestListInstalledPluginsSupportsFilterObject(t *testing.T) {
	agentDir := t.TempDir()
	source := "npm:filtered-pkg"
	writeTestSettings(t, agentDir, []any{
		source,
		map[string]any{
			"source":     "npm:filtered-pkg-2",
			"extensions": []string{"extensions/*.ts"},
			"skills":     []any{},
			"prompts":    []any{"prompts/review.md"},
		},
	})
	writeTestNpmEntity(t, agentDir, "filtered-pkg", map[string]any{"name": "filtered-pkg"})
	writeTestNpmEntity(t, agentDir, "filtered-pkg-2", map[string]any{"name": "filtered-pkg-2"})

	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, &testRunner{})
	packages, err := svc.ListInstalledPackages()
	if err != nil {
		t.Fatalf("ListInstalledPackages: %v", err)
	}
	if len(packages) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(packages))
	}
	var obj *Package
	for i := range packages {
		if packages[i].Source == "npm:filtered-pkg-2" {
			obj = &packages[i]
		}
	}
	if obj == nil {
		t.Fatalf("filter-object package missing: %#v", packages)
	}
	if !reflect.DeepEqual(obj.Extensions, []string{"extensions/*.ts"}) {
		t.Fatalf("unexpected extensions filter: %#v", obj.Extensions)
	}
	if !reflect.DeepEqual(obj.Prompts, []string{"prompts/review.md"}) {
		t.Fatalf("unexpected prompts filter: %#v", obj.Prompts)
	}
	// skills: [] 显式空数组 → nil（toStringSlice 对空数组返回 nil，表示"加载全部"语义由 pi 解释）。
	if obj.Skills != nil {
		t.Fatalf("expected nil skills for empty array, got %#v", obj.Skills)
	}
}

func TestGetPluginDetailsDiscoversResources(t *testing.T) {
	agentDir := t.TempDir()
	source := "npm:rich-pkg"
	writeTestSettings(t, agentDir, []any{source})
	root := writeTestNpmEntity(t, agentDir, "rich-pkg", map[string]any{
		"name":    "rich-pkg",
		"version": "2.0.0",
		"pi": map[string]any{
			"extensions": []any{"./extensions"},
			"skills":     []any{"./skills"},
		},
	})
	writeTestFile(t, filepath.Join(root, "skills", "workflow", "SKILL.md"), "# Workflow")
	writeTestFile(t, filepath.Join(root, "prompts", "review.md"), "# Review")
	writeTestFile(t, filepath.Join(root, "themes", "dark.json"), "{}")
	writeTestFile(t, filepath.Join(root, "extensions", "tool.ts"), "export {}")

	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, &testRunner{})
	detail, err := svc.GetPackageDetails(source)
	if err != nil {
		t.Fatalf("GetPackageDetails: %v", err)
	}
	if !detail.ManifestDeclared {
		t.Fatalf("expected manifest declared")
	}
	// 至少应发现 skill / prompt / theme / extension 各一个。
	counts := map[string]int{}
	for _, r := range detail.Resources {
		counts[r.Type]++
	}
	if counts[resourceSkill] == 0 || counts[resourcePrompt] == 0 || counts[resourceTheme] == 0 || counts[resourceExtension] == 0 {
		t.Fatalf("expected all resource types discovered, got %#v", counts)
	}
}

func TestRefreshPackagesWarnsOnMissingEntity(t *testing.T) {
	agentDir := t.TempDir()
	// npm 源已登记但实体未安装 → 应产生告警。
	writeTestSettings(t, agentDir, []any{"npm:ghost-pkg@1.0.0"})

	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, &testRunner{})
	data, err := svc.RefreshPackages()
	if err != nil {
		t.Fatalf("RefreshPackages: %v", err)
	}
	if len(data.Warnings) == 0 {
		t.Fatalf("expected a warning for missing entity, got none")
	}
	if len(data.Installed) != 1 {
		t.Fatalf("expected 1 installed entry, got %d", len(data.Installed))
	}
}

func TestInstallInvokesPiCLI(t *testing.T) {
	runner := &testRunner{}
	svc := NewServiceWithDeps(t.TempDir(), nil, testResolver{}, runner)

	if _, err := svc.InstallPackage("npm:foo"); err != nil {
		t.Fatalf("InstallPackage: %v", err)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("expected 1 CLI call, got %d", len(runner.specs))
	}
	got := runner.specs[0].Args
	want := []string{"install", "npm:foo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected CLI args: got %#v want %#v", got, want)
	}
	if runner.specs[0].Path != "/fake/pi" {
		t.Fatalf("expected pi executable, got %s", runner.specs[0].Path)
	}
}

func TestRemoveInvokesPiCLI(t *testing.T) {
	runner := &testRunner{}
	svc := NewServiceWithDeps(t.TempDir(), nil, testResolver{}, runner)

	if _, err := svc.RemovePackage("npm:foo"); err != nil {
		t.Fatalf("RemovePackage: %v", err)
	}
	want := []string{"remove", "npm:foo"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("unexpected CLI args: got %#v want %#v", runner.specs[0].Args, want)
	}
}

func TestUpdateRequiresConfiguredPackage(t *testing.T) {
	agentDir := t.TempDir()
	writeTestSettings(t, agentDir, []any{"npm:configured@1.0.0"})
	writeTestNpmEntity(t, agentDir, "configured", map[string]any{"name": "configured"})
	runner := &testRunner{}

	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, runner)
	if _, err := svc.UpdatePackage("npm:not-configured"); err == nil {
		t.Fatalf("expected error for unconfigured package")
	}
	if _, err := svc.UpdatePackage("npm:configured@1.0.0"); err != nil {
		t.Fatalf("UpdatePackage: %v", err)
	}
	want := []string{"update", "npm:configured@1.0.0"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("unexpected CLI args: got %#v want %#v", runner.specs[0].Args, want)
	}
}

func TestParseSourceVariants(t *testing.T) {
	cases := []struct {
		raw       string
		wantType  string
		wantName  string
		wantHost  string
		wantPath  string
		wantRef   string
		wantLocal string
	}{
		{"npm:@scope/pkg@1.0.0", sourceNPM, "@scope/pkg", "", "", "1.0.0", ""},
		{"npm:plain", sourceNPM, "plain", "", "", "", ""},
		{"git:github.com/owner/repo@v1", sourceGit, "", "github.com", "owner/repo", "v1", ""},
		{"git:git@github.com:owner/repo", sourceGit, "", "github.com", "owner/repo", "", ""},
		{"https://github.com/owner/repo.git", sourceGit, "", "github.com", "owner/repo", "", ""},
		{"ssh://git@github.com/owner/repo@deadbeef", sourceGit, "", "github.com", "owner/repo", "deadbeef", ""},
		{"/abs/path/to/pkg", sourceLocal, "", "", "", "", "/abs/path/to/pkg"},
		{"./rel/pkg", sourceLocal, "", "", "", "", "./rel/pkg"},
	}
	for _, c := range cases {
		got := parseSource(c.raw)
		if got.sourceType != c.wantType || got.name != c.wantName || got.host != c.wantHost ||
			got.path != c.wantPath || got.ref != c.wantRef || got.localPath != c.wantLocal {
			t.Fatalf("parseSource(%q): got %+v want type=%s name=%s host=%s path=%s ref=%s local=%s",
				c.raw, got, c.wantType, c.wantName, c.wantHost, c.wantPath, c.wantRef, c.wantLocal)
		}
	}
}

func TestParseSourceRejectsNonGitWithoutPrefix(t *testing.T) {
	// 无 git: 前缀且非协议 URL → 回退 local（与 pi 兜底一致）。
	got := parseSource("github.com/owner/repo")
	if got.sourceType != sourceLocal {
		t.Fatalf("expected local fallback, got %+v", got)
	}
}

func TestFallbackName(t *testing.T) {
	cases := map[string]string{
		"npm:@scope/pkg@1.0.0":          "pkg",
		"npm:plain":                     "plain",
		"git:github.com/owner/repo@v1":  "repo",
		"https://github.com/owner/repo": "repo",
		"/abs/path/to/my-pkg":           "my-pkg",
	}
	for raw, want := range cases {
		if got := fallbackName(raw); !strings.EqualFold(got, want) {
			t.Fatalf("fallbackName(%q): got %q want %q", raw, got, want)
		}
	}
}

func TestDefaultAgentDirRespectsEnv(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "/custom/pi/agent")
	if got := defaultAgentDir(); got != "/custom/pi/agent" {
		t.Fatalf("expected env override, got %s", got)
	}
}

func TestSwitchPackageSourceInvokesRemoveThenInstall(t *testing.T) {
	agentDir := t.TempDir()
	writeTestSettings(t, agentDir, []any{"git:github.com/runrunrain/amagi-pi"})
	runner := &testRunner{}
	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, runner)

	res, err := svc.SwitchPackageSource("git:github.com/runrunrain/amagi-pi", "/local/amagi-pi")
	if err != nil {
		t.Fatalf("SwitchPackageSource: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got %#v", res)
	}
	if len(runner.specs) != 2 {
		t.Fatalf("expected 2 CLI calls (remove+install), got %d", len(runner.specs))
	}
	if want := []string{"remove", "git:github.com/runrunrain/amagi-pi"}; !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("call#1 args: got %#v want %#v", runner.specs[0].Args, want)
	}
	if want := []string{"install", "/local/amagi-pi"}; !reflect.DeepEqual(runner.specs[1].Args, want) {
		t.Fatalf("call#2 args: got %#v want %#v", runner.specs[1].Args, want)
	}
}

func TestSwitchPackageSourceValidations(t *testing.T) {
	agentDir := t.TempDir()
	writeTestSettings(t, agentDir, []any{"npm:amagi-pi"})
	runner := &testRunner{}
	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, runner)

	// 旧源未登记
	if _, err := svc.SwitchPackageSource("git:github.com/x/not-registered", "/local/x"); err == nil {
		t.Fatal("expected error for unregistered old source")
	}
	// 新源已登记（双引用防护）：settings 里同包再登记一个版本化源
	writeTestSettings(t, agentDir, []any{"npm:amagi-pi", "npm:amagi-pi@2"})
	if _, err := svc.SwitchPackageSource("npm:amagi-pi", "npm:amagi-pi@2"); err == nil {
		t.Fatal("expected error when new source already registered")
	}
	writeTestSettings(t, agentDir, []any{"npm:amagi-pi"})
	// 同源直通
	res, err := svc.SwitchPackageSource("npm:amagi-pi", "npm:amagi-pi")
	if err != nil || !res.Success {
		t.Fatalf("same-source no-op should succeed: %v", err)
	}
	if len(runner.specs) != 0 {
		t.Fatalf("validations must not reach CLI, got %d calls", len(runner.specs))
	}
}

func TestSwitchPackageSourceRollsBackOnInstallFailure(t *testing.T) {
	agentDir := t.TempDir()
	writeTestSettings(t, agentDir, []any{"git:github.com/runrunrain/amagi-pi"})
	runner := &testRunner{run: func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		if strings.Join(spec.Args, " ") == "install /local/broken" {
			return &platform.ProcessResult{Stdout: "", Stderr: "install failed"}, errors.New("exit status 1")
		}
		return &platform.ProcessResult{Stdout: "Done"}, nil
	}}
	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, runner)

	res, err := svc.SwitchPackageSource("git:github.com/runrunrain/amagi-pi", "/local/broken")
	// 安装失败 → Switch 返回 error（已回滚）或 Success=false 结果，两者都算失败路径
	if err == nil && res != nil && res.Success {
		t.Fatal("expected failed switch result")
	}
	// remove → install(失败) → 回滚 install 旧源 = 3 次调用
	if len(runner.specs) != 3 {
		t.Fatalf("expected 3 CLI calls (remove+install+rollback), got %d", len(runner.specs))
	}
	if want := []string{"install", "git:github.com/runrunrain/amagi-pi"}; !reflect.DeepEqual(runner.specs[2].Args, want) {
		t.Fatalf("rollback args: got %#v want %#v", runner.specs[2].Args, want)
	}
}

func TestLocalSourceDisplayedAsAbsolutePath(t *testing.T) {
	agentDir := t.TempDir()
	// settings 存相对 agentDir 形态（pi install 规范化产物）
	writeTestSettings(t, agentDir, []any{"../../maorun-workpace/amagi-pi"})
	want := filepath.Join(agentDir, "..", "..", "maorun-workpace", "amagi-pi")

	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, &testRunner{})
	data, err := svc.RefreshPackages()
	if err != nil {
		t.Fatalf("RefreshPackages: %v", err)
	}
	if len(data.Installed) != 1 {
		t.Fatalf("expected 1 package, got %d", len(data.Installed))
	}
	got := data.Installed[0].Source
	if !filepath.IsAbs(got) {
		t.Fatalf("local source should be displayed as absolute path, got %q", got)
	}
	if want != got {
		t.Fatalf("abs path: got %q want %q", got, want)
	}
}

func TestSwitchAcceptsAbsoluteFormOfRegisteredRelativeLocalSource(t *testing.T) {
	agentDir := t.TempDir()
	writeTestSettings(t, agentDir, []any{"../../maorun-workpace/amagi-pi"})
	absForm := filepath.Join(agentDir, "..", "..", "maorun-workpace", "amagi-pi")
	runner := &testRunner{}
	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, runner)

	// 面板传绝对形态（settings 是相对形态）——应匹配登记并 remove 相对原始串
	if _, err := svc.SwitchPackageSource(absForm, "git:github.com/runrunrain/amagi-pi"); err != nil {
		t.Fatalf("SwitchPackageSource with absolute form: %v", err)
	}
	if want := []string{"remove", "../../maorun-workpace/amagi-pi"}; !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("remove args should use settings raw form: got %#v", runner.specs[0].Args)
	}
}

func TestGetDetailsAndRemoveAcceptAbsoluteForm(t *testing.T) {
	agentDir := t.TempDir()
	writeTestSettings(t, agentDir, []any{"../../maorun-workpace/amagi-pi"})
	absForm := filepath.Join(agentDir, "..", "..", "maorun-workpace", "amagi-pi")

	// GetPackageDetails：绝对形态应命中相对登记
	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, &testRunner{})
	if _, err := svc.GetPackageDetails(absForm); err != nil {
		t.Fatalf("GetPackageDetails with absolute form: %v", err)
	}

	// UpdatePackage：绝对形态应转 settings 原始串调 CLI
	runner := &testRunner{}
	svc2 := NewServiceWithDeps(agentDir, nil, testResolver{}, runner)
	if _, err := svc2.UpdatePackage(absForm); err != nil {
		t.Fatalf("UpdatePackage with absolute form: %v", err)
	}
	if want := []string{"update", "../../maorun-workpace/amagi-pi"}; !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("update args should use settings raw form: got %#v", runner.specs[0].Args)
	}

	// RemovePackage：同型转原始串
	runner2 := &testRunner{}
	svc3 := NewServiceWithDeps(agentDir, nil, testResolver{}, runner2)
	if _, err := svc3.RemovePackage(absForm); err != nil {
		t.Fatalf("RemovePackage with absolute form: %v", err)
	}
	if want := []string{"remove", "../../maorun-workpace/amagi-pi"}; !reflect.DeepEqual(runner2.specs[0].Args, want) {
		t.Fatalf("remove args should use settings raw form: got %#v", runner2.specs[0].Args)
	}
}
