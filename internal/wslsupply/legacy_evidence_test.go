package wslsupply

// legacy_evidence_test.go — 攻坚契约 PR01：v1 证据归属与逐目标核验的公开
// 入口回归（phase-rereview-repro.go 的 LEGACY/LEGACY_MISMATCH 反例转正）：
// A 发布→只检查 B→升级 → A 阻（指纹匹配 + 文件在）、B 不凭顶层线索继承
// 风险；clone 同指纹都是候选、逐目标解除；unknown/无归属保真不全局误阻；
// Detached/确切 withdrawn/consent-only 不伪造风险；手动处理后确认 absent
// 是真实解除路线。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// legacyABEnv 构造 A/B 双目标环境（同 machine-id、不同 distro 名——旧公式
// 指纹相同=克隆候选；新代指纹含 distro 各自独立）。
type legacyABEnv struct {
	c        *Coordinator
	roots    map[string]string
	visionFn func(distro string) string
}

func newLegacyABEnv(t *testing.T) *legacyABEnv {
	t.Helper()
	roots := map[string]string{"A": t.TempDir(), "B": t.TempDir()}
	e := &legacyABEnv{c: nil, roots: roots, visionFn: nil}
	e.visionFn = func(distro string) string {
		return filepath.Join(roots[distro], "home/u/.agents", VisionExportFile)
	}
	e.c = NewCoordinator(t.TempDir(), nil, Probes{
		Identity: func(context.Context, string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "m-clone", OK: true}
		},
		ToUNC: func(distro, linuxPath string) string {
			return filepath.Join(roots[distro], filepath.FromSlash(trimLeadingSlash(linuxPath)))
		},
		Chmod: func(context.Context, string, string, ...string) error { return nil },
		StatMode: func(_ context.Context, _, linuxPath string) (string, bool) {
			// 真实文件权限回读（0600 副本健康）。
			for _, root := range roots {
				p := filepath.Join(root, filepath.FromSlash(trimLeadingSlash(linuxPath)))
				if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
					return permStringOf(info), true
				}
			}
			return "", false
		},
	})
	return e
}

