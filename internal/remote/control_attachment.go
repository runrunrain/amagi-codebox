package remote

import (
	"sync"
	"sync/atomic"

	"amagi-codebox/internal/remote/contract"
)

type attachmentKey struct {
	deviceID  contract.DeviceID
	sessionID contract.SessionID
}

type attachmentBootstrap struct {
	payload   atomic.Pointer[[]byte]
	written   atomic.Bool
	ready     chan struct{}
	readyOnce sync.Once
}

func (b *attachmentBootstrap) Store(payload []byte) {
	if b == nil {
		return
	}
	if len(payload) == 0 {
		b.ResolveAbsent()
		return
	}
	owned := append([]byte(nil), payload...)
	b.payload.Store(&owned)
	b.readyOnce.Do(func() { close(b.ready) })
}

func (b *attachmentBootstrap) Load() []byte {
	if b == nil {
		return nil
	}
	payload := b.payload.Load()
	if payload == nil {
		return nil
	}
	return *payload
}

func (b *attachmentBootstrap) MarkWritten() {
	if b != nil {
		b.written.Store(true)
	}
}

func (b *attachmentBootstrap) ResolveAbsent() {
	if b != nil {
		b.readyOnce.Do(func() { close(b.ready) })
	}
}

func (b *attachmentBootstrap) PendingPayload() []byte {
	if b == nil {
		return nil
	}
	if b.ready != nil {
		<-b.ready
	}
	if b.written.Load() {
		return nil
	}
	return b.Load()
}

type attachmentNode struct {
	lease      *ControlConnectionLease
	controlSub *hubSubscriber
	causalSub  *causalHubSubscription
	terminal   RemovalTerminalPort
	bootstrap  *attachmentBootstrap
}

type attachmentSlot struct {
	mu      sync.Mutex
	current *attachmentNode
	pending *AttachReservation
	seq     uint64
}

type AttachReservation struct {
	slot       *attachmentSlot
	key        attachmentKey
	generation uint64
	node       *attachmentNode
	old        *attachmentNode
	committed  bool
	aborted    bool
}

func (r *AttachReservation) Lease() *ControlConnectionLease {
	if r == nil || r.node == nil {
		return nil
	}
	return r.node.lease
}

func (r *AttachReservation) Bootstrap() *attachmentBootstrap {
	if r == nil || r.node == nil {
		return nil
	}
	return r.node.bootstrap
}

type AttachmentDirectory struct {
	mu    sync.Mutex
	slots map[attachmentKey]*attachmentSlot
	ready atomic.Bool
}

func NewAttachmentDirectory() *AttachmentDirectory {
	return &AttachmentDirectory{slots: make(map[attachmentKey]*attachmentSlot)}
}

func (d *AttachmentDirectory) MarkReady()    { d.ready.Store(true) }
func (d *AttachmentDirectory) IsReady() bool { return d.ready.Load() }

// ReserveAttach allocates a complete inactive node without replacing the live
// lease. Final commit only swaps stable pointers and live bits in the slot.
func (d *AttachmentDirectory) ReserveAttach(deviceID contract.DeviceID, deviceName string, connectionID ConnectionID, sessionID contract.SessionID, terminal RemovalTerminalPort) *AttachReservation {
	if d == nil || deviceID == "" || connectionID == "" || sessionID == "" {
		return nil
	}
	key := attachmentKey{deviceID: deviceID, sessionID: sessionID}
	d.mu.Lock()
	slot := d.slots[key]
	if slot == nil {
		slot = &attachmentSlot{}
		d.slots[key] = slot
	}
	d.mu.Unlock()

	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.pending != nil || slot.seq == ^uint64(0) {
		return nil
	}
	slot.seq++
	lease := &ControlConnectionLease{
		deviceID: deviceID, deviceName: deviceName, connectionID: connectionID,
		attachmentGeneration: slot.seq,
	}
	reservation := &AttachReservation{
		slot: slot, key: key, generation: slot.seq,
		node: &attachmentNode{lease: lease, terminal: terminal, bootstrap: &attachmentBootstrap{ready: make(chan struct{})}},
		old:  slot.current,
	}
	slot.pending = reservation
	return reservation
}

func (d *AttachmentDirectory) commitReservedAttachNoFail(reservation *AttachReservation) (*ControlConnectionLease, bool) {
	if reservation == nil || reservation.slot == nil || reservation.node == nil || reservation.node.lease == nil {
		return nil, false
	}
	slot := reservation.slot
	if !d.ready.Load() {
		return nil, false
	}
	slot.mu.Lock()
	if reservation.aborted || reservation.committed || reservation.node.lease.fenced.Load() || slot.pending != reservation || slot.current != reservation.old || reservation.generation != reservation.node.lease.attachmentGeneration {
		slot.mu.Unlock()
		return nil, false
	}
	var oldLease *ControlConnectionLease
	if reservation.old != nil {
		oldLease = reservation.old.lease
		if oldLease != nil {
			oldLease.fence()
		}
	}
	slot.current = reservation.node
	slot.pending = nil
	reservation.node.lease.live.Store(true)
	reservation.committed = true
	slot.mu.Unlock()
	return oldLease, true
}

