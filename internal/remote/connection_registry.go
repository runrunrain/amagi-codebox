package remote

// Connection registry (design §6.6, §11.2). Enforces GLOBAL LIVE ConnectionID
// uniqueness across all devices: any live entry with the same ConnectionID
// (same- or cross-device) rejects the candidate. On duplicate the OLD entry is
// retained (never overwritten, epoch unchanged) and the candidate is Terminated
// OUTSIDE the registry lock. Terminate is NEVER called while holding the
// registry mutex. Revoke writes a permanent device fence then detaches that
// device's connections; a concurrent Register either published before the fence
// (and is detached) or after the fence (and is rejected). Epoch guards stale
// Unregister only; it never replaces the duplicate policy.

import (
	"sync"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// connEntry is one live registration.
type connEntry struct {
	deviceID     contract.DeviceID
	connectionID ConnectionID
	conn         ManagedV1Connection
	epoch        uint64
}

// connectionRegistry tracks live v1 connections, the permanent revoked-device
// fence, and global live ConnectionID uniqueness.
type connectionRegistry struct {
	clock        Clock
	mu           sync.Mutex
	accepting    bool
	epochCounter uint64
	entries      map[ConnectionID]*connEntry
	deviceIndex  map[contract.DeviceID]map[ConnectionID]*connEntry
	revokedFence map[contract.DeviceID]bool
}

// newConnectionRegistry returns a registry in the not-accepting state.
func newConnectionRegistry(clock Clock) *connectionRegistry {
	return &connectionRegistry{
		clock:        clock,
		entries:      make(map[ConnectionID]*connEntry),
		deviceIndex:  make(map[contract.DeviceID]map[ConnectionID]*connEntry),
		revokedFence: make(map[contract.DeviceID]bool),
	}
}

// Start enables accepting new registrations.
func (r *connectionRegistry) Start() {
	r.mu.Lock()
	r.accepting = true
	r.mu.Unlock()
}

// IsAccepting reports whether the registry currently accepts registrations.
func (r *connectionRegistry) IsAccepting() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.accepting
}

// IsDeviceFenced reports whether the device has a permanent revoke fence.
func (r *connectionRegistry) IsDeviceFenced(deviceID contract.DeviceID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.revokedFence[deviceID]
}

// Register attempts to register one connection. On any rejection the candidate
// is NOT published and is Terminated OUTSIDE the lock with the mapped cause.
// On success returns RegistrationAccepted + a handle carrying the current epoch.
func (r *connectionRegistry) Register(
	principal DevicePrincipal,
	connectionID ConnectionID,
	conn ManagedV1Connection,
) (ConnectionRegistrationResult, error) {
	now := r.clock.Now().UTC()
	rejectCause := TerminationSecurityStateUnavailable

	r.mu.Lock()
	if !r.accepting {
		rejectCause = TerminationServerStopped
	} else if r.revokedFence[principal.DeviceID] {
		rejectCause = TerminationDeviceRevoked
	} else if _, live := r.entries[connectionID]; live {
		// Global live duplicate: keep old, do NOT overwrite or change its epoch.
		rejectCause = TerminationDuplicateConnectionID
	}

	switch rejectCause {
	case TerminationServerStopped:
		r.mu.Unlock()
		conn.Terminate(ConnectionTermination{Cause: rejectCause, OccurredAt: now})
		return ConnectionRegistrationResult{Outcome: RegistrationRejectedNotAccepting}, nil
	case TerminationDeviceRevoked:
		r.mu.Unlock()
		conn.Terminate(ConnectionTermination{Cause: rejectCause, OccurredAt: now})
		return ConnectionRegistrationResult{Outcome: RegistrationRejectedRevoked}, nil
	case TerminationDuplicateConnectionID:
		r.mu.Unlock()
		conn.Terminate(ConnectionTermination{Cause: rejectCause, OccurredAt: now})
		return ConnectionRegistrationResult{Outcome: RegistrationRejectedDuplicateLive},
			ConnectionRegistrationError{Outcome: RegistrationRejectedDuplicateLive}
	}

	// Accepted: publish under global ConnectionID and per-device index.
	r.epochCounter++
	if r.epochCounter == 0 { // overflow: refuse to accept further (epoch 0 reserved)
		r.epochCounter--
		r.mu.Unlock()
		conn.Terminate(ConnectionTermination{Cause: TerminationSecurityStateUnavailable, OccurredAt: now})
		return ConnectionRegistrationResult{Outcome: RegistrationRejectedNotAccepting}, nil
	}
	e := &connEntry{
		deviceID:     principal.DeviceID,
		connectionID: connectionID,
		conn:         conn,
		epoch:        r.epochCounter,
	}
	r.entries[connectionID] = e
	idx, ok := r.deviceIndex[principal.DeviceID]
	if !ok {
		idx = make(map[ConnectionID]*connEntry)
		r.deviceIndex[principal.DeviceID] = idx
	}
	idx[connectionID] = e
	reg := ConnectionRegistration{
		deviceID:     principal.DeviceID,
		connectionID: connectionID,
		epoch:        r.epochCounter,
	}
	r.mu.Unlock()
	return ConnectionRegistrationResult{Outcome: RegistrationAccepted, Registration: reg}, nil
}

