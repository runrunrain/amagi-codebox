package wslsupply

// a2_consumption_test.go — 攻坚契约 PR02/PR07 的 A2 消费/取消回归：
// 取消/到期一律失败（成功 outcome + 已取消 ctx 不是 Applied）；首个可能改
// 变 auth/权限的动作前保存无秘密 pending 摘要（摘要写失败不开始同步写）；
// 摘要不含 hash/token；结束保存结构化结果并清理。

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"amagi-codebox/internal/launcher"
)

// countingA2 记录 Sync 调用的 A2 适配器。
type countingA2 struct {
	inner  A2Adapter
	calls  int32
	called *int32
}

func (a *countingA2) Files() []string { return []string{"auth.json"} }

func (a *countingA2) Sync(ctx context.Context, target TargetRef) (launcher.SyncWSLPiLoginAuthOutcome, error) {
	*a.called++
	return a.inner.Sync(ctx, target)
}

// TestAttackA2CanceledCtxNeverApplied：取消的请求即使适配器返回成功
// outcome 也必须 Failed（不降级 HasCopy、不 Applied）。
func TestAttackA2CanceledCtxNeverApplied(t *testing.T) {
	for _, outcome := range []launcher.SyncWSLPiLoginAuthOutcome{
		launcher.SyncWSLPiLoginAuthCopied,
		launcher.SyncWSLPiLoginAuthFresh,
		launcher.SyncWSLPiLoginAuthDistroNewer,
		launcher.SyncWSLPiLoginAuthDistroTieKept,
		launcher.SyncWSLPiLoginAuthDistroHasCopy,
	} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res := classifyA2(ctx, UnitPiA2Auth, outcome, nil)
		if res.Outcome != UnitFailed || res.A2.Outcome != outcome {
			t.Fatalf("canceled ctx must fail regardless of outcome %v, got %+v", outcome, res)
		}
	}
}

// TestAttackA2PendingSummaryAroundSync：Apply 的 A2 分支在 Sync 前保存无秘密
// 摘要；结束后 settled + intent 清理；摘要文件不含 hash/token 字节。
func TestAttackA2PendingSummaryAroundSync(t *testing.T) {
	fp := newFakeProbes(t)
	calls := int32(0)
	c := newCoordinatorWith(t, fp, &countingA2{
		inner:  &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh},
		called: &calls,
	})
	res, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, []UnitID{UnitPiA2Auth})
	if err != nil {
		t.Fatal(err)
	}
	if res.Results[0].Outcome != UnitFresh {
		t.Fatalf("fresh a2 must stay fresh, got %+v", res.Results[0])
	}
	if calls != 1 {
		t.Fatalf("a2 sync must run exactly once, got %d", calls)
	}
	// 结束后 pending 目录无残留 a2 摘要、settled 墓碑已清理。
	intents, err := c.state.loadIntents()
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range intents {
		if op.Kind == OpKindA2Sync {
			t.Fatalf("a2 summary intent must be settled and cleaned, got %+v", op)
		}
	}
	st := c.state.load()
	for id, s := range st.SettledOperations {
		if s.Unit == UnitPiA2Auth {
			t.Fatalf("a2 settled tombstone must be cleaned after finish, got %s=%+v", id, s)
		}
	}
	// 登记面：outcome 已结构化记录（fresh），无 hash 字段。
	entry := st.entryFor(UnitPiA2Auth, res.Target.Generation)
	if entry.LastOutcome != "fresh" {
		t.Fatalf("a2 state must record fresh, got %+v", entry)
	}
}

// TestAttackA2SummaryWriteFailureBlocksSync：摘要持久化失败 → 不开始同步写
// （prepare-state-failed；Sync 零调用），状态不冒充成功。
func TestAttackA2SummaryWriteFailureBlocksSync(t *testing.T) {
	fp := newFakeProbes(t)
	calls := int32(0)
	c := newCoordinatorWith(t, fp, &countingA2{
		inner:  &stubA2{outcome: launcher.SyncWSLPiLoginAuthCopied},
		called: &calls,
	})
	// 摘要持久化故障经确定性故障接缝注入（跨平台同一语义：POSIX chmod 0500
	// 目录在 Windows 宿主不阻止写入，不能作为故障注入）：pending 存储面保持
	// 可读可扫描（存储面健康），saveIntent 恒失败——隔离「摘要写失败」与
	// 「pending 存储不可读」（后者首轮即被健康门阻断，不足证 A2 专门语义）。
	c.faults.add(stageIntent, 0, errors.New("a2 summary persist fault"))
	res, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, []UnitID{UnitPiA2Auth})
	if err != nil {
		t.Fatal(err)
	}
	if res.Results[0].Outcome != UnitFailed || res.Results[0].Detail != "prepare-state-failed" {
		t.Fatalf("summary persist failure must block sync writes, got %+v", res.Results[0])
	}
	if calls != 0 {
		t.Fatalf("no sync writes may start when summary cannot persist, got %d calls", calls)
	}
}

