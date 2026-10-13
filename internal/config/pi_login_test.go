package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiLoginPresetPersistenceAndIsolation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	// Same native name as an ordinary API provider is intentionally allowed.
	if err := svc.SaveProvider("openai-codex", Provider{OpenAI: &OpenAIFormat{Enabled: true}, DefaultModel: "api-model"}); err != nil {
		t.Fatal(err)
	}
	p := PiLoginPreset{Name: "Daily / 日常", Provider: "openai-codex", Model: "native-model", Thinking: "xhigh"}
	if err := svc.SavePiLoginPreset(p); err != nil {
		t.Fatal(err)
	}
	key := PiLoginPresetKey(p.Provider, p.Name)
	loaded := NewConfigService(dir)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	got, err := loaded.ResolvePiLoginPreset(key)
	if err != nil || *got != p {
		t.Fatalf("persisted: %v %v", got, err)
	}
	if _, err := loaded.GetProvider(PiLoginProviderID(p.Provider)); err == nil {
		t.Fatal("login source became API provider")
	}
	if len(loaded.GetProviders()) != 1 {
		t.Fatal("provider collision")
	}
	for _, bucket := range []string{"anthropic", "openai", "pi", "omp", "codex"} {
		presets, err := loaded.GetMergedTerminalPresets(bucket)
		if err != nil || len(presets) != 0 {
			t.Fatalf("login escaped into %s: %v %v", bucket, presets, err)
		}
	}
	if err := loaded.SaveProvider(PiLoginProviderID(p.Provider), Provider{}); err == nil {
		t.Fatal("reserved source id accepted as API")
	}
	if err := loaded.SaveTerminalPreset("openai", "api/login", TerminalPreset{Provider: PiLoginProviderID(p.Provider)}); err == nil {
		t.Fatal("login accepted as public preset")
	}
	if err := loaded.SaveTerminalPreset("openai", key, TerminalPreset{Provider: "openai-codex"}); err == nil {
		t.Fatal("reserved key accepted as public preset")
	}
	// Dedicated CRUD does not trigger media export or expose a token field.
	if _, err := os.Stat(filepath.Join(home, ".agents", "amagi-media-models.json")); !os.IsNotExist(err) {
		t.Fatal("login wrote media export")
	}
	body, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if strings.Contains(string(body), "access") || strings.Contains(string(body), "refresh") {
		t.Fatal("credential fields persisted")
	}
	snapshot, _ := loaded.GetPiLoginPresets()
	delete(snapshot, key)
	if _, err := loaded.ResolvePiLoginPreset(key); err != nil {
		t.Fatal("returned map mutates service")
	}
	if err := loaded.DeletePiLoginPreset(key); err != nil {
		t.Fatal(err)
	}
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := loaded.ResolvePiLoginPreset(key); err == nil {
		t.Fatal("deleted preset resurrected")
	}
}

func TestPiLoginPresetSaveFailureKeepsState(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if err := os.Mkdir(filepath.Join(dir, "models.json.tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := svc.SavePiLoginPreset(PiLoginPreset{Name: "new", Provider: "native", Model: "model"}); err == nil {
		t.Fatal("expected persistence failure")
	}
	presets, err := svc.GetPiLoginPresets()
	if err != nil || len(presets) != 0 {
		t.Fatalf("failed save changed memory: %v %v", presets, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if string(before) != string(after) {
		t.Fatal("failed save changed disk")
	}
	if PiLoginPresetKey("a/b", "c") == PiLoginPresetKey("a", "b/c") {
		t.Fatal("namespace ambiguity")
	}
}

func TestPiLoginPresetValidation(t *testing.T) {
	svc := NewConfigService(t.TempDir())
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []PiLoginPreset{{}, {Name: "n", Provider: "p"}, {Name: "n", Provider: "p", Model: "m", Thinking: "bogus"}} {
		if svc.SavePiLoginPreset(p) == nil {
			t.Fatalf("invalid preset accepted: %+v", p)
		}
	}
	if svc.DeletePiLoginPreset("ordinary/preset") == nil {
		t.Fatal("ordinary key accepted")
	}
}

// R1 回归：字面 "pi-login" 普通 provider 的公共预设不被登录命名空间劫持；
// 分类以「精确命中登录存储 / 结构化登录来源 id」为准，前缀同形不算登录。
func TestPiLoginClassificationOrdinaryProviderStableKeys(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveProvider("pi-login", Provider{OpenAI: &OpenAIFormat{Enabled: true}, DefaultModel: "api-model"}); err != nil {
		t.Fatalf("SaveProvider(pi-login): %v", err)
	}
	for _, key := range []string{"pi-login/Daily", "pi-login/Foo/Bar", "pi-login/Zm9v"} {
		if err := svc.SaveTerminalPreset("openai", key, TerminalPreset{Name: "n", Provider: "pi-login", Model: "api-model"}); err != nil {
			t.Fatalf("public key %s rejected: %v", key, err)
		}
		if _, isLogin, err := svc.ResolvePiLoginLaunch(key, "pi-login"); isLogin || err != nil {
			t.Fatalf("public key %s classified login=%v err=%v", key, isLogin, err)
		}
		if err := svc.RejectPiLoginLaunchRefs("pi-login", key); err != nil {
			t.Fatalf("public key %s rejected: %v", key, err)
		}
	}
	if _, err := svc.ResolvePiLoginPreset("pi-login/Daily"); err == nil {
		t.Fatal("public key resolved as login preset")
	}
	// 登录来源 id 与已存登录 key 的拒绝语义保持。
	login := PiLoginPreset{Name: "L", Provider: "native", Model: "m"}
	if err := svc.SavePiLoginPreset(login); err != nil {
		t.Fatal(err)
	}
	lkey := PiLoginPresetKey(login.Provider, login.Name)
	if _, isLogin, err := svc.ResolvePiLoginLaunch(lkey, PiLoginProviderID(login.Provider)); !isLogin || err != nil {
		t.Fatalf("stored login key: isLogin=%v err=%v", isLogin, err)
	}
	if _, isLogin, err := svc.ResolvePiLoginLaunch("missing", PiLoginProviderID(login.Provider)); !isLogin || err == nil {
		t.Fatalf("stale login selection: isLogin=%v err=%v", isLogin, err)
	}
	if err := svc.RejectPiLoginLaunchRefs(PiLoginProviderID(login.Provider), lkey); err == nil {
		t.Fatal("login refs accepted on foreign CLI")
	}
	if _, ok := ParsePiLoginProviderID("pi-login"); ok {
		t.Fatal("bare reserved segment parsed as login source id")
	}
	if _, ok := ParsePiLoginProviderID("pi-login/=_"); ok {
		t.Fatal("padding segment parsed as login source id")
	}
}

// 登录预设保存/删除回调：保存成功后以 saved 触发，删除以 removed（携带旧值）
// 触发；保存失败不触发；回调在 ConfigService 写锁外执行（锁序见 changePiLoginPreset）。
func TestPiLoginPresetChangeListenerSavedAndRemoved(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	type event struct{ saved, removed *PiLoginPreset }
	var events []event
	svc.SetPiLoginPresetChangeListener(func(saved, removed *PiLoginPreset) {
		events = append(events, event{saved, removed})
	})

	p := PiLoginPreset{Name: "Daily", Provider: "openai-codex", Model: "native-model", Thinking: "max", ContextWindow: 400000}
	if err := svc.SavePiLoginPreset(p); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].saved == nil || events[0].removed != nil {
		t.Fatalf("save events = %+v, want single saved", events)
	}
	if events[0].saved.ContextWindow != 400000 {
		t.Fatalf("saved contextWindow = %d, want 400000", events[0].saved.ContextWindow)
	}

	if err := svc.DeletePiLoginPreset(PiLoginPresetKey(p.Provider, p.Name)); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].saved != nil || events[1].removed == nil {
		t.Fatalf("delete events = %+v, want single removed", events)
	}
	if events[1].removed.Model != "native-model" || events[1].removed.ContextWindow != 400000 {
		t.Fatalf("removed = %+v, want old preset values", events[1].removed)
	}

	// 删除不存在的 key：无 removed 事件。
	if err := svc.DeletePiLoginPreset(PiLoginPresetKey("native", "ghost")); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("ghost delete produced events = %+v", events)
	}
}

