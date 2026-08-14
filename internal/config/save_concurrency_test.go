package config

import (
	"strconv"
	"sync"
	"testing"
)

// TestSave_ConcurrentWithSaveProvider exercises Save() racing against
// SaveProvider(), which reassigns s.config.Models[name] under the write lock.
// Before the fix Save() took only an RLock to grab the shared s.config pointer
// and then marshalled it unlocked, so a concurrent SaveProvider map write could
// race with Save()'s json.Marshal iteration ("concurrent map iteration and map
// write") or trip the race detector. Now both Serialize under s.mu.
//
// Run intentionally under -race:
//
//	go test -race ./internal/config -run TestSave_ConcurrentWithSaveProvider
func TestSave_ConcurrentWithSaveProvider(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := svc.SaveProvider("anthropic-main", Provider{
		Type:      "anthropic",
		AuthKey:   "ANTHROPIC_API_KEY",
		Anthropic: &AnthropicFormat{Enabled: true, AuthKey: "ANTHROPIC_API_KEY"},
	}); err != nil {
		t.Fatalf("seed SaveProvider: %v", err)
	}

	var wg sync.WaitGroup
	const rounds = 30
	for i := 0; i < rounds; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := svc.Save(); err != nil {
				t.Errorf("Save: %v", err)
			}
		}()
		go func(i int) {
			defer wg.Done()
			preset := "p" + strconv.Itoa(i%4)
			if err := svc.SaveProvider("anthropic-main", Provider{
				Type:      "anthropic",
				AuthKey:   "ANTHROPIC_API_KEY",
				Anthropic: &AnthropicFormat{Enabled: true, AuthKey: "ANTHROPIC_API_KEY"},
				Presets: map[string]Preset{
					preset: {Name: preset, Model: "claude-3-5-sonnet"},
				},
			}); err != nil {
				t.Errorf("SaveProvider: %v", err)
			}
		}(i)
	}
	wg.Wait()

	// Sanity: the persisted config reloads cleanly and the provider survived.
	svc2 := NewConfigService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, err := svc2.GetProvider("anthropic-main"); err != nil {
		t.Fatalf("GetProvider after reload: %v", err)
	}
}