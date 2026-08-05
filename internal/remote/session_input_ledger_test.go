package remote

// session_input_ledger_test.go — CG-03 per-session input ledger tests
// (contract-addendum-cg03.md §5/§6 C6-C9; design §9).
//
// Covers Reserve state machine (owner/committed/pending/indeterminate/full),
// Commit/MarkIndeterminate/ReleaseUncalled transitions, no-eviction capacity,
// registry lifecycle, and concurrency (race-detector). The handleInput canonical
// routing + ACK producer is covered by the ledger semantics + the existing gate
// tests; the wire parity (InputAckEvent encode/decode) is in contract/wire_test.go.

import (
	"embed"
	"fmt"
	"sync"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

func canonicalMsg(n int) contract.MessageID {
	// 32 lowercase hex chars (n repeated), total 39 bytes with msg-v1- prefix.
	hex := fmt.Sprintf("%032d", n)
	return contract.MessageID("msg-v1-" + hex)
}

func TestSessionInputLedger_ReserveNew(t *testing.T) {
	l := NewSessionInputLedger()
	got := l.Reserve("dev1", canonicalMsg(1))
	if got != InputLedgerOwner {
		t.Fatalf("first Reserve = %v, want Owner", got)
	}
}

func TestSessionInputLedger_CommittedReACK(t *testing.T) {
	l := NewSessionInputLedger()
	id := canonicalMsg(2)
	if l.Reserve("dev1", id) != InputLedgerOwner {
		t.Fatal("first Reserve should be Owner")
	}
	l.Commit("dev1", id)
	// Second Reserve for a committed key → Committed (re-ACK, no rewrite).
	if got := l.Reserve("dev1", id); got != InputLedgerCommitted {
		t.Fatalf("Reserve committed = %v, want Committed", got)
	}
}

func TestSessionInputLedger_PendingNotOwner(t *testing.T) {
	l := NewSessionInputLedger()
	id := canonicalMsg(3)
	if l.Reserve("dev1", id) != InputLedgerOwner {
		t.Fatal("first Reserve should be Owner")
	}
	// A second Reserve while pending (another attempt in flight) → Pending.
	if got := l.Reserve("dev1", id); got != InputLedgerPending {
		t.Fatalf("Reserve pending = %v, want Pending", got)
	}
}

func TestSessionInputLedger_IndeterminateNoRewrite(t *testing.T) {
	l := NewSessionInputLedger()
	id := canonicalMsg(4)
	l.Reserve("dev1", id)
	l.MarkIndeterminate("dev1", id)
	if got := l.Reserve("dev1", id); got != InputLedgerIndeterminate {
		t.Fatalf("Reserve indeterminate = %v, want Indeterminate", got)
	}
}

func TestSessionInputLedger_ReleaseUncalled(t *testing.T) {
	l := NewSessionInputLedger()
	id := canonicalMsg(5)
	l.Reserve("dev1", id)
	l.ReleaseUncalled("dev1", id)
	// After release, the key is gone → next Reserve is Owner again.
	if got := l.Reserve("dev1", id); got != InputLedgerOwner {
		t.Fatalf("Reserve after release = %v, want Owner", got)
	}
}

func TestSessionInputLedger_DeviceIsolation(t *testing.T) {
	// Same MessageID from different devices are independent keys.
	l := NewSessionInputLedger()
	id := canonicalMsg(6)
	if l.Reserve("devA", id) != InputLedgerOwner {
		t.Fatal("devA first Reserve should be Owner")
	}
	if l.Reserve("devB", id) != InputLedgerOwner {
		t.Fatal("devB same id should be Owner (device isolation)")
	}
}

func TestSessionInputLedger_FullNoEviction(t *testing.T) {
	l := NewSessionInputLedger()
	// Fill to capacity (8192 entries). Each canonical id is 39 bytes; 8192*39 =
	// 319488 < 1MiB, so the entry cap binds first.
	for i := 0; i < sessionInputLedgerMaxEntries; i++ {
		if got := l.Reserve("dev1", canonicalMsg(i+100)); got != InputLedgerOwner {
			t.Fatalf("Reserve %d = %v, want Owner", i, got)
		}
	}
	// One more → Full (no eviction of prior committed/pending).
	extra := canonicalMsg(sessionInputLedgerMaxEntries + 100)
	if got := l.Reserve("dev1", extra); got != InputLedgerFull {
		t.Fatalf("Reserve at capacity = %v, want Full", got)
	}
	// A prior committed key still re-ACKs (not evicted).
	l.Commit("dev1", canonicalMsg(100))
	if got := l.Reserve("dev1", canonicalMsg(100)); got != InputLedgerCommitted {
		t.Fatalf("committed key evicted? Reserve = %v, want Committed", got)
	}
}

func TestSessionInputLedger_RegistryLifecycle(t *testing.T) {
	r := NewSessionInputLedgerRegistry()
	l1 := r.Ledger("sess-1")
	l2 := r.Ledger("sess-1")
	if l1 != l2 {
		t.Fatal("Ledger should return the same instance for the same session")
	}
	l3 := r.Ledger("sess-2")
	if l1 == l3 {
		t.Fatal("different sessions should have different ledgers")
	}
	// Destroy removes the ledger; next Ledger creates a new one.
	r.Destroy("sess-1")
	l4 := r.Ledger("sess-1")
	if l1 == l4 {
		t.Fatal("Ledger after Destroy should be a new instance")
	}
}

func TestSessionInputLedger_ConcurrentReserve(t *testing.T) {
	// Race-detector: many goroutines reserving distinct keys on the same ledger.
	l := NewSessionInputLedger()
	const n = 200
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			l.Reserve(contract.DeviceID(fmt.Sprintf("dev%d", i%4)), canonicalMsg(i+1000))
		}(i)
	}
	wg.Wait()
}

