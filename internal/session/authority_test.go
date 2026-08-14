package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote/contract"
)

type authorityTestBinding struct{ id processcap.BindingID }

func (b *authorityTestBinding) BindingID() processcap.BindingID { return b.id }
func (b *authorityTestBinding) CloseExact(context.Context) processcap.ExactCloseEvidence {
	evidence, err := processcap.NewExactCloseEvidence(b.id, 91, processcap.CloseConfirmed, nil)
	if err != nil {
		panic(err)
	}
	return evidence
}

func reserveEmbedded(t *testing.T, manager *Manager, id string) *CreateReservation {
	t.Helper()
	reservation, err := manager.ReserveCreate(CreateSpec{
		RequestedID: id, AppType: AppTypeClaudeCode, Origin: launchplan.OriginDesktop,
		Mode: launchplan.ModeEmbedded, Workdir: "/work", RemoteEligible: true,
		Provider: "private-provider", Preset: "private-preset", Model: "private-model",
	})
	if err != nil {
		t.Fatalf("ReserveCreate: %v", err)
	}
	return reservation
}

func activateEmbedded(t *testing.T, manager *Manager, reservation *CreateReservation, run uint64) AuthorityActivationReceipt {
	t.Helper()
	binding := processcap.BindingID{Kind: processcap.BackendPTY, Owner: 10, Generation: run}
	token, err := manager.PrepareActivation(reservation, PreparedAuthorityActivation{
		Recipe:    launchplan.StableRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work", ProviderRef: "private-provider"},
		BindingID: binding, PID: 100 + int(run), RunRevision: run,
		StartedAt: time.Unix(100, 0), LastActivityAt: time.Unix(100, 0),
	})
	if err != nil {
		t.Fatalf("PrepareActivation: %v", err)
	}
	receipt, err := manager.CommitPreparedActivation(token, func() {})
	if err != nil {
		t.Fatalf("CommitPreparedActivation: %v", err)
	}
	return receipt
}

func TestAuthorityReservationHiddenAndIDNeverReused(t *testing.T) {
	manager := NewManager()
	reservation := reserveEmbedded(t, manager, "opaque-id")
	if got := manager.List(); len(got) != 0 {
		t.Fatalf("pending leaked to legacy list: %#v", got)
	}
	if got := manager.ListRemoteSafeSnapshots(); len(got) != 0 {
		t.Fatalf("pending leaked to remote list: %#v", got)
	}
	if _, err := manager.RemoteSnapshotByID("opaque-id"); !errors.Is(err, ErrAuthorityNotFound) {
		t.Fatalf("pending remote detail = %v", err)
	}
	manager.AbortCreate(reservation)
	if _, err := manager.ReserveCreate(CreateSpec{RequestedID: "opaque-id", AppType: AppTypeClaudeCode, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded, Workdir: "/work", RemoteEligible: true}); !errors.Is(err, ErrAuthorityIDCollision) {
		t.Fatalf("used ID was reusable: %v", err)
	}
}

func TestAuthorityActivationPublishesLegacyAndRemoteAtSameID(t *testing.T) {
	manager := NewManager()
	reservation := reserveEmbedded(t, manager, "same-id")
	receipt := activateEmbedded(t, manager, reservation, 7)
	legacy := manager.List()
	remote := manager.ListRemoteSafeSnapshots()
	if len(legacy) != 1 || len(remote) != 1 {
		t.Fatalf("legacy=%d remote=%d", len(legacy), len(remote))
	}
	if legacy[0].ID != "same-id" || remote[0].Handle.SessionID() != legacy[0].ID || receipt.Authority.SessionID() != legacy[0].ID {
		t.Fatalf("identity mismatch: legacy=%#v remote=%#v receipt=%#v", legacy[0], remote[0], receipt)
	}
}

func TestAuthorityExternalIsNeverRemoteVisible(t *testing.T) {
	manager := NewManager()
	reservation, err := manager.ReserveCreate(CreateSpec{RequestedID: "external", AppType: AppTypeCodex, Origin: launchplan.OriginDesktop, Mode: launchplan.ModeExternal, Workdir: "/work", RemoteEligible: false})
	if err != nil {
		t.Fatalf("ReserveCreate: %v", err)
	}
	binding := processcap.BindingID{Kind: processcap.BackendExternalLauncher, Owner: 1, Generation: 1}
	token, err := manager.PrepareActivation(reservation, PreparedAuthorityActivation{BindingID: binding, PID: 22, RunRevision: 1, StartedAt: time.Now(), LastActivityAt: time.Now()})
	if err != nil {
		t.Fatalf("PrepareActivation: %v", err)
	}
	if _, err := manager.CommitPreparedExternalActivation(token); err != nil {
		t.Fatalf("CommitPreparedExternalActivation: %v", err)
	}
	if len(manager.List()) != 1 || len(manager.ListRemoteSafeSnapshots()) != 0 {
		t.Fatalf("external visibility legacy=%d remote=%d", len(manager.List()), len(manager.ListRemoteSafeSnapshots()))
	}
	if _, err := manager.ResolveRemoteHandle("external"); !errors.Is(err, ErrAuthorityNotFound) {
		t.Fatalf("external remote handle = %v", err)
	}
}

