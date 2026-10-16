package wslsupply

// projection_test.go — TrackedProjection 接缝与提交原语的行为钉子（S0 生产
// 不注册投影适配器；本文件用桩适配器驱动完整 Plan/Apply/Compensate 链路）：
// 归属判定（unmanaged-existing 冻结 / 本地修改冻结 / 无变化跳过 / 更新）、
// 唯一临时名提交（并发 Apply 不撞名、无 staging 残留、chmod 失败 fail closed）、
// CAS 条件补偿（外部编辑保留、恢复前像、删除新增）。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/launcher"
)

// stubProjection 是最小 TrackedProjection 桩：候选正文来自 source 闭包，
// 版本固定；归属判定按设计 §三的普通文件冲突规则实现（S1 真实分发适配器的
// 行为模板）。
type stubProjection struct {
	unit      UnitID
	rel       string
	source    func() []byte
	sensitive bool // 默认 false；敏感桩（chmod 失败删目标）显式置 true
}

func (p *stubProjection) Unit() UnitID          { return p.unit }
func (p *stubProjection) Policy() Policy        { return PolicyTrackedProjection }
func (p *stubProjection) Sensitive() bool       { return p.sensitive }
func (p *stubProjection) RequiresConsent() bool { return false }
func (p *stubProjection) Files() []string       { return []string{p.rel} }

func (p *stubProjection) Candidate(_ context.Context, _ TargetRef, _ *StateEntry) (ProjectionCandidate, error) {
	return ProjectionCandidate{Content: p.source(), SourceHash: hexHash(p.source()), TransformVersion: "stub-v1"}, nil
}

func (p *stubProjection) Plan(_ SupplyRequest, snap *Snapshot, state *StateEntry) (PlannedUnit, error) {
	content := p.source()
	sourceHash := hexHash(content)
	unit := PlannedUnit{RelPath: p.rel, TransformVersion: "stub-v1", CandidateSize: int64(len(content))}
	meta, ok := snap.File(p.rel)
	recorded := state != nil && state.LastWrittenHash != ""
	exists := ok && meta.Exists
	switch {
	case exists && !recorded:
		unit.Action = ActionBlocked
		unit.BlockedReason = "unmanaged-existing"
	case exists && recorded && meta.Hash != state.LastWrittenHash:
		unit.Action = ActionBlocked
		unit.BlockedReason = "local-modification-frozen"
	case !exists && recorded:
		unit.Action = ActionBlocked
		unit.BlockedReason = "local-deletion-frozen"
	case !exists:
		// 无登记+目标缺失：桩声明允许的默认托管产物才自动创建。
		unit.Action = ActionWriteProjection
		unit.ExpectedTargetHash = ""
	case recorded && state.SourceHash == sourceHash:
		unit.Action = ActionNone // 来源未变、目标仍是上次写入 → 跳过
	default:
		unit.Action = ActionWriteProjection
		unit.ExpectedTargetHash = meta.Hash
	}
	return unit, nil
}
func newProjectionCoordinator(t *testing.T, source func() []byte) (*Coordinator, *fakeProbes, *stubProjection) {
	t.Helper()
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
	stub := &stubProjection{unit: "pi/stub-prefs", rel: "stub-prefs.json", source: source}
	c.RegisterProjection(stub)
	return c, fp, stub
}

func runProjection(t *testing.T, c *Coordinator) RunResult {
	t.Helper()
	res, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, []UnitID{"pi/stub-prefs"})
	if err != nil {
		t.Fatalf("RunUnits: %v", err)
	}
	return res
}

func stubFile(fp *fakeProbes) string {
	return filepath.Join(fp.uncRoot, "home/u/.pi/agent/stub-prefs.json")
}

