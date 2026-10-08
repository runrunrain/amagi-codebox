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
