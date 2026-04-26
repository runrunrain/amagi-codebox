package platform

import "testing"

func TestCapabilitiesForDarwinMatchPhase2MVPReadiness(t *testing.T) {
	capabilities := capabilitiesForTarget("darwin", "arm64")

	if !capabilities.EmbeddedTerminalSupported {
		t.Fatal("expected darwin embedded terminal to be enabled once Phase 2 PTY backend exists")
	}
	if capabilities.StandaloneTerminalSupported {
		t.Fatal("expected darwin standalone terminal to remain disabled")
	}
	if capabilities.SystemTraySupported {
		t.Fatal("expected darwin tray support to remain disabled")
	}
	if capabilities.CloseAction != CloseActionQuit {
		t.Fatalf("expected darwin close action to be quit, got %q", capabilities.CloseAction)
	}
}
