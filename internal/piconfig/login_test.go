package piconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
)

func loginFixtureHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func writeLoginFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPiLoginDiscoveryStandardHomeCredentialFree(t *testing.T) {
	dir := loginFixtureHome(t)
	override := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", override)
	// Synthetic credentials are isolation fixtures, never production accounts.
	fixture := `{"openai-codex":{"type":"oauth","access":"fixture-access-sentinel","refresh":"fixture-refresh-sentinel","expires":1},"key-only":{"type":"api_key","key":"fixture-api-sentinel"},"wrong":null,"amagi-copy":{"type":"api_key"}}`
	writeLoginFixture(t, filepath.Join(dir, "auth.json"), fixture)
	writeLoginFixture(t, filepath.Join(override, "auth.json"), `{"wrong-home":{"type":"oauth"}}`)
	svc := NewService()
	got, err := svc.GetPiLoginProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Provider != "openai-codex" || got[0].ID != config.PiLoginProviderID("openai-codex") || got[0].CLI != "pi" || got[0].Source != "~/.pi/agent/auth.json" {
		t.Fatalf("discovery: %#v", got)
	}
	if got[0].Models == nil || len(got[0].Models) != 0 {
		t.Fatal("missing model catalog must not hide login")
	}
	dto, _ := json.Marshal(got)
	for _, forbidden := range []string{"fixture-", "access", "refresh", "expires", "api_key"} {
		if strings.Contains(string(dto), forbidden) {
			t.Fatalf("DTO includes forbidden field %q", forbidden)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if string(after) != fixture {
		t.Fatal("discovery rewrote auth")
	}
	if canonical, err := RequirePiLogin("openai-codex", false); err != nil || canonical != dir {
		t.Fatalf("canonical dir: %q %v", canonical, err)
	}
	if _, err := RequirePiLogin("openai-codex", true); err == nil {
		t.Fatal("WSL must refuse host login")
	}
	writeLoginFixture(t, filepath.Join(dir, "auth.json"), `{}`)
	got, err = svc.GetPiLoginProviders()
	if err != nil || len(got) != 0 {
		t.Fatalf("logout refresh: %v %v", got, err)
	}
	if _, err := RequirePiLogin("openai-codex", false); err == nil {
		t.Fatal("removed login accepted")
	}
}

func TestPiLoginDiscoveryEmptyAndSafeFailures(t *testing.T) {
	dir := loginFixtureHome(t)
	path := filepath.Join(dir, "auth.json")
	got, err := NewService().GetPiLoginProviders()
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("missing auth: %v %v", got, err)
	}
	for _, fixture := range []string{`{}`, `{"openai":{"type":"api_key","key":"fixture-key"}}`, `{"a":"oauth","b":{"type":4},"c":{"type":"other"}}`} {
		writeLoginFixture(t, path, fixture)
		got, err := NewService().GetPiLoginProviders()
		if err != nil || len(got) != 0 {
			t.Fatalf("empty login: %v %v", got, err)
		}
	}
	for _, fixture := range []string{`{"fixture-secret-leak":`, `[]`, `null`} {
		writeLoginFixture(t, path, fixture)
		_, err := NewService().GetPiLoginProviders()
		if err == nil || strings.Contains(err.Error(), "fixture-secret-leak") {
			t.Fatalf("unsafe JSON error: %v", err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService().GetPiLoginProviders(); err == nil || strings.Contains(err.Error(), dir) {
		t.Fatalf("unsafe I/O error: %v", err)
	}
}

func TestPiLoginDiscoveryCustomIDNamespace(t *testing.T) {
	dir := loginFixtureHome(t)
	writeLoginFixture(t, filepath.Join(dir, "auth.json"), `{"extension:org/provider":{"type":"oauth"}}`)
	got, err := NewService().GetPiLoginProviders()
	if err != nil || len(got) != 1 || got[0].Provider != "extension:org/provider" || got[0].ID != config.PiLoginProviderID("extension:org/provider") {
		t.Fatalf("custom OAuth id: %+v %v", got, err)
	}
}

func TestPiLoginModelHintsUseStandardDirectory(t *testing.T) {
	dir := loginFixtureHome(t)
	writeLoginFixture(t, filepath.Join(dir, "auth.json"), `{"openai-codex":{"type":"oauth"}}`)
	writeLoginFixture(t, filepath.Join(dir, "models-store.json"), `{"openai-codex":{"models":[{"id":"test-model","name":"Test Model","reasoning":true,"thinkingLevelMap":{"max":"fixture"},"apiKey":"fixture-not-a-model-field"}]}}`)
	got, err := NewService().GetPiLoginProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0].Models) != 1 || got[0].Models[0].ID != "test-model" {
		t.Fatalf("models: %#v", got)
	}
	dto, _ := json.Marshal(got)
	if strings.Contains(string(dto), "fixture-") {
		t.Fatal("model hints leak unselected fields")
	}
	writeLoginFixture(t, filepath.Join(dir, "models-store.json"), `broken fixture`)
	got, err = NewService().GetPiLoginProviders()
	if err != nil || len(got) != 1 {
		t.Fatal("bad catalog hides login")
	}
}