func TestProjectionOwnershipRules(t *testing.T) {
	t.Run("unmanaged existing target is frozen", func(t *testing.T) {
		c, fp, _ := newProjectionCoordinator(t, func() []byte { return []byte(`{"a":1}`) })
		if err := os.MkdirAll(filepath.Dir(stubFile(fp)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stubFile(fp), []byte(`{"user":"own"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		res := runProjection(t, c)
		if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "unmanaged-existing" {
			t.Fatalf("unmanaged-existing must block, got %+v", res.Results)
		}
		got, _ := os.ReadFile(stubFile(fp))
		if string(got) != `{"user":"own"}` {
			t.Fatalf("user file must be retained verbatim: %s", got)
		}
	})

	t.Run("first create then fresh skip then update", func(t *testing.T) {
		src := `{"rev":1}`
		c, _, _ := newProjectionCoordinator(t, func() []byte { return []byte(src) })
		res := runProjection(t, c) // 无记录+目标缺失 → 创建
		if res.Results[0].Outcome != UnitApplied {
			t.Fatalf("first create must apply, got %+v", res.Results)
		}
		if res.Results[0].Undo == nil {
			t.Fatal("projection write must carry undo handle")
		}
		res = runProjection(t, c) // 来源未变 → 跳过（零写入）
		if res.Results[0].Outcome != UnitFresh {
			t.Fatalf("unchanged source must skip (UnitFresh), got %+v", res.Results)
		}
	})

	t.Run("local modification freezes the unit", func(t *testing.T) {
		src := `{"rev":1}`
		c, fp, _ := newProjectionCoordinator(t, func() []byte { return []byte(src) })
		runProjection(t, c) // 建立登记
		if err := os.WriteFile(stubFile(fp), []byte(`{"user":"edited"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		res := runProjection(t, c)
		if res.Results[0].Outcome != UnitBlocked {
			t.Fatalf("locally modified target must freeze, got %+v", res.Results)
		}
		got, _ := os.ReadFile(stubFile(fp))
		if string(got) != `{"user":"edited"}` {
			t.Fatalf("local edit must win over newer source: %s", got)
		}
	})
}

func TestProjectionApplySkipsNoActionUnits(t *testing.T) {
	// ActionNone 的投影单元在 Apply 阶段零写入（字节不变，无 Undo）。
	src := `{"rev":1}`
	c, fp, _ := newProjectionCoordinator(t, func() []byte { return []byte(src) })
	res1 := runProjection(t, c)
	if res1.Results[0].Outcome != UnitApplied {
		t.Fatalf("first run must apply: %+v", res1.Results)
	}
	before, _ := os.ReadFile(stubFile(fp))
	res2 := runProjection(t, c)
	after, _ := os.ReadFile(stubFile(fp))
	if string(before) != string(after) {
		t.Fatalf("no-action unit rewrote target: %s -> %s", before, after)
	}
	if res2.Results[0].Undo != nil {
		t.Fatal("no-action unit must not carry undo")
	}
}

func TestConcurrentApplyUniqueStagingNames(t *testing.T) {
	// 并发供给不同单元文件（各自投影）不得撞 staging 名：全部成功、目标齐全、
	// 无 .amagi-supply-* 残留。同一协调器的 applyMu 串行化同一目标上的 Apply。
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
	const writers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		rel := fmt.Sprintf("dir%d/prefs.json", i)
		content := fmt.Sprintf(`{"writer":%d}`, i)
		c.RegisterProjection(&stubProjection{unit: UnitID(fmt.Sprintf("pi/stub-%d", i)), rel: rel,
			source: func() []byte { return []byte(content) }})
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, []UnitID{UnitID(fmt.Sprintf("pi/stub-%d", i))})
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent RunUnits: %v", err)
		}
	}
	agentDir := filepath.Join(fp.uncRoot, "home/u/.pi/agent")
	for i := 0; i < writers; i++ {
		got, err := os.ReadFile(filepath.Join(agentDir, fmt.Sprintf("dir%d/prefs.json", i)))
		if err != nil || string(got) != fmt.Sprintf(`{"writer":%d}`, i) {
			t.Fatalf("writer %d file missing/corrupt: %q err=%v", i, got, err)
		}
	}
	filepath.Walk(agentDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(filepath.Base(path), ".amagi-supply-") {
			t.Errorf("staging leftover: %s", path)
		}
		return nil
	})
}

