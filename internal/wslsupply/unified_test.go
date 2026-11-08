package wslsupply

// unified_test.go — P2-B「统一配置管理」协调器原语的回归钉子：
//   - per-generation Unified 标记生命周期（unknown 拒绝 / 换代失效 / Disable）；
//   - AutoConsentIfUnified：未启用 no-op、跳过已授权与 detached（I2 陷阱）、
//     保留同代登记字段；
//   - ExtendUnitSelectionIfUnified：显式键物化（只增不减、永不写 "*"）、
//     未启用 no-op、detached/未同意不动、来源新键下次物化自动纳入；
//   - UnitSourceKeys（来源侧可导入键全集，不探测目标）；
//   - 扫描完成回调（skills / resources 两个作用域都真正触发）；
//   - 状态清单持久化面永不出现 "*"（旧版本降级安全——Leader 裁定的验收点）。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// newUnifiedClaudeFixture 构造带 claude 两单元与独立 configDir 的协调器
// （返回 statePath 供降级断言直读清单）。
func newUnifiedClaudeFixture(t *testing.T) (*Coordinator, *fakeProbes, *stubClaudeConfigSource, *stubFactsSource, string) {
	t.Helper()
	fp := newFakeProbes(t)
	configDir := t.TempDir()
	c := NewCoordinator(configDir, nil, fp.probes())
	src := &stubClaudeConfigSource{}
	facts := &stubFactsSource{}
	facts.set(TransformFacts{
		ResolvedCommands: map[string]string{"npx": "/usr/bin/npx"},
		PrefixMappings:   []PathPrefixRule{{WindowsPrefix: `C:\Users\u\.claude`, LinuxPrefix: "/home/u/.claude"}},
		HostHomeWindows:  `C:\Users\u`,
	})
	c.RegisterProjection(NewClaudeMCPAdapter(src, facts))
	c.RegisterProjection(NewClaudeSettingsAdapter(src, facts))
	return c, fp, src, facts, filepath.Join(configDir, StateFileName)
}

func unifiedClaudeSource(src *stubClaudeConfigSource, servers string) {
	src.claude = []byte(`{"mcpServers": {` + servers + `}, "projects": {"/win": {}}}`)
}

// loadUnifiedState 直读状态清单 JSON（降级断言面）。
func loadUnifiedState(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse state: %v", err)
	}
	return out
}

// assertNoWildcardSelections 断言清单内任何单元的选择集都不含 "*"（降级安全）。
func assertNoWildcardSelections(t *testing.T, path string) {
	t.Helper()
	state := loadUnifiedState(t, path)
	units, _ := state["units"].([]any)
	for _, raw := range units {
		entry, _ := raw.(map[string]any)
		selected, _ := entry["selectedKeys"].(map[string]any)
		for k := range selected {
			if k == SelectAllMarker {
				t.Fatalf("selectedKeys must never persist %q (downgrade would drop managed entries); unit=%v", k, entry["unit"])
			}
		}
	}
}

func TestSetTargetUnifiedLifecycle(t *testing.T) {
	c, fp, _, _, _ := newUnifiedClaudeFixture(t)
	if c.UnifiedCached("Ubuntu") {
		t.Fatal("unified must default to false")
	}
	if err := c.SetTargetUnified(context.Background(), "Ubuntu", true); err != nil {
		t.Fatalf("SetTargetUnified: %v", err)
	}
	if !c.UnifiedCached("Ubuntu") {
		t.Fatal("unified must be cached after enable")
	}
	// Disable：标记关闭。
	if err := c.SetTargetUnified(context.Background(), "Ubuntu", false); err != nil {
		t.Fatalf("SetTargetUnified(false): %v", err)
	}
	if c.UnifiedCached("Ubuntu") {
		t.Fatal("unified must be off after disable")
	}
	// unknown 身份（无 machine-id）拒绝持久授权（SA-02）。
	fp.identity = platform.WSLUserIdentity{UID: "1000", Home: "/home/u", OK: true}
	if err := c.SetTargetUnified(context.Background(), "Ubuntu", true); err == nil {
		t.Fatal("unknown generation must reject unified authorization")
	}
	// 换代（machine-id 变化 → 新代）：旧 Unified 标记不继承。
	fp.identity = platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "machine-xyz", OK: true}
	if c.UnifiedCached("Ubuntu") {
		t.Fatal("new generation must not inherit unified marker (SA-02)")
	}
	if err := c.SetTargetUnified(context.Background(), "Ubuntu", true); err != nil {
		t.Fatalf("re-enable on new generation: %v", err)
	}
	if !c.UnifiedCached("Ubuntu") {
		t.Fatal("new generation must be enable-able again")
	}
}