func TestAuthorityRemoteSnapshotContainsNoPrivateLaunchFields(t *testing.T) {
	manager := NewManager()
	receipt := activateEmbedded(t, manager, reserveEmbedded(t, manager, "safe"), 1)
	encoded, err := json.Marshal(receipt.Snapshot)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"provider", "preset", "model", "pid", "secret", "env", "output", "claudesessionid"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("snapshot leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestReclaimTombstoneExactAndIdempotent(t *testing.T) {
	manager := NewManager()
	receipt := activateEmbedded(t, manager, reserveEmbedded(t, manager, "remove-me"), 4)
	ref, err := manager.ProcessRef(receipt.Authority)
	if err != nil {
		t.Fatalf("ProcessRef: %v", err)
	}
	token, err := manager.PrepareRemove(receipt.Authority, RemoveExpected{MembershipRevision: 1, LifecycleRevision: 1, RunRevision: 4}, ref.BindingID)
	if err != nil {
		t.Fatalf("PrepareRemove: %v", err)
	}
	binding := &authorityTestBinding{id: ref.BindingID}
	close := binding.CloseExact(context.Background())
	removed, err := manager.CommitPreparedRemove(token, close, time.Unix(200, 0), func() {})
	if err != nil {
		t.Fatalf("CommitPreparedRemove: %v", err)
	}
	wrong := removed
	wrong.LifecycleRevision++
	if err := manager.ReclaimTombstone(wrong); !errors.Is(err, ErrAuthorityInvalidReceipt) {
		t.Fatalf("wrong receipt reclaim = %v", err)
	}
	if err := manager.ReclaimTombstone(removed); err != nil {
		t.Fatalf("ReclaimTombstone: %v", err)
	}
	if err := manager.ReclaimTombstone(removed); err != nil {
		t.Fatalf("idempotent reclaim: %v", err)
	}
	if _, err := manager.ReserveCreate(CreateSpec{RequestedID: "remove-me", AppType: AppTypeClaudeCode, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded, Workdir: "/work", RemoteEligible: true}); !errors.Is(err, ErrAuthorityIDCollision) {
		t.Fatalf("reclaimed ID reused: %v", err)
	}
}

func TestCommitResolvedAttachDoesNotRelookupAndRejectsStaleMembership(t *testing.T) {
	manager := NewManager()
	receipt := activateEmbedded(t, manager, reserveEmbedded(t, manager, "attach"), 3)
	resolved, err := manager.ResolveRemoteHandle("attach")
	if err != nil {
		t.Fatalf("ResolveRemoteHandle: %v", err)
	}
	calls := 0
	if err := manager.CommitResolvedAttach(resolved, func() { calls++ }); err != nil || calls != 1 {
		t.Fatalf("CommitResolvedAttach err=%v calls=%d", err, calls)
	}
	ref, _ := manager.ProcessRef(receipt.Authority)
	removeToken, _ := manager.PrepareRemove(receipt.Authority, RemoveExpected{MembershipRevision: 1, LifecycleRevision: 1, RunRevision: 3}, ref.BindingID)
	binding := &authorityTestBinding{id: ref.BindingID}
	removed, err := manager.CommitPreparedRemove(removeToken, binding.CloseExact(context.Background()), time.Now(), nil)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	calls = 0
	if err := manager.CommitResolvedAttach(resolved, func() { calls++ }); !errors.Is(err, ErrAuthorityNotFound) || calls != 0 {
		t.Fatalf("stale attach err=%v calls=%d receipt=%#v", err, calls, removed)
	}
}

func TestExactRunExitDropsStaleReceipt(t *testing.T) {
	manager := NewManager()
	activateEmbedded(t, manager, reserveEmbedded(t, manager, "exit"), 8)
	if manager.CommitExactRunExit("exit", 7, false, time.Now()) {
		t.Fatal("stale run exit committed")
	}
	if got := manager.GetStatus("exit"); got != StatusRunning {
		t.Fatalf("stale exit changed status to %s", got)
	}
	if !manager.CommitExactRunExit("exit", 8, false, time.Now()) {
		t.Fatal("exact run exit did not commit")
	}
	if got := manager.GetStatus("exit"); got != StatusExited {
		t.Fatalf("exact exit status = %s", got)
	}
}

func TestLegacySessionInfoWireShapeUnchanged(t *testing.T) {
	manager := NewManager()
	s := manager.Create(AppTypePi, "provider", "preset", "model", ModeTerminal, "/work")
	if s == nil {
		t.Fatal("Create returned nil")
	}
	encoded, err := json.Marshal(manager.List()[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	want := []string{"appType", "claudeSessionId", "duration", "id", "mode", "model", "pid", "preset", "provider", "startedAt", "status", "title", "workDir"}
	if !reflect.DeepEqual(stringSet(got), stringSet(want)) {
		t.Fatalf("wire keys = %v, want %v; json=%s", got, want, encoded)
	}
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func TestPreparedLifecycleFencesLegacyExitObserver(t *testing.T) {
	manager := NewManager()
	receipt := activateEmbedded(t, manager, reserveEmbedded(t, manager, "exit-drain"), 6)
	ref, err := manager.ProcessRef(receipt.Authority)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.PrepareLifecycle(receipt.Authority, LifecycleStop, LifecycleExpected{MembershipRevision: 1, LifecycleRevision: 1, RunRevision: 6}, ref.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	manager.MarkExited("exit-drain")
	if manager.GetStatus("exit-drain") != StatusRunning {
		t.Fatalf("legacy exit crossed lifecycle drain: %s", manager.GetStatus("exit-drain"))
	}
	manager.AbortPreparedLifecycle(token)
}

func TestCommitResolvedAttachRejectsPreparedLifecycle(t *testing.T) {
	manager := NewManager()
	receipt := activateEmbedded(t, manager, reserveEmbedded(t, manager, "attach-drain"), 5)
	resolved, err := manager.ResolveRemoteHandle("attach-drain")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := manager.ProcessRef(receipt.Authority)
	if err != nil {
		t.Fatal(err)
	}
	removeToken, err := manager.PrepareRemove(receipt.Authority, RemoveExpected{MembershipRevision: 1, LifecycleRevision: 1, RunRevision: 5}, ref.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := manager.CommitResolvedAttach(resolved, func() { called = true }); !errors.Is(err, ErrAuthorityNotFound) || called {
		t.Fatalf("attach during remove err=%v called=%v", err, called)
	}
	manager.AbortPreparedRemove(removeToken)
}

func TestCommitResolvedAttachDoesNotStarveActivity(t *testing.T) {
	manager := NewManager()
	receipt := activateEmbedded(t, manager, reserveEmbedded(t, manager, "fair"), 2)
	resolved, err := manager.ResolveRemoteHandle("fair")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			manager.TouchActivity(receipt.Authority.SessionID(), 2, time.Unix(int64(101+i), 0))
		}
	}()
	for i := 0; i < 2000; i++ {
		if err := manager.CommitResolvedAttach(resolved, func() {}); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("activity writer starved behind attach guards")
	}
}

func TestAuthorityCommitPanicExits70(t *testing.T) {
	if mode := os.Getenv("AMAGI_AUTHORITY_PANIC_CHILD"); mode != "" {
		manager := NewManager()
		reservation := reserveEmbedded(t, manager, "panic")
		binding := processcap.BindingID{Kind: processcap.BackendPTY, Owner: 1, Generation: 1}
		token, err := manager.PrepareActivation(reservation, PreparedAuthorityActivation{BindingID: binding, PID: 1, RunRevision: 1, StartedAt: time.Now(), LastActivityAt: time.Now()})
		if err != nil {
			os.Exit(69)
		}
		if mode == "activation" {
			_, _ = manager.CommitPreparedActivation(token, func() { panic("activation commit invariant") })
			os.Exit(68)
		}
		receipt, err := manager.CommitPreparedActivation(token, func() {})
		if err != nil {
			os.Exit(67)
		}
		if mode == "attach" {
			resolved, err := manager.ResolveRemoteHandle("panic")
			if err != nil {
				os.Exit(66)
			}
			_ = manager.CommitResolvedAttach(resolved, func() { panic("attach commit invariant") })
			os.Exit(65)
		}
		ref, err := manager.ProcessRef(receipt.Authority)
		if err != nil {
			os.Exit(64)
		}
		removeToken, err := manager.PrepareRemove(receipt.Authority, RemoveExpected{MembershipRevision: 1, LifecycleRevision: 1, RunRevision: 1}, ref.BindingID)
		if err != nil {
			os.Exit(63)
		}
		evidence := (&authorityTestBinding{id: ref.BindingID}).CloseExact(context.Background())
		_, _ = manager.CommitPreparedRemove(removeToken, evidence, time.Now(), func() { panic("remove commit invariant") })
		os.Exit(62)
	}
	for _, mode := range []string{"activation", "attach", "remove"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestAuthorityCommitPanicExits70$")
			cmd.Env = append(os.Environ(), "AMAGI_AUTHORITY_PANIC_CHILD="+mode)
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("child error = %v", err)
			}
			if exitErr.ExitCode() != 70 {
				t.Fatalf("child exit = %d, want 70", exitErr.ExitCode())
			}
		})
	}
}
