package remote

// R6-001: external Launcher runs need an opaque run identity of their own.
// They must not forge a writable Control RunPermit, but their startup admission
// must atomically promote to the same shared-dependency lifetime semantics.

import (
	"context"
	"errors"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

func TestR6_001_ExternalAdmissionPromotesToExactLifetimeLease(t *testing.T) {
	coord := NewSharedServiceCoordinator()
	admission, err := coord.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, [32]byte{6, 1})
	if err != nil {
		t.Fatalf("AcquireLaunchAdmissionForConfig: %v", err)
	}
	identity, err := coord.MintExternalRunIdentity(contract.SessionID("external-claude"))
	if err != nil {
		t.Fatalf("MintExternalRunIdentity: %v", err)
	}
	lease, err := coord.AcquireForExternalRunWithAdmission(
		context.Background(), identity, SharedServiceClaudeCredentials, [32]byte{6, 1}, admission,
	)
	if err != nil {
		t.Fatalf("AcquireForExternalRunWithAdmission: %v", err)
	}
	if lease.run != nil {
		t.Fatal("external lease forged a writable Control run identity")
	}
	if lease.externalRun != identity {
		t.Fatal("external lease did not retain the exact opaque external identity")
	}
	if got := coord.LaunchAdmissionCount(SharedServiceClaudeCredentials); got != 0 {
		t.Fatalf("promotion left startup admission behind: %d", got)
	}
	if got := coord.LeaseCount(SharedServiceClaudeCredentials); got != 1 {
		t.Fatalf("active external leases=%d want 1", got)
	}

	if _, err := coord.AcquireMutationAdmission(SharedServiceClaudeCredentials, MutationStop); !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("manual stop during external run error=%v want ErrSharedServiceInUse", err)
	}

	if err := coord.ReleaseExact(context.Background(), lease); err != nil {
		t.Fatalf("ReleaseExact: %v", err)
	}
	if err := coord.ReleaseExact(context.Background(), lease); err != nil {
		t.Fatalf("idempotent ReleaseExact: %v", err)
	}
	if got := coord.LeaseCount(SharedServiceClaudeCredentials); got != 0 {
		t.Fatalf("released external leases=%d want 0", got)
	}
	mutation, err := coord.AcquireMutationAdmission(SharedServiceClaudeCredentials, MutationStop)
	if err != nil {
		t.Fatalf("manual stop remained blocked after exact release: %v", err)
	}
	coord.ReleaseMutationAdmission(mutation)
}

func TestR6_001_ExternalIdentityIsCoordinatorBoundAndStaleAdmissionFailsClosed(t *testing.T) {
	owner := NewSharedServiceCoordinator()
	other := NewSharedServiceCoordinator()
	identity, err := owner.MintExternalRunIdentity("external-owner")
	if err != nil {
		t.Fatalf("MintExternalRunIdentity: %v", err)
	}
	admission, err := other.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, [32]byte{6, 2})
	if err != nil {
		t.Fatalf("AcquireLaunchAdmission: %v", err)
	}
	if _, err := other.AcquireForExternalRunWithAdmission(context.Background(), identity, SharedServiceClaudeCredentials, [32]byte{6, 2}, admission); err == nil {
		t.Fatal("identity minted by another coordinator was accepted")
	}
	if got := other.LeaseCount(SharedServiceClaudeCredentials); got != 0 {
		t.Fatalf("foreign identity leaked lease count=%d", got)
	}
	if got := other.LaunchAdmissionCount(SharedServiceClaudeCredentials); got != 1 {
		t.Fatalf("failed promotion consumed exact admission: count=%d", got)
	}
	other.ReleaseLaunchAdmission(admission)

	localAdmission, err := owner.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, [32]byte{6, 3})
	if err != nil {
		t.Fatalf("local AcquireLaunchAdmission: %v", err)
	}
	owner.ReleaseLaunchAdmission(localAdmission)
	if _, err := owner.AcquireForExternalRunWithAdmission(context.Background(), identity, SharedServiceClaudeCredentials, [32]byte{6, 3}, localAdmission); err == nil {
		t.Fatal("released startup admission was promoted")
	}
	if got := owner.LeaseCount(SharedServiceClaudeCredentials); got != 0 {
		t.Fatalf("stale promotion leaked lease count=%d", got)
	}
}

func TestR6_001_ShutdownClosesCoordinatorAndClearsExternalLease(t *testing.T) {
	coord := NewSharedServiceCoordinator()
	admission, err := coord.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, [32]byte{6, 4})
	if err != nil {
		t.Fatalf("AcquireLaunchAdmissionForConfig: %v", err)
	}
	identity, err := coord.MintExternalRunIdentity("external-shutdown")
	if err != nil {
		t.Fatalf("MintExternalRunIdentity: %v", err)
	}
	lease, err := coord.AcquireForExternalRunWithAdmission(context.Background(), identity, SharedServiceClaudeCredentials, [32]byte{6, 4}, admission)
	if err != nil {
		t.Fatalf("AcquireForExternalRunWithAdmission: %v", err)
	}

	coord.ClearAll()
	if got := coord.LeaseCount(SharedServiceClaudeCredentials); got != 0 {
		t.Fatalf("shutdown retained external leases=%d", got)
	}
	if _, err := coord.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, [32]byte{}); !errors.Is(err, ErrSharedCoordinatorClosed) {
		t.Fatalf("post-shutdown launch admission error=%v want ErrSharedCoordinatorClosed", err)
	}
	if _, err := coord.MintExternalRunIdentity("late-run"); !errors.Is(err, ErrSharedCoordinatorClosed) {
		t.Fatalf("post-shutdown external identity error=%v want ErrSharedCoordinatorClosed", err)
	}
	if err := coord.ReleaseExact(context.Background(), lease); err != nil {
		t.Fatalf("release after shutdown must remain idempotent: %v", err)
	}
}
