package remote

// shared_coordinator.go — SharedServiceCoordinator: launch, mutation, and
// active-run lease authority for Proxy/Headroom shared singletons (design §6.7).
//
// Policy (§3.4 S1 lease-strict, §6.7.2): manual Start/Stop/reconfigure is
// STABLY REJECTED (raw call = 0) while any pending/active run lease exists for
// the singleton. Exact mutation admissions own the check→raw-I/O interval so
// launches and uninstall drains cannot cross an operation already in flight. The desktop root does NOT auto-interpret as mass takeover.
// Users must stop dependent sessions via normal desktop lifecycle first; only
// unexpected dependency death fans out unavailable (holder unchanged).
//
// The coordinator does NO raw I/O under its lock (design §9.3). It snapshots
// lease state under stateMu, releases the lock, then the caller performs any
// bounded raw mutation (or the rejection is returned without raw call).

import (
	"context"
	"errors"
	"sync"

	"amagi-codebox/internal/remote/contract"
)

// ErrSharedServiceInUse is the stable rejection returned when a manual mutation
// is attempted while one or more active/pending run leases exist for the
// singleton. The fixed message carries no lease/admission count, SessionID, or holder
// identity (design §6.7.2 privacy).
var ErrSharedServiceInUse = errors.New("shared service is in use by active sessions")

// ErrSharedCoordinatorClosed is returned after the app shutdown fence closes
// the coordinator. Shutdown is terminal: no later launch, mutation, or run
// identity may repopulate leases after ClearAll has released them.
var ErrSharedCoordinatorClosed = errors.New("shared service coordinator is closed")

// isHeadroomKind reports whether the kind is a headroom dependency (not proxy).
func isHeadroomKind(kind SharedServiceKind) bool {
	return kind == SharedServiceClaudeHeadroom || kind == SharedServiceCodexHeadroom
}

// SharedServiceMutationKind enumerates the manual facade mutations that are
// lease-guarded (design §6.7.2 method-family table).
type SharedServiceMutationKind uint8

const (
	// MutationStartDifferentConfig: Start with a fingerprint that differs from
	// the current healthy generation. Lease-non-empty → reject.
	MutationStartDifferentConfig SharedServiceMutationKind = iota + 1
	// MutationStop: Stop the shared process. Lease-non-empty → reject.
	MutationStop
	// MutationReconfigure: live backend/rules/port/venv mutation. Lease-non-empty → reject.
	MutationReconfigure
	// MutationExactNoOp: Start with a fingerprint identical to the current
	// healthy generation. Always allowed (idempotent), returns Changed=false.
	MutationExactNoOp
)

// SharedServiceEntry tracks one singleton's lease state (design §6.7.1).
type SharedServiceEntry struct {
	serviceGeneration uint64
	configFingerprint [32]byte
	leases            map[*SharedDependencyLease]struct{}
}

type sharedLaunchTransaction struct {
	kind     SharedServiceKind
	started  bool
	released bool
	pending  map[*SharedDependencyLease]struct{}
}

// SharedLeaseTransfer reserves an exact pending→promoted ownership switch.
// ReleaseExact waits for this ticket so natural exit cannot race the composite.
type SharedLeaseTransfer struct {
	coordinator *SharedServiceCoordinator
	oldLeases   []*SharedDependencyLease
	newLeases   []*SharedDependencyLease
	resolved    chan struct{}
	committed   bool
	consumed    bool
}

// SharedServiceCoordinator manages active-run leases for the three shared
// singletons: ClaudeProxy, ClaudeHeadroom, CodexHeadroom.
type SharedServiceCoordinator struct {
	mu       sync.Mutex
	entries  map[SharedServiceKind]*SharedServiceEntry
	genCount map[SharedServiceKind]uint64

	// launchAdmissions are generation-bound pending launch claims acquired before
	// any Headroom/PTY side effect. An uninstall drain linearized after a claim
	// observes it as non-empty and aborts; a drain linearized first rejects it.
	launchAdmissions   map[*SharedLaunchAdmission]struct{}
	launchTransactions map[*SharedLaunchAdmission]*sharedLaunchTransaction
	mutationAdmissions map[*SharedMutationAdmission]struct{}
	leaseTransfers     map[*SharedDependencyLease]*SharedLeaseTransfer
	admissionSeq       uint64
	externalRunSeq     uint64

	// headroomDraining (R4-002), when true, blocks new launch/run/mutation
	// admissions for BOTH headroom kinds for the duration of an uninstall. It is the
	// install-drain critical section that closes the TOCTOU window between the
	// empty check and the venv deletion (a launch that sneaks in after the check
	// would otherwise have its dependency deleted). Non-headroom kinds (proxy)
	// are unaffected.
	headroomDraining bool
	closed           bool
}

