package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/remote"
)

func TestExternalCleanupStoreFsyncReplayAndExactCompletion(t *testing.T) {
	dir := t.TempDir()
	store := newFileExternalCleanupStore(dir)
	if !store.IsReady() {
		t.Fatal("new cleanup store is not ready")
	}
	reservation := externalCleanupReservation{
		Version:    externalCleanupJournalVersion,
		SessionID:  "durable-cleanup",
		Kind:       remote.SharedServiceClaudeHeadroom,
		ReservedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := store.Reserve(reservation); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	record := externalCleanupRecord{
		Version:         externalCleanupJournalVersion,
		SessionID:       reservation.SessionID,
		PID:             4242,
		ProcessIdentity: "test-start-identity",
		Kind:            reservation.Kind,
		RegisteredAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := store.Register(record); err != nil {
		t.Fatalf("Register: %v", err)
	}
	info, err := os.Lstat(filepath.Join(dir, externalCleanupJournalName))
	if err != nil {
		t.Fatalf("Lstat journal: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != externalCleanupJournalFilePerm {
		t.Fatalf("journal mode=%v want regular 0600", info.Mode())
	}

	reloaded := newFileExternalCleanupStore(dir)
	active, err := reloaded.LoadActive()
	if err != nil || len(active) != 1 || !sameExternalCleanupProcess(active[0], record) {
		t.Fatalf("reloaded active=%+v err=%v", active, err)
	}
	if err := reloaded.Complete(record); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	third := newFileExternalCleanupStore(dir)
	active, err = third.LoadActive()
	if err != nil || len(active) != 0 {
		t.Fatalf("completed record resurrected: active=%+v err=%v", active, err)
	}
}

func TestExternalCleanupStoreReplaysLegacyRegisteredRecordWithoutReservation(t *testing.T) {
	dir := t.TempDir()
	record := externalCleanupRecord{
		Version:         externalCleanupJournalVersion,
		SessionID:       "legacy-active",
		PID:             4244,
		ProcessIdentity: "legacy-process-identity",
		Kind:            remote.SharedServiceCodexHeadroom,
		RegisteredAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}
	event := externalCleanupJournalEvent{
		Version:    externalCleanupJournalVersion,
		Action:     externalCleanupRegistered,
		Record:     record,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	line, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal legacy event: %v", err)
	}
	line = append(line, '\n')
	if err := os.WriteFile(filepath.Join(dir, externalCleanupJournalName), line, externalCleanupJournalFilePerm); err != nil {
		t.Fatalf("WriteFile legacy journal: %v", err)
	}
	store := newFileExternalCleanupStore(dir)
	active, err := store.LoadActive()
	if err != nil || len(active) != 1 || !sameExternalCleanupProcess(active[0], record) {
		t.Fatalf("legacy replay active=%+v err=%v", active, err)
	}
}

func TestExternalCleanupStoreMissingMainInvalidArchiveFailsClosedWithoutOverwrite(t *testing.T) {
	t.Run("corrupt archive", func(t *testing.T) {
		dir := t.TempDir()
		archive := filepath.Join(dir, externalCleanupJournalName+".1")
		history := []byte("corrupt-but-authoritative-history\n")
		if err := os.WriteFile(archive, history, externalCleanupJournalFilePerm); err != nil {
			t.Fatalf("WriteFile archive: %v", err)
		}
		store := newFileExternalCleanupStore(dir)
		if store.IsReady() {
			t.Fatal("missing main + corrupt archive unexpectedly became ready")
		}
		if _, err := os.Lstat(filepath.Join(dir, externalCleanupJournalName)); !os.IsNotExist(err) {
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
		if err := os.WriteFile(target, []byte("history"), externalCleanupJournalFilePerm); err != nil {
			t.Fatalf("WriteFile target: %v", err)
		}
		archive := filepath.Join(dir, externalCleanupJournalName+".1")
		if err := os.Symlink(target, archive); err != nil {
			t.Fatalf("Symlink archive: %v", err)
		}
		store := newFileExternalCleanupStore(dir)
		if store.IsReady() {
			t.Fatal("missing main + symlink archive unexpectedly became ready")
		}
		if _, err := os.Lstat(filepath.Join(dir, externalCleanupJournalName)); !os.IsNotExist(err) {
			t.Fatalf("symlink archive was replaced by a new main: %v", err)
		}
		if info, err := os.Lstat(archive); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("archive symlink was not preserved: mode=%v err=%v", info.Mode(), err)
		}
	})
}

func TestExternalCleanupStoreCrashWindowArchiveRecoveryAndRenameFailure(t *testing.T) {
	seedDir := func(t *testing.T) (string, externalCleanupRecord) {
		t.Helper()
		dir := t.TempDir()
		store := newFileExternalCleanupStore(dir)
		reservation := externalCleanupReservation{
			Version:    externalCleanupJournalVersion,
			SessionID:  "archive-recovery",
			Kind:       remote.SharedServiceClaudeHeadroom,
			ReservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		if err := store.Reserve(reservation); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		record := externalCleanupRecord{
			Version:         externalCleanupJournalVersion,
			SessionID:       reservation.SessionID,
			PID:             4343,
			ProcessIdentity: "archive-process-identity",
			Kind:            reservation.Kind,
			RegisteredAt:    time.Now().UTC().Format(time.RFC3339Nano),
		}
		if err := store.Register(record); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if err := os.Rename(filepath.Join(dir, externalCleanupJournalName), filepath.Join(dir, externalCleanupJournalName+".1")); err != nil {
			t.Fatalf("simulate rotation crash: %v", err)
		}
		return dir, record
	}

	t.Run("valid archive restored", func(t *testing.T) {
		dir, record := seedDir(t)
		store := newFileExternalCleanupStore(dir)
		active, err := store.LoadActive()
		if err != nil || len(active) != 1 || !sameExternalCleanupProcess(active[0], record) {
			t.Fatalf("valid archive recovery active=%+v err=%v", active, err)
		}
		if _, err := os.Lstat(filepath.Join(dir, externalCleanupJournalName)); err != nil {
			t.Fatalf("recovered main missing: %v", err)
		}
	})

	t.Run("rename failure remains fail closed", func(t *testing.T) {
		dir, _ := seedDir(t)
		injected := errors.New("injected archive recovery rename failure")
		store := newFileExternalCleanupStoreWithRename(dir, func(string, string) error { return injected })
		if store.IsReady() {
			t.Fatal("archive rename failure unexpectedly became ready")
		}
		if _, err := os.Lstat(filepath.Join(dir, externalCleanupJournalName)); !os.IsNotExist(err) {
			t.Fatalf("rename failure created empty main: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(dir, externalCleanupJournalName+".1")); err != nil {
			t.Fatalf("rename failure lost archive: %v", err)
		}
	})
}

func TestExternalCleanupStoreCorruptFileFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, externalCleanupJournalName)
	if err := os.WriteFile(path, []byte("not-json\n"), externalCleanupJournalFilePerm); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store := newFileExternalCleanupStore(dir)
	if store.IsReady() {
		t.Fatal("corrupt cleanup journal unexpectedly ready")
	}
	if _, err := store.LoadActive(); err == nil {
		t.Fatal("corrupt cleanup journal did not fail closed")
	}

	app := newTestApp(t)
	app.sharedCoord = remote.NewSharedServiceCoordinator()
	app.externalCleanupStore = store
	if err := app.recoverExternalCleanups(); err == nil {
		t.Fatal("App recovery unexpectedly accepted corrupt cleanup journal")
	}
	if !app.isExternalCleanupRecoveryBlocked() {
		t.Fatal("corrupt durable ownership did not set the Headroom recovery fence")
	}
	if _, err := app.acquireSharedMutation(remote.SharedServiceClaudeHeadroom, remote.MutationStop); !errors.Is(err, remote.ErrSharedServiceInUse) {
		t.Fatalf("recovery fence mutation error=%v", err)
	}
	if err, release := app.stopAllHeadroomForUninstall(); !errors.Is(err, envcheck.ErrHeadroomInUse) {
		if release != nil {
			release()
		}
		t.Fatalf("recovery fence uninstall error=%v", err)
	}
}
