package remote

// R5-002 coordinator transaction tests: uninstall drain and desktop launch
// admission linearize under one mutex. Whichever arrives first wins without a
// check→side-effect→lease window.

import (
	"context"
	"errors"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

func newR5AdmissionRun(t *testing.T, sid contract.SessionID) *RunPermit {
	t.Helper()
	clock := newCtrlFakeClock(time.Now())
	hub := NewSessionEventHub()
	dir := NewAttachmentDirectory()
	arb := NewControlArbiter(clock, hub, dir)
	arb.MarkReady()
	gate := NewControlGate(arb, hub, dir)
	launch, err := gate.BeginDesktopLaunch(context.Background(), MintDesktopAuthority())
	if err != nil {
		t.Fatalf("BeginDesktopLaunch: %v", err)
	}
	run, _, err := gate.RegisterStartingSession(context.Background(), launch, sid)
	if err != nil {
		t.Fatalf("RegisterStartingSession: %v", err)
	}
	return run
}

func TestR5_002_DrainFirstRejectsLaunchAdmission(t *testing.T) {
	coord := NewSharedServiceCoordinator()
	if !coord.BeginHeadroomUninstallDrain() {
		t.Fatal("empty coordinator should enter drain")
	}
	defer coord.EndHeadroomUninstallDrain()

	if _, err := coord.AcquireLaunchAdmission(SharedServiceClaudeHeadroom); !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("admission during drain error=%v want ErrSharedServiceInUse", err)
	}
	if got := coord.LaunchAdmissionCount(SharedServiceClaudeHeadroom); got != 0 {
		t.Fatalf("rejected admission leaked: count=%d", got)
	}
}

func TestR5_002_AdmissionFirstBlocksDrainAndPromotesAtomically(t *testing.T) {
	coord := NewSharedServiceCoordinator()
	admission, err := coord.AcquireLaunchAdmission(SharedServiceClaudeHeadroom)
	if err != nil {
		t.Fatalf("AcquireLaunchAdmission: %v", err)
	}
	defer coord.ReleaseLaunchAdmission(admission)

	// Drain loses because the pending launch linearized first, but remains set
	// briefly until its caller aborts. Exact promotion must still succeed.
	if empty := coord.BeginHeadroomUninstallDrain(); empty {
		t.Fatal("drain ignored a pending launch admission")
	}
	defer coord.EndHeadroomUninstallDrain()

	run := newR5AdmissionRun(t, "r5-admission-first")
	lease, err := coord.AcquireForRunWithAdmission(context.Background(), run, SharedServiceClaudeHeadroom, [32]byte{5}, admission)
	if err != nil {
		t.Fatalf("promote admitted launch while losing drain is set: %v", err)
	}
	defer func() { _ = coord.ReleaseExact(context.Background(), lease) }()
	if got := coord.LaunchAdmissionCount(SharedServiceClaudeHeadroom); got != 0 {
		t.Fatalf("promotion did not consume admission: count=%d", got)
	}
	if got := coord.LeaseCount(SharedServiceClaudeHeadroom); got != 1 {
		t.Fatalf("promoted lease count=%d want 1", got)
	}
}

func TestR5_002_HeadroomMutationAdmissionAndDrainLinearizeBothDirections(t *testing.T) {
	coord := NewSharedServiceCoordinator()
	mutation, err := coord.AcquireMutationAdmission(SharedServiceClaudeHeadroom, MutationStartDifferentConfig)
	if err != nil {
		t.Fatalf("AcquireMutationAdmission: %v", err)
	}
	if got := coord.MutationAdmissionCount(SharedServiceClaudeHeadroom); got != 1 {
		t.Fatalf("mutation admissions=%d want 1", got)
	}
	if empty := coord.BeginHeadroomUninstallDrain(); empty {
		t.Fatal("mutation-first drain reported empty")
	}
	coord.EndHeadroomUninstallDrain()
	if _, err := coord.AcquireLaunchAdmission(SharedServiceClaudeHeadroom); !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("launch crossed in-flight mutation: %v", err)
	}
	coord.ReleaseMutationAdmission(mutation)

	if empty := coord.BeginHeadroomUninstallDrain(); !empty {
		t.Fatal("expected empty drain after mutation release")
	}
	if _, err := coord.AcquireMutationAdmission(SharedServiceClaudeHeadroom, MutationStartDifferentConfig); !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("mutation crossed drain-first barrier: %v", err)
	}
	coord.EndHeadroomUninstallDrain()
}

func TestR5_002_LaunchAdmissionVsDrainConcurrentLinearization(t *testing.T) {
	for i := 0; i < 100; i++ {
		coord := NewSharedServiceCoordinator()
		start := make(chan struct{})
		admissionCh := make(chan *SharedLaunchAdmission, 1)
		admissionErrCh := make(chan error, 1)
		drainCh := make(chan bool, 1)
		go func() {
			<-start
			admission, err := coord.AcquireLaunchAdmission(SharedServiceClaudeHeadroom)
			admissionCh <- admission
			admissionErrCh <- err
		}()
		go func() {
			<-start
			drainCh <- coord.BeginHeadroomUninstallDrain()
		}()
		close(start)
		admission, admissionErr, drainEmpty := <-admissionCh, <-admissionErrCh, <-drainCh

		// Exactly one transaction wins: a successful admission must make the drain
		// non-empty; an empty (proceeding) drain must reject the launch.
		if admissionErr == nil && drainEmpty {
			t.Fatalf("iteration %d: launch admission and empty uninstall drain both won", i)
		}
		if drainEmpty && !errors.Is(admissionErr, ErrSharedServiceInUse) {
			t.Fatalf("iteration %d: drain won but launch error=%v", i, admissionErr)
		}
		if admissionErr == nil && admission == nil {
			t.Fatalf("iteration %d: successful admission returned nil token", i)
		}
		coord.ReleaseLaunchAdmission(admission)
		coord.EndHeadroomUninstallDrain()
	}
}

func TestR5_002_ReleasedAdmissionLetsUninstallRetry(t *testing.T) {
	coord := NewSharedServiceCoordinator()
	admission, err := coord.AcquireLaunchAdmission(SharedServiceCodexHeadroom)
	if err != nil {
		t.Fatalf("AcquireLaunchAdmission: %v", err)
	}
	if empty := coord.BeginHeadroomUninstallDrain(); empty {
		t.Fatal("first drain should lose to pending admission")
	}
	coord.EndHeadroomUninstallDrain()
	coord.ReleaseLaunchAdmission(admission)

	if empty := coord.BeginHeadroomUninstallDrain(); !empty {
		t.Fatal("drain retry should succeed after admission release")
	}
	coord.EndHeadroomUninstallDrain()
}
