package wslsupply

// s1_fix_round_test.go — S1 diting FIX 修复轮的针对性测试：
//   - F2：PendingSources 在「来源的全部滋养单元成功」后清除（推送/启动补漏
//     完成收敛 pendingPush；任一单元失败/阻断保留标记）；
//   - F3：跨代（distro 重装）重新同意不继承旧代 hash——首次发布成功而非
//     local-deletion-frozen；
//   - F4：检查时间按 CLI 作用域记录（pi 作用域探测不再错配 shared 面板）。

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/launcher"
)

func markDirtyOrFail(t *testing.T, c *Coordinator, source string, rev uint64) {
	t.Helper()
	if err := c.MarkDirty(source, rev); err != nil {
		t.Fatal(err)
	}
}

// TestDirtySourcesClearedAfterFullSuccess（F2）：providers 来源需全部三个托管
// 单元成功才清除；pi-login-presets 只需 managed-models；单元阻断时保留。
func TestDirtySourcesClearedAfterFullSuccess(t *testing.T) {
	c, _, piSrc, sharedSrc := newS1Coordinator(t)
	piSrc.set(PiManagedModelsSpec{Providers: map[string]any{"providers": map[string]any{
		"amagi-x": map[string]any{"baseUrl": "u"},
	}}})
	sharedSrc.vision = func() ([]byte, error) { return []byte(`{"version":1,"updated_at":"t","models":[]}`), nil }

	markDirtyOrFail(t, c, DirtySourceProviders, 1)
	markDirtyOrFail(t, c, DirtySourcePiLoginPresets, 2)

	// pi 作用域成功：pi-login-presets 清除；providers 保留（vision/modality 未跑）。
	if res := runUnit(t, c, CLIPi, UnitPiManagedModels); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("managed-models run: %+v", res.Results)
	}
	st := c.GetStatus("Ubuntu", CLIPi)
	if _, still := st.PendingSources[DirtySourcePiLoginPresets]; still {
		t.Fatalf("pi-login-presets must clear after managed-models success: %v", st.PendingSources)
	}
	if _, still := st.PendingSources[DirtySourceProviders]; !still {
		t.Fatal("providers must stay pending until vision/modality also succeed")
	}

	// 未同意的 vision 阻断：providers 仍保留。
	if res := runUnit(t, c, CLICommon, UnitSharedVisionExport); res.Results[0].Outcome != UnitBlocked {
		t.Fatalf("unconsented vision must block: %+v", res.Results)
	}
	st = c.GetStatus("Ubuntu", CLICommon)
	if _, still := st.PendingSources[DirtySourceProviders]; !still {
		t.Fatal("providers must stay pending while vision is blocked")
	}

	// 同意 + vision/modality 成功 → providers 清空，pendingPush 收敛。
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatal(err)
	}
	if res := runUnit(t, c, CLICommon, UnitSharedVisionExport); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("vision publish: %+v", res.Results)
	}
	if res := runUnit(t, c, CLICommon, UnitSharedModalityKB); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("modality fill: %+v", res.Results)
	}
	st = c.GetStatus("Ubuntu", CLICommon)
	if len(st.PendingSources) != 0 {
		t.Fatalf("all dirty sources must clear after full success: %v", st.PendingSources)
	}
}

// TestDirtySourcesKeptOnFailure（F2）：单元失败保留标记（下次补漏后再清）。
func TestDirtySourcesKeptOnFailure(t *testing.T) {
	c, fp, piSrc, _ := newS1Coordinator(t)
	piSrc.set(PiManagedModelsSpec{Providers: map[string]any{"providers": map[string]any{
		"amagi-x": map[string]any{"baseUrl": "u"},
	}}})
	markDirtyOrFail(t, c, DirtySourcePiLoginPresets, 1)
	// 写失败注入：chmod 补偿恒败（managed-models 敏感 → fail closed）。
	fp.chmodErr = errBoom("wsl.exe dead")
	res := runUnit(t, c, CLIPi, UnitPiManagedModels)
	if res.Results[0].Outcome != UnitFailed {
		t.Fatalf("injected failure must fail the unit: %+v", res.Results)
	}
	st := c.GetStatus("Ubuntu", CLIPi)
	if _, still := st.PendingSources[DirtySourcePiLoginPresets]; !still {
		t.Fatal("failed unit must keep its dirty source pending")
	}
	// 恢复后重跑成功 → 清除（补漏收敛）。
	fp.chmodErr = nil
	if res := runUnit(t, c, CLIPi, UnitPiManagedModels); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("retry run: %+v", res.Results)
	}
	st = c.GetStatus("Ubuntu", CLIPi)
	if _, still := st.PendingSources[DirtySourcePiLoginPresets]; still {
		t.Fatalf("retry success must clear the dirty source: %v", st.PendingSources)
	}
}