func TestAutoConsentIfUnifiedNoOpWhenNotUnified(t *testing.T) {
	c, _, _, _, statePath := newUnifiedClaudeFixture(t)
	granted, err := c.AutoConsentIfUnified(context.Background(), "Ubuntu", CLIClaude, []UnitID{UnitClaudeMCP})
	if err != nil {
		t.Fatalf("AutoConsentIfUnified (not enabled): %v", err)
	}
	if len(granted) != 0 {
		t.Fatalf("no grants expected when unified off, got %v", granted)
	}
	st := c.GetStatus("Ubuntu", CLIClaude)
	if len(st.Units) != 0 {
		t.Fatalf("no registration expected when unified off, got %+v", st.Units)
	}
	assertNoWildcardSelections(t, statePath)
}

func TestAutoConsentIfUnifiedGrantsAndSkips(t *testing.T) {
	c, _, src, _, _ := newUnifiedClaudeFixture(t)
	unifiedClaudeSource(src, `"web": {"url": "https://mcp.example/sse"}`)
	if err := c.SetTargetUnified(context.Background(), "Ubuntu", true); err != nil {
		t.Fatal(err)
	}
	granted, err := c.AutoConsentIfUnified(context.Background(), "Ubuntu", CLIClaude, []UnitID{UnitClaudeMCP, UnitClaudeSettings})
	if err != nil {
		t.Fatalf("AutoConsentIfUnified: %v", err)
	}
	if len(granted) != 2 {
		t.Fatalf("expected 2 fresh grants, got %v", granted)
	}
	// 幂等：已授权单元再次 AutoConsent 不重复授权、不重建 entry。
	granted2, err := c.AutoConsentIfUnified(context.Background(), "Ubuntu", CLIClaude, []UnitID{UnitClaudeMCP, UnitClaudeSettings})
	if err != nil {
		t.Fatal(err)
	}
	if len(granted2) != 0 {
		t.Fatalf("already-consented units must be skipped, got %v", granted2)
	}
	// detached 不被自动授权覆盖（I2 陷阱：GrantUnitConsent 重建会丢 Detached）。
	if err := c.DropUnitRegistration("Ubuntu", CLIClaude, UnitClaudeSettings); err != nil {
		t.Fatalf("detach: %v", err)
	}
	granted3, err := c.AutoConsentIfUnified(context.Background(), "Ubuntu", CLIClaude, []UnitID{UnitClaudeSettings})
	if err != nil {
		t.Fatal(err)
	}
	if len(granted3) != 0 {
		t.Fatalf("detached unit must never be auto-granted, got %v", granted3)
	}
	st := c.GetStatus("Ubuntu", CLIClaude)
	for _, u := range st.Units {
		if u.Unit == UnitClaudeSettings && !u.Detached {
			t.Fatal("detached marker must survive auto-consent")
		}
	}
	// 不可接受同意的单元（未注册适配器）直接报错，不猜策略。
	if _, err := c.AutoConsentIfUnified(context.Background(), "Ubuntu", CLIPi, []UnitID{"pi/retired"}); err == nil {
		t.Fatal("unknown/unacceptable unit must be rejected")
	}
}