// ContextWindow 持久化（json context_window）与负值校验。
func TestPiLoginPresetContextWindowValidationAndPersistence(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	p := PiLoginPreset{Name: "Daily", Provider: "openai-codex", Model: "native-model", ContextWindow: 256000}
	if err := svc.SavePiLoginPreset(p); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"context_window": 256000`) {
		t.Fatalf("context_window not persisted: %s", raw)
	}
	loaded := NewConfigService(dir)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	got, err := loaded.ResolvePiLoginPreset(PiLoginPresetKey(p.Provider, p.Name))
	if err != nil || got.ContextWindow != 256000 {
		t.Fatalf("reload contextWindow = %v (%v), want 256000", got, err)
	}
	if err := svc.SavePiLoginPreset(PiLoginPreset{Name: "Bad", Provider: "p", Model: "m", ContextWindow: -1}); err == nil {
		t.Fatal("negative contextWindow accepted")
	}
}

// 同 provider+model 多预设共享模型级覆写（模型级共享值语义）：保存任一预设的
// 窗口即全组生效，兄弟预设配置面同步对齐——不出现「B 显示 256000 实际共享
// 400000」的静默串值（diting F02），也没有「逐个保存被拒」的死锁。
func TestPiLoginPresetContextWindowSharedAcrossPresets(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	if err := svc.SavePiLoginPreset(PiLoginPreset{Name: "A", Provider: "zai", Model: "glm-5.2", ContextWindow: 256000}); err != nil {
		t.Fatal(err)
	}
	// 同模型不同窗口的第二预设：允许保存，且兄弟 A 被对齐到新值（模型级共享值）。
	if err := svc.SavePiLoginPreset(PiLoginPreset{Name: "B", Provider: "zai", Model: "glm-5.2", ContextWindow: 400000}); err != nil {
		t.Fatalf("model-scoped window save rejected: %v", err)
	}
	presets, _ := svc.GetPiLoginPresets()
	if got := presets[PiLoginPresetKey("zai", "A")].ContextWindow; got != 400000 {
		t.Fatalf("sibling A not aligned: %d, want 400000", got)
	}
	// 清零同样全组生效。
	if err := svc.SavePiLoginPreset(PiLoginPreset{Name: "B", Provider: "zai", Model: "glm-5.2"}); err != nil {
		t.Fatal(err)
	}
	presets, _ = svc.GetPiLoginPresets()
	if got := presets[PiLoginPresetKey("zai", "A")].ContextWindow; got != 0 {
		t.Fatalf("sibling A not zeroed: %d, want 0", got)
	}
	// 不同 model 的预设不受影响。
	if err := svc.SavePiLoginPreset(PiLoginPreset{Name: "D", Provider: "zai", Model: "glm-5", ContextWindow: 128000}); err != nil {
		t.Fatal(err)
	}
	presets, _ = svc.GetPiLoginPresets()
	if got := presets[PiLoginPresetKey("zai", "A")].ContextWindow; got != 0 {
		t.Fatalf("different-model save leaked alignment: %d", got)
	}
}
