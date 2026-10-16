package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/platform"
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

func TestStripWSLHostPathEnv(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"PI_CODING_AGENT_DIR=" + `C:\Users\毛润\.pi\agent`,             // PI_ + Windows 路径 → 剥离（1.3.59 原事故面）
		"PI_SESSION_FILE=" + `C:/Users/u/.pi/agent/sessions/x.jsonl`, // 正斜杠形态 → 剥离
		"PI_OFFLINE=1",           // 标量值 → 保留（WSL 内有效）
		"PI_MODEL=glm-5.3",       // 标量值 → 保留
		"ANTHROPIC_API_KEY=sk-x", // 标量值（在转发面但值非路径）→ 保留
		"PI_TOOL=runc:" + `D:\x`, // 非纯路径形态（值不以盘符开头）→ 保留
		// B3/T6 泛化面：其余四前缀 + 精确键 + 自定义键的 Windows 路径值同样剥离。
		"CODEX_HOME=" + `C:\Users\u\.codex`,                           // T6 实例 → 剥离
		"ANTHROPIC_CONFIG_DIR=" + `D:\cfg\claude`,                     // → 剥离
		"CLAUDE_PROJECT_DIR=" + `E:/proj`,                             // 正斜杠 → 剥离
		"OPENAI_LOG_DIR=" + `C:\tmp\oai`,                              // → 剥离
		"OPENCODE_CONFIG=" + `C:\Users\u\.config\opencode.json`,       // 精确键 + Windows 路径 → 剥离
		"MY_TOOL_HOME=" + `F:\tools`,                                  // 自定义键 + Windows 路径 → 剥离（在 extra 列表内）
		"OPENCODE_CONFIG=/home/u/.config/opencode/amagi-codebox.json", // T4 注入的 Linux 路径值 → 保留
		"USERPROFILE=" + `C:/Users/a`,                                 // 不在转发面 → 即使 Windows 路径也保留（根本不会跨界）
	}
	got, stripped := StripWSLHostPathEnv(env, "MY_TOOL_HOME", "OTHER_KEY")
	joined := strings.Join(got, "\n")
	for _, gone := range []string{"PI_CODING_AGENT_DIR", "PI_SESSION_FILE", "CODEX_HOME", "ANTHROPIC_CONFIG_DIR", "CLAUDE_PROJECT_DIR", "OPENAI_LOG_DIR"} {
		if strings.Contains(joined, gone) {
			t.Fatalf("forwardable Windows-path var %q not stripped: %v", gone, got)
		}
	}
	if strings.Contains(joined, "OPENCODE_CONFIG="+`C:`) || strings.Contains(joined, "MY_TOOL_HOME") {
		t.Fatalf("exact/custom Windows-path vars not stripped: %v", got)
	}
	for _, keep := range []string{
		"PI_OFFLINE=1", "PI_MODEL=glm-5.3", "ANTHROPIC_API_KEY=sk-x", "PI_TOOL=",
		"OPENCODE_CONFIG=/home/u/.config/opencode/amagi-codebox.json",
		"USERPROFILE=",
	} {
		if !strings.Contains(joined, keep) {
			t.Fatalf("expected %q kept, got %v", keep, got)
		}
	}
	if len(got) != 7 {
		t.Fatalf("len = %d, want 7: %v", len(got), got)
	}
	wantStripped := []string{
		"PI_CODING_AGENT_DIR", "PI_SESSION_FILE",
		"CODEX_HOME", "ANTHROPIC_CONFIG_DIR", "CLAUDE_PROJECT_DIR",
		"OPENAI_LOG_DIR", "OPENCODE_CONFIG", "MY_TOOL_HOME",
	}
	if strings.Join(stripped, ",") != strings.Join(wantStripped, ",") {
		t.Fatalf("stripped keys = %v, want %v", stripped, wantStripped)
	}

	// extra 列表外的自定义键：值为 Windows 路径也不剥离（不会跨界）。
	got2, stripped2 := StripWSLHostPathEnv([]string{"MY_TOOL_HOME=" + `F:\tools`})
	if len(got2) != 1 || len(stripped2) != 0 {
		t.Fatalf("non-forwardable custom key must be kept: %v / %v", got2, stripped2)
	}

	// 无一命中时返回空 stripped 列表（调用方零日志）。
	_, stripped3 := StripWSLHostPathEnv([]string{"PI_OFFLINE=1"})
	if len(stripped3) != 0 {
		t.Fatalf("unexpected strips: %v", stripped3)
	}
}