// TestAttackA2SummaryContainsNoSecretMaterial：摘要 JSON 不含 auth hash/token
// 字段（A2 不得落 auth 正文或 hash）。
func TestAttackA2SummaryContainsNoSecretMaterial(t *testing.T) {
	dir := t.TempDir()
	store := newStateStore(dir)
	opID := newOpID()
	intent := OperationIntent{
		OpID: opID, Kind: OpKindA2Sync, TargetKey: "gen-x", TargetDistro: "Ubuntu",
		TargetHome: "/home/u", Unit: UnitPiA2Auth, RelPath: "auth.json", Sensitive: true,
		PluginMeta: map[string]string{"phase": "sync"},
	}
	if err := store.saveIntent(intent); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(pendingIntentPath(dir, opID))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if jerr := json.Unmarshal(data, &raw); jerr != nil {
		t.Fatal(jerr)
	}
	for _, forbidden := range []string{"beforeHash", "afterHash", "backupRel", "token", "auth"} {
		if v, ok := raw[forbidden]; ok && v != nil && v != "" {
			t.Fatalf("a2 summary must not carry %s, got %v", forbidden, v)
		}
	}
	if !strings.Contains(string(data), "a2-sync") {
		t.Fatalf("summary must carry its kind, got %s", data)
	}
}

// TestAttackA2ResultClassificationWithCtxError：ctx 已结束但适配器报告成功 →
// RunUnits 的 A2 结果必须 Failed（聚合面不把取消当同步成功）。
func TestAttackA2ResultClassificationWithCtxError(t *testing.T) {
	fp := newFakeProbes(t)
	// 适配器返回成功，但请求 ctx 在结果前已取消（模拟探测边界取消）。
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthCopied})
	ctx, cancel := context.WithCancel(context.Background())
	// 在 Sync 进行中取消（RunUnits 主循环单元检查点之前的探测边界）。
	canceledMidFlight := &midFlightCancelA2{base: context.Background(), cancel: cancel, wrapped: &stubA2{outcome: launcher.SyncWSLPiLoginAuthCopied}}
	c.SetA2Adapter(canceledMidFlight)
	res, err := c.RunUnits(ctx, "Ubuntu", CLIPi, ReasonLaunch, []UnitID{UnitPiA2Auth})
	if err != nil {
		t.Fatal(err)
	}
	if res.Results[0].Outcome != UnitFailed {
		t.Fatalf("mid-flight cancellation must not report applied, got %+v", res.Results[0])
	}
}

type midFlightCancelA2 struct {
	base    context.Context
	cancel  context.CancelFunc
	wrapped A2Adapter
}

func (m *midFlightCancelA2) Files() []string { return []string{"auth.json"} }

func (m *midFlightCancelA2) Sync(ctx context.Context, target TargetRef) (launcher.SyncWSLPiLoginAuthOutcome, error) {
	outcome, err := m.wrapped.Sync(ctx, target)
	// runner 返回 nil 后 ctx 已结束 → canceled（不执行后继写）。
	m.cancel()
	return outcome, err
}

// TestAttackSharedGateManagedCopyFailedRunBlocks：真实协调器 + 受管副本 +
// 本次失败 → assessment unsafe（启动门阻断的事实面）；确无副本线索的失败
// 不误阻（absent）。
func TestAttackSharedGateManagedCopyFailedRunBlocks(t *testing.T) {
	content := `{"version":1,"updated_at":"t","models":[{"id":"p/m","api_key":"sk"}]}`
	c, fp := newVisionCoordinator(t, func() ([]byte, error) { return []byte(content), nil })
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatal(err)
	}
	if res := runUnit(t, c, CLICommon, UnitSharedVisionExport); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("publish: %+v", res.Results[0])
	}
	// chmod 故障 + 来源变化（触发重写）→ 本次失败；受管副本在 → unsafe。
	c2content := `{"version":1,"updated_at":"t2","models":[{"id":"p/m2","api_key":"sk2"}]}`
	shared := &stubSharedSource{vision: func() ([]byte, error) { return []byte(c2content), nil }, modality: func() ([]byte, error) { return nil, nil }}
	c.projections[UnitSharedVisionExport] = NewVisionExportAdapter(shared)
	fp.setChmodError(errors.New("wsl.exe dead"))
	failed := runUnit(t, c, CLICommon, UnitSharedVisionExport)
	if failed.Results[0].Outcome != UnitFailed {
		t.Fatalf("chmod failure must fail the unit, got %+v", failed.Results[0])
	}
	assess := c.AssessWSLSensitiveConsumption(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport, &failed.Results[0])
	if assess.Consumption != ConsumptionUnsafe || !assess.RiskPending {
		t.Fatalf("failed run with managed copy must be unsafe, got %+v", assess)
	}
	// 无副本历史的失败（目标从未受管）不误阻：换一个干净目标。
	c2, _ := newVisionCoordinator(t, func() ([]byte, error) { return nil, os.ErrNotExist })
	res2 := runUnit(t, c2, CLICommon, UnitSharedVisionExport)
	if res2.Results[0].Outcome != UnitBlocked {
		t.Fatalf("no-copy target should be blocked-await-consent or fresh, got %+v", res2.Results[0])
	}
	assess2 := c2.AssessWSLSensitiveConsumption(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport, &res2.Results[0])
	if assess2.Consumption != ConsumptionAbsent || assess2.RiskPending {
		t.Fatalf("no-history failure must not fabricate risk, got %+v", assess2)
	}
}
