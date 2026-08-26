package config

import "testing"

// anthropic 桶预设的 pi/omp 托管同步 opt-in 标记（TerminalPreset.HarnessSync）。
// 本文件覆盖存储往返（models.json 落盘保留 harness_sync）与
// GetMergedTerminalPresets 对 MergedTerminalPreset.HarnessSync 的透传。

func TestTerminalPresetHarnessSyncRoundTrip(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	marked := TerminalPreset{
		Name:        "Marked",
		Provider:    "glm",
		Model:       "glm-5.3",
		HarnessSync: true,
	}
	if err := svc.SaveTerminalPreset("anthropic", "glm/marked", marked); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}
	if err := svc.SaveTerminalPreset("anthropic", "glm/unmarked", TerminalPreset{
		Name:    "Unmarked",
		Provider: "glm",
		Model:   "glm-4",
	}); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}

	presets, err := svc.GetTerminalPresets("anthropic")
	if err != nil {
		t.Fatalf("GetTerminalPresets: %v", err)
	}
	if !presets["glm/marked"].HarnessSync {
		t.Errorf("glm/marked harness_sync lost after save: %+v", presets["glm/marked"])
	}
	if presets["glm/unmarked"].HarnessSync {
		t.Errorf("glm/unmarked harness_sync = true, want false (never marked)")
	}

	// 落盘后由新实例加载仍保留（SaveTerminalPreset 内部 saveLocked 已持久化）。
	svc2 := NewConfigService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	reloaded, err := svc2.GetTerminalPresets("anthropic")
	if err != nil {
		t.Fatalf("GetTerminalPresets(reloaded): %v", err)
	}
	if !reloaded["glm/marked"].HarnessSync {
		t.Errorf("glm/marked harness_sync lost across persistence: %+v", reloaded["glm/marked"])
	}
	if reloaded["glm/unmarked"].HarnessSync {
		t.Errorf("glm/unmarked harness_sync = true after reload, want false")
	}
}

func TestGetMergedTerminalPresetsPassesHarnessSync(t *testing.T) {
	svc := newTestConfigService(t)

	if err := svc.SaveTerminalPreset("anthropic", "glm/marked", TerminalPreset{
		Name:        "Marked",
		Provider:    "glm",
		Model:       "glm-5.3",
		HarnessSync: true,
	}); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}
	if err := svc.SaveTerminalPreset("anthropic", "glm/plain", TerminalPreset{
		Name:    "Plain",
		Provider: "glm",
		Model:   "glm-4",
	}); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}

	merged, err := svc.GetMergedTerminalPresets("anthropic")
	if err != nil {
		t.Fatalf("GetMergedTerminalPresets: %v", err)
	}
	byKey := map[string]MergedTerminalPreset{}
	for _, mp := range merged {
		byKey[mp.Key] = mp
	}
	if !byKey["glm/marked"].HarnessSync {
		t.Errorf("merged glm/marked harness_sync = false, want true (passthrough)")
	}
	if byKey["glm/plain"].HarnessSync {
		t.Errorf("merged glm/plain harness_sync = true, want false (passthrough)")
	}
}
