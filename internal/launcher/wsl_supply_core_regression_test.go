package launcher

// wsl_supply_core_regression_test.go — wsl-goal-closure 供给内核修复的
// launcher 侧正式回归（SA-06 目标侧私有 staging / SA-07 失败不删混合配置 /
// 零写入路径权限复核）。全部经注入接缝与合成字节运行，不触达真实 WSL、
// 不读取真实凭据；真机取证脚本见 agent-outputs/workflow/wsl-goal-closure/。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustTime(layout string) time.Time {
	t, err := time.Parse(time.RFC3339, layout)
	if err != nil {
		panic(err)
	}
	return t
}

// 敏感 chmod 失败必须发生在 rename 之前：已有混合配置（含用户自有 provider）
// 原样保留，绝不整文件删除（SA-07 的 launcher 对应面——旧实现 rename 后才
// chmod，失败时删除整个 models.json，连同用户自有 provider 一起丢失）。
func TestWriteWSLPiAgentConfigChmodFailurePreservesMixedConfig(t *testing.T) {
	ft := installFakeWSLTarget(t)
	agentDir := ft.uncPath("/home/wslu/.pi/agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mixed := `{"providers":{"local-user":{"baseUrl":"https://user.example"}},"userSetting":true}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(mixed), 0o600); err != nil {
		t.Fatal(err)
	}
	origChmod := wslChmodFn
	wslChmodFn = func(context.Context, string, string, ...string) error { return os.ErrPermission }
	t.Cleanup(func() { wslChmodFn = origChmod })

	cfg := map[string]any{"providers": map[string]any{"amagi-glm": map[string]any{"baseUrl": "https://x.example", "apiKey": "sk"}}}
	if _, err := WriteWSLPiAgentConfig("Ubuntu", cfg); err == nil {
		t.Fatal("chmod failure must fail the write (fail closed)")
	}
	got, readErr := os.ReadFile(filepath.Join(agentDir, "models.json"))
	if readErr != nil || string(got) != mixed {
		t.Fatalf("pre-existing user content must be preserved on chmod failure, got %q err=%v", got, readErr)
	}
	// staging 不留半提交字节（chmod 失败在 rename 前；目录可能尚未建立）。
	if entries, readErr := os.ReadDir(filepath.Join(agentDir, ".amagi-staging")); readErr == nil && len(entries) != 0 {
		t.Fatalf("staging must hold no partial bytes after failure: %v", entries)
	}
}

// A2 同步 chmod 失败：distro 侧既有副本（旧凭据）原样保留并降级
// （DistroHasCopy）——不再删除副本（SA-06 后失败发生在 rename 之前）。
func TestSyncAuthChmodFailureKeepsOldDistroCopy(t *testing.T) {
	fakeWindowsAuth(t, `{"newer":{"type":"oauth"}}`)
	root, _ := fakeWSLForSync(t, os.ErrPermission)
	writeDistroAuth(t, root, `{"old":{"type":"oauth"}}`, mustTime("2026-01-01T00:00:00Z"))
	stubDistroMTime(t, mustTime("2025-12-01T00:00:00Z")) // distro 旧 → 前向写触发
	outcome, err := SyncWSLPiLoginAuth("Ubuntu")
	if err == nil {
		t.Fatal("chmod failure must fail the sync attempt")
	}
	if outcome != SyncWSLPiLoginAuthDistroHasCopy {
		t.Fatalf("outcome = %v, want DistroHasCopy (old copy intact, degraded)", outcome)
	}
	got, readErr := os.ReadFile(distroAuthPath(root))
	if readErr != nil || string(got) != `{"old":{"type":"oauth"}}` {
		t.Fatalf("old distro copy must survive: %q err=%v", got, readErr)
	}
}

