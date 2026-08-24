package config

import (
	"os"
	"path/filepath"
	"testing"
)

// useTestKBPath 把学习层指到测试临时文件（覆盖 TestMain 的全局隔离值，
// 每个用例独立文件互不影响）。
func useTestKBPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kb.json")
	t.Setenv(ModalityKBPathEnv, path)
	return path
}

// 正向：写入-读取回环；模型 id 规范化（大小写 + provider 前缀）同键命中。
func TestModalityKBRoundTrip(t *testing.T) {
	path := useTestKBPath(t)
	if err := RecordLearnedModalities("Acme/Vendor/ACME-V9", ModalityProbeSourceImageProbe, ModelModalities{Vision: true}); err != nil {
		t.Fatalf("RecordLearnedModalities: %v", err)
	}
	// 文件落盘且权限收紧。
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatalf("kb file not written: %v", err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Errorf("kb file mode = %o, want 0600", stat.Mode().Perm())
	}
	mods, known := LookupModelModalities("any", "acme-v9")
	if !known || !mods.Vision {
		t.Errorf("lookup learned = %+v,%v, want vision=true known=true", mods, known)
	}
	// 同 id 小写/前缀变体同键。
	if _, known := LookupModelModalities("other", "vendor2/acme-v9"); !known {
		t.Error("learned entry must hit regardless of provider prefix")
	}
}

// 负向：缺失/损坏文件按空表处理，不崩溃不报错。
func TestModalityKBMissingAndCorrupt(t *testing.T) {
	path := useTestKBPath(t)
	if _, known := LookupModelModalities("p", "anything"); known {
		t.Error("missing kb file must yield unknown")
	}
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, known := LookupModelModalities("p", "anything"); known {
		t.Error("corrupt kb file must yield unknown")
	}
	// 损坏后仍可写入自愈。
	if err := RecordLearnedModalities("acme-x", ModalityProbeSourceImageProbe, ModelModalities{Vision: true}); err != nil {
		t.Fatalf("write over corrupt kb: %v", err)
	}
	if mods, known := LookupModelModalities("p", "acme-x"); !known || !mods.Vision {
		t.Error("kb must self-heal after corrupt file")
	}
}

// 否定结论（确认纯文本）落库并抑制后续探测；学习层优先于内置族规则。
func TestModalityKBLearnedNegativeAndPrecedence(t *testing.T) {
	useTestKBPath(t)
	// 否定结论：LookupModelModalities 的 known=true 是 needsModalityProbeLocked
	// 抑制实弹探测的依据。
	if err := RecordLearnedModalities("acme-text-1", ModalityProbeSourceImageProbe, ModelModalities{}); err != nil {
		t.Fatalf("record negative: %v", err)
	}
	mods, known := LookupModelModalities("p", "acme-text-1")
	if !known {
		t.Fatal("learned negative must be known=true (suppresses re-probe)")
	}
	if mods.AcceptsImageInput() {
		t.Error("learned negative must not accept image input")
	}
	// 学习层（实证）优先于内置族规则：k3 族规则判定 vision，但实证否定优先。
	if err := RecordLearnedModalities("k3", ModalityProbeSourceImageProbe, ModelModalities{}); err != nil {
		t.Fatalf("record k3 negative: %v", err)
	}
	if InferModelModalities("kimi", "k3").Vision {
		t.Error("learned empirical result must override built-in family rule")
	}
}

// RecordModalityProbe 联动：有定论结论同时写 AppConfig 缓存与设备学习层。
func TestRecordModalityProbeWritesLearnedKB(t *testing.T) {
	svc, _, _, kbPath := newProbeService(t)
	if err := svc.SaveProvider("acme", openAIProvider()); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := svc.RecordModalityProbe("acme", "acme-learned", ModelModalities{Vision: true}, ModalityProbeSourceModelsAPI, true); err != nil {
		t.Fatalf("RecordModalityProbe: %v", err)
	}
	if _, err := os.Stat(kbPath); err != nil {
		t.Fatalf("learned kb not written: %v", err)
	}
	if mods, known := LookupModelModalities("other-provider", "acme-learned"); !known || !mods.Vision {
		t.Error("probe conclusion must generalize across providers via learned kb")
	}
	// 学习层已知 → 后续同 id 预设保存不再调度探测。svc2 复用本用例的 KB 路径
	//（首个 newProbeService 的 t.Setenv 对整个用例生效），但用独立配置目录。
	svc2 := NewConfigService(t.TempDir())
	if err := svc2.Load(); err != nil {
		t.Fatalf("svc2 Load: %v", err)
	}
	rec2 := &probeRecorder{}
	svc2.SetAPIKeyResolver(fakeKeyResolver("sk-test"))
	svc2.SetModalityProber(rec2.record)
	if err := svc2.SaveProvider("acme", openAIProvider()); err != nil {
		t.Fatalf("SaveProvider svc2: %v", err)
	}
	preset := TerminalPreset{Name: "l", Provider: "acme", Model: "acme-learned"}
	if err := svc2.SaveTerminalPreset("openai", "acme/l", preset); err != nil {
		t.Fatalf("SaveTerminalPreset svc2: %v", err)
	}
	for _, c := range rec2.called() {
		if c == "acme/acme-learned" {
			t.Errorf("learned model must not be re-probed, calls=%v", rec2.called())
		}
	}
}
