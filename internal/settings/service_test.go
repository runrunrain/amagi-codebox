package settings

import "testing"

func TestNormalizeDashboardDefaults_DoesNotPropagateLegacyTerminalModeToNonClaudeEngines(t *testing.T) {
	d := DashboardDefaults{Mode: "terminal"}

	normalizeDashboardDefaults(&d)

	if d.ClaudeMode != "terminal" {
		t.Fatalf("ClaudeMode = %q, want legacy mode terminal", d.ClaudeMode)
	}
	if d.OpenCodeMode != "embedded" {
		t.Fatalf("OpenCodeMode = %q, want embedded", d.OpenCodeMode)
	}
	if d.CodexMode != "embedded" {
		t.Fatalf("CodexMode = %q, want embedded", d.CodexMode)
	}
	if d.AmagiCodeMode != "embedded" {
		t.Fatalf("AmagiCodeMode = %q, want embedded", d.AmagiCodeMode)
	}
}

func TestNormalizeDashboardDefaults_PreservesExplicitEngineModes(t *testing.T) {
	d := DashboardDefaults{
		Mode:          "terminal",
		OpenCodeMode:  "terminal",
		CodexMode:     "terminal",
		AmagiCodeMode: "terminal",
	}

	normalizeDashboardDefaults(&d)

	if d.OpenCodeMode != "terminal" || d.CodexMode != "terminal" || d.AmagiCodeMode != "terminal" {
		t.Fatalf("explicit modes not preserved: opencode=%q codex=%q amagicode=%q", d.OpenCodeMode, d.CodexMode, d.AmagiCodeMode)
	}
}