// NewSharedServiceCoordinator creates a coordinator with empty lease sets.
func NewSharedServiceCoordinator() *SharedServiceCoordinator {
	return &SharedServiceCoordinator{
		entries:            make(map[SharedServiceKind]*SharedServiceEntry),
		genCount:           make(map[SharedServiceKind]uint64),
		launchAdmissions:   make(map[*SharedLaunchAdmission]struct{}),
		launchTransactions: make(map[*SharedLaunchAdmission]*sharedLaunchTransaction),
		mutationAdmissions: make(map[*SharedMutationAdmission]struct{}),
		leaseTransfers:     make(map[*SharedDependencyLease]*SharedLeaseTransfer),
	}
}

// SharedLaunchAdmission is an opaque, generation-bound pending claim for one
// shared singleton. It is acquired before any dependency/process side effect
// and atomically promoted to a run lease. Callers must release it on every path;
// release is idempotent, including after successful promotion.
type SharedLaunchAdmission struct {
	kind              SharedServiceKind
	generation        uint64
	configFingerprint [32]byte
	configBound       bool
}

// SharedMutationAdmission is an exact pending singleton mutation. It keeps the
// coordinator non-empty for the complete raw Start/Stop/reconfigure call, closing
// the one-shot CheckMutation→raw-I/O TOCTOU against uninstall and launches.
type SharedMutationAdmission struct {
	kind       SharedServiceKind
	generation uint64
}

// ExternalRunIdentity is an opaque identity for a Launcher-owned external
// process. It deliberately carries no Control RunPermit and therefore cannot
// authorize PTY input, resize, lifecycle, or activation. The coordinator mints
// it for one non-empty session ID and accepts it only on the minting instance.
type ExternalRunIdentity struct {
	owner      *SharedServiceCoordinator
	sessionID  contract.SessionID
	generation uint64
}

// Kind identifies the singleton protected by this opaque admission.
func (a *SharedLaunchAdmission) Kind() SharedServiceKind {
	if a == nil {
		return 0
	}
	return a.kind
}

// AcquireLaunchAdmission enters the uninstall admission barrier before launch
// side effects. Only Headroom dependencies need the install-drain barrier.
func (c *SharedServiceCoordinator) AcquireLaunchAdmission(kind SharedServiceKind) (*SharedLaunchAdmission, error) {
	return c.acquireLaunchAdmission(kind, [32]byte{}, false)
}

// AcquireLaunchAdmissionForConfig binds the pre-effect admission to the same
// exact fingerprint later used by the effect and run lease. Concurrent launch
// transactions for one singleton may coexist only when this tuple matches.
func (c *SharedServiceCoordinator) AcquireLaunchAdmissionForConfig(kind SharedServiceKind, fingerprint [32]byte) (*SharedLaunchAdmission, error) {
	return c.acquireLaunchAdmission(kind, fingerprint, true)
}

