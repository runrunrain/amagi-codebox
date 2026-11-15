package cleanupstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"amagi-codebox/internal/remote"
)

func TestExternalCleanupStoreFsyncReplayAndExactCompletion(t *testing.T) {
	dir := t.TempDir()
	store := NewFileStore(dir)
	if !store.IsReady() {
		t.Fatal("new cleanup store is not ready")
	}
	reservation := Reservation{
		Version:    JournalVersion,
		SessionID:  "durable-cleanup",
		Kind:       remote.SharedServiceClaudeCredentials,
		ReservedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := store.Reserve(reservation); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	record := Record{
		Version:         JournalVersion,
		SessionID:       reservation.SessionID,
		PID:             4242,
		ProcessIdentity: "test-start-identity",
		Kind:            reservation.Kind,
		RegisteredAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := store.Register(record); err != nil {
		t.Fatalf("Register: %v", err)
	}
	info, err := os.Lstat(filepath.Join(dir, JournalName))
	if err != nil {
		t.Fatalf("Lstat journal: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != JournalFilePerm {
		t.Fatalf("journal mode=%v want regular 0600", info.Mode())
	}

	reloaded := NewFileStore(dir)
	active, err := reloaded.LoadActive()
	if err != nil || len(active) != 1 || !sameExternalCleanupProcess(active[0], record) {
		t.Fatalf("reloaded active=%+v err=%v", active, err)
	}
	if err := reloaded.Complete(record); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	third := NewFileStore(dir)
	active, err = third.LoadActive()
	if err != nil || len(active) != 0 {
		t.Fatalf("completed record resurrected: active=%+v err=%v", active, err)
	}
}

func TestExternalCleanupStoreReplaysLegacyRegisteredRecordWithoutReservation(t *testing.T) {
	dir := t.TempDir()
	record := Record{
		Version:         JournalVersion,
		SessionID:       "legacy-active",
		PID:             4244,
		ProcessIdentity: "legacy-process-identity",
		Kind:            remote.SharedServiceClaudeCredentials,
		RegisteredAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}
	event := JournalEvent{
		Version:    JournalVersion,
		Action:     ActionRegistered,
		Record:     record,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	line, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal legacy event: %v", err)
	}
	line = append(line, '\n')
	if err := os.WriteFile(filepath.Join(dir, JournalName), line, JournalFilePerm); err != nil {
		t.Fatalf("WriteFile legacy journal: %v", err)
	}
	store := NewFileStore(dir)
	active, err := store.LoadActive()
	if err != nil || len(active) != 1 || !sameExternalCleanupProcess(active[0], record) {
		t.Fatalf("legacy replay active=%+v err=%v", active, err)
	}
}

func TestExternalCleanupStoreMissingMainInvalidArchiveFailsClosedWithoutOverwrite(t *testing.T) {
	t.Run("corrupt archive", func(t *testing.T) {
		dir := t.TempDir()
		archive := filepath.Join(dir, JournalName+".1")
		history := []byte("corrupt-but-authoritative-history\n")
		if err := os.WriteFile(archive, history, JournalFilePerm); err != nil {
			t.Fatalf("WriteFile archive: %v", err)
		}
		store := NewFileStore(dir)
		if store.IsReady() {
			t.Fatal("missing main + corrupt archive unexpectedly became ready")
		}
		if _, err := os.Lstat(filepath.Join(dir, JournalName)); !os.IsNotExist(err) {
			t.Fatalf("invalid archive was replaced by a new main: %v", err)
		}
		got, err := os.ReadFile(archive)
		if err != nil || !bytes.Equal(got, history) {
			t.Fatalf("corrupt archive history changed: got=%q err=%v", got, err)
		}
	})

	t.Run("symlink archive", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "history-target")
		if err := os.WriteFile(target, []byte("history"), JournalFilePerm); err != nil {
			t.Fatalf("WriteFile target: %v", err)
		}
		archive := filepath.Join(dir, JournalName+".1")
		if err := os.Symlink(target, archive); err != nil {
			t.Fatalf("Symlink archive: %v", err)
		}
		store := NewFileStore(dir)
		if store.IsReady() {
			t.Fatal("missing main + symlink archive unexpectedly became ready")
		}
		if _, err := os.Lstat(filepath.Join(dir, JournalName)); !os.IsNotExist(err) {
			t.Fatalf("symlink archive was replaced by a new main: %v", err)
		}
		if info, err := os.Lstat(archive); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("archive symlink was not preserved: mode=%v err=%v", info.Mode(), err)
		}
	})
}