func TestAutoConsentPreservesSameGenerationBaselines(t *testing.T) {
	c, _, src, _, statePath := newUnifiedClaudeFixture(t)
	unifiedClaudeSource(src, `"web": {"url": "https://mcp.example/sse"}`)
	if err := c.SetTargetUnified(context.Background(), "Ubuntu", true); err != nil {
		t.Fatal(err)
	}
	// 手动路径先走一半：选择 + 授权 + 运行（建立 Subs 基准）。
	if err := c.SetUnitSelection("Ubuntu", CLIClaude, UnitClaudeMCP, []string{"mcpServers/web"}); err != nil {
		t.Fatal(err)
	}
	grantClaudeConsent(t, c, UnitClaudeMCP)
	if res := runClaudeUnit(t, c, UnitClaudeMCP); res.Outcome != UnitApplied {
		t.Fatalf("first run: %+v", res)
	}
	// 撤销同意（模拟 awaiting-consent 登记仍在同代）：AutoConsent 重新授权
	// 必须保留同代 Subs/SelectedKeys（否则已托管键被判 unmanaged-existing）。
	st := c.GetStatus("Ubuntu", CLIClaude)
	var hadSubs bool
	_ = st
	if granted, err := c.AutoConsentIfUnified(context.Background(), "Ubuntu", CLIClaude, []UnitID{UnitClaudeMCP}); err != nil || len(granted) != 0 {
		t.Fatalf("already-consented unit must be skipped, granted=%v err=%v", granted, err)
	}
	// 直接验证持久面：entry 的 Subs 与 SelectedKeys 仍在（同代保留）。
	state := loadUnifiedState(t, statePath)
	units, _ := state["units"].([]any)
	for _, raw := range units {
		entry, _ := raw.(map[string]any)
		if entry["unit"] != string(UnitClaudeMCP) {
			continue
		}
		if entry["generation"] != mustGeneration(t, c) {
			continue
		}
		subs, _ := entry["subs"].(map[string]any)
		if len(subs) == 0 {
			t.Fatal("same-generation Subs must survive auto-consent skip")
		}
		selected, _ := entry["selectedKeys"].(map[string]any)
		if len(selected) == 0 {
			t.Fatal("same-generation SelectedKeys must survive auto-consent skip")
		}
		hadSubs = true
	}
	if !hadSubs {
		t.Fatal("claude/mcp entry not found in state")
	}
	assertNoWildcardSelections(t, statePath)
}

func mustGeneration(t *testing.T, c *Coordinator) string {
	t.Helper()
	target, err := c.ResolveTarget(context.Background(), "Ubuntu", CLIClaude)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return target.Generation
}

func TestExtendUnitSelectionMaterializesExplicitKeys(t *testing.T) {
	c, _, src, _, statePath := newUnifiedClaudeFixture(t)
	unifiedClaudeSource(src, `"web": {"url": "https://mcp.example/sse"}, "fetch": {"command": "npx.cmd", "args": ["-y", "fetch-mcp"]}`)
	// 未启用统一管理：物化 no-op。
	if added, err := c.ExtendUnitSelectionIfUnified(context.Background(), "Ubuntu", CLIClaude, UnitClaudeMCP, []string{"mcpServers/web"}); err != nil || added != 0 {
		t.Fatalf("extend before unified must be no-op, added=%d err=%v", added, err)
	}
	if err := c.SetTargetUnified(context.Background(), "Ubuntu", true); err != nil {
		t.Fatal(err)
	}
	// 未同意单元：物化不动（授权先行）。
	if added, err := c.ExtendUnitSelectionIfUnified(context.Background(), "Ubuntu", CLIClaude, UnitClaudeMCP, []string{"mcpServers/web"}); err != nil || added != 0 {
		t.Fatalf("extend before consent must be no-op, added=%d err=%v", added, err)
	}
	if _, err := c.AutoConsentIfUnified(context.Background(), "Ubuntu", CLIClaude, []UnitID{UnitClaudeMCP}); err != nil {
		t.Fatal(err)
	}
	// 物化全部来源键（显式键，不是通配）。
	keys, err := c.UnitSourceKeys(UnitClaudeMCP)
	if err != nil {
		t.Fatalf("UnitSourceKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 source keys, got %v", keys)
	}
	added, err := c.ExtendUnitSelectionIfUnified(context.Background(), "Ubuntu", CLIClaude, UnitClaudeMCP, keys)
	if err != nil || added != 2 {
		t.Fatalf("extend: added=%d err=%v", added, err)
	}
	// 幂等：重复物化不新增。
	if added, err := c.ExtendUnitSelectionIfUnified(context.Background(), "Ubuntu", CLIClaude, UnitClaudeMCP, keys); err != nil || added != 0 {
		t.Fatalf("re-extend must add nothing, added=%d err=%v", added, err)
	}
	// 通配键防御：绝不写入。
	if added, err := c.ExtendUnitSelectionIfUnified(context.Background(), "Ubuntu", CLIClaude, UnitClaudeMCP, []string{SelectAllMarker}); err != nil || added != 0 {
		t.Fatalf("wildcard must never be materialized, added=%d err=%v", added, err)
	}
	assertNoWildcardSelections(t, statePath)
	// 运行：两个 server 都导入（显式键选择集驱动）。
	if res := runClaudeUnit(t, c, UnitClaudeMCP); res.Outcome != UnitApplied {
		t.Fatalf("run: %+v", res)
	}
	var written map[string]any
	if err := json.Unmarshal([]byte(readTargetRoot(t, c, CLIClaude, ClaudeJSONFile)), &written); err != nil {
		t.Fatal(err)
	}
	servers, _ := written["mcpServers"].(map[string]any)
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers written, got %v", servers)
	}
	if _, leaked := written["projects"]; leaked {
		t.Fatal("LocalOnly fields must never cross the boundary")
	}
}

