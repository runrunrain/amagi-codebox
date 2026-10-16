package wslsupply

// s1_adapters_test.go — S1 托管面适配器（pi/managed-models、shared/vision-export、
// shared/modality-kb）与同意门的行为钉子。全部经注入的假探测面与桩源运行，
// 不触达真实 WSL。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"amagi-codebox/internal/launcher"
)

// stubPiSource 是 PiManagedModelsSource 桩（可变期望集）。
type stubPiSource struct {
	mu  atomic.Value // PiManagedModelsSpec
	err bool
}
type stubSharedSource struct {
	vision   func() ([]byte, error)
	modality func() ([]byte, error)
}

func (s *stubPiSource) DesiredPiManagedModels() PiManagedModelsSpec {
	if s.err {
		return PiManagedModelsSpec{}
	}
	return s.mu.Load().(PiManagedModelsSpec)
}
func (s *stubPiSource) set(spec PiManagedModelsSpec) { s.mu.Store(spec) }

func (s *stubSharedSource) VisionExportBytes() ([]byte, error) { return s.vision() }
func (s *stubSharedSource) ModalityKBBytes() ([]byte, error)   { return s.modality() }

// newS1Coordinator 构造带 S1 适配器的协调器（假探测面 + 桩源）。
func newS1Coordinator(t *testing.T) (*Coordinator, *fakeProbes, *stubPiSource, *stubSharedSource) {
	t.Helper()
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
	piSrc := &stubPiSource{}
	piSrc.set(PiManagedModelsSpec{Providers: map[string]any{}, RetainProviders: []string{}, LoginOverrides: []PiLoginModelOverrideSpec{}})
	sharedSrc := &stubSharedSource{
		vision:   func() ([]byte, error) { return nil, os.ErrNotExist },
		modality: func() ([]byte, error) { return nil, nil },
	}
	c.RegisterProjection(NewPiManagedModelsAdapter(piSrc))
	c.RegisterProjection(NewVisionExportAdapter(sharedSrc))
	c.RegisterProjection(NewModalityKBAdapter(sharedSrc))
	return c, fp, piSrc, sharedSrc
}

func piModelsFile(fp *fakeProbes) string {
	return filepath.Join(fp.uncRoot, "home/u/.pi/agent/models.json")
}

func runUnit(t *testing.T, c *Coordinator, cli CLI, unit UnitID) RunResult {
	t.Helper()
	res, err := c.RunUnits(context.Background(), "Ubuntu", cli, ReasonLaunch, []UnitID{unit})
	if err != nil {
		t.Fatalf("RunUnits(%s): %v", unit, err)
	}
	return res
}

// ---------------------------------------------------------------------------
// pi/managed-models
// ---------------------------------------------------------------------------