func TestExternalCleanupStoreCrashWindowArchiveRecoveryAndRenameFailure(t *testing.T) {
	seedDir := func(t *testing.T) (string, Record) {
		t.Helper()
		dir := t.TempDir()
		store := NewFileStore(dir)
		reservation := Reservation{
			Version:    JournalVersion,
			SessionID:  "archive-recovery",
			Kind:       remote.SharedServiceClaudeCredentials,
			ReservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		if err := store.Reserve(reservation); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		record := Record{
			Version:         JournalVersion,
			SessionID:       reservation.SessionID,
			PID:             4343,
			ProcessIdentity: "archive-process-identity",
			Kind:            reservation.Kind,
			RegisteredAt:    time.Now().UTC().Format(time.RFC3339Nano),
		}
		if err := store.Register(record); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if err := os.Rename(filepath.Join(dir, JournalName), filepath.Join(dir, JournalName+".1")); err != nil {
			t.Fatalf("simulate rotation crash: %v", err)
		}
		return dir, record
	}

	t.Run("valid archive restored", func(t *testing.T) {
		dir, record := seedDir(t)
		store := NewFileStore(dir)
		active, err := store.LoadActive()
		if err != nil || len(active) != 1 || !sameExternalCleanupProcess(active[0], record) {
			t.Fatalf("valid archive recovery active=%+v err=%v", active, err)
		}
		if _, err := os.Lstat(filepath.Join(dir, JournalName)); err != nil {
			t.Fatalf("recovered main missing: %v", err)
		}
	})

	t.Run("rename failure remains fail closed", func(t *testing.T) {
		dir, _ := seedDir(t)
		injected := errors.New("injected archive recovery rename failure")
		store := NewFileStoreWithRename(dir, func(string, string) error { return injected })
		if store.IsReady() {
			t.Fatal("archive rename failure unexpectedly became ready")
		}
		if _, err := os.Lstat(filepath.Join(dir, JournalName)); !os.IsNotExist(err) {
			t.Fatalf("rename failure created empty main: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(dir, JournalName+".1")); err != nil {
			t.Fatalf("rename failure lost archive: %v", err)
		}
	})
}

// TestExternalCleanupStoreSkipsRetiredKindEvents freezes the retired-kind
// replay semantics (R4): journals written by older versions only ever carried
// shared-service kinds 1/2 (the retired compression-proxy services). Replay
// must SKIP those historical lines (not apply, not abort), the store must be
// ready, LoadActive/LoadPending must not error, and a kind=3 event in the
// same journal must still apply normally.
func TestExternalCleanupStoreSkipsRetiredKindEvents(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	mkLine := func(action journalAction, sessionID string, kind remote.SharedServiceKind, pid int, identity string) []byte {
		t.Helper()
		event := JournalEvent{Version: JournalVersion, Action: action, OccurredAt: now}
		switch action {
		case ActionReserved, ActionReservationCompleted:
			event.Reservation = Reservation{Version: JournalVersion, SessionID: sessionID, Kind: kind, ReservedAt: now}
		default:
			event.Record = Record{Version: JournalVersion, SessionID: sessionID, PID: pid, ProcessIdentity: identity, Kind: kind, RegisteredAt: now}
		}
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("Marshal event: %v", err)
		}
		return append(line, '\n')
	}
	var journal []byte
	journal = append(journal, mkLine(ActionRegistered, "retired-claude", remote.SharedServiceKind(1), 5101, "retired-claude-identity")...)
	journal = append(journal, mkLine(ActionRegistered, "retired-codex", remote.SharedServiceKind(2), 5102, "retired-codex-identity")...)
	journal = append(journal, mkLine(ActionReserved, "retired-reservation", remote.SharedServiceKind(1), 0, "")...)
	activeRecord := Record{Version: JournalVersion, SessionID: "active-kind3", PID: 5103, ProcessIdentity: "active-kind3-identity", Kind: remote.SharedServiceClaudeCredentials, RegisteredAt: now}
	journal = append(journal, mkLine(ActionRegistered, activeRecord.SessionID, activeRecord.Kind, activeRecord.PID, activeRecord.ProcessIdentity)...)

	if err := os.WriteFile(filepath.Join(dir, JournalName), journal, JournalFilePerm); err != nil {
		t.Fatalf("WriteFile journal: %v", err)
	}
	store := NewFileStore(dir)
	if !store.IsReady() {
		t.Fatal("store with retired-kind lines must stay ready (replay skips them)")
	}
	pending, err := store.LoadPending()
	if err != nil || len(pending) != 0 {
		t.Fatalf("retired reservation must be skipped: pending=%+v err=%v", pending, err)
	}
	active, err := store.LoadActive()
	if err != nil || len(active) != 1 || !sameExternalCleanupProcess(active[0], activeRecord) {
		t.Fatalf("only the kind=3 record must apply: active=%+v err=%v", active, err)
	}

	// A retired-kind reservation Reserve() must be rejected as retired for NEW
	// writes (no new producers may resurrect the retired kinds).
	if err := store.Reserve(Reservation{Version: JournalVersion, SessionID: "new-retired", Kind: remote.SharedServiceKind(1), ReservedAt: now}); !errors.Is(err, ErrRetiredKind) {
		t.Fatalf("Reserve with retired kind = %v, want ErrRetiredKind", err)
	}
	if err := store.Register(Record{Version: JournalVersion, SessionID: "new-retired", PID: 5199, ProcessIdentity: "x", Kind: remote.SharedServiceKind(2), RegisteredAt: now}); !errors.Is(err, ErrRetiredKind) {
		t.Fatalf("Register with retired kind = %v, want ErrRetiredKind", err)
	}
}