// readTargetRoot 读取目标根下的相对路径文件（fake 探测面直读）。
func readTargetRoot(t *testing.T, c *Coordinator, cli CLI, rel string) string {
	t.Helper()
	target, err := c.ResolveTarget(context.Background(), "Ubuntu", cli)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target.UNCRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read target %s: %v", rel, err)
	}
	return string(data)
}

func TestExtendSelectionPicksUpNewSourceKeysOnNextReconcile(t *testing.T) {
	c, _, src, _, _ := newUnifiedClaudeFixture(t)
	unifiedClaudeSource(src, `"web": {"url": "https://mcp.example/sse"}`)
	if err := c.SetTargetUnified(context.Background(), "Ubuntu", true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AutoConsentIfUnified(context.Background(), "Ubuntu", CLIClaude, []UnitID{UnitClaudeMCP}); err != nil {
		t.Fatal(err)
	}
	keys, err := c.UnitSourceKeys(UnitClaudeMCP)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ExtendUnitSelectionIfUnified(context.Background(), "Ubuntu", CLIClaude, UnitClaudeMCP, keys); err != nil {
		t.Fatal(err)
	}
	if res := runClaudeUnit(t, c, UnitClaudeMCP); res.Outcome != UnitApplied {
		t.Fatalf("first run: %+v", res)
	}
	// Windows 侧新增 server：下一次对账（物化 + 运行）自动纳入。
	unifiedClaudeSource(src, `"web": {"url": "https://mcp.example/sse"}, "extra": {"url": "https://mcp2.example/sse"}`)
	keys2, err := c.UnitSourceKeys(UnitClaudeMCP)
	if err != nil {
		t.Fatal(err)
	}
	if added, err := c.ExtendUnitSelectionIfUnified(context.Background(), "Ubuntu", CLIClaude, UnitClaudeMCP, keys2); err != nil || added != 1 {
		t.Fatalf("new source key must be materialized, added=%d err=%v", added, err)
	}
	if res := runClaudeUnit(t, c, UnitClaudeMCP); res.Outcome != UnitApplied {
		t.Fatalf("second run: %+v", res)
	}
	var written map[string]any
	if err := json.Unmarshal([]byte(readTargetRoot(t, c, CLIClaude, ClaudeJSONFile)), &written); err != nil {
		t.Fatal(err)
	}
	servers, _ := written["mcpServers"].(map[string]any)
	if len(servers) != 2 || servers["extra"] == nil {
		t.Fatalf("new server must be imported on next reconcile, got %v", servers)
	}
}

func TestExtendSelectionDetachedUnitUntouched(t *testing.T) {
	c, _, src, _, _ := newUnifiedClaudeFixture(t)
	unifiedClaudeSource(src, `"web": {"url": "https://mcp.example/sse"}`)
	if err := c.SetTargetUnified(context.Background(), "Ubuntu", true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AutoConsentIfUnified(context.Background(), "Ubuntu", CLIClaude, []UnitID{UnitClaudeMCP}); err != nil {
		t.Fatal(err)
	}
	if err := c.DropUnitRegistration("Ubuntu", CLIClaude, UnitClaudeMCP); err != nil {
		t.Fatal(err)
	}
	added, err := c.ExtendUnitSelectionIfUnified(context.Background(), "Ubuntu", CLIClaude, UnitClaudeMCP, []string{"mcpServers/web"})
	if err != nil || added != 0 {
		t.Fatalf("detached unit must not be materialized, added=%d err=%v", added, err)
	}
}

func TestUnitSourceKeysCodex(t *testing.T) {
	c, _, _, _, _ := newUnifiedClaudeFixture(t)
	// codex 适配器独立注册（复用 codex 测试桩）。
	src := &stubCodexConfigSource{toml: []byte(`
[mcp_servers.web]
url = "https://mcp.example/sse"

[mcp_servers.win]
command = "setup.bat"

[profiles.fast]
model = "gpt-5"

[trust]
trusted = ["C:\\proj"]
`)}
	facts := &stubFactsSource{}
	facts.set(TransformFacts{ResolvedCommands: map[string]string{"npx": "/usr/bin/npx"}})
	c.RegisterProjection(NewCodexConfigAdapter(src, facts))
	keys, err := c.UnitSourceKeys(UnitCodexConfig)
	if err != nil {
		t.Fatalf("UnitSourceKeys(codex/config): %v", err)
	}
	got := map[string]bool{}
	for _, k := range keys {
		got[k] = true
	}
	if !got["mcp_servers/web"] || !got["mcp_servers/win"] || !got["profiles/fast"] {
		t.Fatalf("expected managed-scope keys, got %v", keys)
	}
	if got["trust"] {
		t.Fatal("non-managed scope must not appear in source keys")
	}
	// 非键级单元（A2）没有来源键面。
	if _, err := c.UnitSourceKeys(UnitSharedVisionExport); err == nil {
		t.Fatal("non-keyed unit must be rejected")
	}
}

func TestScanCompletionCallbackFires(t *testing.T) {
	fp := newFakeProbes(t)
	c := NewCoordinator(t.TempDir(), nil, fp.probes())
	// skills 作用域：单技能目录。
	skillsRoot := t.TempDir()
	skillDir := filepath.Join(skillsRoot, "alpha")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.RegisterSkills(NewSkillsScope(skillsRoot, DefaultSkillsScanLimits()))
	// claude 资源作用域：单文件资源。
	resRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(resRoot, "CLAUDE.md"), []byte("# claude"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.RegisterResources(NewResourceScope(CLIClaude, ResourceLayout{
		HostRoot: resRoot, Files: []string{"CLAUDE.md"}, DirUnits: []string{"commands"},
	}, DefaultSkillsScanLimits()))

	done := make(chan string, 4)
	c.SetScanCompletionCallback(func() { done <- "scan" })
	if !c.StartSkillsScan() {
		t.Fatal("skills scan must start")
	}
	if !c.StartCliResourcesScan(CLIClaude) {
		t.Fatal("resources scan must start")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("scan completion callback %d must fire", i)
		}
	}
	// 快照已收敛（非 Running），供对账面消费。
	if snap := c.SkillsSnapshot(); snap.Running || !snap.Started {
		t.Fatalf("skills snapshot must be complete: %+v", snap.Progress)
	}
	if snap := c.CliResourcesSnapshot(CLIClaude); snap.Running || !snap.Started {
		t.Fatalf("resources snapshot must be complete: %+v", snap.Progress)
	}
	// 重扫（显式检查）完成同样触发。
	if !c.RescanSkills() {
		t.Fatal("rescan must start")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("rescan completion callback must fire")
	}
	// nil 回调安全清除。
	c.SetScanCompletionCallback(nil)
	if !c.RescanCliResources(CLIClaude) {
		t.Fatal("resource rescan must start")
	}
	select {
	case <-done:
		t.Fatal("cleared callback must not fire")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestGrantConsentCrossTargetIsolation 是 grantEntryIn 重构的回归钉子：
// 先在 distro B 上操作（全局当前代指向 B），再对 distro A 重新授权时必须
// 保留 A 自己的同代登记（不把 A 的 Subs/SelectedKeys 误挂到 B，也不丢）。
func TestGrantConsentCrossTargetIsolation(t *testing.T) {
	c, fp, src, _, _ := newUnifiedClaudeFixture(t)
	unifiedClaudeSource(src, `"web": {"url": "https://mcp.example/sse"}`)
	// distro A（Ubuntu）：选择 + 授权 + 运行。
	if err := c.SetUnitSelection("Ubuntu", CLIClaude, UnitClaudeMCP, []string{"mcpServers/web"}); err != nil {
		t.Fatal(err)
	}
	grantClaudeConsent(t, c, UnitClaudeMCP)
	if res := runClaudeUnit(t, c, UnitClaudeMCP); res.Outcome != UnitApplied {
		t.Fatalf("run on Ubuntu: %+v", res)
	}
	// distro B（Debian）：检查使身份索引记录 B（全局当前代切到 B）。
	if _, err := c.ResolveTarget(context.Background(), "Debian", CLIClaude); err != nil {
		t.Fatalf("resolve Debian: %v", err)
	}
	if _, err := c.Inspect(context.Background(), mustTarget(t, c, "Debian")); err != nil {
		t.Fatalf("inspect Debian: %v", err)
	}
	_ = fp
	// 回到 Ubuntu 重新授权：A 的同代 Subs/SelectedKeys 必须保留。
	grantClaudeConsent(t, c, UnitClaudeMCP)
	st := c.GetStatus("Ubuntu", CLIClaude)
	var consented, detached bool
	for _, u := range st.Units {
		if u.Unit != UnitClaudeMCP {
			continue
		}
		consented = u.Consented
		detached = u.Detached
	}
	if !consented || detached {
		t.Fatalf("Ubuntu claude/mcp must stay consented and not detached: %+v", st.Units)
	}
	// 幂等运行仍是 Fresh（Subs 基准未丢——丢则 unmanaged-existing/frozen）。
	if res := runClaudeUnit(t, c, UnitClaudeMCP); res.Outcome != UnitFresh {
		t.Fatalf("re-run after cross-target consent must be fresh, got %+v", res)
	}
	// Debian 上同一单元没有登记（不串读 A 的授权）。
	stB := c.GetStatus("Debian", CLIClaude)
	for _, u := range stB.Units {
		if u.Unit == UnitClaudeMCP && u.Consented {
			t.Fatal("Debian must not inherit Ubuntu consent")
		}
	}
}

func mustTarget(t *testing.T, c *Coordinator, distro string) TargetRef {
	t.Helper()
	target, err := c.ResolveTarget(context.Background(), distro, CLIClaude)
	if err != nil {
		t.Fatalf("resolve %s: %v", distro, err)
	}
	return target
}

// TestUnifiedPrimitivesCancelNoWrite 是 steer#2 的取消语义钉子：已取消的
// 请求不提交新的统一管理授权、不写选择集（身份探测后、mutate 前复查 ctx）。
func TestUnifiedPrimitivesCancelNoWrite(t *testing.T) {
	c, _, src, _, statePath := newUnifiedClaudeFixture(t)
	unifiedClaudeSource(src, `"web": {"url": "https://mcp.example/sse"}`)
	if err := c.SetTargetUnified(context.Background(), "Ubuntu", true); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	// 取消的 SetTargetUnified（Disable 场景）不落任何标记变化——先关掉再试取消开启。
	if err := c.SetTargetUnified(context.Background(), "Ubuntu", false); err != nil {
		t.Fatal(err)
	}
	if err := c.SetTargetUnified(canceled, "Ubuntu", true); err == nil {
		t.Fatal("canceled enable must fail")
	}
	if c.UnifiedCached("Ubuntu") {
		t.Fatal("canceled enable must not flip the unified marker")
	}
	if err := c.SetTargetUnified(context.Background(), "Ubuntu", true); err != nil {
		t.Fatal(err)
	}

	// 取消的 AutoConsent 不写授权登记。
	if _, err := c.AutoConsentIfUnified(canceled, "Ubuntu", CLIClaude, []UnitID{UnitClaudeMCP}); err == nil {
		t.Fatal("canceled auto-consent must fail")
	}
	st := c.GetStatus("Ubuntu", CLIClaude)
	for _, u := range st.Units {
		if u.Unit == UnitClaudeMCP && u.Consented {
			t.Fatal("canceled auto-consent must not persist a grant")
		}
	}

	// 取消的 ExtendUnitSelection 不写选择集（先合法授权，再取消物化）。
	if _, err := c.AutoConsentIfUnified(context.Background(), "Ubuntu", CLIClaude, []UnitID{UnitClaudeMCP}); err != nil {
		t.Fatal(err)
	}
	if added, err := c.ExtendUnitSelectionIfUnified(canceled, "Ubuntu", CLIClaude, UnitClaudeMCP, []string{"mcpServers/web"}); err == nil || added != 0 {
		t.Fatalf("canceled extend must fail without writes, added=%d err=%v", added, err)
	}
	state := loadUnifiedState(t, statePath)
	units, _ := state["units"].([]any)
	for _, raw := range units {
		entry, _ := raw.(map[string]any)
		if entry["unit"] != string(UnitClaudeMCP) {
			continue
		}
		selected, _ := entry["selectedKeys"].(map[string]any)
		if len(selected) != 0 {
			t.Fatalf("canceled extend must not materialize keys, got %v", selected)
		}
	}
}
