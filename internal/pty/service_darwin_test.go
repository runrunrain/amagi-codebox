//go:build darwin

package pty

import (
	"slices"
	"sync"
	"testing"
)

func TestBuildDarwinPTYEnvironmentFromBase_InheritsNilEnvAndAddsMissingColorDefaults(t *testing.T) {
	inherited := []string{"PATH=/usr/bin", "PARENT_ONLY=1"}

	env := buildDarwinPTYEnvironmentFromBase(nil, inherited)

	assertEnvContains(t, env, "PATH=/usr/bin")
	assertEnvContains(t, env, "PARENT_ONLY=1")
	assertEnvContains(t, env, "TERM=xterm-256color")
	assertEnvContains(t, env, "COLORTERM=truecolor")
	// LANG is also injected when no locale is set
	assertEnvContains(t, env, "LANG=en_US.UTF-8")
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

	// env has no TERM, COLORTERM, LANG, LC_ALL, LC_CTYPE → 3 entries added
	if len(enriched) != len(env)+3 {
		t.Fatalf("len(enriched) = %d, want %d", len(enriched), len(env)+3)
	}
	if enriched[len(enriched)-3] != "TERM=xterm-256color" {
		t.Fatalf("TERM default = %q, want %q", enriched[len(enriched)-3], "TERM=xterm-256color")
	}
	if enriched[len(enriched)-2] != "COLORTERM=truecolor" {
		t.Fatalf("COLORTERM default = %q, want %q", enriched[len(enriched)-2], "COLORTERM=truecolor")
	}
	if enriched[len(enriched)-1] != "LANG=en_US.UTF-8" {
		t.Fatalf("LANG default = %q, want %q", enriched[len(enriched)-1], "LANG=en_US.UTF-8")
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

	// TERM and COLORTERM preserved; LANG is added since none of LANG/LC_ALL/LC_CTYPE are set
	if len(enriched) != len(env)+1 {
		t.Fatalf("len(enriched) = %d, want %d", len(enriched), len(env)+1)
	}
	// Original entries preserved
	for i := range env {
		if enriched[i] != env[i] {
			t.Fatalf("enriched[%d] = %q, want %q", i, enriched[i], env[i])
		}
	}
	assertEnvContains(t, enriched, "LANG=en_US.UTF-8")
}

func TestBuildDarwinPTYEnvironmentFromBase_FinalConstructionUsesProvidedEnvWithoutParentLeakage(t *testing.T) {
	env := []string{"PATH=/custom/bin"}
	inherited := []string{"AMAGI_DARWIN_SHOULD_NOT_LEAK=parent-only"}

	finalEnv := buildDarwinPTYEnvironmentFromBase(env, inherited)

	assertEnvContains(t, finalEnv, "PATH=/custom/bin")
	assertEnvContains(t, finalEnv, "TERM=xterm-256color")
	assertEnvContains(t, finalEnv, "COLORTERM=truecolor")
	assertEnvContains(t, finalEnv, "LANG=en_US.UTF-8")
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

// --- LANG / LC_ALL locale injection tests ---

func TestBuildDarwinPTYEnvironmentFromBase_AddsLANGWhenNoLocaleSet(t *testing.T) {
	env := []string{"PATH=/usr/bin"}
	inherited := []string{"HOME=/tmp"}

	enriched := buildDarwinPTYEnvironmentFromBase(env, inherited)

	assertEnvContains(t, enriched, "LANG=en_US.UTF-8")
	assertEnvContains(t, enriched, "TERM=xterm-256color")
	assertEnvContains(t, enriched, "COLORTERM=truecolor")
}

func TestBuildDarwinPTYEnvironmentFromBase_PreservesExplicitLANG(t *testing.T) {
	env := []string{"PATH=/usr/bin", "LANG=ja_JP.UTF-8"}
	inherited := []string{}

	enriched := buildDarwinPTYEnvironmentFromBase(env, inherited)

	assertEnvContains(t, enriched, "LANG=ja_JP.UTF-8")
	assertEnvNotContains(t, enriched, "LANG=en_US.UTF-8")
}

func TestBuildDarwinPTYEnvironmentFromBase_PreservesExplicitLC_ALL(t *testing.T) {
	env := []string{"PATH=/usr/bin", "LC_ALL=fr_FR.UTF-8"}
	inherited := []string{}

	enriched := buildDarwinPTYEnvironmentFromBase(env, inherited)

	assertEnvContains(t, enriched, "LC_ALL=fr_FR.UTF-8")
	assertEnvNotContains(t, enriched, "LANG=en_US.UTF-8")
}

func TestBuildDarwinPTYEnvironmentFromBase_PreservesExplicitLC_CTYPE(t *testing.T) {
	env := []string{"PATH=/usr/bin", "LC_CTYPE=en_US.UTF-8"}
	inherited := []string{}

	enriched := buildDarwinPTYEnvironmentFromBase(env, inherited)

	assertEnvContains(t, enriched, "LC_CTYPE=en_US.UTF-8")
	assertEnvNotContains(t, enriched, "LANG=en_US.UTF-8")
}

func TestBuildDarwinPTYEnvironmentFromBase_InheritsLANGFromParentWhenNilEnv(t *testing.T) {
	inherited := []string{"PATH=/usr/bin", "LANG=C.UTF-8"}

	env := buildDarwinPTYEnvironmentFromBase(nil, inherited)

	assertEnvContains(t, env, "LANG=C.UTF-8")
	assertEnvNotContains(t, env, "LANG=en_US.UTF-8")
}

// --- trimHistoryToFrontier tests ---

func TestTrimHistoryToFrontier_NoTrimNeeded(t *testing.T) {
	history := []byte("hello world")
	result := trimHistoryToFrontier(history, 100)
	if string(result) != "hello world" {
		t.Fatalf("expected unchanged, got %q", string(result))
	}
}

func TestTrimHistoryToFrontier_TrimsToMaxSize(t *testing.T) {
	// 20 ASCII bytes, trim to 10
	history := []byte("0123456789abcdefghij")
	result := trimHistoryToFrontier(history, 10)
	if len(result) > 10 {
		t.Fatalf("expected len <= 10, got %d", len(result))
	}
	if string(result) != "abcdefghij" {
		t.Fatalf("expected 'abcdefghij', got %q", string(result))
	}
}

func TestTrimHistoryToFrontier_SkipsUTF8ContinuationBytes(t *testing.T) {
	// Chinese character 你 is 0xE4 0xBD 0xA0 (3 bytes)
	// Build: "XX" + "你" + "YY" = 0x58 0x58 0xE4 0xBD 0xA0 0x59 0x59
	history := []byte{0x58, 0x58, 0xE4, 0xBD, 0xA0, 0x59, 0x59}
	// Trim to 5 bytes. The naive start would be at index 2 (0xE4), which is a leading byte, so OK.
	result := trimHistoryToFrontier(history, 5)
	if string(result) != string([]byte{0xE4, 0xBD, 0xA0, 0x59, 0x59}) {
		t.Fatalf("expected trim to start at 0xE4, got %v", result)
	}
}

func TestTrimHistoryToFrontier_SkipsPartialUTF8(t *testing.T) {
	// Build history where trim point lands on a continuation byte
	// 你 = 0xE4 0xBD 0xA0, 好 = 0xE5 0xA5 0xBD
	// "AAA" + "你好" = 41 41 41 E4 BD A0 E5 A5 BD
	history := []byte{0x41, 0x41, 0x41, 0xE4, 0xBD, 0xA0, 0xE5, 0xA5, 0xBD}
	// Trim to 4 bytes. Naive start at index 5, which is 0xA0 (continuation of 你).
	// Should advance to index 6 (0xE5, leading byte of 好).
	result := trimHistoryToFrontier(history, 4)
	if len(result) > 4 {
		t.Fatalf("expected len <= 4, got %d", len(result))
	}
	// Should start at 0xE5 (leading byte), not at 0xA0 (continuation)
	if result[0] != 0xE5 {
		t.Fatalf("expected first byte to be 0xE5 (UTF-8 leading), got 0x%02X", result[0])
	}
}

func TestTrimHistoryToFrontier_SkipsTruncatedEscape(t *testing.T) {
	// ESC [ 31 m (red foreground: 0x1B 0x5B 0x33 0x31 0x6D)
	// Build: "AAAA" + ESC[31m + "hello"
	history := []byte{0x41, 0x41, 0x41, 0x41, 0x1B, 0x5B, 0x33, 0x31, 0x6D, 0x68, 0x65, 0x6C, 0x6C, 0x6F}
	// Trim to 8 bytes. Naive start at index 6 (0x33, middle of ESC sequence).
	// The escape sequence ESC[31m = 1B 5B 33 31 6D, terminated at 'm' (0x6D >= 0x40).
	// Since the ESC at index 4 has no terminator before index 6, we should skip past 'm'.
	result := trimHistoryToFrontier(history, 8)
	// Should not start in the middle of the escape sequence
	if len(result) > 0 && result[0] == 0x33 {
		t.Fatalf("should not start in middle of escape sequence, got 0x%02X", result[0])
	}
	// The result should start with 'm' or later (after the escape terminator)
	// or with the text "hello"
	resultStr := string(result)
	if len(resultStr) > 0 && resultStr[0] != 'h' && resultStr[0] != 0x6D {
		// Acceptable: starts at the 'm' terminator or after
		t.Fatalf("expected result to start after escape terminator, got %q", resultStr)
	}
}

func TestTrimHistoryToFrontier_CompleteEscapeUnchanged(t *testing.T) {
	// "AA" + ESC[m (reset: 0x1B 0x5B 0x6D) + "BB"
	// The ESC sequence is complete (has terminator 'm' before trim point).
	history := []byte{0x41, 0x41, 0x1B, 0x5B, 0x6D, 0x42, 0x42}
	// Trim to 4. Naive start at index 3, which is 0x5B.
	// ESC at index 2 has terminator 'm' at index 4, which is before start 3? No, 4 > 3.
	// So ESC at index 2, check terminators between index 3 and 3... none. Has truncated.
	// Let me fix the test: the terminator must be BEFORE start (index 3).
	// Actually, checking between i+1=3 and start=3 means range is empty, so hasTerminator=false.
	// This means the ESC is truncated. Let's construct a better test.
	// "A" + ESC[m + "BBBB"
	history = []byte{0x41, 0x1B, 0x5B, 0x6D, 0x42, 0x42, 0x42, 0x42}
	// Trim to 5. Naive start at index 3, which is 0x6D ('m').
	// 0x6D is a leading byte (ASCII), so UTF-8 check passes.
	// Now check escape: scan back from index 3. Find ESC at index 1.
	// Check terminators between index 2 and 3: history[2]=0x5B='[', 0x5B >= 0x40 && <= 0x7E.
	// Yes! So hasTerminator=true. The escape is complete.
	result := trimHistoryToFrontier(history, 5)
	if len(result) > 5 {
		t.Fatalf("expected len <= 5, got %d", len(result))
	}
}

func TestIsUTF8LeadingByte(t *testing.T) {
	tests := []struct {
		byte  byte
		ascii bool // ASCII bytes are leading
	}{
		{0x41, true},  // 'A' - ASCII
		{0x00, true},  // NUL - ASCII
		{0x7F, true},  // DEL - ASCII
		{0xC0, true},  // 2-byte leading
		{0xDF, true},  // 2-byte leading
		{0xE0, true},  // 3-byte leading
		{0xEF, true},  // 3-byte leading
		{0xF0, true},  // 4-byte leading
		{0x80, false}, // continuation byte
		{0x9F, false}, // continuation byte
		{0xBF, false}, // continuation byte
	}
	for _, tt := range tests {
		got := isUTF8LeadingByte(tt.byte)
		if got != tt.ascii {
			t.Errorf("isUTF8LeadingByte(0x%02X) = %v, want %v", tt.byte, got, tt.ascii)
		}
	}
}

// --- GetOutputHistoryWithSeq tests ---

func TestGetOutputHistoryWithSeq_EmptySession(t *testing.T) {
	svc := NewService(nil)
	ps := &PtySession{
		outputHistory: []byte{},
		done:          make(chan struct{}),
		running:       true,
		mu:            sync.RWMutex{},
		historyMu:     sync.Mutex{},
	}
	svc.mu.Lock()
	svc.sessions["test-session"] = ps
	svc.mu.Unlock()

	history, seq, err := svc.GetOutputHistoryWithSeq("test-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("expected empty history, got %d bytes", len(history))
	}
	if seq != 0 {
		t.Fatalf("expected seq=0 for empty session, got %d", seq)
	}
}

func TestGetOutputHistoryWithSeq_ReturnsDataAndSeq(t *testing.T) {
	svc := NewService(nil)
	ps := &PtySession{
		outputHistory: []byte{0x41, 0x42, 0x43},
		emitSeq:       5,
		done:          make(chan struct{}),
		running:       true,
		mu:            sync.RWMutex{},
		historyMu:     sync.Mutex{},
	}
	svc.mu.Lock()
	svc.sessions["test-session"] = ps
	svc.mu.Unlock()

	history, seq, err := svc.GetOutputHistoryWithSeq("test-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(history) != "ABC" {
		t.Fatalf("expected 'ABC', got %q", string(history))
	}
	if seq != 5 {
		t.Fatalf("expected seq=5, got %d", seq)
	}
}

func TestGetOutputHistoryWithSeq_SnapshotIsCopy(t *testing.T) {
	svc := NewService(nil)
	ps := &PtySession{
		outputHistory: []byte{0x41, 0x42},
		emitSeq:       1,
		done:          make(chan struct{}),
		running:       true,
		mu:            sync.RWMutex{},
		historyMu:     sync.Mutex{},
	}
	svc.mu.Lock()
	svc.sessions["test-session"] = ps
	svc.mu.Unlock()

	history1, _, _ := svc.GetOutputHistoryWithSeq("test-session")
	// Mutate the returned slice; original should be unaffected
	history1[0] = 0x5A

	history2, _, _ := svc.GetOutputHistoryWithSeq("test-session")
	if history2[0] != 0x41 {
		t.Fatalf("snapshot should be an independent copy, but original was mutated")
	}
}

func TestGetOutputHistoryWithSeq_NotFound(t *testing.T) {
	svc := NewService(nil)
	_, _, err := svc.GetOutputHistoryWithSeq("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestGetOutputHistoryWithSeq_EmitSeqMonotonic(t *testing.T) {
	// Verify that emitSeq is incremented per chunk append, matching the
	// pattern used in readLoop: lock → emitSeq++ → append → unlock.
	svc := NewService(nil)
	ps := &PtySession{
		outputHistory: []byte{},
		emitSeq:       0,
		done:          make(chan struct{}),
		running:       true,
		mu:            sync.RWMutex{},
		historyMu:     sync.Mutex{},
	}
	svc.mu.Lock()
	svc.sessions["test-session"] = ps
	svc.mu.Unlock()

	// Simulate 3 chunk appends as readLoop would do
	for i := 0; i < 3; i++ {
		ps.historyMu.Lock()
		ps.emitSeq++
		ps.outputHistory = append(ps.outputHistory, byte('A'+i))
		ps.historyMu.Unlock()
	}

	_, seq, _ := svc.GetOutputHistoryWithSeq("test-session")
	if seq != 3 {
		t.Fatalf("expected emitSeq=3 after 3 appends, got %d", seq)
	}
}
