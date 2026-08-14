package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/headroom"
	"amagi-codebox/internal/remote"
)

type r9FailRegisterStore struct {
	externalCleanupStore
}

func (s *r9FailRegisterStore) Register(externalCleanupRecord) error {
	return errors.New("injected post-start register failure")
}

type r9BlockingRegisterStore struct {
	externalCleanupStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *r9BlockingRegisterStore) Register(externalCleanupRecord) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return errors.New("injected blocked register failure")
}

type r9BlockingStopAllLauncher struct {
	*r6ExternalLauncher
	entered chan struct{}
	release chan struct{}
	done    chan struct{}
	once    sync.Once
}

func (l *r9BlockingStopAllLauncher) StopAll() {
	l.once.Do(func() { close(l.entered) })
	<-l.release
	l.r6ExternalLauncher.StopAll()
	close(l.done)
}

func TestR9_002_CorruptCleanupFenceBlocksCodexGlobalEnableAndDisableRawEffects(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "enable", true: "disable"}[enabled], func(t *testing.T) {
			app, _, configDir := newR6ExternalLeaseApp(t)
			configureR6CodexHeadroom(t, app, configDir)
			runner := &r5CountingHeadroomRunner{}
			app.CodexHeadroom = headroom.NewHeadroomService(runner, app.Log)
			t.Cleanup(func() {
				_ = app.CodexHeadroom.Stop()
				runner.cleanup()
			})

			if enabled {
				if err := app.CodexHeadroom.StartForOpenAI("https://api.openai.com/v1"); err != nil {
					t.Fatalf("seed running Codex Headroom: %v", err)
				}
			}
			baselineStarts := runner.started.Load()
			codexConfig := filepath.Join(os.Getenv("HOME"), ".codex", "config.toml")
			if err := os.MkdirAll(filepath.Dir(codexConfig), 0o700); err != nil {
				t.Fatalf("MkdirAll Codex config: %v", err)
			}
			const configSentinel = "# R9 recovery-fence sentinel\n"
			if err := os.WriteFile(codexConfig, []byte(configSentinel), 0o600); err != nil {
				t.Fatalf("seed Codex config: %v", err)
			}
			journalPath := filepath.Join(configDir, externalCleanupJournalName)
			if err := os.WriteFile(journalPath, []byte("corrupt\n"), externalCleanupJournalFilePerm); err != nil {
				t.Fatalf("corrupt journal: %v", err)
			}
			app.externalCleanupStore = newFileExternalCleanupStore(configDir)
			if err := app.recoverExternalCleanups(); err == nil || !app.isExternalCleanupRecoveryBlocked() {
				t.Fatalf("corrupt store recovery err=%v blocked=%v", err, app.isExternalCleanupRecoveryBlocked())
			}

			_, err := app.SetCodexGlobalHeadroom(!enabled, "https://api.openai.com/v1", CodexGlobalHeadroomDefaultPort)
			if !errors.Is(err, remote.ErrSharedServiceInUse) {
				t.Fatalf("corrupt-store Codex toggle error=%v want ErrSharedServiceInUse", err)
			}
			if got := runner.started.Load(); got != baselineStarts {
				t.Fatalf("blocked toggle reached raw Start: before=%d after=%d", baselineStarts, got)
			}
			if app.CodexHeadroom.IsRunning() != enabled {
				t.Fatalf("blocked toggle changed raw running state: got=%v want=%v", app.CodexHeadroom.IsRunning(), enabled)
			}
			if got := app.Settings.GetCodexGlobalHeadroom().Enabled; !got {
				t.Fatalf("blocked toggle rewrote persisted state: enabled=%v", got)
			}
			if data, readErr := os.ReadFile(codexConfig); readErr != nil || string(data) != configSentinel {
				t.Fatalf("blocked toggle rewrote Codex config: data=%q err=%v", data, readErr)
			}
		})
	}
}

