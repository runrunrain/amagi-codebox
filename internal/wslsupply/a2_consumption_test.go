package wslsupply

// a2_consumption_test.go — 攻坚契约的 shared 敏感消费资格回归（A2 消费/取消
// 用例已随 Pi 凭据供给退役，1.3.126）。

import (
	"context"
	"errors"
	"os"
	"testing"
)

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
