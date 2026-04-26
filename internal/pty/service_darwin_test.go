//go:build darwin

package pty

import (
	"slices"
	"testing"
)

func TestBuildDarwinPTYEnvironmentFromBase_InheritsNilEnvAndAddsMissingColorDefaults(t *testing.T) {
	inherited := []string{"PATH=/usr/bin", "PARENT_ONLY=1"}

	env := buildDarwinPTYEnvironmentFromBase(nil, inherited)

	assertEnvContains(t, env, "PATH=/usr/bin")
	assertEnvContains(t, env, "PARENT_ONLY=1")
	assertEnvContains(t, env, "TERM=xterm-256color")
	assertEnvContains(t, env, "COLORTERM=truecolor")
}

func TestBuildDarwinPTYEnvironmentFromBase_InheritsEmptyEnvAndPreservesParentColorValues(t *testing.T) {
	inherited := []string{"PATH=/usr/bin", "TERM=screen-256color", "COLORTERM=24bit"}

	env := buildDarwinPTYEnvironmentFromBase([]string{}, inherited)

	assertEnvContains(t, env, "PATH=/usr/bin")
	assertEnvContains(t, env, "TERM=screen-256color")
	assertEnvContains(t, env, "COLORTERM=24bit")
	assertEnvNotContains(t, env, "TERM=xterm-256color")
	assertEnvNotContains(t, env, "COLORTERM=truecolor")
}

func TestBuildDarwinPTYEnvironmentFromBase_AddsColorCapabilitiesWhenMissing(t *testing.T) {
	env := []string{"PATH=/usr/bin", "HOME=/tmp/demo"}
	inherited := []string{"PARENT_ONLY=1", "TERM=screen-256color", "COLORTERM=24bit"}

	enriched := buildDarwinPTYEnvironmentFromBase(env, inherited)

	if len(enriched) != len(env)+2 {
		t.Fatalf("len(enriched) = %d, want %d", len(enriched), len(env)+2)
	}
	if enriched[len(enriched)-2] != "TERM=xterm-256color" {
		t.Fatalf("TERM default = %q, want %q", enriched[len(enriched)-2], "TERM=xterm-256color")
	}
	if enriched[len(enriched)-1] != "COLORTERM=truecolor" {
		t.Fatalf("COLORTERM default = %q, want %q", enriched[len(enriched)-1], "COLORTERM=truecolor")
	}
	if env[0] != "PATH=/usr/bin" || env[1] != "HOME=/tmp/demo" {
		t.Fatal("input env slice should remain unchanged")
	}
	assertEnvNotContains(t, enriched, "PARENT_ONLY=1")
}

func TestBuildDarwinPTYEnvironmentFromBase_PreservesExplicitColorConfiguration(t *testing.T) {
	env := []string{"TERM=screen-256color", "COLORTERM=24bit", "PATH=/usr/bin"}
	inherited := []string{"TERM=ansi", "COLORTERM=false", "PARENT_ONLY=1"}

	enriched := buildDarwinPTYEnvironmentFromBase(env, inherited)

	if len(enriched) != len(env) {
		t.Fatalf("len(enriched) = %d, want %d", len(enriched), len(env))
	}
	for i := range env {
		if enriched[i] != env[i] {
			t.Fatalf("enriched[%d] = %q, want %q", i, enriched[i], env[i])
		}
	}
}

func TestBuildDarwinPTYEnvironmentFromBase_FinalConstructionUsesProvidedEnvWithoutParentLeakage(t *testing.T) {
	env := []string{"PATH=/custom/bin"}
	inherited := []string{"AMAGI_DARWIN_SHOULD_NOT_LEAK=parent-only"}

	finalEnv := buildDarwinPTYEnvironmentFromBase(env, inherited)

	assertEnvContains(t, finalEnv, "PATH=/custom/bin")
	assertEnvContains(t, finalEnv, "TERM=xterm-256color")
	assertEnvContains(t, finalEnv, "COLORTERM=truecolor")
	assertEnvNotContains(t, finalEnv, "AMAGI_DARWIN_SHOULD_NOT_LEAK=parent-only")
}

func TestHasEnvKey_RequiresExplicitAssignment(t *testing.T) {
	env := []string{"TERM=xterm-256color", "COLORTERM=truecolor", "INVALID"}

	if !hasEnvKey(env, "TERM") {
		t.Fatal("expected TERM to be detected")
	}
	if !hasEnvKey(env, "COLORTERM") {
		t.Fatal("expected COLORTERM to be detected")
	}
	if hasEnvKey(env, "INVALID") {
		t.Fatal("expected malformed env entry to be ignored")
	}
	if hasEnvKey(env, "NOPE") {
		t.Fatal("unexpected env key detected")
	}
}

func assertEnvContains(t *testing.T, env []string, entry string) {
	t.Helper()
	if !slices.Contains(env, entry) {
		t.Fatalf("env missing %q", entry)
	}
}

func assertEnvNotContains(t *testing.T, env []string, entry string) {
	t.Helper()
	if slices.Contains(env, entry) {
		t.Fatalf("env unexpectedly contains %q", entry)
	}
}
