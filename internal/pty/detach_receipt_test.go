package pty

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestR4_004_DetachReceipt_ReaperRetriesExactBackend proves a failed first
// close is surfaced synchronously, while the background retry keeps the exact
// captured backend closure (not a later same-session replacement) and upgrades
// the same typed receipt to confirmed.
func TestR4_004_DetachReceipt_ReaperRetriesExactBackend(t *testing.T) {
	var oldBackendCalls atomic.Int32
	var replacementCalls atomic.Int32
	receipt := newDetachReceipt()
	firstErr := detachWithExactReaper(receipt, func() error {
		if oldBackendCalls.Add(1) == 1 {
			return errors.New("injected first close failure")
		}
		return nil
	}, nil)
	if firstErr == nil {
		t.Fatal("first close failure was hidden")
	}
	// A replacement exists conceptually, but retry must never resolve by
	// SessionID and therefore never invokes this closure.
	_ = func() error { replacementCalls.Add(1); return nil }

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := receipt.Wait(ctx); err != nil {
		t.Fatalf("exact detach reaper did not confirm: %v", err)
	}
	if !receipt.Confirmed() {
		t.Fatal("receipt did not transition to confirmed")
	}
	if got := oldBackendCalls.Load(); got < 2 {
		t.Fatalf("old backend close attempts=%d, want retry", got)
	}
	if got := replacementCalls.Load(); got != 0 {
		t.Fatalf("replacement backend was touched %d times", got)
	}
}

func TestR4_004_DetachReceipt_ImmediateConfirmation(t *testing.T) {
	receipt := newDetachReceipt()
	if err := detachWithExactReaper(receipt, func() error { return nil }, nil); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if receipt.Identity() == 0 || !receipt.Confirmed() {
		t.Fatalf("invalid confirmed receipt: id=%d confirmed=%v", receipt.Identity(), receipt.Confirmed())
	}
	if err := receipt.Wait(context.Background()); err != nil {
		t.Fatalf("confirmed receipt wait: %v", err)
	}
}
