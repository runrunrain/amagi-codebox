package config

import (
	"sync"
	"testing"
)

// TestSnapshotProvider_SingleLockGenerationConsistency covers the diting
// final-review Minor finding: bridge paths (LaunchSession / LaunchOpenCode
// terminal-preset bridging) used to assemble a provider from GetProvider plus
// a second GetPresets read. Between the two independent RLocks a concurrent
// SaveProvider/SavePreset can commit a new generation, mixing e.g. the old
// provider base fields with the new presets map. SnapshotProvider must return
// both from one critical section, so every snapshot is single-generation.
//
// The writer alternates between two complete provider generations (marker in
// DefaultModel + a preset only present in that generation); any mixed
// generation in a snapshot fails the invariant.
//
// Run with -race:
//
//	go test -race ./internal/config -run TestSnapshotProvider_SingleLock
func TestSnapshotProvider_SingleLockGenerationConsistency(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	gen := func(model, presetName string) Provider {
		return Provider{
			Type:         "anthropic",
			AuthKey:      "ANTHROPIC_API_KEY",
			Anthropic:    &AnthropicFormat{Enabled: true, AuthKey: "ANTHROPIC_API_KEY"},
			DefaultModel: model,
			Presets: map[string]Preset{
				presetName: {Name: presetName, Model: model},
			},
		}
	}
	genA, genB := gen("gen-a", "pa"), gen("gen-b", "pb")
	if err := svc.SaveProvider("bridge-p", genA); err != nil {
		t.Fatalf("seed SaveProvider: %v", err)
	}

	const writes = 100
	var wg sync.WaitGroup
	done := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		next := genA
		for i := 0; i < writes; i++ {
			if i%2 == 1 {
				next = genB
			}
			if err := svc.SaveProvider("bridge-p", next); err != nil {
				t.Errorf("SaveProvider: %v", err)
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			p, err := svc.SnapshotProvider("bridge-p")
			if err != nil {
				t.Errorf("SnapshotProvider: %v", err)
				return
			}
			_, hasA := p.Presets["pa"]
			_, hasB := p.Presets["pb"]
			switch p.DefaultModel {
			case "gen-a":
				if !hasA || hasB || len(p.Presets) != 1 {
					t.Errorf("mixed-generation snapshot: DefaultModel=gen-a presets=%v", p.Presets)
				}
			case "gen-b":
				if !hasB || hasA || len(p.Presets) != 1 {
					t.Errorf("mixed-generation snapshot: DefaultModel=gen-b presets=%v", p.Presets)
				}
			default:
				t.Errorf("unexpected DefaultModel %q", p.DefaultModel)
			}
		}
	}()
	wg.Wait()
}

// TestSnapshotProvider_PresetsMapIsACopy verifies the injection property the
// bridges rely on: the returned Presets map is a private non-nil copy, so
// writing the one-shot bridged preset into it neither mutates the
// ConfigService's shared map nor can be persisted by a later SaveAllConfig.
func TestSnapshotProvider_PresetsMapIsACopy(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := svc.SaveProvider("bridge-p", Provider{
		Type:      "anthropic",
		AuthKey:   "ANTHROPIC_API_KEY",
		Anthropic: &AnthropicFormat{Enabled: true, AuthKey: "ANTHROPIC_API_KEY"},
		Presets:   map[string]Preset{"keep": {Name: "keep", Model: "m"}},
	}); err != nil {
		t.Fatalf("seed SaveProvider: %v", err)
	}

	p, err := svc.SnapshotProvider("bridge-p")
	if err != nil {
		t.Fatalf("SnapshotProvider: %v", err)
	}
	p.Presets["injected"] = Preset{Name: "injected"}
	delete(p.Presets, "keep")

	live, err := svc.GetPresets("bridge-p")
	if err != nil {
		t.Fatalf("GetPresets: %v", err)
	}
	if _, ok := live["injected"]; ok {
		t.Error("snapshot Presets write leaked into ConfigService state")
	}
	if _, ok := live["keep"]; !ok {
		t.Error("snapshot Presets delete leaked into ConfigService state")
	}
}

// TestSnapshotProvider_NilPresetsAndErrors pins the bridge-facing contract:
// nil Presets snapshot as an empty non-nil map (bridges inject without a nil
// check) and missing/unloaded providers surface as errors instead of being
// silently dropped (the old bridge discarded GetPresets errors with `_`).
func TestSnapshotProvider_NilPresetsAndErrors(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := svc.SaveProvider("no-presets", Provider{
		Type:      "anthropic",
		AuthKey:   "ANTHROPIC_API_KEY",
		Anthropic: &AnthropicFormat{Enabled: true, AuthKey: "ANTHROPIC_API_KEY"},
	}); err != nil {
		t.Fatalf("seed SaveProvider: %v", err)
	}

	p, err := svc.SnapshotProvider("no-presets")
	if err != nil {
		t.Fatalf("SnapshotProvider: %v", err)
	}
	if p.Presets == nil || len(p.Presets) != 0 {
		t.Fatalf("nil-presets provider snapshot Presets=%v, want empty non-nil map", p.Presets)
	}

	if _, err := svc.SnapshotProvider("missing"); err == nil {
		t.Error("SnapshotProvider(missing) = nil error, want error")
	}
}
