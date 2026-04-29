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

func TestCapabilitiesDarwinArm64SupportsUpdateInstall(t *testing.T) {
	capabilities := capabilitiesForTarget("darwin", "arm64")
	if !capabilities.UpdateInstallSupported {
		t.Fatal("expected darwin arm64 to support update install")
	}
}

func TestCapabilitiesDarwinAmd64DoesNotSupportUpdateInstall(t *testing.T) {
	capabilities := capabilitiesForTarget("darwin", "amd64")
	if capabilities.UpdateInstallSupported {
		t.Fatal("expected darwin amd64 not to support update install")
	}
}

func TestCapabilitiesLinuxDoesNotSupportUpdateInstall(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		capabilities := capabilitiesForTarget("linux", arch)
		if capabilities.UpdateInstallSupported {
			t.Fatalf("expected linux/%s not to support update install", arch)
		}
	}
}

func TestCapabilitiesOtherPlatformDoesNotSupportUpdateInstall(t *testing.T) {
	capabilities := capabilitiesForTarget("freebsd", "amd64")
	if capabilities.UpdateInstallSupported {
		t.Fatal("expected freebsd/amd64 not to support update install")
	}
}