func (d *AttachmentDirectory) AbortAttachReservation(reservation *AttachReservation) {
	if reservation == nil || reservation.slot == nil {
		return
	}
	reservation.slot.mu.Lock()
	if !reservation.committed && reservation.slot.pending == reservation {
		reservation.slot.pending = nil
		reservation.aborted = true
		if reservation.node != nil {
			reservation.node.bootstrap.ResolveAbsent()
			if reservation.node.lease != nil {
				reservation.node.lease.fence()
			}
		}
	}
	reservation.slot.mu.Unlock()
}

// Attach is the compatibility wrapper used by existing non-WS callers.
func (d *AttachmentDirectory) Attach(deviceID contract.DeviceID, deviceName string, connectionID ConnectionID, sessionID contract.SessionID) (newLease *ControlConnectionLease, fencedOld *ControlConnectionLease) {
	reservation := d.ReserveAttach(deviceID, deviceName, connectionID, sessionID, nil)
	if reservation == nil {
		return nil, nil
	}
	old, ok := d.commitReservedAttachNoFail(reservation)
	if !ok {
		d.AbortAttachReservation(reservation)
		return nil, old
	}
	return reservation.node.lease, old
}

func (d *AttachmentDirectory) Detach(deviceID contract.DeviceID, sessionID contract.SessionID, attachmentGeneration uint64) bool {
	key := attachmentKey{deviceID: deviceID, sessionID: sessionID}
	d.mu.Lock()
	slot := d.slots[key]
	d.mu.Unlock()
	if slot == nil {
		return false
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.current == nil || slot.current.lease == nil || slot.current.lease.attachmentGeneration != attachmentGeneration {
		return false
	}
	slot.current.lease.fence()
	slot.current = nil
	return true
}

func (d *AttachmentDirectory) CurrentLease(deviceID contract.DeviceID, sessionID contract.SessionID) *ControlConnectionLease {
	d.mu.Lock()
	slot := d.slots[attachmentKey{deviceID: deviceID, sessionID: sessionID}]
	d.mu.Unlock()
	if slot == nil {
		return nil
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.current == nil {
		return nil
	}
	return slot.current.lease
}

func (d *AttachmentDirectory) snapshotSlots() []*attachmentSlot {
	d.mu.Lock()
	defer d.mu.Unlock()
	slots := make([]*attachmentSlot, 0, len(d.slots))
	for _, slot := range d.slots {
		slots = append(slots, slot)
	}
	return slots
}

func (d *AttachmentDirectory) DetachAllForDevice(deviceID contract.DeviceID) []*ControlConnectionLease {
	var detached []*ControlConnectionLease
	for _, slot := range d.snapshotSlots() {
		slot.mu.Lock()
		if slot.pending != nil && slot.pending.node != nil && slot.pending.node.lease != nil && slot.pending.node.lease.deviceID == deviceID {
			slot.pending.aborted = true
			slot.pending.node.bootstrap.ResolveAbsent()
			slot.pending.node.lease.fence()
			slot.pending = nil
		}
		if slot.current != nil && slot.current.lease != nil && slot.current.lease.deviceID == deviceID {
			slot.current.lease.fence()
			detached = append(detached, slot.current.lease)
			slot.current = nil
		}
		slot.mu.Unlock()
	}
	return detached
}

func (d *AttachmentDirectory) DetachAll() []*ControlConnectionLease {
	var detached []*ControlConnectionLease
	for _, slot := range d.snapshotSlots() {
		slot.mu.Lock()
		if slot.pending != nil {
			slot.pending.aborted = true
			if slot.pending.node != nil {
				slot.pending.node.bootstrap.ResolveAbsent()
				if slot.pending.node.lease != nil {
					slot.pending.node.lease.fence()
				}
			}
			slot.pending = nil
		}
		if slot.current != nil && slot.current.lease != nil {
			slot.current.lease.fence()
			detached = append(detached, slot.current.lease)
			slot.current = nil
		}
		slot.mu.Unlock()
	}
	return detached
}

func (d *AttachmentDirectory) Clear() {
	d.DetachAll()
	d.mu.Lock()
	d.ready.Store(false)
	d.slots = make(map[attachmentKey]*attachmentSlot)
	d.mu.Unlock()
}