type errBoom string

func (e errBoom) Error() string { return string(e) }

// TestGrantUnitConsentCrossGeneration（F3）：旧代同意+发布 → distro 重装
// （generation 变化）→ 重新同意 → 新代首次发布成功（不继承旧代 lastWritten
// 而误判 local-deletion-frozen）。
func TestGrantUnitConsentCrossGeneration(t *testing.T) {
	c, fp, _, sharedSrc := newS1Coordinator(t)
	content := `{"version":1,"updated_at":"t1","models":[{"id":"p/m","api_key":"k"}]}`
	sharedSrc.vision = func() ([]byte, error) { return []byte(content), nil }
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatal(err)
	}
	if res := runUnit(t, c, CLICommon, UnitSharedVisionExport); res.Results[0].Outcome != UnitApplied {
		t.Fatalf("gen1 publish: %+v", res.Results)
	}

	// distro 重装：machine-id 变化 → generation 变化；新 distro 无目标文件。
	fp.identity.MachineID = "machine-reinstalled"
	visionPath := filepath.Join(fp.uncRoot, "home/u/.agents/amagi-media-models.json")
	os.Remove(visionPath)

	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatalf("re-consent on new generation: %v", err)
	}
	res := runUnit(t, c, CLICommon, UnitSharedVisionExport)
	if res.Results[0].Outcome != UnitApplied {
		t.Fatalf("cross-generation consent must allow first publish, got %+v (F3: old-gen hash inherited → local-deletion-frozen)", res.Results)
	}
	got, err := os.ReadFile(visionPath)
	if err != nil || string(got) != content {
		t.Fatalf("new-generation publish must write the export verbatim: %q err=%v", got, err)
	}
	// 新代登记不携带旧代 hash 基线。
	st := c.GetStatus("Ubuntu", CLICommon)
	for _, u := range st.Units {
		if u.Unit == UnitSharedVisionExport {
			_ = u // UnitStatus 不含 hash（脱敏）；hash 基线由下方状态文件字节断言覆盖。
		}
	}
	raw, _ := os.ReadFile(filepath.Join(c.state.path))
	body := string(raw)
	if count := substringCount(body, "lastWrittenHash"); count != 1 {
		t.Fatalf("state must carry exactly one lastWrittenHash (new-gen entry), got %d:\n%s", count, body)
	}
}

func substringCount(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
			i += len(sub) - 1
		}
	}
	return n
}

// TestGetStatusPerCLICheckTime（F4）：检查时间按 CLI 作用域——pi 探测不点亮
// shared 的 Checked；shared 探测后各自持有时 own 时间。
func TestGetStatusPerCLICheckTime(t *testing.T) {
	c, _, _, _ := newS1Coordinator(t)
	piTarget, err := c.ResolveTarget(context.Background(), "Ubuntu", CLIPi)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Inspect(context.Background(), piTarget); err != nil {
		t.Fatal(err)
	}
	piStatus := c.GetStatus("Ubuntu", CLIPi)
	sharedStatus := c.GetStatus("Ubuntu", CLICommon)
	if !piStatus.Checked {
		t.Fatal("pi scope must be checked after pi inspect")
	}
	if sharedStatus.Checked {
		t.Fatal("shared scope must stay unchecked until its own inspect (F4: no cross-CLI time bleed)")
	}

	sharedTarget, err := c.ResolveTarget(context.Background(), "Ubuntu", CLICommon)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Inspect(context.Background(), sharedTarget); err != nil {
		t.Fatal(err)
	}
	sharedStatus = c.GetStatus("Ubuntu", CLICommon)
	if !sharedStatus.Checked || sharedStatus.CheckedAt.IsZero() {
		t.Fatal("shared scope must be checked after its own inspect")
	}

	_ = launcher.SyncWSLPiLoginAuthFresh // 文档化：A2 链不受 per-CLI 检查时间影响（判定在适配器内）
}
