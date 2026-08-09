package processcap

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

type testWaiter struct{ confirmed atomic.Bool }

func (w *testWaiter) Wait(context.Context) error { return nil }
func (w *testWaiter) Confirmed() bool            { return w.confirmed.Load() }

type testBinding struct {
	id       BindingID
	closeIDs *atomic.Uint64
}

func (b *testBinding) BindingID() BindingID { return b.id }
func (b *testBinding) CloseExact(context.Context) ExactCloseEvidence {
	receipt := b.closeIDs.Add(1)
	evidence, err := NewExactCloseEvidence(b.id, receipt, CloseConfirmed, nil)
	if err != nil {
		panic(err)
	}
	return evidence
}

func TestRegistryExactKeyDoesNotResolveByPartialIdentity(t *testing.T) {
	registry := NewRegistry()
	var receipts atomic.Uint64
	binding := &testBinding{id: BindingID{Kind: BackendPTY, Owner: 41, Generation: 7}, closeIDs: &receipts}
	key, err := registry.Register(binding, 13)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if key.RunGeneration != 13 {
		t.Fatalf("run generation = %d", key.RunGeneration)
	}
	if _, ok := registry.ResolveExact(binding.id, 12); ok {
		t.Fatal("stale run generation resolved a binding")
	}
	other := binding.id
	other.Generation++
	if _, ok := registry.ResolveExact(other, 13); ok {
		t.Fatal("different binding generation resolved a binding")
	}
	got, ok := registry.ResolveExact(binding.id, 13)
	if !ok || got != binding {
		t.Fatal("exact binding did not resolve")
	}
}

func TestRegistryReleaseIsExactAndIdempotent(t *testing.T) {
	registry := NewRegistry()
	var receipts atomic.Uint64
	binding := &testBinding{id: BindingID{Kind: BackendPTY, Owner: 2, Generation: 3}, closeIDs: &receipts}
	if _, err := registry.Register(binding, 5); err != nil {
		t.Fatalf("Register: %v", err)
	}
	other := &testBinding{id: binding.id, closeIDs: &receipts}
	if err := registry.ReleaseExact(binding.id, 5, other); err != ErrBindingMismatch {
		t.Fatalf("mismatched release = %v", err)
	}
	if _, ok := registry.ResolveExact(binding.id, 5); !ok {
		t.Fatal("mismatched release removed binding")
	}
	if err := registry.ReleaseExact(binding.id, 5, binding); err != nil {
		t.Fatalf("exact release: %v", err)
	}
	if err := registry.ReleaseExact(binding.id, 5, binding); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
}

func TestRegistryCloseExactInvokesCapabilityOnceAndReusesEvidence(t *testing.T) {
	registry := NewRegistry()
	var receipts atomic.Uint64
	binding := &testBinding{id: BindingID{Kind: BackendPTY, Owner: 9, Generation: 4}, closeIDs: &receipts}
	if _, err := registry.Register(binding, 7); err != nil {
		t.Fatal(err)
	}
	first, ok := registry.CloseExact(context.Background(), binding.id, 7)
	if !ok {
		t.Fatal("first close unavailable")
	}
	second, ok := registry.CloseExact(context.Background(), binding.id, 7)
	if !ok || second.ReceiptID() != first.ReceiptID() || receipts.Load() != 1 {
		t.Fatalf("first=%d second=%d invocations=%d", first.ReceiptID(), second.ReceiptID(), receipts.Load())
	}
}

func TestRegistryConcurrentCloseExactInvokesOneConcreteCapability(t *testing.T) {
	registry := NewRegistry()
	var receipts atomic.Uint64
	binding := &testBinding{id: BindingID{Kind: BackendExternalLauncher, Owner: 17, Generation: 8}, closeIDs: &receipts}
	if _, err := registry.Register(binding, 21); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	receiptIDs := make(chan uint64, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			evidence, ok := registry.CloseExact(context.Background(), binding.id, 21)
			if !ok {
				receiptIDs <- 0
				return
			}
			receiptIDs <- evidence.ReceiptID()
		}()
	}
	wg.Wait()
	close(receiptIDs)
	for receiptID := range receiptIDs {
		if receiptID != 1 {
			t.Fatalf("concurrent receipt = %d, want 1", receiptID)
		}
	}
	if receipts.Load() != 1 {
		t.Fatalf("concrete close invocations = %d", receipts.Load())
	}
}

func TestCloseEvidenceRejectsUnconfirmedTerminalDisposition(t *testing.T) {
	id := BindingID{Kind: BackendPTY, Owner: 1, Generation: 1}
	waiter := &testWaiter{}
	if _, err := NewExactCloseEvidence(id, 1, CloseConfirmed, waiter); err == nil {
		t.Fatal("unconfirmed waiter accepted as confirmed evidence")
	}
	if _, err := NewExactCloseEvidence(id, 1, CloseIndeterminate, waiter); err != nil {
		t.Fatalf("indeterminate evidence: %v", err)
	}
}