// TestServer_SetSessionAdapter_WiresLedgerDestroyer (M3-005): verifies the Server
// wires the adapter's destroyLedger callback to release the per-session ledger
// from the registry. This is the production wiring that the remote REST remove
// path relies on (handleV1SessionRemove → adapter.RemoveSession → lifecycle
// commit → destroyLedger → registry.Destroy).
func TestServer_SetSessionAdapter_WiresLedgerDestroyer(t *testing.T) {
	s := NewServer(0, nil, nil, embed.FS{})
	adapter := &RemoteSessionAdapter{}
	s.SetSessionAdapter(adapter)
	if adapter.destroyLedger == nil {
		t.Fatal("SetSessionAdapter must wire destroyLedger (M3-005)")
	}
	// Populate the registry with a ledger, then destroy via the wired closure.
	l1 := s.inputLedgers.Ledger("sess-x")
	adapter.destroyLedger("sess-x")
	l2 := s.inputLedgers.Ledger("sess-x")
	if l1 == l2 {
		t.Fatal("wired destroyLedger must release the ledger (new instance after destroy)")
	}
}

// TestServer_DestroySessionInputLedger_PublicAPI (M3-005): the public method
// the App calls after a desktop remove/clear commit. Covers nil-receiver-safety,
// nil-registry-safety, idempotency, and real release.
func TestServer_DestroySessionInputLedger_PublicAPI(t *testing.T) {
	// nil-receiver: no panic (test App without a wired Remote).
	var nilSrv *Server
	nilSrv.DestroySessionInputLedger("sess")

	// nil-registry: Server built without SetSessionAdapter → no panic, no-op.
	srv := NewServer(0, nil, nil, embed.FS{})
	srv.DestroySessionInputLedger("sess")

	// Real release + idempotency.
	srv.SetSessionAdapter(&RemoteSessionAdapter{})
	l1 := srv.LedgerForSession("sess-y")
	if l1 == nil {
		t.Fatal("LedgerForSession must lazily create a ledger after SetSessionAdapter")
	}
	srv.DestroySessionInputLedger("sess-y")
	srv.DestroySessionInputLedger("sess-y") // idempotent: second destroy is a no-op
	l2 := srv.LedgerForSession("sess-y")
	if l1 == l2 {
		t.Fatal("DestroySessionInputLedger must release the ledger (new instance after destroy)")
	}
}