// 零写入路径（字节一致）权限被改宽 → 重收紧 600；无法收紧 → 报错
// （SA-06：无变化路径不得绕过权限检查；steer#4）。
func TestWriteWSLPiAgentConfigZeroWriteRetightensLooseMode(t *testing.T) {
	ft := installFakeWSLTarget(t)
	agentDir := ft.uncPath("/home/wslu/.pi/agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"providers": map[string]any{"amagi-glm": map[string]any{"baseUrl": "https://x.example"}}}
	if _, err := WriteWSLPiAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatal(err)
	}
	// 第一次写入后，最终文件在 fake modes 中无记录（chmod 落在 staging tmp 上）；
	// 显式把最终路径置为 644 模拟「已存在但被改宽」。
	ft.modes["/home/wslu/.pi/agent/models.json"] = "644"
	before, _ := os.ReadFile(filepath.Join(agentDir, "models.json"))
	chmods := len(ft.chmodCalls)
	if _, err := WriteWSLPiAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatalf("zero-write with loose mode must retighten and succeed: %v", err)
	}
	if len(ft.chmodCalls) != chmods+1 {
		t.Fatalf("loose mode must trigger one retighten chmod, %d -> %d", chmods, len(ft.chmodCalls))
	}
	if mode := ft.modes["/home/wslu/.pi/agent/models.json"]; mode != "600" {
		t.Fatalf("final mode must be 600 after retighten, got %q", mode)
	}
	after, _ := os.ReadFile(filepath.Join(agentDir, "models.json"))
	if string(before) != string(after) {
		t.Fatal("zero-write must not rewrite bytes")
	}

	// 重收紧失败（chmod 报错但仅限该路径）→ 必须报错而不是静默放行。
	origChmod := wslChmodFn
	wslChmodFn = func(_ context.Context, _, mode string, paths ...string) error {
		for _, p := range paths {
			if p == "/home/wslu/.pi/agent/models.json" && mode == "600" {
				return os.ErrPermission
			}
		}
		return nil
	}
	t.Cleanup(func() { wslChmodFn = origChmod })
	ft.modes["/home/wslu/.pi/agent/models.json"] = "644"
	if _, err := WriteWSLPiAgentConfig("Ubuntu", cfg); err == nil {
		t.Fatal("un-re-tightenable loose secret file must fail (steer#4: repair or block)")
	}
}

// staging 隔离无法验证（StatMode 探测失败）→ 任何秘密字节落盘前失败，
// 且错误语义可观测（SA-06 fail-closed 验证面）。
func TestWriteWSLPiAgentConfigFailsWhenStagingUnverifiable(t *testing.T) {
	ft := installFakeWSLTarget(t)
	origStatMode := wslStatModeFn
	wslStatModeFn = func(context.Context, string, string) (string, bool) { return "", false }
	t.Cleanup(func() { wslStatModeFn = origStatMode })
	cfg := map[string]any{"providers": map[string]any{"amagi-glm": map[string]any{"baseUrl": "https://x.example", "apiKey": "sk"}}}
	_, err := WriteWSLPiAgentConfig("Ubuntu", cfg)
	if err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatalf("unverifiable staging must fail closed, got %v", err)
	}
	if _, statErr := os.Stat(ft.uncPath("/home/wslu/.pi/agent/models.json")); !os.IsNotExist(statErr) {
		t.Fatal("no secret bytes may land when staging isolation cannot be verified")
	}
}

// 敏感提交序列：chmod 600 落在 staging 内文件上（rename 之前）——最终目录
// 即使 0755（如 ~/.agents、.config 层级由其它工具建立），秘密字节也从未以
// 宽权限可见（SA-06 的泄漏窗口闭合证明，fixture 级）。
func TestWriteWSLAgentFileStagedTightensBeforeRename(t *testing.T) {
	ft := installFakeWSLTarget(t)
	cfg := map[string]any{"model": "openai/gpt-5"}
	if _, err := WriteWSLOpenCodeAgentConfig("Ubuntu", cfg); err != nil {
		t.Fatal(err)
	}
	staged600 := ""
	for _, c := range ft.chmodCalls {
		if strings.HasPrefix(c, "600 /home/wslu/.config/opencode/.amagi-staging/") {
			staged600 = c
		}
	}
	if staged600 == "" {
		t.Fatalf("chmod 600 must target the staged file before rename, calls=%v", ft.chmodCalls)
	}
	// 最终文件（.config 层级在真实场景常为 0755）携带收紧后的 0600 宿主位。
	if info, statErr := os.Stat(ft.uncPath("/home/wslu/.config/opencode/amagi-codebox.json")); statErr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("final file must be 0600, got %v err=%v", info.Mode().Perm(), statErr)
	}
	// 序列化内容可回读（幂等字节）。
	b, _ := os.ReadFile(ft.uncPath("/home/wslu/.config/opencode/amagi-codebox.json"))
	var got map[string]any
	if json.Unmarshal(b, &got) != nil || got["model"] != "openai/gpt-5" {
		t.Fatalf("content roundtrip failed: %s", b)
	}
}
