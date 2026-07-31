package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/config"
)

// 构造全局/运行时目录对。
func setupPiSyncDirs(t *testing.T) (agentDir, globalDir string) {
	t.Helper()
	root := t.TempDir()
	agentDir = filepath.Join(root, "pi-runtime")
	globalDir = filepath.Join(root, "global-agent")
	if err := os.MkdirAll(globalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return agentDir, globalDir
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return out
}

// 账号认证继承：全局 kimi-coding/openai-codex 补缺进 runtime，runtime 已有键优先。
func TestSyncPiGlobalStateMergesAuth(t *testing.T) {
	agentDir, globalDir := setupPiSyncDirs(t)
	writeJSON(t, filepath.Join(globalDir, "auth.json"), map[string]any{
		"kimi-coding":  map[string]any{"type": "api_key", "key": "global-kimi"},
		"openai-codex": map[string]any{"type": "oauth", "access": "global-access"},
		"shared":       "global-value",
	})
	writeJSON(t, filepath.Join(agentDir, "auth.json"), map[string]any{
		"shared": "runtime-value",
	})

	if err := syncPiGlobalStateFrom(agentDir, globalDir, nil); err != nil {
		t.Fatalf("sync: %v", err)
	}

	merged := readJSON(t, filepath.Join(agentDir, "auth.json"))
	if _, ok := merged["kimi-coding"]; !ok {
		t.Error("kimi-coding not inherited")
	}
	if _, ok := merged["openai-codex"]; !ok {
		t.Error("openai-codex not inherited")
	}
	if merged["shared"] != "runtime-value" {
		t.Errorf("runtime key overwritten: got %v, want runtime-value", merged["shared"])
	}
	// 权限收紧 0600（auth 含密钥）。
	info, err := os.Stat(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("auth.json perm = %o, want 0600", info.Mode().Perm())
	}
}

// 幂等：重复同步不产生变化（第二次不应改写文件 mtime 之外的内容差异）。
func TestSyncPiGlobalStateAuthIdempotent(t *testing.T) {
	agentDir, globalDir := setupPiSyncDirs(t)
	writeJSON(t, filepath.Join(globalDir, "auth.json"), map[string]any{"a": "1"})

	if err := syncPiGlobalStateFrom(agentDir, globalDir, nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	first := readJSON(t, filepath.Join(agentDir, "auth.json"))
	if err := syncPiGlobalStateFrom(agentDir, globalDir, nil); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	second := readJSON(t, filepath.Join(agentDir, "auth.json"))
	if len(second) != len(first) || second["a"] != first["a"] {
		t.Errorf("not idempotent: first=%v second=%v", first, second)
	}
}

// settings.json 继承：顶层键补缺 + packages 并集去重（字符串与对象两种元素形态）。
func TestSyncPiGlobalStateMergesSettingsPackages(t *testing.T) {
	agentDir, globalDir := setupPiSyncDirs(t)
	writeJSON(t, filepath.Join(globalDir, "settings.json"), map[string]any{
		"theme":           "dark",
		"defaultProvider": "kimi-coding",
		"packages": []any{
			"git:https://github.com/runrunrain/amagi-pi.git",
			"npm:shared-pkg",
			map[string]any{"source": "./local-pkg", "extensions": []any{"a.ts"}},
		},
	})
	writeJSON(t, filepath.Join(agentDir, "settings.json"), map[string]any{
		"theme":    "light",
		"packages": []any{"npm:shared-pkg"},
	})

	if err := syncPiGlobalStateFrom(agentDir, globalDir, nil); err != nil {
		t.Fatalf("sync: %v", err)
	}

	merged := readJSON(t, filepath.Join(agentDir, "settings.json"))
	if merged["theme"] != "light" {
		t.Errorf("runtime theme overwritten: got %v", merged["theme"])
	}
	if merged["defaultProvider"] != "kimi-coding" {
		t.Errorf("defaultProvider not inherited: got %v", merged["defaultProvider"])
	}
	pkgs, ok := merged["packages"].([]any)
	if !ok {
		t.Fatalf("packages not array: %T", merged["packages"])
	}
	sources := map[string]int{}
	for _, item := range pkgs {
		switch e := item.(type) {
		case string:
			sources[e]++
		case map[string]any:
			sources[e["source"].(string)]++
		}
	}
	want := []string{"git:https://github.com/runrunrain/amagi-pi.git", "npm:shared-pkg", "./local-pkg"}
	for _, w := range want {
		if sources[w] != 1 {
			t.Errorf("package %q count = %d, want 1 (all=%v)", w, sources[w], sources)
		}
	}
	// runtime 条目优先：npm:shared-pkg 保持 runtime 形态（字符串，排首位）。
	if pkgs[0] != "npm:shared-pkg" {
		t.Errorf("runtime package order not preserved: %v", pkgs)
	}
}

// git/npm 实体目录：缺失时符号链接到全局；已存在真实目录时不覆盖。
func TestSyncPiGlobalStateLinksEntityDirs(t *testing.T) {
	agentDir, globalDir := setupPiSyncDirs(t)
	// 全局有 git 实体。
	globalGitPkg := filepath.Join(globalDir, "git", "github.com", "runrunrain", "amagi-pi")
	if err := os.MkdirAll(globalGitPkg, 0o755); err != nil {
		t.Fatal(err)
	}
	// runtime 已有真实 npm 目录（pi 自行安装过），不应被覆盖。
	localNpm := filepath.Join(agentDir, "npm", "node_modules")
	if err := os.MkdirAll(localNpm, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := syncPiGlobalStateFrom(agentDir, globalDir, nil); err != nil {
		t.Fatalf("sync: %v", err)
	}

	link := filepath.Join(agentDir, "git")
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("git link missing: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("git is not a symlink")
	}
	// 透过符号链接可解析到全局实体。
	if _, err := os.Stat(filepath.Join(link, "github.com", "runrunrain", "amagi-pi")); err != nil {
		t.Errorf("entity not resolvable through symlink: %v", err)
	}
	// 已有 npm 真实目录未被替换。
	fi, err = os.Lstat(filepath.Join(agentDir, "npm"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("existing real npm dir was replaced by symlink")
	}
}

// 全局目录缺失/与 runtime 相同/无全局配置时静默跳过。
func TestSyncPiGlobalStateSkipsGracefully(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "pi-runtime")

	// 全局不存在。
	if err := syncPiGlobalStateFrom(agentDir, filepath.Join(root, "nope"), nil); err != nil {
		t.Errorf("missing global should not error: %v", err)
	}
	// 全局 == runtime。
	if err := syncPiGlobalStateFrom(agentDir, agentDir, nil); err != nil {
		t.Errorf("same dir should not error: %v", err)
	}
	// 全局为空目录。
	globalDir := filepath.Join(root, "empty-global")
	if err := os.MkdirAll(globalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syncPiGlobalStateFrom(agentDir, globalDir, nil); err != nil {
		t.Errorf("empty global should not error: %v", err)
	}
	// runtime 未生成多余文件。
	entries, _ := os.ReadDir(agentDir)
	for _, e := range entries {
		if e.Name() == "auth.json" || e.Name() == "settings.json" {
			t.Errorf("unexpected file created from empty global: %s", e.Name())
		}
	}
}

// models.json providers 合并：全局自定义 provider 并入，amagi 托管条目优先。
func TestMergePiProvidersFrom(t *testing.T) {
	globalDir := t.TempDir()
	writeJSON(t, filepath.Join(globalDir, "models.json"), map[string]any{
		"providers": map[string]any{
			"zhipuai":   map[string]any{"baseUrl": "https://open.bigmodel.cn"},
			"amagi-foo": map[string]any{"baseUrl": "https://global-stale"},
		},
	})
	cfg := map[string]any{
		"providers": map[string]any{
			"amagi-foo": map[string]any{"baseUrl": "https://managed-fresh"},
		},
	}
	out := mergePiProvidersFrom(cfg, globalDir)
	providers, ok := out["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers type = %T", out["providers"])
	}
	if _, ok := providers["zhipuai"]; !ok {
		t.Error("global zhipuai provider not merged")
	}
	foo := providers["amagi-foo"].(map[string]any)
	if foo["baseUrl"] != "https://managed-fresh" {
		t.Errorf("managed provider overridden: %v", foo["baseUrl"])
	}
	// 原 cfg 不被污染（返回新 map）。
	orig := cfg["providers"].(map[string]any)
	if _, ok := orig["zhipuai"]; ok {
		t.Error("input cfg mutated")
	}
}

// 全局无 models.json / 无 providers 时 cfg 原样返回。
func TestMergePiProvidersFromEmptyGlobal(t *testing.T) {
	globalDir := t.TempDir()
	cfg := map[string]any{"providers": map[string]any{"amagi-x": map[string]any{}}}
	out := mergePiProvidersFrom(cfg, globalDir)
	if len(out["providers"].(map[string]any)) != 1 {
		t.Errorf("cfg changed unexpectedly: %v", out)
	}
}

// 回归（真实启动链）：BuildPiModelsConfig 产出的 providers 是
// map[string]map[string]any（非 map[string]any），合并时类型断言失败曾导致
// amagi 托管 provider 被静默丢弃（pi 报 Unknown provider "amagi-glm"）。
func TestMergePiProvidersFromRealBuildOutput(t *testing.T) {
	globalDir := t.TempDir()
	writeJSON(t, filepath.Join(globalDir, "models.json"), map[string]any{
		"providers": map[string]any{
			"zhipuai": map[string]any{"baseUrl": "https://open.bigmodel.cn"},
		},
	})

	cfg, err := BuildPiModelsConfig("glm", config.Provider{
		OpenAI: &config.OpenAIFormat{
			Enabled: true,
			BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4",
		},
	}, "glm-5.2", "test-key", config.Parameters{})
	if err != nil {
		t.Fatalf("BuildPiModelsConfig: %v", err)
	}

	out := mergePiProvidersFrom(cfg, globalDir)
	providers := out["providers"].(map[string]any)
	amagi, ok := providers["amagi-glm"].(map[string]any)
	if !ok {
		t.Fatalf("amagi-glm provider lost after merge: %v", providers)
	}
	if amagi["apiKey"] != "test-key" {
		t.Errorf("amagi provider apiKey lost: %v", amagi)
	}
	if _, ok := providers["zhipuai"]; !ok {
		t.Error("global zhipuai not merged")
	}
}

// 包源 identity 提取（unionPiPackages 单元测试）。
func TestUnionPiPackages(t *testing.T) {
	local := []any{"npm:a", map[string]any{"source": "git:x"}}
	global := []any{"npm:a", "git:x", "npm:b", map[string]any{"source": " "}}
	out, added := unionPiPackages(local, global)
	if !added {
		t.Fatal("expected additions from global")
	}
	if len(out) != 3 {
		t.Fatalf("union len = %d, want 3: %v", len(out), out)
	}
	if out[0] != "npm:a" {
		t.Errorf("local order not preserved: %v", out)
	}
	// 无全局数组时原样。
	if out2, added2 := unionPiPackages(local, nil); added2 || len(out2) != 2 {
		t.Errorf("nil global: added=%v out=%v", added2, out2)
	}
}

// 快审 FINDING-1 回归：已存在的 0755 agentDir 被收紧为 0700。
func TestSyncPiGlobalStateTightensExistingDirPerms(t *testing.T) {
	agentDir, globalDir := setupPiSyncDirs(t)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syncPiGlobalStateFrom(agentDir, globalDir, nil); err != nil {
		t.Fatalf("sync: %v", err)
	}
	info, err := os.Stat(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("agentDir perm = %o, want 0700", info.Mode().Perm())
	}
}

// 快审 FINDING-2 回归：断链符号链接自愈重建；完好链接幂等不动。
func TestSyncPiGlobalStateHealsBrokenEntityLink(t *testing.T) {
	agentDir, globalDir := setupPiSyncDirs(t)
	globalGit := filepath.Join(globalDir, "git")
	if err := os.MkdirAll(globalGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(agentDir, "git")
	// 断链：指向不存在的目标
	if err := os.Symlink(filepath.Join(t.TempDir(), "moved-away"), link); err != nil {
		t.Fatal(err)
	}
	if err := syncPiGlobalStateFrom(agentDir, globalDir, nil); err != nil {
		t.Fatalf("sync: %v", err)
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("link missing after heal: %v", err)
	}
	if target != globalGit {
		t.Errorf("healed link target = %s, want %s", target, globalGit)
	}
	if _, err := os.Stat(link); err != nil {
		t.Errorf("healed link still broken: %v", err)
	}
}
