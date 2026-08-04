package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/remote"
)

type r10BlockingReserveStore struct {
	externalCleanupStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *r10BlockingReserveStore) Reserve(reservation externalCleanupReservation) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.externalCleanupStore.Reserve(reservation)
}

type r10LegacyRecoveryLauncher struct {
	*r6ExternalLauncher
	recoverErr error
}

func (l *r10LegacyRecoveryLauncher) RecoverProcess(id string, _ int, identity string) (bool, error) {
	l.mu.Lock()
	l.running[id] = true
	l.identities[id] = identity
	l.mu.Unlock()
	return true, l.recoverErr
}

func TestR10_001_SlowReservePastShutdownBudgetCannotLateStart(t *testing.T) {
	for _, cli := range []string{"claude", "codex"} {
		t.Run(cli, func(t *testing.T) {
			app, fake, configDir := newR6ExternalLeaseApp(t)
			underlying := newFileExternalCleanupStore(configDir)
			blocking := &r10BlockingReserveStore{
				externalCleanupStore: underlying,
				entered:              make(chan struct{}),
				release:              make(chan struct{}),
			}
			app.externalCleanupStore = blocking
			app.externalShutdownCleanupBudget = 50 * time.Millisecond
			var rawStarts atomic.Int32
			fake.onStarted = func(string) { rawStarts.Add(1) }

			workDir := t.TempDir()
			launchDone := make(chan error, 1)
			switch cli {
			case "claude":
				providerID := configureR6ClaudeProvider(t, app)
				go func() {
					_, err := app.LaunchSession(providerID, "", "terminal", workDir, false, true, "")
					launchDone <- err
				}()
			case "codex":
				configureR6CodexHeadroom(t, app, configDir)
				go func() {
					_, err := app.LaunchCodexSession("", "", "terminal", workDir, "")
					launchDone <- err
				}()
			}

			select {
			case <-blocking.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("launch did not enter the blocked pre-start Reserve")
			}

			shutdownStarted := time.Now()
			report := app.shutdownExternalOwnershipBounded()
			if elapsed := time.Since(shutdownStarted); elapsed > 400*time.Millisecond {
				t.Fatalf("Shutdown exceeded its budget while Reserve was blocked: %v", elapsed)
			}
			if !report.HandoffTimedOut || len(report.Unrecovered) != 1 {
				t.Fatalf("blocked Reserve shutdown report=%+v", report)
			}
			sessionID := report.Unrecovered[0].SessionID
			if sessionID == "" || report.Unrecovered[0].DurableReservation {
				t.Fatalf("pre-fsync attempt report=%+v", report.Unrecovered[0])
			}

			close(blocking.release)
			select {
			case err := <-launchDone:
				if !errors.Is(err, remote.ErrSharedCoordinatorClosed) {
					t.Fatalf("late-start rejection error=%v want ErrSharedCoordinatorClosed", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("launch did not return after Reserve was released")
			}
			if got := rawStarts.Load(); got != 0 {
				t.Fatalf("Shutdown-fenced slow Reserve reached raw %s launch %d time(s)", cli, got)
			}
			if fake.IsRunning(sessionID) {
				t.Fatal("Shutdown-fenced slow Reserve left a running external process")
			}
			pending, err := underlying.LoadPending()
			if err != nil || len(pending) != 0 {
				t.Fatalf("aborted late-start reservation was not exactly completed: pending=%+v err=%v", pending, err)
			}
			active, err := underlying.LoadActive()
			if err != nil || len(active) != 0 {
				t.Fatalf("aborted late start created an active process record: active=%+v err=%v", active, err)
			}

			app.externalCleanupMu.Lock()
			events := append([]remote.ExternalCleanupAbandonmentEvent(nil), app.externalCleanupEvents...)
			app.externalCleanupMu.Unlock()
			found := false
			for _, event := range events {
				if event.SessionID == sessionID && string(event.Reason) == "shutdown_start_fenced" {
					found = true
					if event.DurableReservation {
						t.Fatalf("completed late-start reservation reported durable: %+v", event)
					}
				}
			}
			if !found {
				t.Fatalf("no typed shutdown_start_fenced event for %s: %+v", sessionID, events)
			}
		})
	}
}

func TestR10_001_StartCommitGenerationLinearizesBothOrders(t *testing.T) {
	t.Run("commit-before-fence", func(t *testing.T) {
		app := &App{}
		generation, err := app.captureExternalProcessStartGeneration()
		if err != nil {
			t.Fatalf("capture generation: %v", err)
		}
		attempt := app.beginExternalOwnershipAttempt("commit-first", remote.SharedServiceClaudeHeadroom, generation)
		defer app.endExternalOwnershipAttempt(attempt)
		if !app.commitExternalProcessStart(attempt, generation) || !attempt.startCommitted {
			t.Fatal("open generation did not commit start")
		}
		app.fenceExternalProcessStarts()
	})

	t.Run("fence-before-commit", func(t *testing.T) {
		app := &App{}
		generation, err := app.captureExternalProcessStartGeneration()
		if err != nil {
			t.Fatalf("capture generation: %v", err)
		}
		attempt := app.beginExternalOwnershipAttempt("fence-first", remote.SharedServiceCodexHeadroom, generation)
		defer app.endExternalOwnershipAttempt(attempt)
		app.fenceExternalProcessStarts()
		if app.commitExternalProcessStart(attempt, generation) || attempt.startCommitted {
			t.Fatal("closed generation committed a late start")
		}
	})
}

func TestR10_002_LegacyProcFSRecordRecoveryRetainsOwnershipAndGlobalFence(t *testing.T) {
	app, _, configDir := newR6ExternalLeaseApp(t)
	record := externalCleanupRecord{
		Version:         externalCleanupJournalVersion,
		SessionID:       "legacy-procfs-live",
		PID:             4242,
		ProcessIdentity: "procfs:987654",
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
	if err := os.WriteFile(filepath.Join(configDir, externalCleanupJournalName), line, externalCleanupJournalFilePerm); err != nil {
		t.Fatalf("Write legacy journal: %v", err)
	}
	store := newFileExternalCleanupStore(configDir)
	loaded, err := store.LoadActive()
	if err != nil || len(loaded) != 1 || loaded[0].ProcessIdentity != record.ProcessIdentity {
		t.Fatalf("legacy journal replay=%+v err=%v", loaded, err)
	}

	legacyErr := launcher.ErrLegacyProcFSIdentity
	fake := &r10LegacyRecoveryLauncher{
		r6ExternalLauncher: newR6ExternalLauncher(),
		recoverErr:         legacyErr,
	}
	fake.setStopError(legacyErr)
	defer fake.finish(record.SessionID)
	app.externalLauncher = fake
	app.externalCleanupStore = store
	app.externalRunPollInterval = 5 * time.Millisecond

	recoverErr := app.recoverExternalCleanups()
	if !errors.Is(recoverErr, legacyErr) {
		t.Fatalf("legacy recovery error=%v want migration uncertainty", recoverErr)
	}
	if !app.isExternalCleanupRecoveryBlocked() {
		t.Fatal("legacy live identity did not latch the global recovery fence")
	}
	if got := r7ExternalCleanupCount(app); got != 1 {
		t.Fatalf("legacy cleanup owners=%d want 1", got)
	}
	if got := app.sharedCoord.LaunchAdmissionCount(remote.SharedServiceCodexHeadroom); got != 1 {
		t.Fatalf("legacy recovery admission count=%d want 1", got)
	}
	if _, err := app.acquireSharedMutation(remote.SharedServiceClaudeHeadroom, remote.MutationStop); !errors.Is(err, remote.ErrSharedServiceInUse) {
		t.Fatalf("legacy global recovery fence allowed other Headroom mutation: %v", err)
	}

	time.Sleep(25 * time.Millisecond)
	active, err := store.LoadActive()
	if err != nil || len(active) != 1 || active[0].SessionID != record.SessionID {
		t.Fatalf("legacy live owner was incorrectly completed: active=%+v err=%v", active, err)
	}
	if !fake.IsRunning(record.SessionID) {
		t.Fatal("legacy live owner was discarded by the recovery reaper")
	}

	fake.finish(record.SessionID)
	waitR6(t, func() bool {
		status := app.GetExternalCleanupRecoveryStatus()
		return len(status.Items) == 1 && status.Items[0].SessionID == record.SessionID && status.Items[0].CanConfirm
	}, "legacy record terminal confirmation availability")
	result, err := app.ConfirmExternalCleanupRecovery(record.SessionID, true)
	if err != nil || !result.Cleared || !result.FenceReleased {
		t.Fatalf("confirm legacy terminal result=%+v err=%v", result, err)
	}
	active, err = store.LoadActive()
	if err != nil || len(active) != 0 || r7ExternalCleanupCount(app) != 0 ||
		app.sharedCoord.LaunchAdmissionCount(remote.SharedServiceCodexHeadroom) != 0 {
		t.Fatalf("legacy confirmed completion active=%+v claims=%d admissions=%d err=%v", active, r7ExternalCleanupCount(app), app.sharedCoord.LaunchAdmissionCount(remote.SharedServiceCodexHeadroom), err)
	}
}