func (c *SharedServiceCoordinator) acquireLaunchAdmission(kind SharedServiceKind, fingerprint [32]byte, configBound bool) (*SharedLaunchAdmission, error) {
	if kind != SharedServiceClaudeHeadroom && kind != SharedServiceCodexHeadroom && kind != SharedServiceClaudeProxy {
		return nil, errors.New("control: launch admission is only valid for headroom or proxy dependencies")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrSharedCoordinatorClosed
	}
	if isHeadroomKind(kind) && c.headroomDraining {
		return nil, ErrSharedServiceInUse
	}
	for mutation := range c.mutationAdmissions {
		if mutation.kind == kind {
			return nil, ErrSharedServiceInUse
		}
	}
	if configBound {
		if entry := c.entries[kind]; entry != nil && len(entry.leases) > 0 && entry.configFingerprint != fingerprint {
			return nil, ErrSharedServiceInUse
		}
		for existing, transaction := range c.launchTransactions {
			if transaction == nil || transaction.released || transaction.kind != kind {
				continue
			}
			if !existing.configBound || existing.configFingerprint != fingerprint {
				return nil, ErrSharedServiceInUse
			}
		}
	} else {
		for existing, transaction := range c.launchTransactions {
			if transaction != nil && !transaction.released && transaction.kind == kind && existing.configBound {
				return nil, ErrSharedServiceInUse
			}
		}
	}
	if c.admissionSeq == ^uint64(0) {
		return nil, errors.New("control: launch admission generation exhausted")
	}
	c.admissionSeq++
	admission := &SharedLaunchAdmission{
		kind: kind, generation: c.admissionSeq,
		configFingerprint: fingerprint, configBound: configBound,
	}
	c.launchAdmissions[admission] = struct{}{}
	c.launchTransactions[admission] = &sharedLaunchTransaction{kind: kind, pending: make(map[*SharedDependencyLease]struct{})}
	return admission, nil
}

// ReleaseLaunchAdmission releases an unpromoted pending claim. It is exact-
// pointer and idempotent so deferred cleanup is safe after promotion.
func (c *SharedServiceCoordinator) ReleaseLaunchAdmission(admission *SharedLaunchAdmission) {
	if admission == nil {
		return
	}
	c.mu.Lock()
	delete(c.launchAdmissions, admission)
	if txn := c.launchTransactions[admission]; txn != nil {
		txn.released = true
		if !txn.started && len(txn.pending) == 0 {
			delete(c.launchTransactions, admission)
		}
	}
	c.mu.Unlock()
}

// MarkLaunchTransactionStarted records that the exact admitted transaction
// started the singleton. It must be called immediately after raw Start succeeds.
func (c *SharedServiceCoordinator) MarkLaunchTransactionStarted(admission *SharedLaunchAdmission) error {
	if admission == nil {
		return errors.New("control: shared start has no launch admission")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrSharedCoordinatorClosed
	}
	txn := c.launchTransactions[admission]
	if txn == nil || txn.kind != admission.kind || admission.generation == 0 || txn.started {
		return errors.New("control: stale shared start transaction")
	}
	txn.started = true
	return nil
}

// AuthorizeCompensatingStop proves that this exact transaction started the
// service and that no promoted or competing pending owner depends on it.
func (c *SharedServiceCoordinator) AuthorizeCompensatingStop(admission *SharedLaunchAdmission) bool {
	if admission == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	txn := c.launchTransactions[admission]
	if txn == nil || !txn.started || txn.kind != admission.kind {
		return false
	}
	entry := c.entries[txn.kind]
	if entry != nil {
		for lease := range entry.leases {
			if lease.promoted || lease.admission != admission {
				return false
			}
		}
	}
	for other, otherTxn := range c.launchTransactions {
		if other != admission && otherTxn.kind == txn.kind && (!otherTxn.released || len(otherTxn.pending) > 0) {
			return false
		}
	}
	txn.started = false
	if txn.released && len(txn.pending) == 0 {
		delete(c.launchTransactions, admission)
	}
	return true
}

// AcquireMutationAdmission atomically checks all pending/active users and owns
// the singleton mutation until ReleaseMutationAdmission. Raw I/O occurs outside
// the coordinator lock, but uninstall/launch admission observes this token.
func (c *SharedServiceCoordinator) AcquireMutationAdmission(kind SharedServiceKind, mutation SharedServiceMutationKind) (*SharedMutationAdmission, error) {
	if mutation == MutationExactNoOp {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrSharedCoordinatorClosed
	}
	if c.headroomDraining && (kind == SharedServiceClaudeHeadroom || kind == SharedServiceCodexHeadroom) {
		return nil, ErrSharedServiceInUse
	}
	for launch := range c.launchAdmissions {
		if launch.kind == kind {
			return nil, ErrSharedServiceInUse
		}
	}
	for active := range c.mutationAdmissions {
		if active.kind == kind {
			return nil, ErrSharedServiceInUse
		}
	}
	if entry := c.entries[kind]; entry != nil && len(entry.leases) > 0 {
		return nil, ErrSharedServiceInUse
	}
	if c.admissionSeq == ^uint64(0) {
		return nil, errors.New("control: shared admission generation exhausted")
	}
	c.admissionSeq++
	admission := &SharedMutationAdmission{kind: kind, generation: c.admissionSeq}
	c.mutationAdmissions[admission] = struct{}{}
	return admission, nil
}

