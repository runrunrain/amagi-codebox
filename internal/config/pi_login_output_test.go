package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiLoginOutputLimitPersistenceAndAlignment(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	a := PiLoginPreset{Name: "A", Provider: "p", Model: "m", MaxTokens: 32768}
	if err := svc.SavePiLoginPreset(a); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil || !strings.Contains(string(raw), `"max_tokens": 32768`) {
		t.Fatalf("persisted output missing: %s (%v)", raw, err)
	}
	loaded := NewConfigService(dir)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	got, err := loaded.ResolvePiLoginPreset(PiLoginPresetKey("p", "A"))
	if err != nil || got.MaxTokens != 32768 {
		t.Fatalf("reload: %+v %v", got, err)
	}
	b := a
	b.Name = "B"
	b.MaxTokens = 65536
	if err := svc.SavePiLoginPreset(b); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.ResolvePiLoginPreset(PiLoginPresetKey("p", "A"))
	if got.MaxTokens != 65536 {
		t.Fatalf("sibling not aligned: %+v", got)
	}
	b.MaxTokens = 0
	if err := svc.SavePiLoginPreset(b); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.ResolvePiLoginPreset(PiLoginPresetKey("p", "A"))
	if got.MaxTokens != 0 {
		t.Fatal("default not aligned")
	}
	b.MaxTokens = -1
	if err := svc.SavePiLoginPreset(b); err == nil {
		t.Fatal("negative output accepted")
	}
	got, _ = svc.ResolvePiLoginPreset(PiLoginPresetKey("p", "B"))
	if got.MaxTokens != 0 {
		t.Fatal("failed save mutated state")
	}
}

func TestPiLoginModelChangeNotifiesOldModel(t *testing.T) {
	svc := NewConfigService(t.TempDir())
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	p := PiLoginPreset{Name: "A", Provider: "p", Model: "old", MaxTokens: 1234}
	if err := svc.SavePiLoginPreset(p); err != nil {
		t.Fatal(err)
	}
	var events []string
	svc.SetPiLoginPresetChangeListener(func(saved, removed *PiLoginPreset) {
		if removed != nil {
			events = append(events, "remove:"+removed.Model)
		}
		if saved != nil {
			events = append(events, "save:"+saved.Model)
		}
	})
	p.Model = "new"
	if err := svc.SavePiLoginPreset(p); err != nil {
		t.Fatal(err)
	}
	if strings.Join(events, ",") != "remove:old,save:new" {
		t.Fatalf("events: %v", events)
	}
}
