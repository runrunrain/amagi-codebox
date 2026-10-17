package wslsupply

// core_security_regression_test.go — wsl-goal-closure 供给内核安全/身份/状态
// 修复的正式回归（SA-01/02/06/07/15/16 + SA-09 撤回面）。先以反例驱动：
// 每个用例对应 supply-audit.md 的可复现触发条件，修复前红、修复后绿；
// 全部走注入探测面与合成字节，不触达真实 WSL、不读取真实凭据。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// 本文件专用探测面：distro 键控身份 + 模式追踪（chmod 记录 + stat 读回），
// 事件日志记录「chmod 发生时目标路径是否已存在」用于证明泄漏窗口语义。
// ---------------------------------------------------------------------------

type coreProbes struct {
	mu        sync.Mutex
	uncRoot   string
	machines  map[string]string // distro -> machine-id（"" = 无 machine-id）
	uidHome   string            // 所有 distro 共用 uid/home（克隆场景）
	chmodErr  error
	modes     map[string]string // linuxPath -> mode（Chmod 写入，StatMode 读回）
	events    []string          // "chmod <mode> <path>" / "stat <path>"
	identityN int32
}

func newCoreProbes(t *testing.T) *coreProbes {
	t.Helper()
	return &coreProbes{
		uncRoot:  t.TempDir(),
		machines: map[string]string{"Ubuntu": "machine-A"},
		uidHome:  "/home/u",
		modes:    map[string]string{},
	}
}

func (f *coreProbes) identityFor(distro string) platform.WSLUserIdentity {
	machine := f.machines[distro]
	return platform.WSLUserIdentity{UID: "1000", Home: f.uidHome, MachineID: machine, OK: true}
}

