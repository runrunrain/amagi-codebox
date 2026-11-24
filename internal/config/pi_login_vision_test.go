package config

import "testing"

func TestPiLoginVisionPersistsAndSharesOnlyWithSameProviderModel(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []PiLoginPreset{
		{Name: "old", Provider: "native", Model: "m"},
		{Name: "other model", Provider: "native", Model: "another"},
		{Name: "other provider", Provider: "second", Model: "m"},
		{Name: "new", Provider: "native", Model: "m", Vision: true},
	} {
		if err := svc.SavePiLoginPreset(p); err != nil {
			t.Fatal(err)
		}
	}
	loaded := NewConfigService(dir)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	assertVision := func(s *ConfigService, firstModel bool) {
		t.Helper()
		presets, err := s.GetPiLoginPresets()
		if err != nil || len(presets) != 4 {
			t.Fatalf("get login presets: %d, %v", len(presets), err)
		}
		for _, p := range presets {
			want := firstModel && p.Provider == "native" && p.Model == "m"
			if p.Vision != want {
				t.Fatalf("%s/%s vision=%v, want %v", p.Provider, p.Name, p.Vision, want)
			}
		}
	}
	assertVision(loaded, true)
	if err := loaded.SavePiLoginPreset(PiLoginPreset{Name: "old", Provider: "native", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	assertVision(loaded, false)
}