// ReleaseMutationAdmission releases an exact mutation token. It is idempotent.
func (c *SharedServiceCoordinator) ReleaseMutationAdmission(admission *SharedMutationAdmission) {
	if admission == nil {
		return
	}
	c.mu.Lock()
	delete(c.mutationAdmissions, admission)
	c.mu.Unlock()
}

// MintExternalRunIdentity creates a coordinator-bound, non-writable identity
// for one Launcher-owned process lifetime. Minting has no shared-service side
// effect; the caller must promote an existing launch admission before returning
// a successful dependency-bearing launch.
func (c *SharedServiceCoordinator) MintExternalRunIdentity(sessionID contract.SessionID) (*ExternalRunIdentity, error) {
	if sessionID == "" {
		return nil, errors.New("control: empty external run session ID")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrSharedCoordinatorClosed
	}
	if c.externalRunSeq == ^uint64(0) {
		return nil, errors.New("control: external run generation exhausted")
	}
	c.externalRunSeq++
	return &ExternalRunIdentity{owner: c, sessionID: sessionID, generation: c.externalRunSeq}, nil
}

// AcquireForExternalRunWithAdmission atomically promotes the exact startup
// admission to a lifetime lease for a Launcher-owned process. The opaque
// identity is not a Control permit and cannot authorize writes.
func (c *SharedServiceCoordinator) AcquireForExternalRunWithAdmission(
	ctx context.Context,
	identity *ExternalRunIdentity,
	kind SharedServiceKind,
	configFingerprint [32]byte,
	admission *SharedLaunchAdmission,
) (*SharedDependencyLease, error) {
	return c.acquireForExternalRun(ctx, identity, kind, configFingerprint, admission)
}

func (c *SharedServiceCoordinator) acquireForExternalRun(
	_ context.Context,
	identity *ExternalRunIdentity,
	kind SharedServiceKind,
	configFingerprint [32]byte,
	admission *SharedLaunchAdmission,
) (*SharedDependencyLease, error) {
	if identity == nil || identity.owner != c || identity.sessionID == "" || identity.generation == 0 {
		return nil, errors.New("control: invalid external run identity")
	}
	if admission == nil {
		return nil, errors.New("control: external headroom run requires launch admission")
	}
	lease := &SharedDependencyLease{
		sessionID: identity.sessionID, runEpoch: identity.generation,
		externalRun: identity, kind: kind,
	}
	return c.acquireLease(kind, configFingerprint, admission, lease, true)
}

// AcquireForRun adds a run lease for the given service kind. A Headroom run
// without a prior admission is rejected while uninstall drains. Launch paths
// that admitted before the drain use AcquireForRunWithAdmission to atomically
// promote that exact pending claim.
func (c *SharedServiceCoordinator) AcquireForRun(
	ctx context.Context,
	runPermit *RunPermit,
	kind SharedServiceKind,
	configFingerprint [32]byte,
) (*SharedDependencyLease, error) {
	return c.acquireForRun(ctx, runPermit, kind, configFingerprint, nil, true)
}

// AcquireForRunWithAdmission atomically promotes admission to the exact run
// lease. A drain that started after this admission must abort because it saw the
// pending claim; promotion is therefore allowed even while that losing drain is
// briefly set. Stale/released/wrong-kind admissions fail closed.
func (c *SharedServiceCoordinator) AcquireForRunWithAdmission(
	ctx context.Context,
	runPermit *RunPermit,
	kind SharedServiceKind,
	configFingerprint [32]byte,
	admission *SharedLaunchAdmission,
) (*SharedDependencyLease, error) {
	return c.acquireForRun(ctx, runPermit, kind, configFingerprint, admission, true)
}

