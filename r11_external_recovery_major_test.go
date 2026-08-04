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

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/session"
)

type r11FailCompleteStore struct{ externalCleanupStore }

func (s *r11FailCompleteStore) Complete(externalCleanupRecord) error {
	return errors.New("injected recovery completion failure")
}

type r11BlockedBeforeRawLauncher struct {
	*r6ExternalLauncher
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (l *r11BlockedBeforeRawLauncher) waitBeforeRawStart() {
	l.once.Do(func() { close(l.entered) })
	<-l.release
}

func (l *r11BlockedBeforeRawLauncher) Launch(
	id string,
	provider config.Provider,
	presetName string,
	apiKey string,
	agentTeams config.AgentTeamsConfig,
	mode session.LaunchMode,
	workDir string,
) (*launcher.LaunchResult, error) {
	l.waitBeforeRawStart()
	return l.r6ExternalLauncher.Launch(id, provider, presetName, apiKey, agentTeams, mode, workDir)
}

func (l *r11BlockedBeforeRawLauncher) LaunchGuarded(
	id string,
	provider config.Provider,
	presetName string,
	apiKey string,
	agentTeams config.AgentTeamsConfig,
	mode session.LaunchMode,
	workDir string,
	beforeRawStart func() error,
) (*launcher.LaunchResult, error) {
	l.waitBeforeRawStart()
	if beforeRawStart != nil {
		if err := beforeRawStart(); err != nil {
			return nil, err
		}
	}
	return l.r6ExternalLauncher.Launch(id, provider, presetName, apiKey, agentTeams, mode, workDir)
}

func (l *r11BlockedBeforeRawLauncher) LaunchCodex(
	id string,
	model string,
	mode session.LaunchMode,
	workDir string,
	env map[string]string,
) (*launcher.LaunchResult, error) {
	l.waitBeforeRawStart()
	return l.r6ExternalLauncher.LaunchCodex(id, model, mode, workDir, env)
}

func (l *r11BlockedBeforeRawLauncher) LaunchCodexGuarded(
	id string,
	model string,
	mode session.LaunchMode,
	workDir string,
	env map[string]string,
	beforeRawStart func() error,
) (*launcher.LaunchResult, error) {
	l.waitBeforeRawStart()
	if beforeRawStart != nil {
		if err := beforeRawStart(); err != nil {
			return nil, err
		}
	}
	return l.r6ExternalLauncher.LaunchCodex(id, model, mode, workDir, env)
}

type r11BlockedAfterAuthorizationLauncher struct {
	*r6ExternalLauncher
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (l *r11BlockedAfterAuthorizationLauncher) LaunchGuarded(
	id string,
	provider config.Provider,
	presetName string,
	apiKey string,
	agentTeams config.AgentTeamsConfig,
	mode session.LaunchMode,
	workDir string,
	beforeRawStart func() error,
) (*launcher.LaunchResult, error) {
	if beforeRawStart != nil {
		if err := beforeRawStart(); err != nil {
			return nil, err
		}
	}
	l.once.Do(func() { close(l.entered) })
	<-l.release
	return l.r6ExternalLauncher.Launch(id, provider, presetName, apiKey, agentTeams, mode, workDir)
}

func TestR11_001_NoHeadroomExternalCommitIsShutdownVisibleAndCannotLateStart(t *testing.T) {
	for _, cli := range []string{"claude", "codex"} {
		t.Run(cli, func(t *testing.T) {
			app, fake, _ := newR6ExternalLeaseApp(t)
			blocked := &r11BlockedBeforeRawLauncher{
				r6ExternalLauncher: fake,
				entered:            make(chan struct{}),
				release:            make(chan struct{}),
			}
			app.externalLauncher = blocked
			app.externalShutdownCleanupBudget = 50 * time.Millisecond
			var rawStarts atomic.Int32
			fake.onStarted = func(string) { rawStarts.Add(1) }
			workDir := t.TempDir()
			launchDone := make(chan error, 1)

			switch cli {
			case "claude":
				providerID := configureR6ClaudeProvider(t, app)
				go func() {
					_, err := app.LaunchSession(providerID, "", "terminal", workDir, false, false, "")
					launchDone <- err
				}()
			case "codex":
				go func() {
					_, err := app.LaunchCodexSession("", "", "terminal", workDir, "")
					launchDone <- err
				}()
			}

			select {
			case <-blocked.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("external launch did not reach the before-raw-start gate")
			}
			report := app.shutdownExternalOwnershipBounded()
			close(blocked.release)

			var launchErr error
			select {
			case launchErr = <-launchDone:
			case <-time.After(2 * time.Second):
				t.Fatal("external launch did not return after gate release")
			}
			defer fake.StopAll()
			if !report.HandoffTimedOut || len(report.Unrecovered) != 1 || report.Unrecovered[0].DurableReservation {
				t.Fatalf("no-Headroom in-flight start was absent from Shutdown report: %+v", report)
			}
			if !errors.Is(launchErr, remote.ErrSharedCoordinatorClosed) {
				t.Fatalf("post-fence no-Headroom launch error=%v want ErrSharedCoordinatorClosed", launchErr)
			}
			if got := rawStarts.Load(); got != 0 {
				t.Fatalf("post-fence %s raw start count=%d want 0", cli, got)
			}
			fake.mu.Lock()
			running := len(fake.running)
			fake.mu.Unlock()
			if running != 0 || r7ExternalCleanupCount(app) != 0 {
				t.Fatalf("post-fence %s left running=%d cleanupClaims=%d", cli, running, r7ExternalCleanupCount(app))
			}
		})
	}
}

func TestR11_001_AuthorizedNoHeadroomLateStartTransfersToReaper(t *testing.T) {
	app, fake, _ := newR6ExternalLeaseApp(t)
	providerID := configureR6ClaudeProvider(t, app)
	blocked := &r11BlockedAfterAuthorizationLauncher{
		r6ExternalLauncher: fake,
		entered:            make(chan struct{}),
		release:            make(chan struct{}),
	}
	app.externalLauncher = blocked
	app.externalShutdownCleanupBudget = 50 * time.Millisecond
	fake.setAsyncStop(true)
	var rawStarts atomic.Int32
	var sessionID string
	fake.onStarted = func(id string) {
		sessionID = id
		rawStarts.Add(1)
	}
	launchDone := make(chan error, 1)
	workDir := t.TempDir()
	go func() {
		_, err := app.LaunchSession(providerID, "", "terminal", workDir, false, false, "")
		launchDone <- err
	}()
	select {
	case <-blocked.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("external launch did not pass raw-start authorization")
	}
	report := app.shutdownExternalOwnershipBounded()
	close(blocked.release)
	select {
	case err := <-launchDone:
		if !errors.Is(err, remote.ErrSharedCoordinatorClosed) {
			t.Fatalf("authorized post-fence launch error=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("authorized late launch did not transfer to cleanup")
	}
	if !report.HandoffTimedOut || len(report.Unrecovered) != 1 || rawStarts.Load() != 1 {
		t.Fatalf("authorized late-start report=%+v rawStarts=%d", report, rawStarts.Load())
	}
	if sessionID == "" || !fake.IsRunning(sessionID) || r7ExternalCleanupCount(app) != 1 {
		t.Fatalf("late child missing reaper ownership: id=%q running=%v claims=%d", sessionID, fake.IsRunning(sessionID), r7ExternalCleanupCount(app))
	}
	fake.finish(sessionID)
	waitR6(t, func() bool { return r7ExternalCleanupCount(app) == 0 }, "authorized late-start reaper terminal")
}

func TestR11_002_LegacyRecoveryConfirmAPIAuditsAndUnlocksSameAppLaunch(t *testing.T) {
	app, _, configDir := newR6ExternalLeaseApp(t)
	record := externalCleanupRecord{
		Version:         externalCleanupJournalVersion,
		SessionID:       "r11-legacy-recovery",
		PID:             5252,
		ProcessIdentity: "procfs:771122",
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
	fake := &r10LegacyRecoveryLauncher{
		r6ExternalLauncher: newR6ExternalLauncher(),
		recoverErr:         launcher.ErrLegacyProcFSIdentity,
	}
	fake.setStopError(launcher.ErrLegacyProcFSIdentity)
	defer fake.finish(record.SessionID)
	app.externalLauncher = fake
	app.externalCleanupStore = store
	app.externalRunPollInterval = 5 * time.Millisecond
	if err := app.recoverExternalCleanups(); !errors.Is(err, launcher.ErrLegacyProcFSIdentity) {
		t.Fatalf("recover legacy record: %v", err)
	}

	status := app.GetExternalCleanupRecoveryStatus()
	if !status.Blocked || len(status.Items) != 1 || status.Items[0].SessionID != record.SessionID || status.Items[0].CanConfirm {
		t.Fatalf("initial legacy recovery status=%+v", status)
	}
	if _, err := app.ConfirmExternalCleanupRecovery(record.SessionID, false); !errors.Is(err, ErrExternalCleanupConfirmationRequired) {
		t.Fatalf("missing explicit confirmation error=%v", err)
	}
	if _, err := app.ConfirmExternalCleanupRecovery(record.SessionID, true); !errors.Is(err, ErrExternalCleanupStillRunning) {
		t.Fatalf("live legacy confirmation error=%v", err)
	}
	active, err := store.LoadActive()
	if err != nil || len(active) != 1 || !app.isExternalCleanupRecoveryBlocked() {
		t.Fatalf("live confirmation altered ownership: active=%+v blocked=%v err=%v", active, app.isExternalCleanupRecoveryBlocked(), err)
	}

	fake.finish(record.SessionID)
	waitR6(t, func() bool {
		status = app.GetExternalCleanupRecoveryStatus()
		return status.Blocked && len(status.Items) == 1 && status.Items[0].CanConfirm
	}, "legacy terminal confirmation availability")
	app.externalCleanupStore = &r11FailCompleteStore{externalCleanupStore: store}
	if _, err := app.ConfirmExternalCleanupRecovery(record.SessionID, true); err == nil {
		t.Fatal("failed exact journal completion unexpectedly unlocked recovery")
	}
	active, err = store.LoadActive()
	if err != nil || len(active) != 1 || !app.isExternalCleanupRecoveryBlocked() || r7ExternalCleanupCount(app) != 1 {
		t.Fatalf("failed confirmation lost authority: active=%+v blocked=%v claims=%d err=%v", active, app.isExternalCleanupRecoveryBlocked(), r7ExternalCleanupCount(app), err)
	}
	app.externalCleanupStore = store
	result, err := app.ConfirmExternalCleanupRecovery(record.SessionID, true)
	if err != nil {
		t.Fatalf("ConfirmExternalCleanupRecovery terminal: %v", err)
	}
	if !result.Cleared || !result.FenceReleased {
		t.Fatalf("terminal recovery result=%+v", result)
	}
	active, err = store.LoadActive()
	if err != nil || len(active) != 0 || app.isExternalCleanupRecoveryBlocked() || r7ExternalCleanupCount(app) != 0 {
		t.Fatalf("confirmed cleanup did not unlock same App: active=%+v blocked=%v claims=%d err=%v", active, app.isExternalCleanupRecoveryBlocked(), r7ExternalCleanupCount(app), err)
	}
	if got := app.sharedCoord.LaunchAdmissionCount(remote.SharedServiceCodexHeadroom); got != 0 {
		t.Fatalf("confirmed cleanup retained admission count=%d", got)
	}
	app.externalCleanupMu.Lock()
	audits := append([]remote.ExternalCleanupRecoveryAuditEvent(nil), app.externalCleanupRecoveryAuditEvents...)
	app.externalCleanupMu.Unlock()
	if len(audits) < 3 || audits[len(audits)-1].Outcome != remote.ExternalCleanupRecoveryAuditCompleted || !audits[len(audits)-1].FenceReleased {
		t.Fatalf("recovery audit trail=%+v", audits)
	}

	fake.setStopError(nil)
	providerID := configureR6ClaudeProvider(t, app)
	id, err := app.LaunchSession(providerID, "", "terminal", t.TempDir(), false, true, "")
	if err != nil {
		t.Fatalf("normal Headroom launch after same-App recovery: %v", err)
	}
	if !fake.IsRunning(id) {
		t.Fatal("normal launch after recovery was not started")
	}
	fake.finish(id)
	waitR6(t, func() bool {
		return app.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom) == 0
	}, "post-recovery normal launch terminal")
}
