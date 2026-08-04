package remote

// M-007: attached must be the first event a /ws/v1 client observes, and a control
// transition enqueued during the attach window must NOT preempt the attached
// frame on the socket. The control-transition writer goroutine is NOT started at
// Subscribe time; it is started explicitly (ControlAttachmentHandle.
// StartControlDelivery) only AFTER session.attached is on the wire.

import (
	"context"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// TestM007_SubscribeDoesNotStartWriter: in writer mode (consumer != nil),
// Subscribe must NOT auto-start the writer goroutine — events accumulate in the
// FIFO undelivered until StartWriter. This is what guarantees attached-first.
func TestM007_SubscribeDoesNotStartWriter(t *testing.T) {
	arb, _, hub, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)

	consumer := newSpyConsumer()
	sub := hub.Subscribe(sid, pA.DeviceID, leaseA, consumer)
	// Enqueue a transition during the "attach window" (writer not yet started).
	arb.Acquire(pA, leaseA, sid)
	// Give any (incorrectly started) writer a chance to deliver.
	time.Sleep(30 * time.Millisecond)
	if got := len(consumer.Events()); got != 0 {
		t.Fatalf("M-007 regression: control event delivered before StartWriter (attached-first violated): %d events", got)
	}
	// Now the attach flow writes attached (simulated) and starts the writer.
	sub.StartWriter()
	waitForEvents(t, consumer, 1)
	evs := consumer.Events()
	if len(evs) != 1 || evs[0].State != contract.ControlStateYou {
		t.Fatalf("after StartWriter expected 1 'you' event, got %+v", evs)
	}
	hub.Unsubscribe(sub)
}

// TestM007_AttachControlHandleStartControlDelivery: AttachControl returns a
// handle whose StartControlDelivery launches the control writer (after attached).
func TestM007_AttachControlHandleStartControlDelivery(t *testing.T) {
	rt := NewControlRuntime(newCtrlFakeClock(time.Now()), nil)
	rt.MarkReady()
	sid := contract.SessionID("s-m007")
	lp, rp, _, err := rt.BeginDesktopRun(context.Background(), sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), rp); err != nil {
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	_ = lp

	principal := DevicePrincipal{DeviceID: "devA", DeviceName: "Dev A"}
	consumer := newSpyConsumer()
	handle, _, _, gErr := rt.AttachControl(principal, "connA", sid, consumer)
	if gErr != nil {
		t.Fatalf("AttachControl: %v", gErr)
	}
	// A transition during the attach window must not deliver before StartControlDelivery.
	rt.Arbiter().TakeDesktop(rt.DesktopAuthority(), sid)
	time.Sleep(30 * time.Millisecond)
	if got := len(consumer.Events()); got != 0 {
		t.Fatalf("control event delivered before StartControlDelivery: %d", got)
	}
	// attached is now on the wire (simulated); start control delivery.
	handle.StartControlDelivery()
	waitForEvents(t, consumer, 1)
	// Idempotent.
	handle.StartControlDelivery()
	if got := len(consumer.Events()); got != 1 {
		t.Fatalf("StartControlDelivery not idempotent: %d events", got)
	}
}