func (f *coreProbes) probes() Probes {
	return Probes{
		Identity: func(_ context.Context, distro string) platform.WSLUserIdentity {
			atomic.AddInt32(&f.identityN, 1)
			return f.identityFor(distro)
		},
		ToUNC: func(distro, linuxPath string) string {
			// 不同 distro 是不同文件系统：映射到独立子目录（A→B→A/克隆场景
			// 不得共享目录树）。
			return filepath.Join(f.uncRoot, distro, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(_ context.Context, _, mode string, paths ...string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.events = append(f.events, fmt.Sprintf("chmod %s %s", mode, strings.Join(paths, ",")))
			if f.chmodErr != nil {
				return f.chmodErr
			}
			for _, p := range paths {
				f.modes[p] = mode
			}
			return nil
		},
		StatMode: func(_ context.Context, _, linuxPath string) (string, bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.events = append(f.events, "stat "+linuxPath)
			m, ok := f.modes[linuxPath]
			return m, ok
		},
	}
}

func (f *coreProbes) eventLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *coreProbes) statMode(linuxPath string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.modes[linuxPath]
	return m, ok
}

// newCoreCoord 构造带 stub 投影的协调器（unit pi/core-stub，目标文件
// core-stub.json）。
func newCoreCoord(t *testing.T, fp *coreProbes, source *string, sensitive bool) *Coordinator {
	t.Helper()
	c := NewCoordinator(t.TempDir(), nil, fp.probes())
	c.RegisterProjection(&stubProjection{
		unit: "pi/core-stub", rel: "core-stub.json",
		source:    func() []byte { return []byte(*source) },
		sensitive: sensitive,
	})
	return c
}

func coreRun(t *testing.T, c *Coordinator, cli CLI, unit UnitID) RunResult {
	return coreRunOn(t, c, "Ubuntu", cli, unit)
}

func coreRunOn(t *testing.T, c *Coordinator, distro string, cli CLI, unit UnitID) RunResult {
	t.Helper()
	res, err := c.RunUnits(context.Background(), distro, cli, ReasonManual, []UnitID{unit})
	if err != nil {
		t.Fatalf("RunUnits(%s): %v", unit, err)
	}
	return res
}

func coreTargetFile(fp *coreProbes) string {
	return filepath.Join(fp.uncRoot, "Ubuntu", "home/u/.pi/agent/core-stub.json")
}

// ---------------------------------------------------------------------------
// SA-01：Apply 的身份复核必须比较 generation——计划后 machine-id 换代，
// 旧计划必须整体拒绝，不得写入。
// ---------------------------------------------------------------------------

func TestApplyRejectsPlanAfterGenerationSwap(t *testing.T) {
	fp := newCoreProbes(t)
	src := `{"rev":1}`
	c := newCoreCoord(t, fp, &src, false)
	target, err := c.ResolveTarget(context.Background(), "Ubuntu", CLIPi)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.Inspect(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.Plan(SupplyRequest{Target: target, SelectedUnits: []UnitID{"pi/core-stub"}}, snap)
	if err != nil {
		t.Fatal(err)
	}
	// 计划后同名 distro 重装：uid/home/根路径相同，machine-id 改变。
	fp.machines["Ubuntu"] = "machine-B"
	res, err := c.Apply(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Results {
		if r.Outcome != UnitBlocked || r.Detail != "target-changed-since-planning" {
			t.Fatalf("generation swap must reject the stale plan, got %s/%s", r.Outcome, r.Detail)
		}
	}
	if _, statErr := os.Stat(coreTargetFile(fp)); !os.IsNotExist(statErr) {
		t.Fatal("stale plan must not write the target file")
	}
}

// ---------------------------------------------------------------------------
// SA-02：身份分区清单。
// ---------------------------------------------------------------------------

// 未知 generation（无 machine-id）不得持久化/复用敏感跨界授权。
func TestConsentRefusedForUnknownGeneration(t *testing.T) {
	fp := newCoreProbes(t)
	fp.machines["Ubuntu"] = "" // 最小 distro：拿不到稳定身份
	c := NewCoordinator(t.TempDir(), nil, fp.probes())
	c.RegisterProjection(NewVisionExportAdapter(coreVisionSource(t, "Ubuntu")))
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err == nil {
		t.Fatal("consent must be refused when the distro identity is unverifiable (unknown generation)")
	}
	// 且无同意的运行必须阻断（B 场景：另一台 unknown distro 也拿不到副本）。
	res := coreRunShared(t, c, "Ubuntu", UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "awaiting-consent" {
		t.Fatalf("unknown generation run must stay awaiting-consent, got %s/%s", res.Results[0].Outcome, res.Results[0].Detail)
	}
}

// A→B→A 交替（两台不同 distro）：A 的授权与登记在 B 使用后必须仍然存在
// （分区保存，不覆盖）。
func TestMultiTargetEntriesNotOverwritten(t *testing.T) {
	fp := newCoreProbes(t)
	fp.machines["UbuntuB"] = "machine-B"
	src := `{"rev":1}`
	c := newCoreCoord(t, fp, &src, false)
	if res := coreRun(t, c, CLIPi, "pi/core-stub"); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("A first run: %+v", res.Results[0])
	}
	if res := coreRunOn(t, c, "UbuntuB", CLIPi, "pi/core-stub"); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("B run: %+v", res.Results[0])
	}
	// 回到 A：登记仍在，无变化 → Fresh（而不是 awaiting-consent / 重新写入失败）。
	res := coreRun(t, c, CLIPi, "pi/core-stub")
	if res.Results[0].Outcome != UnitFresh {
		t.Fatalf("return to A must keep its own registration, got %s/%s", res.Results[0].Outcome, res.Results[0].Detail)
	}
	// 状态页：A、B 各自的分区互不串读。
	stB := c.GetStatus("UbuntuB", CLIPi)
	if stB.Generation == c.GetStatus("Ubuntu", CLIPi).Generation {
		t.Fatal("A and B must map to distinct generation partitions")
	}
}

// 克隆 distro（machine-id/uid/home 相同、名字不同）不得继承授权。
func TestClonedDistroDoesNotInheritConsent(t *testing.T) {
	fp := newCoreProbes(t)
	fp.machines["Clone"] = "machine-A" // 与 Ubuntu 同 machine-id/uid/home
	c := NewCoordinator(t.TempDir(), nil, fp.probes())
	c.RegisterProjection(NewVisionExportAdapter(coreVisionSource(t, "Ubuntu")))
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatal(err)
	}
	if res := coreRunShared(t, c, "Ubuntu", UnitSharedVisionExport); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("Ubuntu publish: %+v", res.Results[0])
	}
	// 克隆体从未同意：即便 machine-id 相同也不得拿到密钥副本。
	res := coreRunShared(t, c, "Clone", UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "awaiting-consent" {
		t.Fatalf("clone must not inherit consent, got %s/%s", res.Results[0].Outcome, res.Results[0].Detail)
	}
}

// 旧清单（schema v1 / 空 generation 记录）的授权不得被新目标复用：保守迁移。
func TestLegacyEntriesQuarantined(t *testing.T) {
	fp := newCoreProbes(t)
	dir := t.TempDir()
	// 手工落一份 v1 清单：vision-export 已同意，但 Generation 为空（无法归属身份）。
	legacy := StateFile{
		SchemaVersion: 1,
		Generation:    "",
		Distro:        "Ubuntu",
		Units: []StateEntry{{
			Unit: UnitSharedVisionExport, Policy: PolicyManagedNamespace,
			Generation: "", Consented: true, LastOutcome: "written", LastSuccess: time.Now(),
		}},
	}
	b, _ := json.Marshal(&legacy)
	if err := os.WriteFile(filepath.Join(dir, StateFileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCoordinator(dir, nil, fp.probes())
	c.RegisterProjection(NewVisionExportAdapter(coreVisionSource(t, "Ubuntu")))
	res := coreRunShared(t, c, "Ubuntu", UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "awaiting-consent" {
		t.Fatalf("legacy unattributable consent must not be inherited, got %s/%s", res.Results[0].Outcome, res.Results[0].Detail)
	}
}

// ---------------------------------------------------------------------------
// SA-06/SA-07：敏感提交原语。
// ---------------------------------------------------------------------------

// 敏感 chmod 失败必须发生在 rename 之前：已有混合配置（含用户自有内容）
// 原样保留，绝不整文件删除（ManagedNamespace 语义：目标被外部改写仍允许
// 更新，与审计 CHMOD_FAILURE 反例同构）。
func TestSensitiveChmodFailurePreservesMixedConfig(t *testing.T) {
	fp := newCoreProbes(t)
	dir := filepath.Join(fp.uncRoot, "Ubuntu", "home/u/.pi/agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	mixed := `{"providers":{"local-user":{"models":[]}},"userSetting":true}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(mixed), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCoordinator(t.TempDir(), nil, fp.probes())
	c.RegisterProjection(NewPiManagedModelsAdapter(coreManagedSource{}))
	fp.chmodErr = errors.New("injected chmod failure")
	res := coreRun(t, c, CLIPi, UnitPiManagedModels)
	if res.Results[0].Outcome != UnitFailed {
		t.Fatalf("chmod failure must fail the unit: %+v", res.Results[0])
	}
	got, readErr := os.ReadFile(filepath.Join(dir, "models.json"))
	if readErr != nil || string(got) != mixed {
		t.Fatalf("pre-existing user content must be preserved on chmod failure, got %q err=%v", got, readErr)
	}
}

// coreManagedSource 是托管 models 期望集的合成源（无真实密钥）。
type coreManagedSource struct{}

func (coreManagedSource) DesiredPiManagedModels() PiManagedModelsSpec {
	return PiManagedModelsSpec{
		Providers: map[string]any{"providers": map[string]any{
			"amagi-demo": map[string]any{"apiKey": "SYNTHETIC-NOT-A-KEY", "baseURL": "https://example.invalid/v1", "models": []any{map[string]any{"name": "demo"}}},
		}},
	}
}

// 敏感写入必须先建立并验证 0700 staging：chmod 600 发生在 staging 文件上
// （rename 之前），且 0600 位在目标目录收紧前已经落位。
func TestSensitiveSubmitUsesVerifiedPrivateStaging(t *testing.T) {
	fp := newCoreProbes(t)
	src := `{"secret":1}`
	c := newCoreCoord(t, fp, &src, true)
	if res := coreRun(t, c, CLIPi, "pi/core-stub"); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("publish: %+v", res.Results[0])
	}
	events := fp.eventLog()
	stagingChmod := -1
	fileChmodInStaging := -1
	for i, e := range events {
		if strings.HasPrefix(e, "chmod 700 ") && strings.Contains(e, ".amagi-staging") && stagingChmod < 0 {
			stagingChmod = i
		}
		if strings.HasPrefix(e, "chmod 600 ") && strings.Contains(e, ".amagi-staging") && fileChmodInStaging < 0 {
			fileChmodInStaging = i
		}
	}
	if stagingChmod < 0 {
		t.Fatalf("sensitive submit must chmod 700 the staging dir first, events: %v", events)
	}
	if fileChmodInStaging < 0 {
		t.Fatalf("sensitive submit must chmod 600 the staging file BEFORE rename, events: %v", events)
	}
	if fileChmodInStaging < stagingChmod {
		t.Fatalf("staging dir isolation must precede secret file chmod, events: %v", events)
	}
	if mode, ok := fp.statMode("/home/u/.pi/agent/.amagi-staging"); !ok || mode != "700" {
		t.Fatalf("staging dir must be 700 in distro, got %q ok=%v", mode, ok)
	}
	// rename 保留文件权限位：distro 侧探测（权威验证面）证明目标副本提交后
	// 为 600（verifyPublished 复验收紧）。宿主 POSIX 位仅在能表达它的平台断言
	//（Windows os.Chmod 只翻只读位、os.Stat 恒 0666/0444，无 0600 语义）。
	if mode, ok := fp.statMode("/home/u/.pi/agent/core-stub.json"); !ok || mode != "600" {
		t.Fatalf("final file must be verified private (600) via the distro probe surface, got %q ok=%v", mode, ok)
	}
	if runtime.GOOS != "windows" {
		if info, statErr := os.Stat(coreTargetFile(fp)); statErr != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("final file must carry the pre-rename 0600 mode, got %v err=%v", info.Mode().Perm(), statErr)
		}
	} else if _, statErr := os.Stat(coreTargetFile(fp)); statErr != nil {
		t.Fatalf("final file must exist after commit, err=%v", statErr)
	}
	// staging 目录中不得残留提交后的临时文件。
	if entries, readErr := os.ReadDir(filepath.Join(fp.uncRoot, "Ubuntu/home/u/.pi/agent/.amagi-staging")); readErr != nil || len(entries) != 0 {
		t.Fatalf("staging must be empty after commit, got %v err=%v", entries, readErr)
	}
}

// staging 隔离无法验证（stat 探测缺失/失败）→ 敏感写入必须在任何秘密字节
// 落盘前失败。
func TestSensitiveSubmitFailsWhenStagingUnverifiable(t *testing.T) {
	fp := newCoreProbes(t)
	probes := fp.probes()
	probes.StatMode = func(context.Context, string, string) (string, bool) { return "", false }
	c := NewCoordinator(t.TempDir(), nil, probes)
	src := `{"secret":1}`
	c.RegisterProjection(&stubProjection{unit: "pi/core-stub", rel: "core-stub.json",
		source: func() []byte { return []byte(src) }, sensitive: true})
	res := coreRun(t, c, CLIPi, "pi/core-stub")
	if res.Results[0].Outcome != UnitFailed {
		t.Fatalf("unverifiable staging must fail the sensitive unit: %+v", res.Results[0])
	}
	if _, statErr := os.Stat(coreTargetFile(fp)); !os.IsNotExist(statErr) {
		t.Fatal("no secret bytes may land when staging isolation cannot be verified")
	}
}

// ---------------------------------------------------------------------------
// SA-15：失败必须逐单元登记——状态页不得沿用上次「已同步」。
// ---------------------------------------------------------------------------

func TestFailureRecordedNotStaleSynced(t *testing.T) {
	fp := newCoreProbes(t)
	src := `{"rev":1}`
	c := newCoreCoord(t, fp, &src, false)
	if res := coreRun(t, c, CLIPi, "pi/core-stub"); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("setup: %+v", res.Results[0])
	}
	// 来源变化 + chmod 失败 → 本次失败必须反映到状态。
	newSrc := `{"rev":2}`
	c.RegisterProjection(&stubProjection{unit: "pi/core-stub", rel: "core-stub.json",
		source: func() []byte { return []byte(newSrc) }})
	fp.chmodErr = errors.New("wsl.exe dead")
	defer func() { fp.chmodErr = nil }()
	if res := coreRun(t, c, CLIPi, "pi/core-stub"); res.Results[0].Outcome != UnitFailed {
		t.Fatalf("must fail on chmod error: %+v", res.Results[0])
	}
	st := c.GetStatus("Ubuntu", CLIPi)
	var unit *UnitStatus
	for i := range st.Units {
		if st.Units[i].Unit == "pi/core-stub" {
			unit = &st.Units[i]
		}
	}
	if unit == nil {
		t.Fatal("unit status missing after failure")
	}
	if unit.LastOutcome != "failed" || unit.LastErrorCode == "" {
		t.Fatalf("failure must be recorded, got outcome=%q err=%q", unit.LastOutcome, unit.LastErrorCode)
	}
	if unit.LastSuccess.IsZero() {
		// lastSuccess 保留（上次成功时间），但 LastOutcome 必须区分当前失败。
		t.Fatal("last success time should be preserved across failure recording")
	}
}

// ---------------------------------------------------------------------------
// SA-16：已取消的 ctx 不得继续写配置。
// ---------------------------------------------------------------------------

func TestCanceledContextNeverWrites(t *testing.T) {
	fp := newCoreProbes(t)
	src := `{"rev":1}`
	c := newCoreCoord(t, fp, &src, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := c.RunUnits(ctx, "Ubuntu", CLIPi, ReasonLaunch, []UnitID{"pi/core-stub"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "context-canceled" {
		t.Fatalf("canceled ctx must block before any write, got %s/%s", res.Results[0].Outcome, res.Results[0].Detail)
	}
	if _, statErr := os.Stat(coreTargetFile(fp)); !os.IsNotExist(statErr) {
		t.Fatal("canceled supply request must not create the target file")
	}
}

// ---------------------------------------------------------------------------
// SA-09（适配器/撤回面）：宿主导出删除后的撤回。
// ---------------------------------------------------------------------------

type coreVisionSrc struct {
	mu      sync.Mutex
	bytes   []byte
	missing bool // 宿主文件不存在（撤回形态）
	readErr error
}

func coreVisionSource(t *testing.T, distro string) *coreVisionSrc {
	t.Helper()
	return &coreVisionSrc{bytes: []byte(`{"version":1,"updated_at":"2026-01-01T00:00:00Z","models":[{"id":"m","api_key":"SYNTHETIC-NOT-A-KEY"}]}`)}
}

func (s *coreVisionSrc) VisionExportBytes() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		return nil, s.readErr
	}
	if s.missing {
		return nil, fmt.Errorf("read host vision export: %w", os.ErrNotExist)
	}
	return s.bytes, nil
}
func (s *coreVisionSrc) ModalityKBBytes() ([]byte, error) { return nil, nil }

func coreRunShared(t *testing.T, c *Coordinator, distro string, unit UnitID) RunResult {
	t.Helper()
	res, err := c.RunUnits(context.Background(), distro, CLICommon, ReasonManual, []UnitID{unit})
	if err != nil {
		t.Fatalf("RunUnits: %v", err)
	}
	return res
}

func coreVisionFile(fp *coreProbes) string {
	return filepath.Join(fp.uncRoot, "home/u/agents-root-does-not-apply") // 占位，实际由 target 推导
}

var _ = coreVisionFile

// 宿主导出被删除且目标副本仍等于上次写入 → 撤回（删除目标副本）。
func TestVisionExportWithdrawalRemovesCopy(t *testing.T) {
	fp := newCoreProbes(t)
	src := coreVisionSource(t, "Ubuntu")
	c := NewCoordinator(t.TempDir(), nil, fp.probes())
	c.RegisterProjection(NewVisionExportAdapter(src))
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatal(err)
	}
	if res := coreRunShared(t, c, "Ubuntu", UnitSharedVisionExport); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("publish: %+v", res.Results[0])
	}
	target, err := c.ResolveTarget(context.Background(), "Ubuntu", CLICommon)
	if err != nil {
		t.Fatal(err)
	}
	uncFile := filepath.Join(target.UNCRoot, filepath.FromSlash(VisionExportFile))
	if _, statErr := os.Stat(uncFile); statErr != nil {
		t.Fatal("setup: published copy must exist")
	}
	// 宿主撤回（导出文件删除）→ 下一次运行必须移除 WSL 副本（密钥不再留盘）。
	src.mu.Lock()
	src.missing = true
	src.mu.Unlock()
	res := coreRunShared(t, c, "Ubuntu", UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitApplied || !strings.Contains(res.Results[0].Detail, "withdrawn") {
		t.Fatalf("withdrawal must remove the managed copy, got %s/%s", res.Results[0].Outcome, res.Results[0].Detail)
	}
	if _, statErr := os.Stat(uncFile); !os.IsNotExist(statErr) {
		t.Fatal("withdrawn key copy must be removed from the distro")
	}
}

// 目标被手改 + 宿主撤回 → withdrawal-unconfirmed-frozen（不能静默沿用旧副本）。
func TestVisionExportWithdrawalUnconfirmedFrozen(t *testing.T) {
	fp := newCoreProbes(t)
	src := coreVisionSource(t, "Ubuntu")
	c := NewCoordinator(t.TempDir(), nil, fp.probes())
	c.RegisterProjection(NewVisionExportAdapter(src))
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatal(err)
	}
	if res := coreRunShared(t, c, "Ubuntu", UnitSharedVisionExport); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("publish: %+v", res.Results[0])
	}
	target, err := c.ResolveTarget(context.Background(), "Ubuntu", CLICommon)
	if err != nil {
		t.Fatal(err)
	}
	uncFile := filepath.Join(target.UNCRoot, filepath.FromSlash(VisionExportFile))
	if err := os.WriteFile(uncFile, []byte(`{"version":1,"models":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	src.mu.Lock()
	src.missing = true
	src.mu.Unlock()
	res := coreRunShared(t, c, "Ubuntu", UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "withdrawal-unconfirmed-frozen" {
		t.Fatalf("hand-modified copy under withdrawal must freeze unconfirmed, got %s/%s", res.Results[0].Outcome, res.Results[0].Detail)
	}
	if _, statErr := os.Stat(uncFile); statErr != nil {
		t.Fatal("frozen local edit must be retained")
	}
}

// 瞬时读错误（非删除）不是撤回：保持 source-unavailable，不删副本。
func TestVisionExportTransientSourceErrorKeepsCopy(t *testing.T) {
	fp := newCoreProbes(t)
	src := coreVisionSource(t, "Ubuntu")
	c := NewCoordinator(t.TempDir(), nil, fp.probes())
	c.RegisterProjection(NewVisionExportAdapter(src))
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatal(err)
	}
	if res := coreRunShared(t, c, "Ubuntu", UnitSharedVisionExport); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("publish: %+v", res.Results[0])
	}
	target, _ := c.ResolveTarget(context.Background(), "Ubuntu", CLICommon)
	uncFile := filepath.Join(target.UNCRoot, filepath.FromSlash(VisionExportFile))
	src.mu.Lock()
	src.readErr = errors.New("transient io failure")
	src.mu.Unlock()
	res := coreRunShared(t, c, "Ubuntu", UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "source-unavailable" {
		t.Fatalf("transient read error is not withdrawal, got %s/%s", res.Results[0].Outcome, res.Results[0].Detail)
	}
	if _, statErr := os.Stat(uncFile); statErr != nil {
		t.Fatal("transient source error must keep the published copy")
	}
}

// 钉住 A2 分类映射在修复中不被破坏（回归护栏）。
func TestA2ClassificationStillIntact(t *testing.T) {
	res := classifyA2(context.Background(), UnitPiA2Auth, launcher.SyncWSLPiLoginAuthDistroHasCopy, errors.New("x"))
	if res.Outcome != UnitWarnDegraded {
		t.Fatalf("A2 HasCopy must stay degraded, got %s", res.Outcome)
	}
	res = classifyA2(context.Background(), UnitPiA2Auth, launcher.SyncWSLPiLoginAuthFresh, nil)
	if res.Outcome != UnitFresh {
		t.Fatalf("A2 Fresh must stay fresh, got %s", res.Outcome)
	}
}