func TestPiManagedModelsPublishAndMerge(t *testing.T) {
	c, fp, piSrc, _ := newS1Coordinator(t)
	// 旧 distro（S0 前播种树）：已有非 amagi provider、未知顶层字段与旧 amagi 条目。
	os.MkdirAll(filepath.Dir(piModelsFile(fp)), 0o700)
	os.WriteFile(piModelsFile(fp), []byte(`{
  "providers": {
    "user-own": {"baseUrl": "https://user.example"},
    "amagi-stale": {"baseUrl": "https://stale.example"}
  },
  "equivalence": {"keep": true}
}`), 0o600)

	piSrc.set(PiManagedModelsSpec{
		Providers: map[string]any{"providers": map[string]any{
			"amagi-new": map[string]any{"baseUrl": "https://new.example", "apiKey": "k"},
		}},
		RetainProviders: []string{},
		LoginOverrides:  []PiLoginModelOverrideSpec{{Provider: "openai", Model: "gpt-5.2", ContextWindow: 400000}},
	})
	res := runUnit(t, c, CLIPi, UnitPiManagedModels)
	if res.Results[0].Outcome != UnitApplied {
		t.Fatalf("existing unrecorded target must merge (ManagedNamespace), got %+v", res.Results)
	}
	raw, _ := os.ReadFile(piModelsFile(fp))
	body := string(raw)
	for _, want := range []string{`"user-own"`, `"keep": true`, `"amagi-new"`, `"contextWindow": 400000`} {
		if !strings.Contains(body, want) {
			t.Fatalf("merged models.json missing %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, "amagi-stale") {
		t.Fatalf("removed managed provider must be pruned:\n%s", body)
	}

	// 无变化第二次运行：UnitFresh + 零 chmod（零写入）。
	chmodBefore := atomic.LoadInt32(&fp.chmodCalls)
	res = runUnit(t, c, CLIPi, UnitPiManagedModels)
	if res.Results[0].Outcome != UnitFresh {
		t.Fatalf("unchanged source+target must be fresh, got %+v", res.Results)
	}
	if got := atomic.LoadInt32(&fp.chmodCalls); got != chmodBefore {
		t.Fatalf("unchanged run must not write (chmod calls %d -> %d)", chmodBefore, got)
	}
}

func TestPiManagedModelsCorruptTargetRefused(t *testing.T) {
	c, fp, piSrc, _ := newS1Coordinator(t)
	os.MkdirAll(filepath.Dir(piModelsFile(fp)), 0o700)
	corrupt := []byte(`{"providers": {"broken"`)
	os.WriteFile(piModelsFile(fp), corrupt, 0o600)
	piSrc.set(PiManagedModelsSpec{Providers: map[string]any{"providers": map[string]any{
		"amagi-x": map[string]any{"baseUrl": "u"},
	}}})
	res := runUnit(t, c, CLIPi, UnitPiManagedModels)
	if res.Results[0].Outcome != UnitFailed || res.Results[0].Detail != "target-corrupt" {
		t.Fatalf("corrupt target must fail with target-corrupt, got %+v", res.Results)
	}
	got, _ := os.ReadFile(piModelsFile(fp))
	if string(got) != string(corrupt) {
		t.Fatal("corrupt file must be retained verbatim (never overwritten)")
	}
}

func TestPiManagedModelsPartialBuildFailureRetains(t *testing.T) {
	c, fp, piSrc, _ := newS1Coordinator(t)
	piSrc.set(PiManagedModelsSpec{
		Providers: map[string]any{"providers": map[string]any{
			"amagi-good": map[string]any{"baseUrl": "https://good.example"},
		}},
		RetainProviders: []string{"amagi-bad"},
		BuildErrors:     []string{`provider "bad" 构建失败，保留 WSL 侧旧条目: boom`},
	})
	// 首写建立基线。
	runUnit(t, c, CLIPi, UnitPiManagedModels)
	// 第二轮：amagi-bad 仍构建失败、amagi-good 内容变化。
	piSrc.set(PiManagedModelsSpec{
		Providers: map[string]any{"providers": map[string]any{
			"amagi-good": map[string]any{"baseUrl": "https://good2.example"},
		}},
		RetainProviders: []string{"amagi-bad"},
		BuildErrors:     []string{`provider "bad" 构建失败，保留 WSL 侧旧条目: boom2`},
	})
	res := runUnit(t, c, CLIPi, UnitPiManagedModels)
	if res.Results[0].Outcome != UnitApplied {
		t.Fatalf("partial build failure must not fail the unit, got %+v", res.Results)
	}
	if !strings.Contains(res.Results[0].Detail, "amagi-bad") && !strings.Contains(res.Results[0].Detail, "构建失败") {
		t.Fatalf("build failures must surface in result detail: %q", res.Results[0].Detail)
	}
	raw, _ := os.ReadFile(piModelsFile(fp))
	if !strings.Contains(string(raw), "amagi-good") {
		t.Fatal("healthy provider must still be written")
	}
}

func TestPiManagedModelsExternalEditStillMerges(t *testing.T) {
	// ManagedNamespace：目标被外部修改不冻结——合并保留外部内容，恢复命名空间。
	c, fp, piSrc, _ := newS1Coordinator(t)
	piSrc.set(PiManagedModelsSpec{Providers: map[string]any{"providers": map[string]any{
		"amagi-x": map[string]any{"baseUrl": "https://x1"},
	}}})
	runUnit(t, c, CLIPi, UnitPiManagedModels)
	// 外部编辑（新增用户 provider + 破坏 amagi 条目）。
	os.WriteFile(piModelsFile(fp), []byte(`{"providers": {
		"user-late": {"baseUrl": "https://late.example"},
		"amagi-x": {"baseUrl": "https://tampered.example"}
	}}`), 0o600)
	res := runUnit(t, c, CLIPi, UnitPiManagedModels)
	if res.Results[0].Outcome != UnitApplied {
		t.Fatalf("managed namespace must not freeze on external edit, got %+v", res.Results)
	}
	raw, _ := os.ReadFile(piModelsFile(fp))
	if !strings.Contains(string(raw), "user-late") || !strings.Contains(string(raw), "https://x1") {
		t.Fatalf("merge must preserve external content and restore owned namespace:\n%s", raw)
	}
	if strings.Contains(string(raw), "tampered") {
		t.Fatalf("owned namespace entry must be restored:\n%s", raw)
	}
}

// ---------------------------------------------------------------------------
// shared/vision-export
// ---------------------------------------------------------------------------

func visionFile(fp *fakeProbes) string {
	return filepath.Join(fp.uncRoot, "home/u/.agents/amagi-media-models.json")
}

func TestVisionExportConsentFlow(t *testing.T) {
	c, fp, _, sharedSrc := newS1Coordinator(t)
	content := `{"version":1,"updated_at":"2026-10-16T10:00:00Z","models":[{"id":"p/m","api_key":"sk-secret"}]}`
	sharedSrc.vision = func() ([]byte, error) { return []byte(content), nil }

	// 未同意 → blocked awaiting-consent，不写文件。
	res := runUnit(t, c, CLICommon, UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "awaiting-consent" {
		t.Fatalf("unconsented publish must block, got %+v", res.Results)
	}
	if _, err := os.Stat(visionFile(fp)); !os.IsNotExist(err) {
		t.Fatal("no file may be written before consent")
	}
	st := c.GetStatus("Ubuntu", CLICommon)
	if len(st.Units) != 1 || st.Units[0].Consented {
		t.Fatalf("status must expose awaiting-consent face: %+v", st.Units)
	}

	// 同意（不可解析目标拒绝；正常目标落登记）。
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatalf("GrantUnitConsent: %v", err)
	}

	// 同意后首发布：写入源字节、chmod 600 补偿。
	res = runUnit(t, c, CLICommon, UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitApplied {
		t.Fatalf("consented publish must apply, got %+v", res.Results)
	}
	got, _ := os.ReadFile(visionFile(fp))
	if string(got) != content {
		t.Fatalf("published bytes must equal host export verbatim:\n%s", got)
	}
	chmodAfterPublish := atomic.LoadInt32(&fp.chmodCalls)

	// 来源 updated_at 变化但规范化内容不变 → 零动作。
	content2 := `{"version":1,"updated_at":"2026-10-16T11:00:00Z","models":[{"id":"p/m","api_key":"sk-secret"}]}`
	sharedSrc.vision = func() ([]byte, error) { return []byte(content2), nil }
	res = runUnit(t, c, CLICommon, UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitFresh {
		t.Fatalf("normalized-unchanged source must be fresh, got %+v", res.Results)
	}
	if got := atomic.LoadInt32(&fp.chmodCalls); got != chmodAfterPublish {
		t.Fatalf("no-op run must not write (chmod %d -> %d)", chmodAfterPublish, got)
	}

	// 来源内容变化（撤回全部条目）→ 更新目标。
	withdrawn := `{"version":1,"updated_at":"2026-10-16T12:00:00Z","models":[]}`
	sharedSrc.vision = func() ([]byte, error) { return []byte(withdrawn), nil }
	res = runUnit(t, c, CLICommon, UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitApplied {
		t.Fatalf("withdrawal must update target, got %+v", res.Results)
	}
	got2, _ := os.ReadFile(visionFile(fp))
	if strings.Contains(string(got2), "sk-secret") {
		t.Fatalf("withdrawn entries must not stay in distro copy:\n%s", got2)
	}
}

func TestVisionExportHandEditFreezesAndWithdrawalUnconfirmed(t *testing.T) {
	c, fp, _, sharedSrc := newS1Coordinator(t)
	content := `{"version":1,"updated_at":"t1","models":[{"id":"p/m","api_key":"sk-secret"}]}`
	sharedSrc.vision = func() ([]byte, error) { return []byte(content), nil }
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatal(err)
	}
	runUnit(t, c, CLICommon, UnitSharedVisionExport)

	// 手改目标 + 来源变化（撤回）→ 冻结且「密钥副本未确认撤回」可见。
	os.WriteFile(visionFile(fp), []byte(`{"version":1,"models":[{"id":"p/m","api_key":"sk-secret"}],"user":"edited"}`), 0o600)
	withdrawn := `{"version":1,"updated_at":"t2","models":[]}`
	sharedSrc.vision = func() ([]byte, error) { return []byte(withdrawn), nil }
	res := runUnit(t, c, CLICommon, UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "withdrawal-unconfirmed-frozen" {
		t.Fatalf("withdrawal over frozen target must be visible, got %+v", res.Results)
	}
	got, _ := os.ReadFile(visionFile(fp))
	if !strings.Contains(string(got), "sk-secret") {
		t.Fatal("frozen target content must be retained (not silently deleted nor reported deleted)")
	}
	st := c.GetStatus("Ubuntu", CLICommon)
	if len(st.Units) != 1 || !st.Units[0].Frozen || st.Units[0].FrozenReason != "withdrawal-unconfirmed-frozen" {
		t.Fatalf("frozen withdrawal must persist in status: %+v", st.Units)
	}
}

func TestVisionExportUnmanagedExistingFrozen(t *testing.T) {
	c, fp, _, sharedSrc := newS1Coordinator(t)
	sharedSrc.vision = func() ([]byte, error) {
		return []byte(`{"version":1,"updated_at":"t","models":[]}`), nil
	}
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(visionFile(fp)), 0o700)
	os.WriteFile(visionFile(fp), []byte(`{"user":"own-file"}`), 0o600)
	res := runUnit(t, c, CLICommon, UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "unmanaged-existing" {
		t.Fatalf("user-owned file must freeze pending takeover, got %+v", res.Results)
	}
	got, _ := os.ReadFile(visionFile(fp))
	if string(got) != `{"user":"own-file"}` {
		t.Fatal("user file must be retained verbatim")
	}
}

// ---------------------------------------------------------------------------
// shared/modality-kb
// ---------------------------------------------------------------------------

func modalityFile(fp *fakeProbes) string {
	return filepath.Join(fp.uncRoot, "home/u/.agents/amagi-modalities.json")
}

func TestModalityKBGapFillAndConflicts(t *testing.T) {
	c, fp, _, sharedSrc := newS1Coordinator(t)
	host := `{"version":1,"updated_at":"t1","models":{"glm-5.2":{"vision":true,"video":false,"source":"probe"},"GPT/X":{"vision":true}}}`
	sharedSrc.modality = func() ([]byte, error) { return []byte(host), nil }

	res := runUnit(t, c, CLICommon, UnitSharedModalityKB)
	if res.Results[0].Outcome != UnitApplied {
		t.Fatalf("first fill must apply, got %+v", res.Results)
	}
	raw, _ := os.ReadFile(modalityFile(fp))
	if !strings.Contains(string(raw), "glm-5.2") || !strings.Contains(string(raw), "x") {
		t.Fatalf("missing entries must be filled with normalized ids:\n%s", raw)
	}

	// distro 侧已有不同结论：保留 + 冲突报告，不反写宿主。
	os.WriteFile(modalityFile(fp), []byte(`{"version":1,"updated_at":"t2","models":{
		"glm-5.2":{"vision":false,"video":false},
		"k3":{"vision":true}
	}}`), 0o600)
	res = runUnit(t, c, CLICommon, UnitSharedModalityKB)
	if res.Results[0].Outcome != UnitApplied {
		t.Fatalf("conflict fill must apply, got %+v", res.Results)
	}
	if !strings.Contains(res.Results[0].Detail, "glm-5.2") || !strings.Contains(res.Results[0].Detail, "保留 WSL 侧") {
		t.Fatalf("conflict must be reported in detail: %q", res.Results[0].Detail)
	}
	raw, _ = os.ReadFile(modalityFile(fp))
	if !strings.Contains(string(raw), `"k3"`) {
		t.Fatalf("distro-only entries must be kept:\n%s", raw)
	}
	// 宿主不被反写（源未变化）。
	res = runUnit(t, c, CLICommon, UnitSharedModalityKB)
	if res.Results[0].Outcome != UnitFresh {
		t.Fatalf("after fill, unchanged run must be fresh, got %+v", res.Results)
	}

	// 损坏的 distro 文件：拒绝不覆盖。
	os.WriteFile(modalityFile(fp), []byte(`{"version":2,"models":{}}`), 0o600)
	res = runUnit(t, c, CLICommon, UnitSharedModalityKB)
	if res.Results[0].Outcome != UnitFailed || res.Results[0].Detail != "target-corrupt" {
		t.Fatalf("schema-mismatched distro kb must fail, got %+v", res.Results)
	}
	got, _ := os.ReadFile(modalityFile(fp))
	if !strings.Contains(string(got), `"version":2`) {
		t.Fatal("corrupt distro file must be retained verbatim")
	}
}

func TestGrantUnitConsentValidations(t *testing.T) {
	c, _, _, _ := newS1Coordinator(t)
	// 非同意面单元拒绝。
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLIPi, UnitPiManagedModels); err == nil {
		t.Fatal("non-consent unit must be rejected")
	}
	// 身份不可解析 → 拒绝（不落登记）。
	bad := newFakeProbes(t)
	bad.identity.OK = false
	c2 := newCoordinatorWith(t, bad, nil)
	if err := c2.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err == nil {
		t.Fatal("unresolvable target must reject consent")
	}
}

func TestGetStatusExposesConsentedFlag(t *testing.T) {
	c, _, _, sharedSrc := newS1Coordinator(t)
	sharedSrc.vision = func() ([]byte, error) { return []byte(`{"version":1,"models":[]}`), nil }
	runUnit(t, c, CLICommon, UnitSharedVisionExport) // awaiting-consent 记入 checked
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatal(err)
	}
	st := c.GetStatus("Ubuntu", CLICommon)
	for _, u := range st.Units {
		if u.Unit == UnitSharedVisionExport && !u.Consented {
			t.Fatal("consent flag must surface in sanitized status")
		}
	}
}