func TestR9_004_PostStartDurabilityFailureRetainsReservationAndShutdownIsBounded(t *testing.T) {
	app, fake, configDir := newR6ExternalLeaseApp(t)
	providerID := configureR6ClaudeProvider(t, app)
	underlying := newFileExternalCleanupStore(configDir)
	app.externalCleanupStore = &r9FailRegisterStore{externalCleanupStore: underlying}
	app.externalShutdownCleanupBudget = 75 * time.Millisecond
	fake.setStopError(errors.New("injected permanent Stop failure"))

	events := make(chan remote.ExternalCleanupAbandonmentEvent, 8)
	app.externalCleanupEventSink = func(event remote.ExternalCleanupAbandonmentEvent) { events <- event }
	var sessionID string
	fake.onStarted = func(id string) { sessionID = id }

	launchStarted := time.Now()
	if _, err := app.LaunchSession(providerID, "", "terminal", t.TempDir(), true, ""); err == nil {
		t.Fatal("post-start register failure unexpectedly returned success")
	}
	if elapsed := time.Since(launchStarted); elapsed > 500*time.Millisecond {
		t.Fatalf("durability+Stop failure blocked launch for %v", elapsed)
	}
	if sessionID == "" || !fake.IsRunning(sessionID) {
		t.Fatalf("test process not retained: id=%q running=%v", sessionID, fake.IsRunning(sessionID))
	}
	select {
	case event := <-events:
		if event.SessionID != sessionID || event.Reason != remote.ExternalCleanupAbandonmentDurabilityHandoff || !event.DurableReservation {
			t.Fatalf("typed abandonment event=%+v", event)
		}
	default:
		t.Fatal("post-start failure emitted no typed abandonment event")
	}

	freshStore := newFileExternalCleanupStore(configDir)
	pending, err := freshStore.LoadPending()
	if err != nil || len(pending) != 1 || pending[0].SessionID != sessionID {
		t.Fatalf("pre-start reservation was not durable: pending=%+v err=%v", pending, err)
	}
	freshApp := newTestApp(t)
	freshApp.externalCleanupStore = freshStore
	freshApp.sharedCoord = remote.NewSharedServiceCoordinator()
	freshApp.externalLauncher = newR6ExternalLauncher()
	if err := freshApp.recoverExternalCleanups(); err == nil || !freshApp.isExternalCleanupRecoveryBlocked() {
		t.Fatalf("fresh App did not fail-close unresolved reservation: err=%v blocked=%v", err, freshApp.isExternalCleanupRecoveryBlocked())
	}

	shutdownStarted := time.Now()
	report := app.shutdownExternalOwnershipBounded()
	if elapsed := time.Since(shutdownStarted); elapsed > 500*time.Millisecond {
		t.Fatalf("bounded external shutdown took %v", elapsed)
	}
	if len(report.Unrecovered) == 0 || report.Unrecovered[0].SessionID != sessionID || !report.Unrecovered[0].DurableReservation {
		t.Fatalf("shutdown did not honestly report durable unrecovered process: %+v", report)
	}

	fake.finish(sessionID)
	waitR6(t, func() bool {
		pending, loadErr := underlying.LoadPending()
		return loadErr == nil && len(pending) == 0
	}, "terminal completion of durable reservation")
}

func TestR9_004_ConcurrentBlockedRegisterCannotHoldShutdownPastBudget(t *testing.T) {
	app, fake, configDir := newR6ExternalLeaseApp(t)
	providerID := configureR6ClaudeProvider(t, app)
	underlying := newFileExternalCleanupStore(configDir)
	blockingStore := &r9BlockingRegisterStore{
		externalCleanupStore: underlying,
		entered:              make(chan struct{}),
		release:              make(chan struct{}),
	}
	app.externalCleanupStore = blockingStore
	app.externalShutdownCleanupBudget = 60 * time.Millisecond
	fake.setStopError(errors.New("injected permanent Stop failure"))
	var sessionID string
	fake.onStarted = func(id string) { sessionID = id }

	launchDone := make(chan error, 1)
	workDir := t.TempDir()
	go func() {
		_, err := app.LaunchSession(providerID, "", "terminal", workDir, true, "")
		launchDone <- err
	}()
	select {
	case <-blockingStore.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("launch did not enter blocked ownership registration")
	}

	started := time.Now()
	report := app.shutdownExternalOwnershipBounded()
	if elapsed := time.Since(started); elapsed > 400*time.Millisecond {
		t.Fatalf("in-flight register held Shutdown for %v", elapsed)
	}
	if !report.HandoffTimedOut || len(report.Unrecovered) != 1 || report.Unrecovered[0].SessionID != sessionID || !report.Unrecovered[0].DurableReservation {
		t.Fatalf("blocked handoff report=%+v", report)
	}

	close(blockingStore.release)
	select {
	case err := <-launchDone:
		if err == nil {
			t.Fatal("released register failure unexpectedly launched successfully")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("launch did not return after blocked register release")
	}
	fake.finish(sessionID)
	waitR6(t, func() bool {
		pending, err := underlying.LoadPending()
		return err == nil && len(pending) == 0
	}, "blocked-register reservation terminal completion")
}

func TestR9_004_BlockedStopAllReturnsAtBudgetWithTypedUnrecoveredReport(t *testing.T) {
	app, fake, _ := newR6ExternalLeaseApp(t)
	providerID := configureR6ClaudeProvider(t, app)
	blocking := &r9BlockingStopAllLauncher{
		r6ExternalLauncher: fake,
		entered:            make(chan struct{}),
		release:            make(chan struct{}),
		done:               make(chan struct{}),
	}
	app.externalLauncher = blocking
	app.externalShutdownCleanupBudget = 60 * time.Millisecond
	id, err := app.LaunchSession(providerID, "", "terminal", t.TempDir(), true, "")
	if err != nil {
		t.Fatalf("LaunchSession: %v", err)
	}

	started := time.Now()
	report := app.shutdownExternalOwnershipBounded()
	if elapsed := time.Since(started); elapsed > 400*time.Millisecond {
		t.Fatalf("blocked StopAll exceeded shutdown bound: %v", elapsed)
	}
	select {
	case <-blocking.entered:
	default:
		t.Fatal("shutdown did not attempt StopAll")
	}
	if !report.StopAllTimedOut || len(report.Unrecovered) == 0 || report.Unrecovered[0].SessionID != id {
		t.Fatalf("blocked StopAll report=%+v", report)
	}
	if report.Unrecovered[0].Reason != remote.ExternalCleanupAbandonmentShutdownStopTimeout {
		t.Fatalf("blocked StopAll reason=%q", report.Unrecovered[0].Reason)
	}

	close(blocking.release)
	select {
	case <-blocking.done:
	case <-time.After(2 * time.Second):
		t.Fatal("released StopAll goroutine did not finish")
	}
}
