package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
)

// fakeWSLTarget 把 WSL 探测接缝指到本地临时目录，模拟 distro 内 /home：
//
//	/home/wslu/.pi/agent -> <tmpDir>/home/wslu/.pi/agent（Windows UNC 的替身）
//
// 并记录 chmod 补偿调用。跨平台可跑（Linux CI / Windows 本地均通过）。
type fakeWSLTarget struct {
	root       string
	chmodCalls []string // "<mode> <path>" 记录
}

func installFakeWSLTarget(t *testing.T) *fakeWSLTarget {
	t.Helper()
	ft := &fakeWSLTarget{root: t.TempDir()}
	origHome, origUNC, origChmod := wslUserHomeFn, wslToUNCFn, wslChmodFn
	wslUserHomeFn = func(distro string) string { return "/home/wslu" }
	wslToUNCFn = func(distro, linuxPath string) string {
		return filepath.Join(ft.root, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
	}
	wslChmodFn = func(distro, mode string, paths ...string) error {
		for _, p := range paths {
			ft.chmodCalls = append(ft.chmodCalls, mode+" "+p)
		}
		return nil
	}
	t.Cleanup(func() {
		wslUserHomeFn, wslToUNCFn, wslChmodFn = origHome, origUNC, origChmod
	})
	return ft
}

func (ft *fakeWSLTarget) uncPath(linuxPath string) string {
	return filepath.Join(ft.root, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
}

func TestWriteWSLPiAgentConfigMergeAndChmod(t *testing.T) {
	ft := installFakeWSLTarget(t)

	// 预置 WSL 侧已有配置：保留既有 provider（含其它 amagi-*）与顶层字段。
	existing := map[string]any{
		"providers": map[string]any{
			"user-custom": map[string]any{"baseUrl": "https://existing.example", "api": "anthropic-messages"},
			"amagi-old":   map[string]any{"baseUrl": "https://old.example", "api": "openai-completions"},
		},
		"equivalence": map[string]any{"note": "keep-me"},
	}
	existingBytes, _ := json.Marshal(existing)
	if err := os.MkdirAll(ft.uncPath("/home/wslu/.pi/agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ft.uncPath("/home/wslu/.pi/agent/models.json"), existingBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	provider := config.Provider{Anthropic: &config.AnthropicFormat{Enabled: true, BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4"}}
	cfg, err := BuildPiModelsConfig("glm", provider, "glm-5.3", "sk-test", config.Parameters{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	linuxDir, err := WriteWSLPiAgentConfig("Ubuntu", cfg)
	if err != nil {
		t.Fatalf("WriteWSLPiAgentConfig: %v", err)
	}
	if linuxDir != "/home/wslu/.pi/agent" {
		t.Fatalf("linuxDir = %q, want /home/wslu/.pi/agent", linuxDir)
	}

	// 合并语义：既有 provider / 顶层字段保留，当次 amagi-glm 条目生效。
	data, err := os.ReadFile(ft.uncPath("/home/wslu/.pi/agent/models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	providers, _ := got["providers"].(map[string]any)
	if _, ok := providers["user-custom"]; !ok {
		t.Fatalf("existing provider dropped: %v", got)
	}
	if _, ok := providers["amagi-old"]; !ok {
		t.Fatalf("sibling amagi provider dropped: %v", got)
	}
	if _, ok := providers["amagi-glm"]; !ok {
		t.Fatalf("amagi-glm provider missing: %v", got)
	}
	if _, ok := got["equivalence"]; !ok {
		t.Fatalf("top-level field dropped: %v", got)
	}
	entry, _ := providers["amagi-glm"].(map[string]any)
	if entry["apiKey"] != "sk-test" || entry["baseUrl"] != "https://open.bigmodel.cn/api/coding/paas/v4" {
		t.Fatalf("amagi-glm entry wrong: %v", entry)
	}

	// chmod 补偿：目录 0700 + 文件 0600（Windows 侧写 UNC 不带 POSIX 位）。
	want := []string{
		"700 /home/wslu/.pi/agent",
		"600 /home/wslu/.pi/agent/models.json",
	}
	if strings.Join(ft.chmodCalls, "|") != strings.Join(want, "|") {
		t.Fatalf("chmod calls = %v, want %v", ft.chmodCalls, want)
	}
}

func TestWriteWSLPiAgentConfigFreshDistro(t *testing.T) {
	ft := installFakeWSLTarget(t)
	cfg := map[string]any{
		"providers": map[string]any{
			"amagi-glm": map[string]any{"baseUrl": "https://x.example", "api": "anthropic-messages", "apiKey": "k"},
		},
	}
	linuxDir, err := WriteWSLPiAgentConfig("Ubuntu", cfg)
	if err != nil {
		t.Fatalf("WriteWSLPiAgentConfig on fresh distro: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ft.uncPath(linuxDir), "models.json")); err != nil {
		t.Fatalf("models.json not written: %v", err)
	}
}

func TestWriteWSLOmpAgentConfigMergeAndChmod(t *testing.T) {
	ft := installFakeWSLTarget(t)
	provider := config.Provider{Anthropic: &config.AnthropicFormat{Enabled: true, BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4"}}
	cfg, err := BuildOmpModelsConfig("glm", provider, "glm-5.3", "sk-omp", config.Parameters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	linuxDir, err := WriteWSLOmpAgentConfig("Ubuntu", cfg)
	if err != nil {
		t.Fatalf("WriteWSLOmpAgentConfig: %v", err)
	}
	if linuxDir != "/home/wslu/.omp/agent" {
		t.Fatalf("linuxDir = %q, want /home/wslu/.omp/agent", linuxDir)
	}
	data, err := os.ReadFile(ft.uncPath("/home/wslu/.omp/agent/models.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "amagi-glm") || !strings.Contains(string(data), "sk-omp") {
		t.Fatalf("models.yml content wrong:\n%s", data)
	}
	want := []string{
		"700 /home/wslu/.omp/agent",
		"600 /home/wslu/.omp/agent/models.yml",
	}
	if strings.Join(ft.chmodCalls, "|") != strings.Join(want, "|") {
		t.Fatalf("chmod calls = %v, want %v", ft.chmodCalls, want)
	}
}

func TestWriteWSLPiAgentConfigChmodFailureFailsClosed(t *testing.T) {
	_ = installFakeWSLTarget(t)
	origChmod := wslChmodFn
	wslChmodFn = func(distro, mode string, paths ...string) error {
		return os.ErrPermission
	}
	t.Cleanup(func() { wslChmodFn = origChmod })
	cfg := map[string]any{"providers": map[string]any{"amagi-glm": map[string]any{"baseUrl": "https://x.example"}}}
	if _, err := WriteWSLPiAgentConfig("Ubuntu", cfg); err == nil {
		t.Fatal("chmod failure must fail the WSL write (fail closed)")
	}
}

func TestResolveWSLAgentTargetErrors(t *testing.T) {
	origHome, origUNC := wslUserHomeFn, wslToUNCFn
	t.Cleanup(func() { wslUserHomeFn, wslToUNCFn = origHome, origUNC })

	wslUserHomeFn = func(string) string { return "" }
	if _, err := resolveWSLAgentTarget("Ubuntu", ".pi/agent"); err == nil {
		t.Fatal("unresolvable home must error")
	}
	wslUserHomeFn = func(string) string { return "/home/wslu" }
	wslToUNCFn = func(string, string) string { return "" }
	if _, err := resolveWSLAgentTarget("Ubuntu", ".pi/agent"); err == nil {
		t.Fatal("unreachable UNC must error")
	}
	if _, err := resolveWSLAgentTarget("  ", ".pi/agent"); err == nil {
		t.Fatal("empty distro must error")
	}
}

func TestStripWSLHostPathPIEnv(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"PI_CODING_AGENT_DIR=" + `C:\Users\毛润\.pi\agent`,             // Windows 路径值 → 剥离
		"PI_SESSION_FILE=" + `C:/Users/u/.pi/agent/sessions/x.jsonl`, // 正斜杠形态 → 剥离
		"PI_OFFLINE=1",           // 标量值 → 保留（WSL 内有效）
		"PI_MODEL=glm-5.3",       // 标量值 → 保留
		"ANTHROPIC_API_KEY=sk-x", // 非 PI_ 前缀 → 保留
		"PI_TOOL=runc:" + `D:\x`, // 非纯路径形态（值不以盘符开头）→ 保留
	}
	got := StripWSLHostPathPIEnv(env)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "PI_CODING_AGENT_DIR") || strings.Contains(joined, "PI_SESSION_FILE") {
		t.Fatalf("Windows-path PI_ vars not stripped: %v", got)
	}
	for _, keep := range []string{"PI_OFFLINE=1", "PI_MODEL=glm-5.3", "ANTHROPIC_API_KEY=sk-x", "PI_TOOL="} {
		if !strings.Contains(joined, keep) {
			t.Fatalf("expected %q kept, got %v", keep, got)
		}
	}
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5: %v", len(got), got)
	}
}
