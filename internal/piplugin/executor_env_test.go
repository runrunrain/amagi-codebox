package piplugin

import (
	"reflect"
	"testing"
)

// envValue reads a KEY=VALUE entry from an env slice; returns ("", false) if absent.
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, e := range env {
		if len(e) > len(prefix) && e[:len(prefix)] == prefix {
			return e[len(prefix):], true
		}
	}
	return "", false
}

// TestExecutePiCommandInjectsAgentDir (P1-2) verifies that pi CLI write operations
// (install/remove/update) explicitly inject PI_CODING_AGENT_DIR pointing at the
// CodeBox-managed runtime dir, so the package lands where LaunchPiSession will
// load it from.
func TestExecutePiCommandInjectsAgentDir(t *testing.T) {
	agentDir := "/codebox/pi-runtime"
	runner := &testRunner{}
	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, runner)

	if _, err := svc.InstallPackage("npm:foo"); err != nil {
		t.Fatalf("InstallPackage: %v", err)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("expected 1 CLI call, got %d", len(runner.specs))
	}
	got, ok := envValue(runner.specs[0].Env, "PI_CODING_AGENT_DIR")
	if !ok {
		t.Fatalf("PI_CODING_AGENT_DIR not injected into pi CLI env: %v", runner.specs[0].Env)
	}
	if got != agentDir {
		t.Errorf("PI_CODING_AGENT_DIR = %q, want %q", got, agentDir)
	}
	// the install subcommand + source are unchanged
	wantArgs := []string{"install", "npm:foo"}
	if !reflect.DeepEqual(runner.specs[0].Args, wantArgs) {
		t.Errorf("CLI args = %#v, want %#v", runner.specs[0].Args, wantArgs)
	}
}
