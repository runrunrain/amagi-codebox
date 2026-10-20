package platform

import "testing"

// TestControlDarwinResolutionOverridesRestore verifies the two exported test
// control hooks swap their target and restore the previous value, including
// the non-concurrent-use contract (restore returns the pre-call state).
func TestControlDarwinResolutionOverridesRestore(t *testing.T) {
	defaultBaseline := append([]string(nil), darwinBaselinePATH...)

	restoreBaseline := ControlDarwinBaselinePATH([]string{"/nonexistent-test-baseline"})
	if len(darwinBaselinePATH) != 1 || darwinBaselinePATH[0] != "/nonexistent-test-baseline" {
		t.Fatalf("baseline override not applied: %v", darwinBaselinePATH)
	}
	restoreBaseline()
	if len(darwinBaselinePATH) != len(defaultBaseline) {
		t.Fatalf("baseline not restored: %v", darwinBaselinePATH)
	}

	// nil must yield an empty baseline (fully removed), still restorable.
	restoreBaseline = ControlDarwinBaselinePATH(nil)
	if len(darwinBaselinePATH) != 0 {
		t.Fatalf("nil override should clear baseline, got %v", darwinBaselinePATH)
	}
	restoreBaseline()
	if len(darwinBaselinePATH) != len(defaultBaseline) {
		t.Fatalf("baseline not restored after nil override: %v", darwinBaselinePATH)
	}

	restoreFallback := ControlDarwinShellFallback(false)
	if darwinShellFallbackEnabled {
		t.Fatal("fallback override not applied")
	}
	restoreFallback()
	if !darwinShellFallbackEnabled {
		t.Fatal("fallback not restored")
	}
}
