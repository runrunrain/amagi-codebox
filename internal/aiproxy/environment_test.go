package aiproxy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// wslGatedEnv returns the AMAGI_CODEBOX_AI_PROXY-active env entries with
// USERPROFILE pinned at a fresh temp home; networkingMode is the .wslconfig
// content to plant there ("" = no file). Pinning the home decouples the gate's
// verdict from the host machine's real WSL networking mode — CI/dev hosts may
// legitimately run mirrored (WSLNetworkingMode caches per home, so distinct
// temp homes always probe fresh).
func wslGatedEnv(t *testing.T, networkingMode string) []string {
	t.Helper()
	home := t.TempDir()
	if networkingMode != "" {
		if err := os.WriteFile(filepath.Join(home, ".wslconfig"), []byte(networkingMode), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return []string{"AMAGI_CODEBOX_AI_PROXY=1", "HTTPS_PROXY=http://127.0.0.1:18890", "USERPROFILE=" + home}
}

func wslLaunchSpec(env []string) platform.ResolvedLaunchSpec {
	return platform.ResolvedLaunchSpec{
		BootstrapMode:  platform.BootstrapWSL,
		StartupCommand: "claude",
		Env:            platform.ResolvedEnv{Variables: env},
	}
}

// Mirrored networking makes the Windows loopback bidirectionally reachable
// from the distro: the session is admitted and the spec (with the unified
// proxy entry + WSLENV-forwarded proxy keys) passes through unchanged.
func TestPinProxyEnvironmentWSLMirroredPasses(t *testing.T) {
	env := wslGatedEnv(t, "[wsl2]\nnetworkingMode=mirrored\n")
	spec, err := PinProxyEnvironment(wslLaunchSpec(env))
	if err != nil {
		t.Fatalf("mirrored WSL session must be admitted: %v", err)
	}
	if spec.StartupCommand != "claude" {
		t.Fatalf("mirrored spec must pass through unchanged, got startup %q", spec.StartupCommand)
	}
	if len(spec.Env.Variables) != len(env) {
		t.Fatalf("mirrored spec env must be untouched: %v", spec.Env.Variables)
	}
}

// Explicit NAT keeps failing closed, and the rejection text must carry the
// actionable switch guidance (C5): .wslconfig path, [wsl2] key, wsl --shutdown.
func TestPinProxyEnvironmentWSLNATRejected(t *testing.T) {
	env := wslGatedEnv(t, "[wsl2]\nnetworkingMode=nat\n")
	_, err := PinProxyEnvironment(wslLaunchSpec(env))
	if err == nil {
		t.Fatal("NAT WSL session must be rejected")
	}
	msg := err.Error()
	for _, want := range []string{"NAT", "networkingMode=mirrored", "wsl --shutdown", ".wslconfig"} {
		if !strings.Contains(msg, want) {
			t.Errorf("rejection text missing %q: %s", want, msg)
		}
	}
}

// No .wslconfig at all: mode unknown, treated as NAT (fail-safe, unchanged
// from the pre-mirrored behavior).
func TestPinProxyEnvironmentWSLNoConfigRejected(t *testing.T) {
	env := wslGatedEnv(t, "")
	if _, err := PinProxyEnvironment(wslLaunchSpec(env)); err == nil {
		t.Fatal("WSL session without .wslconfig (unknown mode -> NAT) must be rejected")
	}
}

// Inactive sessions (no AMAGI_CODEBOX_AI_PROXY marker) never hit the gate,
// regardless of the resolved networking mode.
func TestPinProxyEnvironmentWSLInactiveBypassesGate(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".wslconfig"), []byte("[wsl2]\nnetworkingMode=nat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := PinProxyEnvironment(wslLaunchSpec([]string{"USERPROFILE=" + home}))
	if err != nil || spec.StartupCommand != "claude" {
		t.Fatalf("inactive session must pass through untouched: %v", err)
	}
}

// D4: the disabled ApplyEnv path must strip an inherited AMAGI_CODEBOX_AI_PROXY
// marker. Nested CodeBox (a CodeBox-managed CLI session hosting another
// CodeBox) inherits the parent marker through MergeWithSystem; leaving it in
// place made PinProxyEnvironment treat the already-disabled proxy as active
// and reject WSL sessions with NAT guidance the user cannot act on.
func TestApplyEnvDisabledStripsInheritedMarker(t *testing.T) {
	s := New(t.TempDir())
	defer s.Close()
	if err := s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	base := []string{
		"AMAGI_CODEBOX_AI_PROXY=1", // inherited from a CodeBox-proxied parent
		"HTTP_PROXY=http://user-proxy:8080", "HTTPS_PROXY=http://user-proxy:8080",
		"NO_PROXY=corp.example", "HOME=/example", "API_KEY=keep",
	}
	got, err := s.ApplyEnv(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range got {
		if strings.HasPrefix(entry, aiProxyEnvMarker+"=") {
			t.Fatalf("disabled path leaked inherited marker: %v", got)
		}
	}
	for _, keep := range []string{"HTTP_PROXY=http://user-proxy:8080", "NO_PROXY=corp.example", "API_KEY=keep"} {
		if !containsEntry(got, keep) {
			t.Errorf("disabled path must preserve user env as-is, missing %q: %v", keep, got)
		}
	}
}

// D4: no marker in the base env — the disabled path returns the user env
// unchanged (deep-equal, no accidental reordering or filtering).
func TestApplyEnvDisabledWithoutMarkerUnchanged(t *testing.T) {
	s := New(t.TempDir())
	defer s.Close()
	if err := s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	base := []string{"HTTP_PROXY=http://user-proxy:8080", "HOME=/example"}
	got, err := s.ApplyEnv(base)
	if err != nil || len(got) != len(base) {
		t.Fatalf("disabled path must not alter plain env: %v (%v)", got, err)
	}
}

func containsEntry(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}