// Unregister removes a registration if it is still the live entry for its
// ConnectionID and its epoch matches. Stale/duplicate unregistrations are
// idempotent no-ops; epoch never allows a duplicate to overwrite a newer entry.
func (r *connectionRegistry) Unregister(reg ConnectionRegistration) {
	r.mu.Lock()
	e, ok := r.entries[reg.connectionID]
	if !ok || e.epoch != reg.epoch {
		r.mu.Unlock()
		return
	}
	delete(r.entries, reg.connectionID)
	if idx := r.deviceIndex[reg.deviceID]; idx != nil {
		delete(idx, reg.connectionID)
		if len(idx) == 0 {
			delete(r.deviceIndex, reg.deviceID)
		}
	}
	r.mu.Unlock()
}

// FenceDevice writes a permanent revoke fence for the device and detaches all
// of its live connections. The detached connections are returned so the caller
// can Terminate them OUTSIDE the lock with TerminationDeviceRevoked.
func (r *connectionRegistry) FenceDevice(deviceID contract.DeviceID, at time.Time) []ManagedV1Connection {
	r.mu.Lock()
	r.revokedFence[deviceID] = true
	detached := r.detachDeviceLocked(deviceID)
	r.mu.Unlock()
	return detached
}

// Stop marks the registry not-accepting and detaches ALL live connections. The
// detached connections are returned so the caller can Terminate them OUTSIDE the
// lock with TerminationServerStopped.
func (r *connectionRegistry) Stop(at time.Time) []ManagedV1Connection {
	r.mu.Lock()
	r.accepting = false
	detached := make([]ManagedV1Connection, 0, len(r.entries))
	for _, e := range r.entries {
		detached = append(detached, e.conn)
	}
	r.entries = make(map[ConnectionID]*connEntry)
	r.deviceIndex = make(map[contract.DeviceID]map[ConnectionID]*connEntry)
	r.mu.Unlock()
	return detached
}

// LiveCount returns the number of live entries (diagnostic/test).
func (r *connectionRegistry) LiveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// detachDeviceLocked removes all of a device's connections and returns them.
// Caller holds r.mu.
func (r *connectionRegistry) detachDeviceLocked(deviceID contract.DeviceID) []ManagedV1Connection {
	idx := r.deviceIndex[deviceID]
	if len(idx) == 0 {
		delete(r.deviceIndex, deviceID)
		return nil
	}
	detached := make([]ManagedV1Connection, 0, len(idx))
	for cid, e := range idx {
		detached = append(detached, e.conn)
		delete(r.entries, cid)
	}
	delete(r.deviceIndex, deviceID)
	return detached
}