// AcquirePendingForRunWithAdmission converts an admission into an exact hidden
// run lease without promoting it. The composite commit must transfer/promote it.
func (c *SharedServiceCoordinator) AcquirePendingForRunWithAdmission(
	ctx context.Context,
	runPermit *RunPermit,
	kind SharedServiceKind,
	configFingerprint [32]byte,
	admission *SharedLaunchAdmission,
) (*SharedDependencyLease, error) {
	return c.acquireForRun(ctx, runPermit, kind, configFingerprint, admission, false)
}

// AcquirePendingForObservationWithAdmission is the restart equivalent: the
// hidden observation permit identifies the reserved new run before publication.
func (c *SharedServiceCoordinator) AcquirePendingForObservationWithAdmission(
	_ context.Context,
	permit *RunObservationPermit,
	kind SharedServiceKind,
	configFingerprint [32]byte,
	admission *SharedLaunchAdmission,
) (*SharedDependencyLease, error) {
	if permit == nil || permit.entry == nil || permit.run == nil || permit.runEpoch == 0 {
		return nil, errors.New("control: invalid restart observation permit")
	}
	lease := &SharedDependencyLease{
		sessionID: permit.entry.sessionID, run: permit.run, runEpoch: permit.runEpoch, kind: kind,
	}
	return c.acquireLease(kind, configFingerprint, admission, lease, false)
}

func (c *SharedServiceCoordinator) acquireForRun(
	_ context.Context,
	runPermit *RunPermit,
	kind SharedServiceKind,
	configFingerprint [32]byte,
	admission *SharedLaunchAdmission,
	promoted bool,
) (*SharedDependencyLease, error) {
	if runPermit == nil || runPermit.run == nil {
		return nil, errors.New("control: nil run permit")
	}
	lease := &SharedDependencyLease{
		sessionID: entryIDFromRunPermit(runPermit), run: runPermit.run,
		runEpoch: runPermit.runEpoch, kind: kind,
	}
	return c.acquireLease(kind, configFingerprint, admission, lease, promoted)
}

func (c *SharedServiceCoordinator) acquireLease(
	kind SharedServiceKind,
	configFingerprint [32]byte,
	admission *SharedLaunchAdmission,
	lease *SharedDependencyLease,
	promoted bool,
) (*SharedDependencyLease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrSharedCoordinatorClosed
	}

	admitted := false
	var transaction *sharedLaunchTransaction
	if admission != nil {
		_, admitted = c.launchAdmissions[admission]
		transaction = c.launchTransactions[admission]
		if !admitted || transaction == nil || transaction.released || admission.generation == 0 || admission.kind != kind || transaction.kind != kind ||
			(admission.configBound && admission.configFingerprint != configFingerprint) {
			return nil, errors.New("control: stale or mismatched launch admission")
		}
	}
	// A newly arriving Headroom run cannot cross an existing drain. An exact
	// pending admission linearized first and is allowed to promote; the drain's
	// empty check necessarily returned false and its caller aborts uninstall.
	if !admitted && c.headroomDraining && (kind == SharedServiceClaudeHeadroom || kind == SharedServiceCodexHeadroom) {
		return nil, ErrSharedServiceInUse
	}
	for mutation := range c.mutationAdmissions {
		if mutation.kind == kind {
			return nil, ErrSharedServiceInUse
		}
	}

	entry := c.entries[kind]
	if entry == nil {
		// First lease for this kind: establish the generation.
		if c.genCount[kind] == ^uint64(0) {
			return nil, errors.New("control: shared service generation exhausted")
		}
		c.genCount[kind]++
		entry = &SharedServiceEntry{
			serviceGeneration: c.genCount[kind],
			configFingerprint: configFingerprint,
			leases:            make(map[*SharedDependencyLease]struct{}),
		}
		c.entries[kind] = entry
	} else if entry.configFingerprint != configFingerprint {
		if len(entry.leases) > 0 {
			// Incompatible config while leases exist: reject (do NOT stop the old
			// instance — design §6.7.1 forbids the old LaunchSession behavior).
			return nil, ErrSharedServiceInUse
		}
		if c.genCount[kind] == ^uint64(0) {
			return nil, errors.New("control: shared service generation exhausted")
		}
		c.genCount[kind]++
		entry = &SharedServiceEntry{
			serviceGeneration: c.genCount[kind], configFingerprint: configFingerprint,
			leases: make(map[*SharedDependencyLease]struct{}),
		}
		c.entries[kind] = entry
	}

	lease.serviceGeneration = entry.serviceGeneration
	lease.promoted = promoted
	lease.admission = admission
	entry.leases[lease] = struct{}{}
	if admitted {
		delete(c.launchAdmissions, admission)
		if promoted {
			delete(c.launchTransactions, admission)
		} else {
			transaction.pending[lease] = struct{}{}
		}
	}
	return lease, nil
}

