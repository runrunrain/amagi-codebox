package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// writeJSONLWithOnlyAssistant writes a jsonl fixture whose only record is a
// non-user (assistant) line, so claude.ExtractFirstUserMessage scans the whole
// file and returns found=false — the negative-result case the cache targets.
func writeJSONLWithOnlyAssistant(t *testing.T, baseDir, workDir, sessionID string) string {
	t.Helper()
	encoded := encodeWorkDirForTest(workDir)
	dir := filepath.Join(baseDir, ".claude", "projects", encoded)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	line, err := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"content": []map[string]any{{"type": "text", "text": "hi"}},
		},
		"origin": map[string]any{"kind": "not-human"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	fp := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(fp, append(line, '\n'), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return fp
}

// appendHumanLine appends a real human user jsonl record to fp, changing the
// file's size (and mtime), which must invalidate the negative-result cache.
func appendHumanLine(t *testing.T, fp, content string) {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": content,
		},
		"origin": map[string]any{"kind": "human"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	f, err := os.OpenFile(fp, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatalf("append: %v", err)
	}
}

// TestList_NegativeResultCache_SkipsRescanOnUnchangedFile verifies that a
// stopped claudecode session whose jsonl has no extractable title is NOT
// rescanned on every poll while the file is unchanged, and that the cache
// invalidates once the file changes so a later title is still detected.
//
// Run with -race (tryReadCLITitle uses entry.guard):
//
//	go test -race ./internal/session -run TestList_NegativeResultCache
func TestList_NegativeResultCache_SkipsRescanOnUnchangedFile(t *testing.T) {
	homeDir := t.TempDir()
	const workDir = "X:/WorkSpace/cache-demo"
	const claudeSID = "no-human-uuid"

	fp := writeJSONLWithOnlyAssistant(t, homeDir, workDir, claudeSID)

	var scanCount int32
	orig := extractFirstUserMessage
	t.Cleanup(func() { extractFirstUserMessage = orig })
	extractFirstUserMessage = func(path string) (string, bool, error) {
		atomic.AddInt32(&scanCount, 1)
		return orig(path)
	}

	mgr := NewManager()
	mgr.SetHomeDir(homeDir)
	sess := mgr.Create(AppTypeClaudeCode, "p", "default", "m", ModeEmbedded, workDir)
	mgr.SetClaudeSessionID(sess.ID, claudeSID)
	mgr.MarkExited(sess.ID) // stopped + ClaudeSessionID set + no Title → backfill path

	// First poll: scans once (negative), records the fingerprint.
	mgr.List()
	if got := atomic.LoadInt32(&scanCount); got != 1 {
		t.Fatalf("first List scanCount=%d want 1", got)
	}

	// Repeated polls with the file unchanged must hit the negative cache.
	mgr.List()
	mgr.List()
	if got := atomic.LoadInt32(&scanCount); got != 1 {
		t.Fatalf("unchanged file rescanned: scanCount=%d want 1 (negative cache miss)", got)
	}

	// Append a real human user message: size changes → cache invalidates → rescan.
	const wantTitle = "写一个真实首条消息"
	appendHumanLine(t, fp, wantTitle)
	infos := mgr.List()
	if got := atomic.LoadInt32(&scanCount); got != 2 {
		t.Fatalf("after file change scanCount=%d want 2 (cache should invalidate)", got)
	}
	var gotTitle string
	for _, info := range infos {
		if info.ID == sess.ID {
			gotTitle = info.Title
		}
	}
	if gotTitle != wantTitle {
		t.Fatalf("after change Title=%q want %q", gotTitle, wantTitle)
	}

	// Now the title is filled; subsequent polls skip the backfill entirely
	// (s.Title != "") and must NOT scan.
	mgr.List()
	if got := atomic.LoadInt32(&scanCount); got != 2 {
		t.Fatalf("post-title List rescanned: scanCount=%d want 2", got)
	}
}

// TestList_NegativeResultCache_MissingFileNotCached verifies that a missing
// jsonl file is not cached (os.Stat gates the scan, so no negative fingerprint
// is ever stored), and therefore a later-created file is still picked up —
// guarding against permanently suppressing titles for files that appear late.
func TestList_NegativeResultCache_MissingFileNotCached(t *testing.T) {
	homeDir := t.TempDir()
	const workDir = "X:/WorkSpace/late-demo"
	const claudeSID = "late-uuid"

	var scanCount int32
	orig := extractFirstUserMessage
	t.Cleanup(func() { extractFirstUserMessage = orig })
	extractFirstUserMessage = func(path string) (string, bool, error) {
		atomic.AddInt32(&scanCount, 1)
		return orig(path)
	}

	mgr := NewManager()
	mgr.SetHomeDir(homeDir)
	sess := mgr.Create(AppTypeClaudeCode, "p", "default", "m", ModeEmbedded, workDir)
	mgr.SetClaudeSessionID(sess.ID, claudeSID)
	mgr.MarkExited(sess.ID)

	titleOf := func(infos []SessionInfo) string {
		for _, info := range infos {
			if info.ID == sess.ID {
				return info.Title
			}
		}
		return "<<missing>>"
	}

	// Missing file: os.Stat fails so tryReadCLITitle short-circuits before any
	// scan (no negative cache is populated). Title stays empty.
	if got := titleOf(mgr.List()); got != "" {
		t.Fatalf("missing-file Title=%q want empty", got)
	}
	if got := atomic.LoadInt32(&scanCount); got != 0 {
		t.Fatalf("missing file scanned: scanCount=%d want 0 (stat short-circuits)", got)
	}
	// Re-poll while still missing: still no scan, still empty (no suppression).
	if got := titleOf(mgr.List()); got != "" {
		t.Fatalf("still-missing Title=%q want empty", got)
	}
	if got := atomic.LoadInt32(&scanCount); got != 0 {
		t.Fatalf("missing file re-scanned: scanCount=%d want 0", got)
	}

	// Now create the file with a title: stat succeeds → scan → title filled,
	// proving a late-appearing file is not permanently suppressed.
	writeJSONLFixture(t, homeDir, workDir, claudeSID, "迟到的真实首条消息")
	infos := mgr.List()
	if got := atomic.LoadInt32(&scanCount); got != 1 {
		t.Fatalf("after create scanCount=%d want 1 (must rescan newly present file)", got)
	}
	if got := titleOf(infos); got != "迟到的真实首条消息" {
		t.Fatalf("Title=%q want %q", got, "迟到的真实首条消息")
	}
}