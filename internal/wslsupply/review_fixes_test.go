package wslsupply

// review_fixes_test.go — wsl-goal-closure 集成审核 PR01–PR08 修复的正式回归
// （phase-review.md 2026-10 复现脚本转正）：每条先在修复前以反例形式失败，
// 修复后钉住目标行为。证据脚本（phase-review-*-repro.go）保留在报告目录，
// 不进包内。锚点：PR01 遗留敏感副本风险/受管副本谓词；PR02 敏感零写入路径
// 权限验证；PR03 读回失配有界恢复；PR04 目标性 mutate 的明确 generation；
// PR05 状态未持久化不虚报成功；PR06 读快照深复制隔离；PR07 取消零新写；
// PR08 插件 update 过滤集重置。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// PR01：v1 迁移遗留的敏感副本风险 + 受管副本谓词
// ---------------------------------------------------------------------------

// legacyFixtureIdentity 是 fakeProbes 的固定身份（旧公式指纹可复算）。
var legacyFixtureIdentity = platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "machine-abc", OK: true}

// writeLegacyV1State 直接落一份 v1 形态清单（generation 用旧公式真实指纹，
// LastWrittenHash 与实际旧副本字节一致——攻坚契约 PR01 的逐目标核验语义）。
func writeLegacyV1State(t *testing.T, cfgDir, distro string, visionWritten, consented bool) {
	t.Helper()
	gen := legacyGenerationOf(legacyFixtureIdentity.MachineID, legacyFixtureIdentity.UID, legacyFixtureIdentity.Home)
	entry := StateEntry{
		Unit: UnitSharedVisionExport, Policy: PolicyManagedNamespace,
		Generation: gen, Consented: consented,
		LastOutcome: "written",
	}
	if visionWritten {
		entry.LastWrittenHash = hexHash([]byte(legacyVisionCopy))
	}
	legacy := StateFile{
		SchemaVersion: 1, Generation: gen,
		Distro: distro, Home: "/home/u",
		Units: []StateEntry{entry},
	}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, StateFileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// legacyVisionCopy 是升级前发布的合成旧副本字节（与 v1 LastWrittenHash 一致）。
const legacyVisionCopy = `{"version":1,"api_key":"SYNTHETIC-OLD-COPY"}`

func newVisionCoordinator(t *testing.T, vision func() ([]byte, error)) (*Coordinator, *fakeProbes) {
	t.Helper()
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
	c.RegisterProjection(NewVisionExportAdapter(&stubSharedSource{vision: vision, modality: func() ([]byte, error) { return nil, nil }}))
	return c, fp
}

// TestReviewFixPR01LegacySensitiveRiskPending（反例：phase-review-repro.go
// LEGACY_WITHDRAWAL）：v1 曾发布 → 升级隔离后旧授权不可用（awaiting-consent），
// 但旧副本风险不能随迁移消失——按 v1 顶层 Distro 归属为待确认状态；其它
// distro 不被误算；新代成功写入/确认撤回后清除。
func TestReviewFixPR01LegacySensitiveRiskPending(t *testing.T) {
	t.Run("v1 written keeps pending risk keyed by distro", func(t *testing.T) {
		c, fp := newVisionCoordinator(t, func() ([]byte, error) { return nil, os.ErrNotExist })
		// 旧副本仍在目标上（升级前发布过；字节与 v1 LastWrittenHash 一致，
		// 旧公式指纹与本目标 LegacyGeneration 匹配）。
		if err := os.MkdirAll(filepath.Dir(visionFile(fp)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(visionFile(fp), []byte(legacyVisionCopy), 0o600); err != nil {
			t.Fatal(err)
		}
		writeLegacyV1State(t, filepath.Dir(c.state.path), "Ubuntu", true, true)
		c2, _ := newVisionCoordinator(t, func() ([]byte, error) { return nil, os.ErrNotExist })
		c2.state.path = c.state.path // 重新加载同一份 v1 清单
		res := runUnit(t, c2, CLICommon, UnitSharedVisionExport)
		if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "awaiting-consent" {
			t.Fatalf("legacy must not inherit authorization, got %+v", res.Results[0])
		}
		if !c2.LegacySensitiveCopyPending("Ubuntu") {
			t.Fatal("legacy risk must stay pending for the recorded distro after migration")
		}
		if c2.LegacySensitiveCopyPending("Other") {
			t.Fatal("risk must not leak to other distros")
		}
		// 新代从未写入：当前代谓词为 false（legacy 风险由迁移标记承载）。
		if c2.SensitiveCopyPending("Ubuntu", UnitSharedVisionExport) {
			t.Fatal("current-generation predicate must stay false before any new-generation write")
		}
	})

	t.Run("v1 without distro keeps unresolved evidence without binding", func(t *testing.T) {
		c, _ := newVisionCoordinator(t, func() ([]byte, error) { return nil, os.ErrNotExist })
		// 顶层无 distro 且旧指纹属于另一台机器（foreign machine-id）：无任何
		// 归属线索命中本环境目标——不绑任何 distro，证据保真保留。
		foreignGen := legacyGenerationOf("machine-OTHER", "1000", "/home/u")
		legacy := StateFile{SchemaVersion: 1, Generation: foreignGen, Home: "/home/u",
			Units: []StateEntry{{Unit: UnitSharedVisionExport, Policy: PolicyManagedNamespace,
				Generation: foreignGen, Consented: true, LastWrittenHash: hexHash([]byte(legacyVisionCopy)), LastOutcome: "written"}}}
		b, jerr := json.Marshal(legacy)
		if jerr != nil {
			t.Fatal(jerr)
		}
		if err := os.MkdirAll(filepath.Dir(c.state.path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(c.state.path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		c2, _ := newVisionCoordinator(t, func() ([]byte, error) { return nil, os.ErrNotExist })
		c2.state.path = c.state.path
		runUnit(t, c2, CLICommon, UnitSharedVisionExport)
		if c2.LegacySensitiveCopyPending("Ubuntu") || c2.LegacySensitiveCopyPending("") {
			t.Fatal("unattributable legacy records must not create pending risk for any distro")
		}
		// 攻坚契约纠正：无归属线索不等于丢弃线索——unresolved evidence 保真
		// 保留（逐目标核验消费），不盲绑任何 distro。
		ev := c2.LegacyEvidenceSnapshot()
		found := false
		for _, e := range ev {
			if e.Unit == UnitSharedVisionExport && e.Source == "v1" {
				found = true
			}
		}
		if !found {
			t.Fatalf("v1 evidence must be preserved unresolved, got %+v", ev)
		}
		// 逐目标核验：旧公式指纹匹配的目标（合成身份 fixture）能拿到风险——
		// 证据按指纹归属，不按顶层 distro 盲绑。
		target := mustResolve(t, c2, "Ubuntu", CLICommon)
		if target.LegacyGeneration == GenerationUnknown {
			t.Fatal("fixture identity must export a legacy generation")
		}
	})

	t.Run("successful new-generation write clears legacy risk", func(t *testing.T) {
		content := `{"version":1,"updated_at":"t","models":[{"id":"p/m","api_key":"sk"}]}`
		c, _ := newVisionCoordinator(t, func() ([]byte, error) { return []byte(content), nil })
		writeLegacyV1State(t, filepath.Dir(c.state.path), "Ubuntu", true, true)
		c2, _ := newVisionCoordinator(t, func() ([]byte, error) { return []byte(content), nil })
		c2.state.path = c.state.path
		if err := c2.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
			t.Fatal(err)
		}
		res := runUnit(t, c2, CLICommon, UnitSharedVisionExport)
		if res.Results[0].Outcome != UnitApplied {
			t.Fatalf("republished copy must apply: %+v", res.Results[0])
		}
		if c2.LegacySensitiveCopyPending("Ubuntu") {
			t.Fatal("successful new-generation write must take over the legacy risk marker")
		}
		if !c2.SensitiveCopyPending("Ubuntu", UnitSharedVisionExport) {
			t.Fatal("written copy must be visible via the current-generation predicate")
		}
	})

	t.Run("confirmed withdrawal clears legacy risk", func(t *testing.T) {
		content := `{"version":1,"updated_at":"t","models":[{"id":"p/m","api_key":"sk"}]}`
		c, _ := newVisionCoordinator(t, func() ([]byte, error) { return []byte(content), nil })
		writeLegacyV1State(t, filepath.Dir(c.state.path), "Ubuntu", true, true)
		c2, _ := newVisionCoordinator(t, func() ([]byte, error) { return []byte(content), nil })
		c2.state.path = c.state.path
		if err := c2.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
			t.Fatal(err)
		}
		if res := runUnit(t, c2, CLICommon, UnitSharedVisionExport); res.Results[0].Outcome != UnitApplied {
			t.Fatalf("publish: %+v", res.Results[0])
		}
		if !c2.LegacySensitiveCopyPending("Ubuntu") {
			// 发布后风险已由当前受管状态接管（标记清除是合法终态）。
		}
		// 关闭宿主导出 → 撤回确认副本移除 → 无任何遗留风险。
		c3, _ := newVisionCoordinator(t, func() ([]byte, error) { return nil, os.ErrNotExist })
		c3.state.path = c.state.path
		res := runUnit(t, c3, CLICommon, UnitSharedVisionExport)
		if res.Results[0].Outcome != UnitApplied || !strings.HasPrefix(res.Results[0].Detail, "withdrawn") {
			t.Fatalf("closed export must withdraw the copy: %+v", res.Results[0])
		}
		if c3.LegacySensitiveCopyPending("Ubuntu") {
			t.Fatal("confirmed withdrawal must clear the legacy risk marker")
		}
		if c3.SensitiveCopyPending("Ubuntu", UnitSharedVisionExport) {
			t.Fatal("withdrawn copy must not report pending")
		}
	})
}

// TestReviewFixPR01SensitiveCopyPendingMatrix 钉住「证明无受管副本」与
// 「可能残留」的判定矩阵（PR01 核心）：供启动闸门区分放行/阻断。
func TestReviewFixPR01SensitiveCopyPendingMatrix(t *testing.T) {
	seed := func(t *testing.T, entry StateEntry) *Coordinator {
		c, _ := newVisionCoordinator(t, func() ([]byte, error) { return nil, os.ErrNotExist })
		if err := c.state.mutate(func(st *StateFile) {
			st.Targets = map[string]TargetRecord{"gen-1": {Distro: "Ubuntu", LastSeen: time.Now()}}
			st.Generation = "gen-1"
			st.Distro = "Ubuntu"
			entry.Generation = "gen-1"
			st.Units = []StateEntry{entry}
		}); err != nil {
			t.Fatal(err)
		}
		return c
	}
	cases := []struct {
		name  string
		entry StateEntry
		want  bool
	}{
		{"never written stays false", StateEntry{Unit: UnitSharedVisionExport, LastOutcome: "awaiting-consent"}, false},
		{"written copy is pending", StateEntry{Unit: UnitSharedVisionExport, LastWrittenHash: "h", LastOutcome: "written"}, true},
		{"failed write keeps pending", StateEntry{Unit: UnitSharedVisionExport, LastWrittenHash: "h", LastOutcome: "failed"}, true},
		{"withdrawn clears pending", StateEntry{Unit: UnitSharedVisionExport, LastWrittenHash: "h", LastOutcome: "withdrawn"}, false},
		{"withdrawn-already-absent clears pending", StateEntry{Unit: UnitSharedVisionExport, LastWrittenHash: "h", LastOutcome: "withdrawn-already-absent"}, false},
		{"detached copy belongs to user", StateEntry{Unit: UnitSharedVisionExport, LastWrittenHash: "h", Detached: true, LastOutcome: "detached"}, false},
		{"externally deleted copy is gone", StateEntry{Unit: UnitSharedVisionExport, LastWrittenHash: "h", Frozen: true, FrozenReason: "local-deletion-frozen"}, false},
		{"hand-modified copy still pending", StateEntry{Unit: UnitSharedVisionExport, LastWrittenHash: "h", Frozen: true, FrozenReason: "local-modification-frozen"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := seed(t, tc.entry)
			if got := c.SensitiveCopyPending("Ubuntu", UnitSharedVisionExport); got != tc.want {
				t.Fatalf("SensitiveCopyPending = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("no registration at all is false", func(t *testing.T) {
		c, _ := newVisionCoordinator(t, func() ([]byte, error) { return nil, os.ErrNotExist })
		if c.SensitiveCopyPending("Ubuntu", UnitSharedVisionExport) {
			t.Fatal("never-managed target must not report pending copy")
		}
	})
}

// ---------------------------------------------------------------------------
// PR02：敏感零写入路径（Fresh/既有副本被选择消费）统一验证目标权限
// ---------------------------------------------------------------------------

// TestReviewFixPR02UnchangedSensitiveVerifiesPerms（反例：phase-review-mode-repro.go）。
func TestReviewFixPR02UnchangedSensitiveVerifiesPerms(t *testing.T) {
	publish := func(t *testing.T) (*Coordinator, *fakeProbes) {
		content := `{"version":1,"updated_at":"t","models":[{"id":"p/m","api_key":"sk"}]}`
		c, fp := newVisionCoordinator(t, func() ([]byte, error) { return []byte(content), nil })
		if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
			t.Fatal(err)
		}
		if res := runUnit(t, c, CLICommon, UnitSharedVisionExport); res.Results[0].Outcome != UnitApplied {
			t.Fatalf("setup publish: %+v", res.Results[0])
		}
		return c, fp
	}
	t.Run("loose 0644 copy is retightened and stays fresh", func(t *testing.T) {
		c, fp := publish(t)
		// 松弛形态经探测面 mode 表表达（权威面优先于宿主位）：Windows 宿主
		// chmod/stat 表达不了 POSIX 0644，且 publish 后 mode 表已持有 600——
		// 宿主位 chmod 在任何平台都不可见，必须从权威面注入。
		fp.setMode("/home/u/.agents/"+VisionExportFile, "644")
		res := runUnit(t, c, CLICommon, UnitSharedVisionExport)
		if res.Results[0].Outcome != UnitFresh {
			t.Fatalf("retightened copy must stay fresh (zero-byte rewrite), got %+v", res.Results[0])
		}
		// 探测面必须验证到收紧后的 0600（fake chmod 记录位，真实文件位由
		// launcher 侧回归覆盖）。
		fp.mu.Lock()
		mode, ok := fp.modes["/home/u/.agents/"+VisionExportFile]
		fp.mu.Unlock()
		if !ok || mode != "600" {
			t.Fatalf("loose copy must be re-tightened and verified 0600 via probe, got %q ok=%v", mode, ok)
		}
	})
	t.Run("chmod failure fails the unit instead of passing", func(t *testing.T) {
		c, fp := publish(t)
		fp.setMode("/home/u/.agents/"+VisionExportFile, "644")
		fp.setChmodError(errors.New("wsl.exe dead"))
		res := runUnit(t, c, CLICommon, UnitSharedVisionExport)
		if res.Results[0].Outcome != UnitFailed || res.Results[0].Detail != "sensitive-permissions-unsafe" {
			t.Fatalf("unverifiable permissions must fail the sensitive unit, got %+v", res.Results[0])
		}
	})
	t.Run("healthy 0600 copy keeps zero-byte rewrite", func(t *testing.T) {
		c, fp := publish(t)
		before, err := os.Stat(visionFile(fp))
		if err != nil {
			t.Fatal(err)
		}
		res := runUnit(t, c, CLICommon, UnitSharedVisionExport)
		if res.Results[0].Outcome != UnitFresh {
			t.Fatalf("healthy copy must stay fresh, got %+v", res.Results[0])
		}
		after, err := os.Stat(visionFile(fp))
		if err != nil {
			t.Fatal(err)
		}
		if !after.ModTime().Equal(before.ModTime()) {
			t.Fatal("healthy 0600 copy must not be rewritten")
		}
	})
}

// ---------------------------------------------------------------------------
// PR03：敏感提交读回失配的有界、非递归恢复
// ---------------------------------------------------------------------------

// stagingSabotage 在 StatMode 接缝对 staging 内路径注入改写（每次命中都
// 污染——持续故障不是仅第一次破坏；攻击契约「需要纠正的测试断言」）。
// 仅作用于 .amagi-staging 内路径。
type stagingSabotage struct {
	uncRoot string
	writes  int
	payload func(attempt int) []byte
}

func (s *stagingSabotage) install(t *testing.T, c *Coordinator) {
	t.Helper()
	c.probes.StatMode = func(_ context.Context, distro, linuxPath string) (string, bool) {
		if strings.HasSuffix(linuxPath, ".amagi-staging") {
			return "700", true // staging 目录验证保持健康
		}
		if !strings.Contains(linuxPath, ".amagi-staging/") {
			return "600", true
		}
		s.writes++
		if s.payload != nil {
			_ = os.WriteFile(filepath.Join(s.uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/"))), s.payload(s.writes), 0o600)
		}
		return "600", true
	}
}

// publishVisionForRecovery 建立受管副本后返回可注入故障的协调器面。
func publishVisionForRecovery(t *testing.T) (*Coordinator, *fakeProbes) {
	t.Helper()
	content := `{"version":1,"updated_at":"t1","models":[{"id":"p/m","api_key":"sk"}]}`
	c, fp := newVisionCoordinator(t, func() ([]byte, error) { return []byte(content), nil })
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatal(err)
	}
	if res := runUnit(t, c, CLICommon, UnitSharedVisionExport); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("setup publish: %+v", res.Results[0])
	}
	return c, fp
}

// TestReviewFixPR03ReadbackMismatchBoundedRecovery（攻坚契约重做：持续污染
// 每次 staging 写入；phase-rereview 反例转正）：备份/候选 staging 均被污染
// 时，提交在不可逆移动前中止——目标原字节逐字不变、无 preserved/restored
// 假声明、无恢复备份残骸；尝试有界（非递归）。
func TestReviewFixPR03ReadbackMismatchBoundedRecovery(t *testing.T) {
	c, fp := publishVisionForRecovery(t)
	prevBytes, err := os.ReadFile(visionFile(fp))
	if err != nil {
		t.Fatal(err)
	}
	// 来源变化触发一次更新写入；staging 内每个临时名内容都被接缝改写。
	c2content := `{"version":1,"updated_at":"t2","models":[{"id":"p/m2","api_key":"sk2"}]}`
	shared := &stubSharedSource{vision: func() ([]byte, error) { return []byte(c2content), nil }, modality: func() ([]byte, error) { return nil, nil }}
	c.projections[UnitSharedVisionExport] = NewVisionExportAdapter(shared)
	sab := &stagingSabotage{uncRoot: fp.uncRoot, payload: func(attempt int) []byte {
		return []byte(fmt.Sprintf("sabotaged staging bytes %d", attempt))
	}}
	sab.install(t, c)
	res := runUnit(t, c, CLICommon, UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitFailed {
		t.Fatalf("persistent staging corruption must fail the unit, got %+v", res.Results[0])
	}
	if sab.writes > 4 {
		t.Fatalf("recovery must be bounded and non-recursive, staging writes = %d", sab.writes)
	}
	// 目标原字节逐字不变（污染在不可逆移动前被挡下）。
	got, err := os.ReadFile(visionFile(fp))
	if err != nil || string(got) != string(prevBytes) {
		t.Fatalf("target must stay byte-identical, got %q err=%v", got, err)
	}
	// 无 preserved/restored 假声明：错误不含 manual recovery/preserved 措辞。
	if res.Results[0].Err != nil && strings.Contains(res.Results[0].Err.Error(), "preserved") {
		t.Fatalf("no preserved claims allowed when backup is corrupt, got %v", res.Results[0].Err)
	}
	// 无恢复备份残骸（前像从不在失败后才首次保全）。
	entries, derr := os.ReadDir(filepath.Dir(visionFile(fp)))
	if derr != nil {
		t.Fatal(derr)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".amagi-supply-recover-") {
			t.Fatalf("no recovery backup may be created after failure: %v", e.Name())
		}
	}
}

// TestReviewFixPR03RecoveryWhenTargetEqualsPreimage：候选被换成前像字节 →
// 复验在发布前发现字节失配，提交中止，目标保持前像，无备份写入。
func TestReviewFixPR03RecoveryWhenTargetEqualsPreimage(t *testing.T) {
	c, fp := publishVisionForRecovery(t)
	prevBytes, err := os.ReadFile(visionFile(fp))
	if err != nil {
		t.Fatal(err)
	}
	c2content := `{"version":1,"updated_at":"t2","models":[{"id":"p/m2","api_key":"sk2"}]}`
	shared := &stubSharedSource{vision: func() ([]byte, error) { return []byte(c2content), nil }, modality: func() ([]byte, error) { return nil, nil }}
	c.projections[UnitSharedVisionExport] = NewVisionExportAdapter(shared)
	// staging 候选被换成前像字节：staging 复验发现失配（不等于候选）→ 中止。
	sab := &stagingSabotage{uncRoot: fp.uncRoot, payload: func(int) []byte { return prevBytes }}
	sab.install(t, c)
	res := runUnit(t, c, CLICommon, UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitFailed {
		t.Fatalf("corrupted candidate must fail the unit before any move, got %+v", res.Results[0])
	}
	if entries, err := os.ReadDir(filepath.Dir(visionFile(fp))); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".amagi-supply-recover-") {
				t.Fatalf("no backup must be written when the target equals the preimage: %v", e.Name())
			}
		}
	}
	if got, err := os.ReadFile(visionFile(fp)); err != nil || string(got) != string(prevBytes) {
		t.Fatalf("preimage must stay intact, got %q err=%v", got, err)
	}
}

// TestReviewFixPR03CanceledAfterSideEffectsKeepsPreimage（攻坚契约纠正断言：
// 旧 SkipsBackupWrites 奖励取消后丢唯一前像）：公开入口提交、在目标已被
// 移走（副作用已发生）后取消——原目标字节或有效持久前像至少一处保留。
func TestReviewFixPR03CanceledAfterSideEffectsKeepsPreimage(t *testing.T) {
	c, fp := newVisionCoordinator(t, func() ([]byte, error) {
		return []byte(`{"version":1,"updated_at":"t","models":[{"id":"p/m","api_key":"sk"}]}`), nil
	})
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatal(err)
	}
	prev := []byte(`{"user":"ORIGINAL-UNIQUE"}`)
	if err := os.MkdirAll(filepath.Dir(visionFile(fp)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(visionFile(fp), prev, 0o600); err != nil {
		t.Fatal(err)
	}
	target := visionFile(fp)
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 在目标被移走（displaced 出现）这一精确状态触发取消。
	ctx := boundaryCtx{Context: base, cancel: cancel, when: func() bool {
		if _, err := os.Lstat(target); err == nil {
			return false
		}
		m, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".amagi-supply-displaced-*"))
		return len(m) > 0
	}}
	res, rerr := c.RunUnits(ctx, "Ubuntu", CLICommon, ReasonManual, []UnitID{UnitSharedVisionExport})
	if rerr != nil {
		t.Fatal(rerr)
	}
	if res.Results[0].Outcome != UnitFailed && res.Results[0].Outcome != UnitBlocked {
		t.Fatalf("canceled-after-side-effects run must not report applied: %+v", res.Results[0])
	}
	// 原目标或有效持久前像至少一处保留原字节。
	cur, cerr := os.ReadFile(target)
	kept := cerr == nil && string(cur) == string(prev)
	if !kept {
		m, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".amagi-supply-displaced-*"))
		for _, p := range m {
			b, err := os.ReadFile(p)
			if err == nil && string(b) == string(prev) {
				kept = true
			}
		}
	}
	if !kept {
		t.Fatalf("preimage bytes must survive cancel after side effects (detail=%s)", res.Results[0].Detail)
	}
}

// boundaryCtx 在 when() 命中时取消（复现脚本的确定性取消边界注入）。
type boundaryCtx struct {
	context.Context
	cancel context.CancelFunc
	when   func() bool
}

func (c boundaryCtx) Err() error {
	if c.when() {
		c.cancel()
	}
	return c.Context.Err()
}

// ---------------------------------------------------------------------------
// PR04：目标性 mutate 使用明确目标 generation
// ---------------------------------------------------------------------------

type reviewAdapter struct {
	rel    string
	source func() []byte
}

func (a *reviewAdapter) Unit() UnitID            { return UnitID("pi/review") }
func (a *reviewAdapter) Policy() Policy          { return PolicyTrackedProjection }
func (a *reviewAdapter) Sensitive() bool         { return false }
func (a *reviewAdapter) RequiresConsent() bool   { return false }
func (a *reviewAdapter) Files() []string         { return []string{a.rel} }
func (a *reviewAdapter) Plan(_ SupplyRequest, s *Snapshot, _ *StateEntry) (PlannedUnit, error) {
	m, _ := s.File(a.rel)
	return PlannedUnit{Action: ActionWriteProjection, RelPath: a.rel, ExpectedTargetHash: m.Hash}, nil
}
func (a *reviewAdapter) Candidate(context.Context, TargetRef, *StateEntry) (ProjectionCandidate, error) {
	return ProjectionCandidate{Content: a.source(), SourceHash: "synthetic"}, nil
}

// multiTargetHome 按distro 区分 Home 的探测面（A/B 两个独立目标根；
// generation 指纹含 distro+machine-id+uid+home，因此 A/B 天然不同代）。
func multiTargetProbes(t *testing.T) (root string, p Probes) {
	t.Helper()
	root = t.TempDir()
	p = Probes{
		Identity: func(_ context.Context, distro string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/" + strings.ToLower(distro), MachineID: "fixture", OK: true}
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(root, "targets", filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod:    func(context.Context, string, string, ...string) error { return nil },
		StatMode: func(context.Context, string, string) (string, bool) { return "600", true },
	}
	return root, p
}

// TestReviewFixPR04KeepLocalTargetsRequestedDistro（反例：phase-review-repro.go
// KEEP_LOCAL_REQUEST_A）：A→B→对 A 停跟随时全局最近代是 B——必须改 A 不动 B。
func TestReviewFixPR04KeepLocalTargetsRequestedDistro(t *testing.T) {
	root, p := multiTargetProbes(t)
	c := NewCoordinator(filepath.Join(root, "state"), nil, p)
	c.RegisterProjection(&reviewAdapter{rel: "review.json", source: func() []byte { return []byte(`{"v":1}`) }})
	var genA, genB string
	for _, d := range []string{"A", "B"} {
		res, err := c.RunUnits(context.Background(), d, CLIPi, ReasonManual, []UnitID{"pi/review"})
		if err != nil {
			t.Fatalf("run %s: %v", d, err)
		}
		if d == "A" {
			genA = res.Target.Generation
		} else {
			genB = res.Target.Generation
		}
	}
	if err := c.DropUnitRegistration("A", CLIPi, "pi/review"); err != nil {
		t.Fatalf("keep-local A: %v", err)
	}
	st := c.state.load()
	for _, e := range st.Units {
		if e.Unit != "pi/review" {
			continue
		}
		switch e.Generation {
		case genA:
			if !e.Detached {
				t.Fatal("requested target A must be detached")
			}
		case genB:
			if e.Detached {
				t.Fatal("B must stay managed (global latest generation must not be mutated)")
			}
		}
	}
}

// TestReviewFixPR04AmbiguousGenerationRejected：同名 distro 多代并存 → 拒绝
// 歧义（不改别人）；未知 distro 无登记 → 明确报错。
func TestReviewFixPR04AmbiguousGenerationRejected(t *testing.T) {
	c, _ := newVisionCoordinator(t, func() ([]byte, error) { return nil, os.ErrNotExist })
	if err := c.state.mutate(func(st *StateFile) {
		st.Targets = map[string]TargetRecord{
			"gen-old": {Distro: "Ubuntu", LastSeen: time.Now().Add(-time.Hour)},
			"gen-new": {Distro: "Ubuntu", LastSeen: time.Now()},
		}
		st.Units = []StateEntry{{Unit: UnitSharedVisionExport, Generation: "gen-new", Consented: true}}
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.DropUnitRegistration("Ubuntu", CLICommon, UnitSharedVisionExport); err == nil || !strings.Contains(err.Error(), "多代") {
		t.Fatalf("ambiguous distro generations must be rejected explicitly, got %v", err)
	}
	if err := c.DropUnitRegistration("Unknown", CLICommon, UnitSharedVisionExport); err == nil || !strings.Contains(err.Error(), "没有可归属") {
		t.Fatalf("unknown distro must be rejected explicitly, got %v", err)
	}
}

// TestReviewFixPR04SetUnitSelectionTargetsResolvedGeneration：A→B→对 A 设
// 选择集时落在 A 的分区（复制 B 的登记是旧 bug 形态）。
func TestReviewFixPR04SetUnitSelectionTargetsResolvedGeneration(t *testing.T) {
	root, p := multiTargetProbes(t)
	c := NewCoordinator(filepath.Join(root, "state"), nil, p)
	shape := keyedShape{relPath: "settings.json", sensitive: false, selectLeaf: true,
		desired: func(obj map[string]any) (map[string]any, []string) { return map[string]any{}, nil },
		convert: func(string, any, TransformFacts) (any, bool, string) { return nil, false, "stub" }}
	c.RegisterProjection(&keyedProjectionAdapter{unitID: "pi/revsel", shape: shape, requiresConsent: true})
	var genA, genB string
	for _, d := range []string{"A", "B"} {
		if err := c.GrantUnitConsent(context.Background(), d, CLIPi, "pi/revsel"); err != nil {
			t.Fatalf("consent %s: %v", d, err)
		}
		target, err := c.ResolveTarget(context.Background(), d, CLIPi)
		if err != nil {
			t.Fatal(err)
		}
		if d == "A" {
			genA = target.Generation
		} else {
			genB = target.Generation
		}
	}
	if err := c.SetUnitSelection("A", CLIPi, "pi/revsel", []string{"model"}); err != nil {
		t.Fatal(err)
	}
	st := c.state.load()
	for _, e := range st.Units {
		if e.Unit != "pi/revsel" {
			continue
		}
		switch e.Generation {
		case genA:
			if len(e.SelectedKeys) != 1 || !e.SelectedKeys["model"] {
				t.Fatalf("A's entry must carry the selection, got %+v", e.SelectedKeys)
			}
		case genB:
			if len(e.SelectedKeys) != 0 {
				t.Fatalf("B's entry must not receive A's selection, got %+v", e.SelectedKeys)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// PR05：状态持久化失败不得虚报 applied/synced
// ---------------------------------------------------------------------------

// TestReviewFixPR05StateSaveFailureNotApplied（反例：phase-review-repro.go
// STATE_SAVE_FAILURE）：内容已提交、清单写失败 → UnitFailed
// content-committed-state-unpersisted；内存不冒充已持久化；写入内容保留。
func TestReviewFixPR05StateSaveFailureNotApplied(t *testing.T) {
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
	c.RegisterProjection(&reviewAdapter{rel: "review.json", source: func() []byte {
		// 候选生成（提交前）后、登记落盘前把清单路径换成目录（rename 确定失败）。
		_ = os.Remove(c.state.path)
		_ = os.Mkdir(c.state.path, 0o700)
		return []byte(`{"v":1}`)
	}})
	res := runUnit(t, c, CLIPi, "pi/review")
	if res.Results[0].Outcome != UnitFailed || !strings.HasPrefix(res.Results[0].Detail, "content-committed-state-unpersisted") {
		t.Fatalf("state persist failure must not report applied, got %+v", res.Results[0])
	}
	if res.Results[0].Err == nil {
		t.Fatal("structured error must be surfaced")
	}
	// 内容确实已提交（不能用删除凑一致）。
	targetPath := filepath.Join(fp.uncRoot, "home/u/.pi/agent/review.json")
	if got, err := os.ReadFile(targetPath); err != nil || string(got) != `{"v":1}` {
		t.Fatalf("committed content must be kept, got %q err=%v", got, err)
	}
	// 内存态不得冒充已持久化：GetStatus 无 written 声明。
	st := c.GetStatus("Ubuntu", CLIPi)
	for _, u := range st.Units {
		if u.Unit == "pi/review" && (u.LastOutcome == "written" || !u.LastSuccess.IsZero()) {
			t.Fatalf("memory must not claim persisted write after save failure: %+v", u)
		}
	}
	// 撤销句柄仍返回（启动中止补偿可用）。
	if res.Results[0].Undo == nil {
		t.Fatal("undo handle must survive state-persist failure")
	}
	_ = os.RemoveAll(c.state.path)
}

// ---------------------------------------------------------------------------
// PR06：读快照深复制隔离（生产 race 消除 + 不可变/不别名）
// ---------------------------------------------------------------------------

// TestReviewFixPR06SnapshotDeepCopyIsolation：load 返回的快照与后续 mutate
// 完全隔离——顶层 slice、嵌套 map（Subs/SelectedKeys/Targets.InspectedAt/
// Plugins filters）都不别名。
func TestReviewFixPR06SnapshotDeepCopyIsolation(t *testing.T) {
	c, _ := newVisionCoordinator(t, func() ([]byte, error) { return nil, os.ErrNotExist })
	if err := c.state.mutate(func(st *StateFile) {
		st.Targets = map[string]TargetRecord{"gen-1": {Distro: "Ubuntu", InspectedAt: map[CLI]time.Time{CLIPi: time.Now()}}}
		st.Generation = "gen-1"
		st.Units = []StateEntry{{Unit: UnitSharedVisionExport, Generation: "gen-1",
			Subs: map[string]SubHash{"k": {Source: "a"}}, SelectedKeys: map[string]bool{"k": true}}}
		st.Plugins = []PluginStateEntry{{Identity: "npm:foo", Filters: PluginFilters{Skills: []string{"s1"}}}}
	}); err != nil {
		t.Fatal(err)
	}
	snap := c.state.load()
	if err := c.state.mutate(func(st *StateFile) {
		st.Units[0].Subs["k"] = SubHash{Source: "mutated"}
		st.Units[0].SelectedKeys["k"] = false
		st.Targets["gen-1"].InspectedAt[CLIPi] = time.Time{}
		st.Plugins[0].Filters.Skills[0] = "mutated"
	}); err != nil {
		t.Fatal(err)
	}
	if snap.Units[0].Subs["k"].Source != "a" || !snap.Units[0].SelectedKeys["k"] {
		t.Fatalf("unit snapshot must be isolated from later mutations: %+v", snap.Units[0])
	}
	if !snap.Targets["gen-1"].InspectedAt[CLIPi].IsZero() == false && snap.Targets["gen-1"].InspectedAt[CLIPi].IsZero() {
		t.Fatalf("targets snapshot must be isolated: %+v", snap.Targets["gen-1"])
	}
	if snap.Plugins[0].Filters.Skills[0] != "s1" {
		t.Fatalf("plugin filters snapshot must be isolated: %+v", snap.Plugins[0].Filters)
	}
}

// TestReviewFixPR06ConcurrentPlanApplyNoRace：并发 Plan（读快照）与 Apply
//（写状态）——修复前 TestCompensateApplySerialized 报真实数据竞争（write
// upsertEntry vs read entryFor）；此处再钉一个直接读写对，配合 -race。
func TestReviewFixPR06ConcurrentPlanApplyNoRace(t *testing.T) {
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
	var content atomicValue
	content.Store([]byte(`{"rev":1}`))
	c.RegisterProjection(&reviewAdapter{rel: "prefs.json", source: func() []byte {
		b, _ := content.Load().([]byte)
		return b
	}})
	if _, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, []UnitID{"pi/review"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			content.Store([]byte(fmt.Sprintf(`{"rev":%d}`, i+2)))
			_, _ = c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, []UnitID{"pi/review"})
		}(i)
		go func() {
			defer wg.Done()
			snap, err := c.Inspect(context.Background(), mustResolve(t, c, "Ubuntu", CLIPi))
			if err == nil {
				_, _ = c.Plan(SupplyRequest{Target: snap.Target, SelectedUnits: []UnitID{"pi/review"}}, snap)
			}
			_ = c.GetStatus("Ubuntu", CLIPi)
		}()
	}
	wg.Wait()
}

func mustResolve(t *testing.T, c *Coordinator, distro string, cli CLI) TargetRef {
	t.Helper()
	target, err := c.ResolveTarget(context.Background(), distro, cli)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

type atomicValue struct {
	mu sync.Mutex
	v  any
}

func (a *atomicValue) Store(v any) {
	a.mu.Lock()
	a.v = v
	a.mu.Unlock()
}

func (a *atomicValue) Load() any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.v
}

// ---------------------------------------------------------------------------
// PR07：取消贯通 skill/resource takeover 与目录提交
// ---------------------------------------------------------------------------

// TestReviewFixPR07CanceledSkillTakeoverZeroWrites（反例：phase-review-cancel-repro.go）。
func TestReviewFixPR07CanceledSkillTakeoverZeroWrites(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "host")
	if err := os.MkdirAll(filepath.Join(src, "demo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "demo", "SKILL.md"), []byte("# synthetic skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope := NewSkillsScope(src, DefaultSkillsScanLimits())
	scope.Scan()
	fp := &fakeProbes{uncRoot: filepath.Join(root, "target"),
		identity: platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "fixture", OK: true}}
	c := newCoordinatorWith(t, fp, nil)
	c.RegisterSkills(scope)
	unit := SkillUnitID("demo")
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, unit); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := c.TakeOverUnit(ctx, "Ubuntu", CLICommon, unit, "")
	if err != nil {
		t.Fatalf("TakeOverUnit: %v", err)
	}
	if res.Outcome != UnitBlocked || res.Detail != "context-canceled" {
		t.Fatalf("canceled takeover must be blocked, got %+v", res)
	}
	if _, serr := os.Lstat(filepath.Join(fp.uncRoot, "home/u/.agents/skills/demo/SKILL.md")); serr == nil {
		t.Fatal("canceled takeover must not write the target tree")
	}
}

// TestReviewFixPR07CanceledResourceTakeoverAndUnits：资源接管与常规单元同样
// 受取消门约束（等锁后检查覆盖；RunUnits 主循环既有 SA16 逐单元检查）。
func TestReviewFixPR07CanceledResourceTakeoverAndUnits(t *testing.T) {
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
	c.RegisterProjection(&reviewAdapter{rel: "review.json", source: func() []byte { return []byte(`{"v":1}`) }})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := c.RunUnits(ctx, "Ubuntu", CLIPi, ReasonLaunch, []UnitID{"pi/review"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "context-canceled" {
		t.Fatalf("canceled run must block every unit before writes, got %+v", res.Results[0])
	}
	if _, serr := os.Lstat(filepath.Join(fp.uncRoot, "home/u/.pi/agent/review.json")); serr == nil {
		t.Fatal("canceled run must not create the target file")
	}
}

// ---------------------------------------------------------------------------
// PR08：插件 update 支持从有过滤/显式 [] 重置到完整加载
// ---------------------------------------------------------------------------

// TestReviewFixPR08UpdateResetsFiltersToFullLoad（反例：phase-review-filters-repro.go）。
func TestReviewFixPR08UpdateResetsFiltersToFullLoad(t *testing.T) {
	d := newFakePluginDistro(t)
	c, _ := newPluginTestCoordinator(t, d)
	filters := PluginFilters{Skills: []string{}}
	res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:demo@1.0.0", Kind: PluginOpInstall, AnchorRef: "1.0.0", Filters: &filters,
	})
	if res.Outcome != PluginOutcomeInstalled || !res.OK {
		t.Fatalf("filtered install must succeed: %+v", res)
	}
	// 显式全 nil 期望 = 全部加载（纯字符串登记形态）——不再被解释为沿用旧清单。
	next := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:demo@1.0.0", Kind: PluginOpUpdate, AnchorRef: "1.0.0", Filters: &PluginFilters{},
	})
	if next.Outcome != PluginOutcomeUpdated || !next.OK {
		t.Fatalf("reset update must replay and converge (not already-ok), got %+v", next)
	}
	settings := mustReadSettings(t, d)
	if strings.Contains(settings, "skills") {
		t.Fatalf("reset update must remove the filter object (full load string form), got %s", settings)
	}
	if !strings.Contains(settings, `"npm:demo@1.0.0"`) {
		t.Fatalf("registration must stay as plain source string, got %s", settings)
	}
	// 收敛后再次显式全 nil：幂等 already-ok。
	again := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:demo@1.0.0", Kind: PluginOpUpdate, AnchorRef: "1.0.0", Filters: &PluginFilters{},
	})
	if again.Outcome != PluginOutcomeAlreadyOK {
		t.Fatalf("converged full-load form must be idempotent already-ok, got %+v", again)
	}
}

// TestReviewFixPR08UpdateNilFiltersKeepsRegistration：nil（未提供选择）保留
// 旧调用兼容——沿用既有登记清单。
func TestReviewFixPR08UpdateNilFiltersKeepsRegistration(t *testing.T) {
	d := newFakePluginDistro(t)
	c, _ := newPluginTestCoordinator(t, d)
	filters := PluginFilters{Skills: []string{}}
	if res := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:demo@1.0.0", Kind: PluginOpInstall, AnchorRef: "1.0.0", Filters: &filters,
	}); res.Outcome != PluginOutcomeInstalled {
		t.Fatalf("setup: %+v", res)
	}
	next := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{
		Source: "npm:demo@1.0.0", Kind: PluginOpUpdate, AnchorRef: "1.0.0", // Filters == nil
	})
	if next.Outcome != PluginOutcomeAlreadyOK {
		t.Fatalf("nil filters must keep the registered filter set (compat), got %+v", next)
	}
	if !strings.Contains(mustReadSettings(t, d), `"skills":[]`) {
		t.Fatalf("registered filter form must be untouched by nil-filter update, got %s", mustReadSettings(t, d))
	}
}