// ReleaseExact removes the exact run lease. Called on run terminal/remove/
// shutdown. If the last lease is released, the singleton is left idle (not
// stopped) to preserve current singleton behavior (design §6.7.1).
func (c *SharedServiceCoordinator) ReleaseExact(ctx context.Context, lease *SharedDependencyLease) error {
	if lease == nil {
		return nil
	}
	for {
		c.mu.Lock()
		if transfer := c.leaseTransfers[lease]; transfer != nil {
			resolved := transfer.resolved
			c.mu.Unlock()
			if ctx == nil {
				<-resolved
				continue
			}
			select {
			case <-resolved:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		entry := c.entries[lease.kind]
		if entry != nil {
			delete(entry.leases, lease)
		}
		if txn := c.launchTransactions[lease.admission]; txn != nil {
			delete(txn.pending, lease)
			if txn.released && !txn.started && len(txn.pending) == 0 {
				delete(c.launchTransactions, lease.admission)
			}
		}
		c.mu.Unlock()
		return nil
	}
}

// PrepareLeaseTransfer reserves an exact composite ownership switch. Old
// leases must be promoted; new leases must be pending and belong to newEpoch.
func (c *SharedServiceCoordinator) PrepareLeaseTransfer(
	sessionID contract.SessionID,
	oldEpoch, newEpoch uint64,
	oldLeases, newLeases []*SharedDependencyLease,
) (*SharedLeaseTransfer, error) {
	if sessionID == "" || newEpoch == 0 {
		return nil, errors.New("control: invalid shared lease transfer owner")
	}
	token := &SharedLeaseTransfer{
		coordinator: c, oldLeases: append([]*SharedDependencyLease(nil), oldLeases...),
		newLeases: append([]*SharedDependencyLease(nil), newLeases...), resolved: make(chan struct{}),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrSharedCoordinatorClosed
	}
	seen := make(map[*SharedDependencyLease]struct{}, len(oldLeases)+len(newLeases))
	for _, lease := range oldLeases {
		if lease == nil || lease.sessionID != sessionID || lease.runEpoch != oldEpoch || !lease.promoted || c.leaseTransfers[lease] != nil {
			return nil, errors.New("control: stale old shared lease")
		}
		entry := c.entries[lease.kind]
		if entry == nil {
			return nil, errors.New("control: old shared lease generation unavailable")
		}
		if _, ok := entry.leases[lease]; !ok {
			return nil, errors.New("control: old shared lease is not owned")
		}
		seen[lease] = struct{}{}
	}
	for _, lease := range newLeases {
		if lease == nil || lease.sessionID != sessionID || lease.runEpoch != newEpoch || lease.promoted || c.leaseTransfers[lease] != nil {
			return nil, errors.New("control: stale pending shared lease")
		}
		if _, duplicate := seen[lease]; duplicate {
			return nil, errors.New("control: duplicate shared lease transfer member")
		}
		entry := c.entries[lease.kind]
		if entry == nil {
			return nil, errors.New("control: pending shared lease generation unavailable")
		}
		if _, ok := entry.leases[lease]; !ok {
			return nil, errors.New("control: pending shared lease is not owned")
		}
		seen[lease] = struct{}{}
	}
	for lease := range seen {
		c.leaseTransfers[lease] = token
	}
	return token, nil
}

// CommitLeaseTransferNoFail promotes the new generation, releases the old
// generation, and updates the outer owner registry while coordinator.mu is held.
func (c *SharedServiceCoordinator) CommitLeaseTransferNoFail(token *SharedLeaseTransfer, updateOwner func()) {
	if token == nil || token.coordinator != c || token.committed || token.consumed {
		panic("control: invalid shared lease transfer commit")
	}
	c.mu.Lock()
	for _, lease := range token.oldLeases {
		if c.leaseTransfers[lease] != token || !lease.promoted {
			c.mu.Unlock()
			panic("control: old shared lease transfer ownership changed")
		}
	}
	for _, lease := range token.newLeases {
		if c.leaseTransfers[lease] != token || lease.promoted {
			c.mu.Unlock()
			panic("control: pending shared lease transfer ownership changed")
		}
	}
	for _, lease := range token.oldLeases {
		if entry := c.entries[lease.kind]; entry != nil {
			delete(entry.leases, lease)
		}
	}
	for _, lease := range token.newLeases {
		lease.promoted = true
		if txn := c.launchTransactions[lease.admission]; txn != nil {
			delete(txn.pending, lease)
			delete(c.launchTransactions, lease.admission)
		}
	}
	if updateOwner != nil {
		updateOwner()
	}
	token.committed = true
	c.mu.Unlock()
}

func (c *SharedServiceCoordinator) FinishLeaseTransfer(token *SharedLeaseTransfer) {
	if token == nil || token.coordinator != c || !token.committed || token.consumed {
		return
	}
	c.mu.Lock()
	for _, lease := range token.oldLeases {
		delete(c.leaseTransfers, lease)
	}
	for _, lease := range token.newLeases {
		delete(c.leaseTransfers, lease)
	}
	token.consumed = true
	c.mu.Unlock()
	close(token.resolved)
}

func (c *SharedServiceCoordinator) AbortLeaseTransfer(token *SharedLeaseTransfer) {
	if token == nil || token.coordinator != c || token.committed || token.consumed {
		return
	}
	c.mu.Lock()
	for _, lease := range token.oldLeases {
		if c.leaseTransfers[lease] == token {
			delete(c.leaseTransfers, lease)
		}
	}
	for _, lease := range token.newLeases {
		if c.leaseTransfers[lease] == token {
			delete(c.leaseTransfers, lease)
		}
	}
	token.consumed = true
	c.mu.Unlock()
	close(token.resolved)
}

// CheckMutation performs a one-shot compatibility check against current lease/
// admission state (design §6.7.2). Operations with raw I/O must instead hold a
// SharedMutationAdmission across that I/O. Returns nil if mutation may proceed, or
// ErrSharedServiceInUse if leases block it. MutationExactNoOp always returns
// nil (idempotent Start with same config).
//
// This is the lease-guard gate: it does NOT execute the raw mutation. The
// caller checks this BEFORE invoking raw Start/Stop/reconfigure.
func (c *SharedServiceCoordinator) CheckMutation(
	kind SharedServiceKind,
	mutation SharedServiceMutationKind,
	configFingerprint [32]byte,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrSharedCoordinatorClosed
	}
	entry := c.entries[kind]
	if mutation == MutationExactNoOp {
		return nil // idempotent: always allowed
	}
	if c.headroomDraining && (kind == SharedServiceClaudeHeadroom || kind == SharedServiceCodexHeadroom) {
		return ErrSharedServiceInUse
	}
	for admission := range c.launchAdmissions {
		if admission.kind == kind {
			return ErrSharedServiceInUse
		}
	}
	for admission := range c.mutationAdmissions {
		if admission.kind == kind {
			return ErrSharedServiceInUse
		}
	}
	if entry == nil || len(entry.leases) == 0 {
		return nil // no active/pending leases: mutation allowed
	}
	// Leases exist: reject all non-no-op mutations (design §6.7.2).
	return ErrSharedServiceInUse
}

// BeginHeadroomUninstallDrain (R4-002) atomically enters the install-drain
// critical section for a Headroom uninstall: it sets headroomDraining (so all
// subsequent AcquireForRun for either headroom kind is rejected) AND reports
// whether BOTH headroom kinds are free of leases and pending launch/mutation
// admissions. The caller MUST call
// EndHeadroomUninstallDrain exactly once when the uninstall completes or aborts
// (the drain must span the stop + venv removal to close the TOCTOU window).
//
// Returns empty=true only when no prior lease/admission exists for either kind
// at the moment the drain is entered; the drain is set regardless so the caller can
// release it on the abort path.
func (c *SharedServiceCoordinator) BeginHeadroomUninstallDrain() (empty bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.headroomDraining = true
	claudeN, codexN, pendingN := 0, 0, 0
	if e := c.entries[SharedServiceClaudeHeadroom]; e != nil {
		claudeN = len(e.leases)
	}
	if e := c.entries[SharedServiceCodexHeadroom]; e != nil {
		codexN = len(e.leases)
	}
	for admission := range c.launchAdmissions {
		if admission.kind == SharedServiceClaudeHeadroom || admission.kind == SharedServiceCodexHeadroom {
			pendingN++
		}
	}
	for admission := range c.mutationAdmissions {
		if admission.kind == SharedServiceClaudeHeadroom || admission.kind == SharedServiceCodexHeadroom {
			pendingN++
		}
	}
	return claudeN == 0 && codexN == 0 && pendingN == 0
}

// EndHeadroomUninstallDrain (R4-002) releases the install-drain critical
// section, allowing new headroom run leases again. Idempotent (safe to call
// when no drain is active).
func (c *SharedServiceCoordinator) EndHeadroomUninstallDrain() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.headroomDraining = false
}

