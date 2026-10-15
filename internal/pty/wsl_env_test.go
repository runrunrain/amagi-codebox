//go:build windows

package pty

import (
	"strings"
	"testing"
)

// TestEnsureWSLUTF8LocaleEnvAddsLangAndWslenv pins the T13 injection: with no
// locale vars at all, LANG=en_US.UTF-8 is appended AND listed in WSLENV (env
// vars only cross the Windows→WSL boundary through the WSLENV allowlist —
// setting LANG alone would be a no-op inside the distro).
func TestEnsureWSLUTF8LocaleEnvAddsLangAndWslenv(t *testing.T) {
	env := []string{"PATH=/x", "ANTHROPIC_API_KEY=sk"}
	out := ensureWSLUTF8LocaleEnv(env)
	if envEntryValue(out, "LANG") != "en_US.UTF-8" {
		t.Fatalf("LANG = %q, want en_US.UTF-8", envEntryValue(out, "LANG"))
	}
	if wslenv := envEntryValue(out, "WSLENV"); wslenv != "LANG" {
		t.Fatalf("WSLENV = %q, want LANG", wslenv)
	}
	// 入参不被修改。
	if len(env) != 2 || hasEnvEntry(env, "LANG") || hasEnvEntry(env, "WSLENV") {
		t.Fatalf("input env mutated: %v", env)
	}
}

// TestEnsureWSLUTF8LocaleEnvAppendsToExistingWslenv: a resolver-computed WSLENV
// list is preserved and LANG is appended at the end.
func TestEnsureWSLUTF8LocaleEnvAppendsToExistingWslenv(t *testing.T) {
	env := []string{"WSLENV=ANTHROPIC_API_KEY:PI_OFFLINE", "ANTHROPIC_API_KEY=sk", "PI_OFFLINE=1"}
	out := ensureWSLUTF8LocaleEnv(env)
	if wslenv := envEntryValue(out, "WSLENV"); wslenv != "ANTHROPIC_API_KEY:PI_OFFLINE:LANG" {
		t.Fatalf("WSLENV = %q, want ANTHROPIC_API_KEY:PI_OFFLINE:LANG", wslenv)
	}
}

// TestEnsureWSLUTF8LocaleEnvSkipsExistingWslenvLang: LANG already in WSLENV
// (including the /flagged form) is not duplicated.
func TestEnsureWSLUTF8LocaleEnvSkipsExistingWslenvLang(t *testing.T) {
	for _, existing := range []string{"LANG", "LANG/p", "FOO:lang"} {
		env := []string{"WSLENV=" + existing}
		out := ensureWSLUTF8LocaleEnv(env)
		if wslenv := envEntryValue(out, "WSLENV"); wslenv != existing {
			t.Errorf("WSLENV = %q, want unchanged %q", wslenv, existing)
		}
	}
}

// TestEnsureWSLUTF8LocaleEnvNeverOverridesExistingLocale mirrors the darwin
// contract (buildDarwinPTYEnvironmentFromBase): any of LANG/LC_ALL/LC_CTYPE
// already present (even with an empty value or different casing) suppresses
// the injection entirely.
func TestEnsureWSLUTF8LocaleEnvNeverOverridesExistingLocale(t *testing.T) {
	for _, kv := range []string{"LANG=zh_CN.UTF-8", "LC_ALL=C.UTF-8", "LC_CTYPE=en_US.UTF-8", "lang=", "Lc_All=x"} {
		env := []string{kv, "WSLENV=ANTHROPIC_API_KEY"}
		out := ensureWSLUTF8LocaleEnv(env)
		if len(out) != len(env) {
			t.Errorf("locale entry %q must suppress injection, got %v", kv, out)
		}
		if wslenv := envEntryValue(out, "WSLENV"); strings.Contains(wslenv, "LANG") {
			t.Errorf("WSLENV must stay untouched when %q present, got %q", kv, wslenv)
		}
	}
}