// writeLegacyV1TopB 写一份 v1 清单：entry 旧指纹 = 克隆指纹（A/B 共享），
// 顶层线索 = B（升级前最后检查的是 B——原反例的归属陷阱）。
func (e *legacyABEnv) writeLegacyV1TopB(t *testing.T, entryOutcome string, written bool) {
	t.Helper()
	gen := legacyGenerationOf("m-clone", "1000", "/home/u")
	entry := StateEntry{
		Unit: UnitSharedVisionExport, Policy: PolicyManagedNamespace,
		Generation: gen, Consented: true, LastOutcome: entryOutcome,
	}
	if written {
		entry.LastWrittenHash = hexHash([]byte(legacyCloneCopy))
	}
	b, err := json.Marshal(StateFile{
		SchemaVersion: 1, Generation: gen, Distro: "B", Home: "/home/u",
		Units: []StateEntry{entry},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(e.c.state.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.c.state.path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

const legacyCloneCopy = `{"version":1,"api_key":"CLONE-OLD-COPY"}`

// TestAttackLegacyABMismatchBlocksOwnerNotChecker：A 发布→只检查 B→升级 →
// A 待处理（指纹匹配 + 文件在）、B 放行（指纹不匹配 + 文件不在）；B 的顶层
// 线索不得让它继承 A 的风险。
func TestAttackLegacyABMismatchBlocksOwnerNotChecker(t *testing.T) {
	e := newLegacyABEnv(t)
	e.writeLegacyV1TopB(t, "written", true)
	// A 上留有旧副本；B 干净。
	if err := os.MkdirAll(filepath.Dir(e.visionFn("A")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.visionFn("A"), []byte(legacyCloneCopy), 0o600); err != nil {
		t.Fatal(err)
	}
	src := &stubSharedSource{vision: func() ([]byte, error) { return nil, os.ErrNotExist }, modality: nil}
	e.c.RegisterProjection(NewVisionExportAdapter(src))
	// A：指纹匹配 + 文件在 → 阻断（待处理）。
	aAssess := e.c.AssessWSLSensitiveConsumption(context.Background(), "A", CLICommon, UnitSharedVisionExport, nil)
	if aAssess.Consumption != ConsumptionUnsafe || !aAssess.RiskPending {
		t.Fatalf("A must be blocked (fingerprint match + file present), got %+v", aAssess)
	}
	// B：指纹不匹配 + 文件不在 → 放行（本目标）；顶层线索是 B 也不继承。
	bAssess := e.c.AssessWSLSensitiveConsumption(context.Background(), "B", CLICommon, UnitSharedVisionExport, nil)
	if bAssess.Consumption != ConsumptionAbsent || bAssess.RiskPending {
		t.Fatalf("B must pass (fingerprint mismatch + file absent; top-clue must not inherit), got %+v", bAssess)
	}
	// 公开启动面（root 包 TestWSLSharedGate* 钉 5CLI 桌面/remote/restart 面）。
	// 手动处理 A 的文件后确认 absent → A 解除（真实解除路线）。
	if err := os.Remove(e.visionFn("A")); err != nil {
		t.Fatal(err)
	}
	if err := e.c.ConfirmLegacyVisionAbsent(context.Background(), "A"); err != nil {
		t.Fatalf("confirm absent must succeed after manual cleanup: %v", err)
	}
	aAfter := e.c.AssessWSLSensitiveConsumption(context.Background(), "A", CLICommon, UnitSharedVisionExport, nil)
	if aAfter.Consumption != ConsumptionAbsent || aAfter.RiskPending {
		t.Fatalf("A must be released after confirmed absent, got %+v", aAfter)
	}
}

// TestAttackLegacyCloneBothCandidatesResolvedPerTarget：相同旧指纹的克隆
// A/B 都是候选：A 解除后 B 证据仍在（逐目标解除，不互相解除、不授写权）。
func TestAttackLegacyCloneBothCandidatesResolvedPerTarget(t *testing.T) {
	e := newLegacyABEnv(t)
	e.writeLegacyV1TopB(t, "written", true)
	// 两个克隆目标都留有旧副本。
	for _, d := range []string{"A", "B"} {
		if err := os.MkdirAll(filepath.Dir(e.visionFn(d)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.visionFn(d), []byte(legacyCloneCopy), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"A", "B"} {
		assess := e.c.AssessWSLSensitiveConsumption(context.Background(), d, CLICommon, UnitSharedVisionExport, nil)
		if assess.Consumption != ConsumptionUnsafe || !assess.RiskPending {
			t.Fatalf("%s (clone) must be a candidate owner, got %+v", d, assess)
		}
	}
	// A 手动处理 + 确认 absent → A 解除；B 风险仍在。
	if err := os.Remove(e.visionFn("A")); err != nil {
		t.Fatal(err)
	}
	if err := e.c.ConfirmLegacyVisionAbsent(context.Background(), "A"); err != nil {
		t.Fatal(err)
	}
	aAssess := e.c.AssessWSLSensitiveConsumption(context.Background(), "A", CLICommon, UnitSharedVisionExport, nil)
	bAssess := e.c.AssessWSLSensitiveConsumption(context.Background(), "B", CLICommon, UnitSharedVisionExport, nil)
	if aAssess.RiskPending {
		t.Fatalf("A must be released per-target, got %+v", aAssess)
	}
	if !bAssess.RiskPending {
		t.Fatalf("B evidence must survive A's resolution (per-target), got %+v", bAssess)
	}
}

// TestAttackLegacyTerminalStatesNoFabricatedRisk：detached / 确切 withdrawn /
// consent-only（无写基准）不重建受管风险。
func TestAttackLegacyTerminalStatesNoFabricatedRisk(t *testing.T) {
	cases := []struct {
		name    string
		outcome string
		written bool
	}{
		{"detached clears risk", "detached", true},
		{"exact withdrawn clears risk", "withdrawn", true},
		{"withdrawn-already-absent clears risk", "withdrawn-already-absent", true},
		{"consent-only never fabricated risk", "consented", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newLegacyABEnv(t)
			e.writeLegacyV1TopB(t, tc.outcome, tc.written)
			if err := os.MkdirAll(filepath.Dir(e.visionFn("A")), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(e.visionFn("A"), []byte(legacyCloneCopy), 0o600); err != nil {
				t.Fatal(err)
			}
			assess := e.c.AssessWSLSensitiveConsumption(context.Background(), "A", CLICommon, UnitSharedVisionExport, nil)
			if assess.RiskPending {
				t.Fatalf("terminal state must not fabricate managed risk, got %+v", assess)
			}
		})
	}
}

// TestAttackLegacyConfirmAbsentBoundaries：文件仍在时确认失败（精确指引、
// 状态仍是未撤回）；文件 absent 时确认成功并持久化；确认不删除原证据。
func TestAttackLegacyConfirmAbsentBoundaries(t *testing.T) {
	e := newLegacyABEnv(t)
	e.writeLegacyV1TopB(t, "written", true)
	if err := os.MkdirAll(filepath.Dir(e.visionFn("A")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.visionFn("A"), []byte(legacyCloneCopy), 0o600); err != nil {
		t.Fatal(err)
	}
	// 文件仍在 → 拒绝确认，给出精确 distro/Home/文件名。
	err := e.c.ConfirmLegacyVisionAbsent(context.Background(), "A")
	if err == nil {
		t.Fatal("confirm must fail while the file is present")
	}
	if !containsAll(err.Error(), "A", "home/u", VisionExportFile) {
		t.Fatalf("error must carry exact distro/home/file, got %v", err)
	}
	// 原证据保留。
	if ev := e.c.LegacyEvidenceSnapshot(); len(ev) == 0 {
		t.Fatal("evidence must be retained on failed confirm")
	}
	// 处理后确认成功并持久化（重启后仍解除）。
	if err := os.Remove(e.visionFn("A")); err != nil {
		t.Fatal(err)
	}
	if err := e.c.ConfirmLegacyVisionAbsent(context.Background(), "A"); err != nil {
		t.Fatal(err)
	}
	st := e.c.state.load()
	if len(st.LegacyEvidence) == 0 {
		t.Fatal("evidence entries must be kept (保真) after resolution")
	}
	resolvedFound := false
	for _, ev := range st.LegacyEvidence {
		for _, d := range ev.Resolved {
			if d == "absent-confirmed" {
				resolvedFound = true
			}
		}
	}
	if !resolvedFound {
		t.Fatal("resolution must be persisted per target")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func trimLeadingSlash(p string) string {
	for len(p) > 0 && p[0] == '/' {
		p = p[1:]
	}
	return p
}

func permStringOf(info os.FileInfo) string {
	const digits = "01234567"
	perm := info.Mode().Perm()
	return string([]byte{digits[perm>>6&7], digits[perm>>3&7], digits[perm&7]})
}
