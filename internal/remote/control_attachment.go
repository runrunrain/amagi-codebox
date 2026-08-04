package remote

// control_attachment.go — AttachmentDirectory: authoritative connection lease
// management for (DeviceID, SessionID) pairs (design §4.1, §7.1).
//
// The directory is NOT a holder authority — it only tracks which connection is
// authoritative for a (device, session) pair and supports atomic replacement.
// The ControlArbiter separately owns the holder state. acquire input must
// present an exact live lease from this directory.
//
// Lock order (design §9.3): AttachmentDirectory.mu is acquired/released
// independently. The arbiter NEVER holds AttachmentDirectory.mu while holding
// stateMu, and vice versa. Rebind/Detach happen after releasing the directory
// lock.

import (
	"sync"
	"sync/atomic"

	"amagi-codebox/internal/remote/contract"
)

// attachmentKey identifies one (device, session) attachment slot.
type attachmentKey struct {
	deviceID  contract.DeviceID
	sessionID contract.SessionID
}

// AttachmentDirectory maps (DeviceID, SessionID) to the authoritative
// ControlConnectionLease. A new attach for the same key atomically fences the
// old lease so that two connections can never simultaneously be authoritative
// writers for the same pair.
type AttachmentDirectory struct {
	mu     sync.Mutex
	leases map[attachmentKey]*ControlConnectionLease

	// generationCounter is the monotonic attachment generation. Each new lease
	// gets a unique generation; stale leases/detaches/timers are suppressed by
	// exact-generation mismatch.
	generationCounter uint64

	ready atomic.Bool
}

// NewAttachmentDirectory creates a directory in the not-ready state.
func NewAttachmentDirectory() *AttachmentDirectory {
	return &AttachmentDirectory{
		leases: make(map[attachmentKey]*ControlConnectionLease),
	}
}

// MarkReady enables the directory for production use.
func (d *AttachmentDirectory) MarkReady() {
	d.ready.Store(true)
}

// IsReady reports whether the directory is ready.
func (d *AttachmentDirectory) IsReady() bool { return d.ready.Load() }

// Attach mints a new authoritative ControlConnectionLease for (deviceID,
// sessionID). If a previous lease exists for the same key, it is atomically
// fenced (replaced). The fenced old lease is returned so the caller can
// downgrade/close the old connection after releasing the directory lock.
//
// deviceName is the projection-only name from the authenticated record.
func (d *AttachmentDirectory) Attach(
	deviceID contract.DeviceID,
	deviceName string,
	connectionID ConnectionID,
	sessionID contract.SessionID,
) (newLease *ControlConnectionLease, fencedOld *ControlConnectionLease) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.generationCounter++
	if d.generationCounter == 0 {
		// Overflow: refuse to mint (generation 0 reserved for sentinel).
		d.generationCounter--
		return nil, nil
	}
	key := attachmentKey{deviceID: deviceID, sessionID: sessionID}
	old := d.leases[key]
	if old != nil {
		old.fence()
	}
	newLease = &ControlConnectionLease{
		deviceID:             deviceID,
		deviceName:           deviceName,
		connectionID:         connectionID,
		attachmentGeneration: d.generationCounter,
	}
	newLease.live.Store(true)
	d.leases[key] = newLease
	return newLease, old
}

// Detach fences the lease for (deviceID, sessionID) if it matches the given
// attachment generation. A stale detach (wrong generation) is a no-op and does
// not affect a replacement lease. Returns true if a live lease was actually
// detached.
func (d *AttachmentDirectory) Detach(
	deviceID contract.DeviceID,
	sessionID contract.SessionID,
	attachmentGeneration uint64,
) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := attachmentKey{deviceID: deviceID, sessionID: sessionID}
	lease, ok := d.leases[key]
	if !ok || lease.attachmentGeneration != attachmentGeneration {
		return false // stale detach
	}
	lease.fence()
	delete(d.leases, key)
	return true
}

// CurrentLease returns the current lease for (deviceID, sessionID), or nil if
// none exists. The returned pointer's live bit reflects the current state.
func (d *AttachmentDirectory) CurrentLease(
	deviceID contract.DeviceID,
	sessionID contract.SessionID,
) *ControlConnectionLease {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.leases[attachmentKey{deviceID: deviceID, sessionID: sessionID}]
}

// DetachAllForDevice fences and removes all leases for the given device across
// all sessions. Used during revoke to fence the device's authoritative
// attachments. Returns the detached leases (snapshot taken under lock).
func (d *AttachmentDirectory) DetachAllForDevice(deviceID contract.DeviceID) []*ControlConnectionLease {
	d.mu.Lock()
	defer d.mu.Unlock()

	var detached []*ControlConnectionLease
	for key, lease := range d.leases {
		if key.deviceID == deviceID {
			lease.fence()
			detached = append(detached, lease)
			delete(d.leases, key)
		}
	}
	return detached
}

// DetachAll fences and removes all leases (Server Stop / shutdown).
func (d *AttachmentDirectory) DetachAll() []*ControlConnectionLease {
	d.mu.Lock()
	defer d.mu.Unlock()

	detached := make([]*ControlConnectionLease, 0, len(d.leases))
	for key, lease := range d.leases {
		lease.fence()
		detached = append(detached, lease)
		delete(d.leases, key)
	}
	return detached
}

// Clear marks the directory not-ready and drops all leases (shutdown).
func (d *AttachmentDirectory) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ready.Store(false)
	d.leases = make(map[attachmentKey]*ControlConnectionLease)
}
