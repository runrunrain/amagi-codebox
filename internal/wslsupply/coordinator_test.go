package wslsupply

// coordinator_test.go — S0 骨架的行为钉子：目标解析与 generation、批量只读
// Inspect、Plan 纯读零副作用、A2 适配器委托与结果分类、状态清单落盘与脱敏
// GetStatus、MarkDirty 只标脏不探测、目标复核拒绝旧计划。全部经注入的假探测
// 面运行，不触达真实 WSL。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/platform"
)

// fakeProbes 构造注入本地 temp dir 的探测面；identityCalls 计数身份探测次数
// （Plan 纯读断言消费）。
type fakeProbes struct {
	uncRoot       string
	identity      platform.WSLUserIdentity
	identityCalls int32
	chmodErr      error
	chmodCalls    int32
}

func (f *fakeProbes) probes() Probes {
	return Probes{
		Identity: func(string) platform.WSLUserIdentity {
			atomic.AddInt32(&f.identityCalls, 1)
			return f.identity
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(f.uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(_, _ string, _ ...string) error {
			atomic.AddInt32(&f.chmodCalls, 1)
			return f.chmodErr
		},
	}
}

func newFakeProbes(t *testing.T) *fakeProbes {
	t.Helper()
	return &fakeProbes{
		uncRoot:  t.TempDir(),
		identity: platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "machine-abc", OK: true},
	}
}

// stubA2 固定返回结果的 A2 适配器。
type stubA2 struct {
	outcome launcher.SyncWSLPiLoginAuthOutcome
	err     error
	calls   int32
}

func (s *stubA2) Files() []string { return []string{"auth.json"} }

func (s *stubA2) Sync(string) (launcher.SyncWSLPiLoginAuthOutcome, error) {
	atomic.AddInt32(&s.calls, 1)
	return s.outcome, s.err
}

func newCoordinatorWith(t *testing.T, fp *fakeProbes, a2 A2Adapter) *Coordinator {
	t.Helper()
	c := NewCoordinator(t.TempDir(), nil, fp.probes())
	if a2 != nil {
		c.SetA2Adapter(a2)
	}
	return c
}

func TestResolveTargetGeneration(t *testing.T) {
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, nil)
	target, err := c.ResolveTarget(context.Background(), "Ubuntu", CLIPi)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.Distro != "Ubuntu" || target.CLI != CLIPi || target.UID != "1000" || target.Home != "/home/u" {
		t.Fatalf("target identity fields wrong: %+v", target)
	}
	if target.LinuxRoot != "/home/u/.pi/agent" {
		t.Fatalf("LinuxRoot = %q", target.LinuxRoot)
	}
	if target.Generation == "" || target.Generation == GenerationUnknown {
		t.Fatalf("generation with machine-id must be derived, got %q", target.Generation)
	}
	// machine-id 缺失（稳定身份不可得）→ 按未知处理。
	fp.identity.MachineID = ""
	target2, err := c.ResolveTarget(context.Background(), "Ubuntu", CLIPi)
	if err != nil {
		t.Fatal(err)
	}
	if target2.Generation != GenerationUnknown {
		t.Fatalf("generation without machine-id = %q, want %q", target2.Generation, GenerationUnknown)
	}
	// 身份探测失败 / 未知 CLI / 空 distro → 目标不可解析（fail closed）。
	fp.identity.OK = false
	if _, err := c.ResolveTarget(context.Background(), "Ubuntu", CLIPi); err == nil {
		t.Fatal("identity probe failure must fail target resolution")
	}
	if _, err := c.ResolveTarget(context.Background(), "Ubuntu", "unknown-cli"); err == nil {
		t.Fatal("unknown CLI must be rejected")
	}
	if _, err := c.ResolveTarget(context.Background(), "  ", CLIPi); err == nil {
		t.Fatal("empty distro must be rejected")
	}
}

