package config

import (
	"path/filepath"
	"sync"
	"testing"
)

// probeRecorder 记录调度入口收到的 (provider, model)，满足非阻塞契约（直接返回）。
type probeRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *probeRecorder) record(provider, model string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, ModalityProbeKey(provider, model))
}

func (r *probeRecorder) called() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.calls...)
}

func newProbeService(t *testing.T) (*ConfigService, *probeRecorder, string, string) {
	t.Helper()
	dir := t.TempDir()
	exportPath := visionTestPath(t)
	t.Setenv(VisionModelsPathEnv, exportPath)
	// 学习层按测试隔离（TestMain 的全局值被本用例覆盖），防跨用例学习结果污染。
	kbPath := useTestKBPath(t)
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec := &probeRecorder{}
	svc.SetAPIKeyResolver(fakeKeyResolver("sk-test"))
	svc.SetModalityProber(rec.record)
	return svc, rec, exportPath, kbPath
}

// 正向：保存未标记、KB 未知、缓存未探的 preset → 调度一次实弹探测。
func TestSaveTerminalPresetDispatchesModalityProbe(t *testing.T) {
	svc, rec, _, _ := newProbeService(t)
	if err := svc.SaveProvider("acme", openAIProvider()); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	// SaveProvider 会探测 provider DefaultModel（fallback-model，KB 未知）。
	preset := TerminalPreset{Name: "v9", Provider: "acme", Model: "acme-v9"}
	if err := svc.SaveTerminalPreset("openai", "acme/v9", preset); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}
	calls := rec.called()
	want := map[string]bool{"acme/acme-v9": true, "acme/fallback-model": true}
	if len(calls) != len(want) {
		t.Fatalf("probe calls = %v, want keys %v", calls, want)
	}
	for _, c := range calls {
		if !want[c] {
			t.Errorf("unexpected probe target %q (calls=%v)", c, calls)
		}
	}
}

// 负向：手动标记 / KB 已知 / 缓存已探 / anthropic-only provider 均不调度。
func TestSaveTerminalPresetSkipsKnownOrMarked(t *testing.T) {
	svc, rec, _, _ := newProbeService(t)
	if err := svc.SaveProvider("acme", openAIProvider()); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	// anthropic-only provider 不在探测范围。
	if err := svc.SaveProvider("anth", anthropicOnlyProvider()); err != nil {
		t.Fatalf("SaveProvider anth: %v", err)
	}
	before := len(rec.called()) // SaveProvider 的 DefaultModel 探测（fallback-model）
	marked := TerminalPreset{Name: "m", Provider: "acme", Model: "acme-marked", Vision: true}
	if err := svc.SaveTerminalPreset("openai", "acme/m", marked); err != nil {
		t.Fatalf("save marked: %v", err)
	}
	// KB 已知族（k3）不需实弹。
	kbKnown := TerminalPreset{Name: "k3", Provider: "acme", Model: "k3"}
	if err := svc.SaveTerminalPreset("openai", "acme/k3", kbKnown); err != nil {
		t.Fatalf("save kb-known: %v", err)
	}
	// 缓存已有定论（含否定结论）不重复探测。
	if err := svc.RecordModalityProbe("acme", "acme-probed", ModelModalities{}, ModalityProbeSourceImageProbe, true); err != nil {
		t.Fatalf("RecordModalityProbe: %v", err)
	}
	cached := TerminalPreset{Name: "p", Provider: "acme", Model: "acme-probed"}
	if err := svc.SaveTerminalPreset("openai", "acme/p", cached); err != nil {
		t.Fatalf("save cached: %v", err)
	}
	// anthropic-only provider 的 preset 不探测。
	anthPreset := TerminalPreset{Name: "a", Provider: "anth", Model: "acme-unknown"}
	if err := svc.SaveTerminalPreset("anthropic", "anth/a", anthPreset); err != nil {
		t.Fatalf("save anthropic preset: %v", err)
	}
	calls := rec.called()
	if len(calls) != before {
		t.Fatalf("probe calls grew: before=%d after=%v", before, calls)
	}
}

// RecordModalityProbe：有定论结论持久化并可被 Lookup 与视觉导出消费；
// 未决结论不落缓存、不写盘。
func TestRecordModalityProbePersistsConclusive(t *testing.T) {
	svc, _, exportPath, _ := newProbeService(t)
	if err := svc.SaveProvider("acme", openAIProvider()); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	// 未决：不落缓存。
	if err := svc.RecordModalityProbe("acme", "acme-v9", ModelModalities{}, "", false); err != nil {
		t.Fatalf("RecordModalityProbe inconclusive: %v", err)
	}
	if LookupProbedSafe(svc.GetModalityProbeCache(), "acme", "acme-v9").AcceptsImageInput() {
		t.Fatal("inconclusive result must not be cached")
	}
	// 有定论：落缓存、持久化（重载可见）、触发视觉重导出（未标记未知族 preset
	// 凭探测结论进入导出）。
	preset := TerminalPreset{Name: "v9", Provider: "acme", Model: "acme-v9"}
	if err := svc.SaveTerminalPreset("openai", "acme/v9", preset); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}
	if err := svc.RecordModalityProbe("acme", "acme-v9", ModelModalities{Vision: true}, ModalityProbeSourceImageProbe, true); err != nil {
		t.Fatalf("RecordModalityProbe conclusive: %v", err)
	}
	if mods := LookupProbedSafe(svc.GetModalityProbeCache(), "acme", "acme-v9"); !mods.Vision {
		t.Fatalf("cache lookup = %+v, want vision=true cached", mods)
	}
	// 持久化验证：新 service 实例从磁盘重载。
	svc2 := NewConfigService(svc.configPathDir())
	if err := svc2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if mods := LookupProbedSafe(svc2.GetModalityProbeCache(), "acme", "acme-v9"); !mods.Vision {
		t.Errorf("reloaded cache lookup = %+v, want persisted vision=true", mods)
	}
	// 视觉导出联动：探测结论让未标记 preset 出现在导出文件。
	f := readVisionExport(t, exportPath)
	m := findVisionModel(t, f, "acme/v9")
	if len(m.Capabilities) != 1 || m.Capabilities[0] != "image" {
		t.Errorf("capabilities = %v, want [image] from probe cache", m.Capabilities)
	}
}

// configPathDir 测试辅助：取 service 的配置目录。
func (s *ConfigService) configPathDir() string {
	return filepath.Dir(s.configPath)
}
