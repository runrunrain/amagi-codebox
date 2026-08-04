package remote

// m2a_catalog_journal_test.go — Tests for SessionCatalog (§5.3) and
// SessionOperationJournal (§8.5).

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// SessionCatalog tests
// ---------------------------------------------------------------------------

func TestSessionCatalog_ActivateAndList(t *testing.T) {
	c := NewSessionCatalog()
	now := time.Now()

	c.Activate("s2", "Title2", contract.CLITypeClaudeCode, "/work/dir2", now.Add(1*time.Second))
	c.Activate("s1", "Title1", contract.CLITypeOpenCode, "/work/dir1", now)

	list := c.ListEntries()
	if len(list) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(list))
	}
	// Sorted by lastActivityAt desc: s2 first (later time).
	if list[0].id != "s2" {
		t.Fatalf("expected s2 first, got %s", list[0].id)
	}
}

func TestSessionCatalog_EmptyList(t *testing.T) {
	c := NewSessionCatalog()
	list := c.ListEntries()
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}
}

func TestSessionCatalog_RemoveHidesFromList(t *testing.T) {
	c := NewSessionCatalog()
	now := time.Now()
	c.Activate("s1", "T1", contract.CLITypeClaudeCode, "/w", now)
	c.Remove("s1")

	list := c.ListEntries()
	if len(list) != 0 {
		t.Fatalf("after remove, expected 0, got %d", len(list))
	}

	// Detail should also be not found.
	_, ok := c.Entry("s1")
	if ok {
		t.Fatal("removed session should not be found")
	}
}

func TestSessionCatalog_RestartPreservesStartedAt(t *testing.T) {
	c := NewSessionCatalog()
	startedAt := time.Now()
	c.Activate("s1", "T1", contract.CLITypeClaudeCode, "/w", startedAt)

	// Simulate restart: re-activate with later time.
	restartTime := startedAt.Add(5 * time.Minute)
	c.Activate("s1", "T1-restarted", contract.CLITypeClaudeCode, "/w", restartTime)

	entry, ok := c.Entry("s1")
	if !ok {
		t.Fatal("session should exist")
	}
	if !entry.startedAt.Equal(startedAt) {
		t.Fatalf("startedAt should be preserved: expected %v, got %v", startedAt, entry.startedAt)
	}
	if !entry.lastActivityAt.Equal(restartTime) {
		t.Fatalf("lastActivityAt should be updated: expected %v, got %v", restartTime, entry.lastActivityAt)
	}
}

func TestSessionCatalog_TouchActivityMonotonic(t *testing.T) {
	c := NewSessionCatalog()
	t0 := time.Now()
	c.Activate("s1", "T1", contract.CLITypeClaudeCode, "/w", t0)

	// Earlier time should NOT update.
	c.TouchActivity("s1", t0.Add(-1*time.Minute))
	entry, _ := c.Entry("s1")
	if !entry.lastActivityAt.Equal(t0) {
		t.Fatalf("earlier touch should not change lastActivityAt")
	}

	// Later time should update.
	later := t0.Add(1 * time.Minute)
	c.TouchActivity("s1", later)
	entry, _ = c.Entry("s1")
	if !entry.lastActivityAt.Equal(later) {
		t.Fatalf("later touch should update lastActivityAt")
	}
}