func TestInspectReadonlySnapshot(t *testing.T) {
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, nil)
	target, err := c.ResolveTarget(context.Background(), "Ubuntu", CLIPi)
	if err != nil {
		t.Fatal(err)
	}
	// distro 侧已有一份 auth.json（经假 UNC 根落本地）。
	if err := os.MkdirAll(filepath.Join(fp.uncRoot, "home/u/.pi/agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fp.uncRoot, "home/u/.pi/agent/auth.json"), []byte(`{"x":{"type":"oauth"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	uncBefore := fp.uncRoot
	snap, err := c.Inspect(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := snap.File("auth.json")
	if !ok || !meta.Exists || meta.Size == 0 {
		t.Fatalf("auth.json probe missing: %+v ok=%v", meta, ok)
	}
	if meta.Hash != "" {
		t.Fatalf("A2 credential hash must never enter the snapshot, got %q", meta.Hash)
	}
	// 只读断言：UNC 根内容不变（不写任何文件）。
	entries, err := os.ReadDir(filepath.Join(uncBefore, "home/u/.pi/agent"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "auth.json" {
		t.Fatalf("inspect must not write targets: entries=%v err=%v", entries, err)
	}
}

func TestPlanIsPureComputation(t *testing.T) {
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, nil)
	target, err := c.ResolveTarget(context.Background(), "Ubuntu", CLIPi)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.Inspect(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	identityBefore := atomic.LoadInt32(&fp.identityCalls)
	plan, err := c.Plan(SupplyRequest{Target: target, Reason: ReasonLaunch, SelectedUnits: []UnitID{UnitPiA2Auth}}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Units) != 1 || plan.Units[0].Action != ActionSyncA2 {
		t.Fatalf("a2 plan unit = %+v", plan.Units)
	}
	// 纯读断言：Plan 不触发身份探测（不 spawn）、不写任何目标文件。
	if got := atomic.LoadInt32(&fp.identityCalls); got != identityBefore {
		t.Fatalf("Plan spawned identity probes: %d -> %d", identityBefore, got)
	}
	if plan.Units[0].ExpectedTargetHash != "" || plan.Units[0].CandidateSize != 0 {
		t.Fatalf("a2 plan unit must not carry hashes/candidates: %+v", plan.Units[0])
	}
	// 快照/目标不匹配、空单元、未知单元拒绝。
	if _, err := c.Plan(SupplyRequest{Target: TargetRef{Distro: "Other", CLI: CLIPi, LinuxRoot: "/x"}, SelectedUnits: []UnitID{UnitPiA2Auth}}, snap); err == nil {
		t.Fatal("snapshot/request mismatch must be rejected")
	}
	if _, err := c.Plan(SupplyRequest{Target: target, SelectedUnits: nil}, snap); err == nil {
		t.Fatal("empty selection must be rejected")
	}
	if _, err := c.Plan(SupplyRequest{Target: target, SelectedUnits: []UnitID{"pi/nope"}}, snap); err == nil {
		t.Fatal("unknown unit must be rejected")
	}
}

func TestApplyA2DelegatesAndRecordsState(t *testing.T) {
	fp := newFakeProbes(t)
	for _, tc := range []struct {
		name        string
		outcome     launcher.SyncWSLPiLoginAuthOutcome
		wantUnit    UnitOutcome
		wantSuccess bool
	}{
		{"copied", launcher.SyncWSLPiLoginAuthCopied, UnitApplied, true},
		{"fresh", launcher.SyncWSLPiLoginAuthFresh, UnitFresh, true},
		{"distro newer", launcher.SyncWSLPiLoginAuthDistroNewer, UnitApplied, true},
		{"tie kept", launcher.SyncWSLPiLoginAuthDistroTieKept, UnitApplied, true},
		{"has copy degrades", launcher.SyncWSLPiLoginAuthDistroHasCopy, UnitWarnDegraded, false},
		{"no auth fails", launcher.SyncWSLPiLoginAuthDistroNoAuth, UnitFailed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a2 := &stubA2{outcome: tc.outcome, err: errors.New("boom")}
			c := newCoordinatorWith(t, fp, a2)
			res, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, []UnitID{UnitPiA2Auth})
			if err != nil {
				t.Fatalf("RunUnits: %v", err)
			}
			if a2.calls != 1 {
				t.Fatalf("a2 adapter calls = %d", a2.calls)
			}
			if len(res.Results) != 1 || res.Results[0].Outcome != tc.wantUnit {
				t.Fatalf("unit result = %+v, want outcome %v", res.Results, tc.wantUnit)
			}
			if res.Results[0].A2 == nil || res.Results[0].A2.Outcome != tc.outcome {
				t.Fatalf("a2 outcome not propagated: %+v", res.Results[0].A2)
			}
			// 状态登记：outcome 与 LastSuccess；A2 永不记录任何 hash。
			st := c.GetStatus("Ubuntu", CLIPi)
			if !st.Checked {
				t.Fatal("status must be checked after a run")
			}
			if len(st.Units) != 1 || st.Units[0].Unit != UnitPiA2Auth {
				t.Fatalf("status units = %+v", st.Units)
			}
			if tc.wantSuccess && st.Units[0].LastSuccess.IsZero() {
				t.Fatalf("LastSuccess must be recorded for %v", tc.outcome)
			}
			raw, err := os.ReadFile(filepath.Join(c.state.path))
			if err != nil {
				t.Fatal(err)
			}
			body := string(raw)
			if strings.Contains(body, "sourceHash") || strings.Contains(body, "lastWrittenHash") {
				t.Fatalf("A2 unit must never persist credential hashes: %s", body)
			}
			// Undo：A2 凭据刷新不携带撤销能力。
			if res.Results[0].Undo != nil {
				t.Fatal("a2 result must not carry an undo handle")
			}
		})
	}
}

func TestApplyRefusesStalePlanWhenTargetChanged(t *testing.T) {
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
	target, err := c.ResolveTarget(context.Background(), "Ubuntu", CLIPi)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.Inspect(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.Plan(SupplyRequest{Target: target, Reason: ReasonLaunch, SelectedUnits: []UnitID{UnitPiA2Auth}}, snap)
	if err != nil {
		t.Fatal(err)
	}
	// 计划后目标被替换（默认用户 Home 变化 ⇒ 身份面变化）。
	fp.identity.Home = "/home/other"
	res, err := c.Apply(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 1 || res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "target-changed-since-planning" {
		t.Fatalf("stale plan must be refused: %+v", res.Results)
	}
}

func TestMarkDirtyMergesAndPersistsWithoutProbing(t *testing.T) {
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, nil)
	before := atomic.LoadInt32(&fp.identityCalls)
	if err := c.MarkDirty("providers", 5); err != nil {
		t.Fatal(err)
	}
	if err := c.MarkDirty("providers", 3); err != nil { // 旧 revision 不回退
		t.Fatal(err)
	}
	if err := c.MarkDirty("pi-login-presets", 9); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&fp.identityCalls); got != before {
		t.Fatalf("MarkDirty must not probe WSL: %d -> %d", before, got)
	}
	st := c.GetStatus("Ubuntu", CLIPi)
	if st.Checked {
		t.Fatal("MarkDirty alone must not mark the target checked")
	}
	if st.DesiredRevision != 9 || st.PendingSources["providers"] != 5 || st.PendingSources["pi-login-presets"] != 9 {
		t.Fatalf("dirty revisions wrong: %+v", st)
	}
	if err := c.MarkDirty("", 1); err == nil {
		t.Fatal("empty source must be rejected")
	}
}

func TestGetStatusSanitizedAndTargetScoped(t *testing.T) {
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthCopied})
	if _, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, []UnitID{UnitPiA2Auth}); err != nil {
		t.Fatal(err)
	}
	st := c.GetStatus("Ubuntu", CLIPi)
	if !st.Checked || st.Generation == "" || st.Home != "/home/u" {
		t.Fatalf("status snapshot incomplete: %+v", st)
	}
	for _, u := range st.Units {
		if u.LastOutcome == "" {
			t.Fatalf("unit status missing outcome: %+v", u)
		}
	}
	// 另一 distro：无缓存 → 未检查。
	other := c.GetStatus("Debian", CLIPi)
	if other.Checked || len(other.Units) != 0 {
		t.Fatalf("unknown target must read unchecked, got %+v", other)
	}
	// 状态文件是私有原子写（0600）；Windows 上 os.Chmod 只翻只读位，权限
	// 断言仅在 POSIX 平台有意义。
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(c.state.path))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("state file mode = %v, want 0600", info.Mode().Perm())
		}
	}
}

func TestRunUnitsValidatesUnits(t *testing.T) {
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, nil)
	if _, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, nil); err == nil {
		t.Fatal("empty units must be rejected")
	}
	if _, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, []UnitID{"pi/nope"}); err == nil {
		t.Fatal("unknown unit must be rejected")
	}
}

func TestCorruptStateFileFallsBackToUnmanaged(t *testing.T) {
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, nil)
	if err := os.WriteFile(filepath.Join(c.state.path), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 损坏清单退回未托管空清单，不得 panic，也不得重置目标文件。
	a2 := &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh}
	c.SetA2Adapter(a2)
	if _, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, []UnitID{UnitPiA2Auth}); err != nil {
		t.Fatal(err)
	}
	st := c.GetStatus("Ubuntu", CLIPi)
	if len(st.Units) != 1 {
		t.Fatalf("re-run after corrupt state must re-record unit: %+v", st.Units)
	}
}