func TestWriteWSLOpenCodeAgentConfigWritesAndChmod(t *testing.T) {
	ft := installFakeWSLTarget(t)

	// 预置一个旧的 amagi-codebox.json：整体覆盖语义（CodeBox 命名空间私有
	// 文件，无 merge——与 pi/omp 的差异），旧 provider 残留不得存活。
	uncFile := ft.uncPath("/home/wslu/.config/opencode/amagi-codebox.json")
	if err := os.MkdirAll(filepath.Dir(uncFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(uncFile, []byte(`{"model":"stale/old","provider":{"stale":{"options":{"apiKey":"old"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := map[string]any{
		"model": "glm/glm-5.3",
		"provider": map[string]any{
			"glm": map[string]any{
				"npm":     "@ai-sdk/openai-compatible",
				"options": map[string]any{"apiKey": "sk-oc", "baseURL": "https://api.example/v4"},
			},
		},
	}
	linuxPath, err := WriteWSLOpenCodeAgentConfig("Ubuntu", cfg)
	if err != nil {
		t.Fatalf("WriteWSLOpenCodeAgentConfig: %v", err)
	}
	if want := "/home/wslu/.config/opencode/amagi-codebox.json"; linuxPath != want {
		t.Fatalf("linuxPath = %q, want %q", linuxPath, want)
	}

	data, err := os.ReadFile(uncFile)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	if got["model"] != "glm/glm-5.3" {
		t.Fatalf("model = %v, want glm/glm-5.3", got["model"])
	}
	providers, _ := got["provider"].(map[string]any)
	if _, ok := providers["stale"]; ok {
		t.Fatalf("stale provider survived an overwrite write: %v", providers)
	}
	entry, _ := providers["glm"].(map[string]any)
	options, _ := entry["options"].(map[string]any)
	if options["apiKey"] != "sk-oc" {
		t.Fatalf("apiKey = %v, want sk-oc", options)
	}
	if string(data[len(data)-1]) != "\n" {
		t.Fatalf("file must end with newline, tail = %q", data[len(data)-3:])
	}

	// chmod 补偿：目录 0700 + 文件 0600（apiKey 明文落盘契约）。
	want := []string{
		"700 /home/wslu/.config/opencode",
		"600 /home/wslu/.config/opencode/amagi-codebox.json",
	}
	if strings.Join(ft.chmodCalls, "|") != strings.Join(want, "|") {
		t.Fatalf("chmod calls = %v, want %v", ft.chmodCalls, want)
	}
}

func TestWriteWSLOpenCodeAgentConfigFreshDistro(t *testing.T) {
	ft := installFakeWSLTarget(t)
	linuxPath, err := WriteWSLOpenCodeAgentConfig("Ubuntu", map[string]any{"model": "openai/gpt-5"})
	if err != nil {
		t.Fatalf("fresh distro write: %v", err)
	}
	if _, err := os.Stat(ft.uncPath(linuxPath)); err != nil {
		t.Fatalf("config not written: %v", err)
	}
}

func TestWriteWSLOpenCodeAgentConfigChmodFailureFailsClosed(t *testing.T) {
	ft := installFakeWSLTarget(t)
	origChmod := wslChmodFn
	wslChmodFn = func(distro, mode string, paths ...string) error {
		return os.ErrPermission
	}
	t.Cleanup(func() { wslChmodFn = origChmod })
	if _, err := WriteWSLOpenCodeAgentConfig("Ubuntu", map[string]any{"model": "openai/gpt-5"}); err == nil {
		t.Fatal("chmod failure must fail the WSL write (fail closed)")
	}
	// fail closed：已落盘的明文 apiKey 内容必须被清理。
	if _, err := os.Stat(ft.uncPath("/home/wslu/.config/opencode/amagi-codebox.json")); err == nil {
		t.Fatal("plaintext config must be removed after chmod failure")
	}
}

// TestStripWSLHostPathEnvAllowGate pins the P4-A2 per-key path gate: keys marked
// allowPathValue (envvars.json) keep their Windows drive-path values — the
// forward side attaches the WSLENV /p flag — while unmarked keys keep the
// default strip behavior (P1 PI_* leak baseline unchanged), and reserved-key
// markings are no-ops (reserved keys are not in the forward face at all).
func TestStripWSLHostPathEnvAllowGate(t *testing.T) {
	env := []string{
		"UE_ROOT=" + `C:\Program Files\Epic Games\UE_5.4`, // 标记放行 → 保留
		"MY_TOOL_HOME=" + `F:\tools`,                      // 未标记 → 照旧剥离
		"PI_OFFLINE=1",                                    // 标量 → 保留
	}
	kept, stripped, allowed := StripWSLHostPathEnvWithAllow(env, []string{"UE_ROOT", "MY_TOOL_HOME"}, []string{"UE_ROOT"})
	joined := strings.Join(kept, "\n")
	if !strings.Contains(joined, "UE_ROOT=") {
		t.Fatalf("allow-marked key with drive-path value must be kept: %v", kept)
	}
	if strings.Contains(joined, "MY_TOOL_HOME") {
		t.Fatalf("unmarked key must still be stripped: %v", kept)
	}
	if strings.Join(stripped, ",") != "MY_TOOL_HOME" {
		t.Fatalf("stripped = %v, want [MY_TOOL_HOME]", stripped)
	}
	if strings.Join(allowed, ",") != "UE_ROOT" {
		t.Fatalf("allowed = %v, want [UE_ROOT]", allowed)
	}
	if !strings.Contains(joined, "PI_OFFLINE=1") {
		t.Fatalf("scalar value must be kept: %v", kept)
	}

	// 保留键标记无效：PATH 不在转发面，既不剥离也谈不上豁免。
	kept2, stripped2, allowed2 := StripWSLHostPathEnvWithAllow(
		[]string{"PATH=" + `C:\Windows`},
		nil, []string{"PATH"},
	)
	if len(kept2) != 1 || len(stripped2) != 0 || len(allowed2) != 0 {
		t.Fatalf("reserved-key marking must be a no-op: %v / %v / %v", kept2, stripped2, allowed2)
	}

	// 放行键但值非盘符路径：不产生豁免记录（豁免面只关心路径值键）。
	_, _, allowed3 := StripWSLHostPathEnvWithAllow([]string{"UE_ROOT=5.4"}, []string{"UE_ROOT"}, []string{"UE_ROOT"})
	if len(allowed3) != 0 {
		t.Fatalf("non-path value must not be reported as allowed: %v", allowed3)
	}

	// 大小写不敏感：envvars.json 记 ue_root，进程 env 键 UE_ROOT 同样豁免。
	kept4, stripped4, allowed4 := StripWSLHostPathEnvWithAllow(
		[]string{"UE_ROOT=" + `D:\eng`, "MY_TOOL_HOME=" + `F:\tools`},
		[]string{"UE_ROOT", "MY_TOOL_HOME"}, []string{"ue_root"},
	)
	if len(allowed4) != 1 || allowed4[0] != "UE_ROOT" || strings.Join(stripped4, ",") != "MY_TOOL_HOME" {
		t.Fatalf("case-insensitive allow failed: %v / %v", allowed4, stripped4)
	}
	if !strings.Contains(strings.Join(kept4, "\n"), "UE_ROOT=") {
		t.Fatalf("case-insensitive allow must keep the var: %v", kept4)
	}

	// 旧签名回归：无放行清单时行为与默认剥离完全一致。
	gotLegacy, strippedLegacy := StripWSLHostPathEnv(env, "UE_ROOT", "MY_TOOL_HOME")
	legacyJoined := strings.Join(gotLegacy, "\n")
	if strings.Contains(legacyJoined, "UE_ROOT=") || strings.Contains(legacyJoined, "MY_TOOL_HOME") {
		t.Fatalf("legacy strip must still strip both drive-path vars: %v", gotLegacy)
	}
	if strings.Join(strippedLegacy, ",") != "UE_ROOT,MY_TOOL_HOME" {
		t.Fatalf("legacy stripped = %v, want [UE_ROOT MY_TOOL_HOME]", strippedLegacy)
	}
}

// envValueFor 返回 env 中指定键的值（不存在返回空串），链路测试用。
func envValueFor(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// TestStripThenForwardPathAllowChain pins the P4-A2 launch-funnel order
// (app.go resolveEmbeddedLaunchSpec)：StripWSLHostPathEnvWithAllow 先行，随后
// AppendWSLENVForwardingWithPaths 拼清单——两侧拿到的是同一份 allow 清单
// （EnvPathFlagKeys）。放行键存活于 env 且在 WSLENV 中带 /p；未标记的路径值键
// 在转发前已被剥离，清单中不再出现。
func TestStripThenForwardPathAllowChain(t *testing.T) {
	env := []string{
		"UE_ROOT=" + `C:\Program Files\Epic Games\UE_5.4`,
		"MY_TOOL_HOME=" + `F:\tools`,
		"TAVILY_API_KEY=tk",
	}
	extra := []string{"UE_ROOT", "MY_TOOL_HOME", "TAVILY_API_KEY"}
	allow := []string{"UE_ROOT"}

	kept, stripped, allowed := StripWSLHostPathEnvWithAllow(env, extra, allow)
	if strings.Join(stripped, ",") != "MY_TOOL_HOME" || strings.Join(allowed, ",") != "UE_ROOT" {
		t.Fatalf("strip phase: stripped=%v allowed=%v", stripped, allowed)
	}

	// 与 resolver WSL 分支相同的转发调用形态。
	out := platform.AppendWSLENVForwardingWithPaths(kept, extra, allow)
	wslenv := envValueFor(out, "WSLENV")
	if !strings.Contains(wslenv, "UE_ROOT/p") {
		t.Fatalf("WSLENV missing /p entry for allow-marked key: %q", wslenv)
	}
	if strings.Contains(wslenv, "MY_TOOL_HOME") {
		t.Fatalf("stripped key must not appear in WSLENV: %q", wslenv)
	}
	if !strings.Contains(wslenv, "TAVILY_API_KEY") || strings.Contains(wslenv, "TAVILY_API_KEY/") {
		t.Fatalf("scalar custom key must forward without flags: %q", wslenv)
	}
	// 放行键的值原样保留（/p 翻译由 WSL 跨界时执行）。
	if v := envValueFor(out, "UE_ROOT"); v != `C:\Program Files\Epic Games\UE_5.4` {
		t.Fatalf("allowed key value must stay verbatim, got %q", v)
	}
}