func TestSafeTitle(t *testing.T) {
	tests := []struct {
		cliType contract.CLIType
		workdir string
		want    string
	}{
		{contract.CLITypeClaudeCode, "/home/user/project", "Claude Code · project"},
		{contract.CLITypeOpenCode, "/opt/code", "OpenCode · code"},
		{contract.CLITypeCodex, "", "Codex"},
		{contract.CLITypePi, "/root/", "Pi · root"},
	}
	for _, tt := range tests {
		got := safeTitle(tt.cliType, tt.workdir)
		if got != tt.want {
			t.Errorf("safeTitle(%s, %q) = %q, want %q", tt.cliType, tt.workdir, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// SessionOperationJournal tests
// ---------------------------------------------------------------------------

func TestSessionOperationJournal_NoopNotReady(t *testing.T) {
	j := NewNoopSessionOperationJournal(false)
	_, err := j.BeginIntent(context.Background(), SessionOperationIntent{
		OperationID: "op1",
		SessionID:   "s1",
		CLIType:     contract.CLITypeClaudeCode,
		Operation:   SessionOpStop,
		Actor:       SessionActorRemote,
	})
	if err == nil {
		t.Fatal("not-ready journal should reject BeginIntent")
	}
}

func TestSessionOperationJournal_NoopHappyPath(t *testing.T) {
	j := NewNoopSessionOperationJournal(true)
	permit, err := j.BeginIntent(context.Background(), SessionOperationIntent{
		OperationID: "op1",
		SessionID:   "s1",
		CLIType:     contract.CLITypeClaudeCode,
		Operation:   SessionOpStop,
		Actor:       SessionActorRemote,
	})
	if err != nil {
		t.Fatalf("BeginIntent failed: %v", err)
	}
	err = j.Complete(context.Background(), permit, SessionOutcomeCommitted, "")
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	records, err := j.ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecent failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records (intent+result), got %d", len(records))
	}
	// Newest first: result should be first.
	if records[0].Phase != SessionPhaseResult {
		t.Fatalf("expected result first, got %s", records[0].Phase)
	}
}

func TestSessionOperationJournal_FileBased(t *testing.T) {
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, journalFilename)
	j := NewSessionOperationJournal(tmpDir)

	if !j.IsReady() {
		t.Fatal("journal should be ready after init")
	}

	// Check file was created with correct perms.
	info, err := os.Lstat(journalPath)
	if err != nil {
		t.Fatalf("journal file not created: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("journal file should not be a symlink")
	}
	perm := info.Mode().Perm()
	if perm != journalFilePerm {
		t.Fatalf("journal file perm: expected %o, got %o", journalFilePerm, perm)
	}

	// Begin + complete.
	permit, err := j.BeginIntent(context.Background(), SessionOperationIntent{
		OperationID: "op1",
		SessionID:   "s1",
		CLIType:     contract.CLITypeCodex,
		Operation:   SessionOpRemove,
		Actor:       SessionActorDesktop,
	})
	if err != nil {
		t.Fatalf("BeginIntent: %v", err)
	}
	err = j.Complete(context.Background(), permit, SessionOutcomeCommitted, "")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// List recent.
	records, err := j.ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// Check directory perms.
	dirInfo, _ := os.Lstat(tmpDir)
	if dirInfo.Mode().Perm() != journalDirPerm {
		t.Logf("dir perm: expected %o, got %o (may be masked by tmpDir)", journalDirPerm, dirInfo.Mode().Perm())
	}
}

func TestSessionOperationJournal_RejectInvalidIntent(t *testing.T) {
	j := NewNoopSessionOperationJournal(true)
	// Empty operation ID.
	_, err := j.BeginIntent(context.Background(), SessionOperationIntent{
		OperationID: "",
		SessionID:   "s1",
		CLIType:     contract.CLITypeClaudeCode,
		Operation:   SessionOpStop,
		Actor:       SessionActorRemote,
	})
	if err == nil {
		t.Fatal("should reject empty operation ID")
	}
}

func TestSessionOperationJournal_FailedWithCode(t *testing.T) {
	j := NewNoopSessionOperationJournal(true)
	permit, _ := j.BeginIntent(context.Background(), SessionOperationIntent{
		OperationID: "op1",
		SessionID:   "s1",
		CLIType:     contract.CLITypeClaudeCode,
		Operation:   SessionOpStop,
		Actor:       SessionActorRemote,
	})
	fc := contract.ErrorCodeControlForbidden
	_ = j.Complete(context.Background(), permit, SessionOutcomeFailed, fc)

	records, _ := j.ListRecent(context.Background(), 10)
	var resultRec *SessionOperationRecord
	for i := range records {
		if records[i].Phase == SessionPhaseResult {
			resultRec = &records[i]
		}
	}
	if resultRec == nil {
		t.Fatal("no result record found")
	}
	if resultRec.Outcome != SessionOutcomeFailed {
		t.Fatalf("expected failed outcome, got %s", resultRec.Outcome)
	}
	if resultRec.FailureCode == nil || *resultRec.FailureCode != fc {
		t.Fatal("failureCode should be set for failed outcome")
	}
}