// LeaseCount returns the number of active leases for a kind (diagnostic only;
// not exposed to wire/log per privacy design).
func (c *SharedServiceCoordinator) LeaseCount(kind SharedServiceKind) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[kind]
	if entry == nil {
		return 0
	}
	return len(entry.leases)
}

// PromotedLeaseCount returns committed run owners for compensation/lifecycle
// diagnostics. Pending transaction leases are deliberately excluded.
func (c *SharedServiceCoordinator) PromotedLeaseCount(kind SharedServiceKind) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	if entry := c.entries[kind]; entry != nil {
		for lease := range entry.leases {
			if lease.promoted {
				count++
			}
		}
	}
	return count
}

// LaunchAdmissionCount returns pending claims for a kind (diagnostic/test only).
func (c *SharedServiceCoordinator) LaunchAdmissionCount(kind SharedServiceKind) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for admission := range c.launchAdmissions {
		if admission.kind == kind {
			count++
		}
	}
	return count
}

// MutationAdmissionCount returns in-flight raw mutations for a kind (test only).
func (c *SharedServiceCoordinator) MutationAdmissionCount(kind SharedServiceKind) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for admission := range c.mutationAdmissions {
		if admission.kind == kind {
			count++
		}
	}
	return count
}

// ClearAll permanently closes the coordinator for shutdown and releases all
// leases plus launch/mutation admissions. It does not stop singletons; the App
// owns bounded process cleanup after this logical fence.
func (c *SharedServiceCoordinator) ClearAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for _, entry := range c.entries {
		entry.leases = make(map[*SharedDependencyLease]struct{})
	}
	c.launchAdmissions = make(map[*SharedLaunchAdmission]struct{})
	c.launchTransactions = make(map[*SharedLaunchAdmission]*sharedLaunchTransaction)
	c.mutationAdmissions = make(map[*SharedMutationAdmission]struct{})
	resolvedTransfers := make(map[*SharedLeaseTransfer]struct{})
	for _, transfer := range c.leaseTransfers {
		resolvedTransfers[transfer] = struct{}{}
	}
	for transfer := range resolvedTransfers {
		if !transfer.consumed {
			transfer.consumed = true
			close(transfer.resolved)
		}
	}
	c.leaseTransfers = make(map[*SharedDependencyLease]*SharedLeaseTransfer)
	c.headroomDraining = false
}

// entryIDFromRunPermit extracts the SessionID from a RunPermit's entry.
func entryIDFromRunPermit(p *RunPermit) contract.SessionID {
	if p == nil || p.entry == nil {
		return ""
	}
	return p.entry.sessionID
}