func TestSubmitFileChmodFailureFailsClosed(t *testing.T) {
	fp := newFakeProbes(t)
	fp.chmodErr = fmt.Errorf("wsl.exe dead")
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
	c.RegisterProjection(&stubProjection{unit: "pi/stub-prefs", rel: "stub-prefs.json",
		source: func() []byte { return []byte(`{"secret":"k"}`) }})
	res := runProjection(t, c)
	if res.Results[0].Outcome != UnitFailed {
		t.Fatalf("chmod failure must fail the unit, got %+v", res.Results)
	}
	if _, statErr := os.Stat(stubFile(fp)); !os.IsNotExist(statErr) {
		t.Fatal("loose-permission plaintext must not stay on disk (fail closed)")
	}
}

func TestCompensateCAS(t *testing.T) {
	// updateWithUndo 建立「已登记目标 → 来源更新 → 带撤销写入」的基准序列。
	updateWithUndo := func(t *testing.T) (*Coordinator, *fakeProbes, RunResult) {
		t.Helper()
		src := `{"rev":1}`
		c, fp, _ := newProjectionCoordinator(t, func() []byte { return []byte(src) })
		if res := runProjection(t, c); res.Results[0].Outcome != UnitApplied {
			t.Fatalf("first create must apply: %+v", res.Results)
		}
		src = `{"rev":2}`
		res := runProjection(t, c)
		if res.Results[0].Outcome != UnitApplied || res.Results[0].Undo == nil {
			t.Fatalf("update must apply with undo: %+v", res.Results)
		}
		return c, fp, res
	}

	t.Run("restore previous content when hash still matches", func(t *testing.T) {
		c, fp, res := updateWithUndo(t)
		outcomes := c.Compensate(context.Background(), res.Results[0].Undo)
		if len(outcomes) != 1 || outcomes[0].Disposition != CompensationConfirmed {
			t.Fatalf("compensation = %+v", outcomes)
		}
		got, _ := os.ReadFile(stubFile(fp))
		if string(got) != `{"rev":1}` {
			t.Fatalf("previous content must be restored, got %s", got)
		}
	})

	t.Run("external edit retained on hash mismatch", func(t *testing.T) {
		c, fp, res := updateWithUndo(t)
		// Abort 前目标被外部进程改写（不遵循 CodeBox 锁）。
		if err := os.WriteFile(stubFile(fp), []byte(`{"user":"raced"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		outcomes := c.Compensate(context.Background(), res.Results[0].Undo)
		if len(outcomes) != 1 || outcomes[0].Disposition != CompensationSkipped {
			t.Fatalf("CAS mismatch must skip, got %+v", outcomes)
		}
		got, _ := os.ReadFile(stubFile(fp))
		if string(got) != `{"user":"raced"}` {
			t.Fatalf("external edit must be retained, got %s", got)
		}
	})

	t.Run("newly created file is removed on compensate", func(t *testing.T) {
		c, fp, _ := newProjectionCoordinator(t, func() []byte { return []byte(`{"rev":1}`) })
		res := runProjection(t, c)
		if res.Results[0].Undo == nil {
			t.Fatal("create must carry undo")
		}
		outcomes := c.Compensate(context.Background(), res.Results[0].Undo)
		if len(outcomes) != 1 || outcomes[0].Disposition != CompensationConfirmed {
			t.Fatalf("compensation = %+v", outcomes)
		}
		if _, statErr := os.Stat(stubFile(fp)); !os.IsNotExist(statErr) {
			t.Fatal("created file must be removed on compensation")
		}
	})
}

func TestA2NeverCompensates(t *testing.T) {
	// A2 凭据刷新是持久认证状态：结果永不携带 Undo（effect 的 Abort 不得把
	// distro/Windows 侧凭据回滚成旧 token）。该语义由「无 Undo 句柄」钉住。
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthDistroNewer})
	res, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, []UnitID{UnitPiA2Auth})
	if err != nil {
		t.Fatal(err)
	}
	if res.Results[0].Undo != nil {
		t.Fatal("a2 credential refresh must never carry compensation")
	}
	if got := c.Compensate(context.Background()); len(got) != 0 {
		t.Fatalf("compensating nothing must be a no-op, got %+v", got)
	}
}
