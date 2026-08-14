package session

import (
	"crypto/rand"
	"errors"
	"math"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/processcap"

	"github.com/google/uuid"
)

type authorityPhase uint8

const (
	authorityPending authorityPhase = iota + 1
	authorityPresent
	authorityTombstoned
)

type AuthorityLifecycleState uint8

const (
	AuthorityRunning AuthorityLifecycleState = iota + 1
	AuthorityStopping
	AuthorityStopped
	AuthorityExited
	AuthorityUnavailable
)

type SessionRevisions struct {
	Membership uint64
	Lifecycle  uint64
	Run        uint64
	Activity   uint64
}

type authorityPrivate struct {
	origin         launchplan.Origin
	mode           launchplan.Mode
	remoteEligible bool
	safeTitle      string
	recipe         launchplan.StableRecipe
	bindingID      processcap.BindingID
	state          AuthorityLifecycleState
	revisions      SessionRevisions
	lastActivityAt time.Time

	pendingRemoveID    uint64
	pendingLifecycleID uint64
	removeReceipt      RemoveReceipt

	// titleScanMtime/titleScanSize cache the jsonl file fingerprint of the last
	// List() title backfill that scanned this stopped claudecode session and
	// found no extractable title. While the file's (mtime, size) is unchanged,
	// subsequent polls (frontend useSessionList polls every 2s) skip the
	// rescan. titleScanned marks whether the fingerprint is set. Guarded by the
	// owning entry.guard. Reset (titleScanned=false) when a title is found.
	titleScanMtime int64
	titleScanSize  int64
	titleScanned   bool
}

type authorityEntry struct {
	guard sync.Mutex

	phase           authorityPhase
	reservationID   uint64
	membershipNonce [16]byte
	session         Session
	private         authorityPrivate

	activityAt       atomic.Int64
	activityRevision atomic.Uint64
}

// AuthorityHandle is an opaque, copyable membership capability. It deliberately
// exposes no fields, JSON support, string rendering, process data, or entry
// pointer.
type AuthorityHandle struct {
	sessionID          string
	membershipRevision uint64
	nonce              [16]byte
}

func (h AuthorityHandle) SessionID() string          { return h.sessionID }
func (h AuthorityHandle) MembershipRevision() uint64 { return h.membershipRevision }
func (h AuthorityHandle) valid() bool {
	return h.sessionID != "" && h.membershipRevision != 0 && h.nonce != [16]byte{}
}

type ResolvedRemoteHandle struct {
	entry              *authorityEntry
	sessionID          string
	membershipRevision uint64
	nonce              [16]byte
}

type CreateSpec struct {
	RequestedID    string
	AppType        AppType
	Origin         launchplan.Origin
	Mode           launchplan.Mode
	Workdir        string
	RemoteEligible bool
	Provider       string
	Preset         string
	Model          string
	StartedAt      time.Time
}

type CreateReservation struct {
	manager       *Manager
	entry         *authorityEntry
	reservationID uint64
	used          atomic.Bool
}

func (r *CreateReservation) Session() Session {
	if r == nil || r.entry == nil {
		return Session{}
	}
	r.entry.guard.Lock()
	defer r.entry.guard.Unlock()
	return r.entry.session
}

func (r *CreateReservation) SessionID() string {
	if r == nil || r.entry == nil {
		return ""
	}
	return r.entry.session.ID
}

type PreparedAuthorityActivation struct {
	Session        Session
	Recipe         launchplan.StableRecipe
	BindingID      processcap.BindingID
	PID            int
	RunRevision    uint64
	StartedAt      time.Time
	LastActivityAt time.Time
}

type PreparedActivationToken struct {
	manager       *Manager
	entry         *authorityEntry
	reservationID uint64
	values        PreparedAuthorityActivation
	committed     bool
	aborted       bool
}

type AuthoritySnapshot struct {
	Handle         AuthorityHandle
	CLIType        launchplan.CLIType
	Mode           launchplan.Mode
	RemoteEligible bool
	SafeTitle      string
	Workdir        string
	State          AuthorityLifecycleState
	StartedAt      time.Time
	LastActivityAt time.Time
	Revisions      SessionRevisions
}

type AuthorityActivationReceipt struct {
	Snapshot  AuthoritySnapshot
	Authority AuthorityHandle
}

type AuthorityProcessRef struct {
	BindingID   processcap.BindingID
	RunRevision uint64
}

type RemoveExpected struct {
	MembershipRevision uint64
	LifecycleRevision  uint64
	RunRevision        uint64
}

type PreparedRemoveToken struct {
	manager   *Manager
	entry     *authorityEntry
	handle    AuthorityHandle
	expected  RemoveExpected
	binding   processcap.BindingID
	receiptID uint64
	committed bool
	aborted   bool
}

func (t *PreparedRemoveToken) SessionID() string {
	if t == nil {
		return ""
	}
	return t.handle.sessionID
}

func (t *PreparedRemoveToken) ReceiptID() uint64 {
	if t == nil {
		return 0
	}
	return t.receiptID
}

type RemoveReceipt struct {
	ReceiptID          uint64
	SessionID          string
	MembershipRevision uint64
	LifecycleRevision  uint64
	RemovedAt          time.Time
}

type LifecycleKind uint8

const (
	LifecycleStop LifecycleKind = iota + 1
	LifecycleRestart
)

type LifecycleExpected struct {
	MembershipRevision uint64
	LifecycleRevision  uint64
	RunRevision        uint64
}

type AuthorityLifecycleReceipt struct {
	Snapshot  AuthoritySnapshot
	Revisions SessionRevisions
}

type PreparedRestartValues struct {
	BindingID   processcap.BindingID
	PID         int
	RunRevision uint64
	Recipe      launchplan.StableRecipe
}

type PreparedLifecycleToken struct {
	manager   *Manager
	entry     *authorityEntry
	handle    AuthorityHandle
	kind      LifecycleKind
	expected  LifecycleExpected
	binding   processcap.BindingID
	tokenID   uint64
	restart   PreparedRestartValues
	bound     bool
	committed bool
	aborted   bool
}

type PreparedExitToken struct {
	manager   *Manager
	entry     *authorityEntry
	handle    AuthorityHandle
	expected  LifecycleExpected
	tokenID   uint64
	failed    bool
	at        time.Time
	committed bool
}

func (m *Manager) ReserveCreate(spec CreateSpec) (*CreateReservation, error) {
	if m == nil || spec.AppType == "" || spec.Origin == 0 || spec.Mode == 0 || spec.Workdir == "" {
		return nil, ErrAuthorityInvalidCreate
	}
	if spec.RemoteEligible && spec.Mode != launchplan.ModeEmbedded {
		return nil, ErrAuthorityInvalidCreate
	}
	id := spec.RequestedID
	if id == "" {
		for attempt := 0; attempt < 8; attempt++ {
			candidate := uuid.New().String()[:8]
			m.indexMu.RLock()
			_, used := m.usedIDs[candidate]
			m.indexMu.RUnlock()
			if !used {
				id = candidate
				break
			}
		}
		if id == "" {
			return nil, ErrAuthorityIDCollision
		}
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil || nonce == [16]byte{} {
		return nil, ErrAuthorityEntropy
	}
	startedAt := spec.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	legacyMode, err := launchModeFromAuthorityMode(spec.Mode)
	if err != nil {
		return nil, err
	}
	entry := &authorityEntry{
		phase:           authorityPending,
		membershipNonce: nonce,
		session: Session{
			ID:        id,
			AppType:   spec.AppType,
			Provider:  spec.Provider,
			Preset:    spec.Preset,
			Model:     spec.Model,
			Mode:      legacyMode,
			WorkDir:   spec.Workdir,
			Status:    StatusRunning,
			StartedAt: startedAt,
		},
		private: authorityPrivate{
			origin:         spec.Origin,
			mode:           spec.Mode,
			remoteEligible: spec.RemoteEligible,
			safeTitle:      authoritySafeTitle(spec.AppType, spec.Workdir),
			state:          AuthorityRunning,
			lastActivityAt: startedAt,
		},
	}
	entry.activityAt.Store(startedAt.UnixNano())
	m.indexMu.Lock()
	if _, used := m.usedIDs[id]; used {
		m.indexMu.Unlock()
		return nil, ErrAuthorityIDCollision
	}
	if _, exists := m.entries[id]; exists {
		m.indexMu.Unlock()
		return nil, ErrAuthorityIDCollision
	}
	m.reservationSeq++
	if m.reservationSeq == 0 {
		m.indexMu.Unlock()
		return nil, ErrAuthorityRevisionOverflow
	}
	entry.reservationID = m.reservationSeq
	m.entries[id] = entry
	m.usedIDs[id] = struct{}{}
	m.indexMu.Unlock()
	return &CreateReservation{manager: m, entry: entry, reservationID: entry.reservationID}, nil
}

func (m *Manager) PrepareActivation(reservation *CreateReservation, values PreparedAuthorityActivation) (*PreparedActivationToken, error) {
	if reservation == nil || reservation.manager != m || reservation.entry == nil || reservation.used.Load() {
		return nil, ErrAuthorityStaleToken
	}
	if values.PID <= 0 || values.RunRevision == 0 || values.BindingID.Validate(0) != nil {
		return nil, ErrAuthorityInvalidActivation
	}
	entry := reservation.entry
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPending || entry.reservationID != reservation.reservationID {
		return nil, ErrAuthorityStaleToken
	}
	if values.StartedAt.IsZero() {
		values.StartedAt = entry.session.StartedAt
	}
	if values.LastActivityAt.IsZero() {
		values.LastActivityAt = values.StartedAt
	}
	if values.Session.ID != "" && values.Session.ID != entry.session.ID {
		return nil, ErrAuthorityInvalidActivation
	}
	return &PreparedActivationToken{manager: m, entry: entry, reservationID: reservation.reservationID, values: values}, nil
}

func (m *Manager) CommitPreparedActivation(token *PreparedActivationToken, noFailRemoteCommit func()) (AuthorityActivationReceipt, error) {
	if token == nil || token.manager != m || token.entry == nil {
		return AuthorityActivationReceipt{}, ErrAuthorityStaleToken
	}
	entry := token.entry
	entry.guard.Lock()
	if token.committed || token.aborted || entry.phase != authorityPending || entry.reservationID != token.reservationID {
		entry.guard.Unlock()
		return AuthorityActivationReceipt{}, ErrAuthorityStaleToken
	}
	if entry.private.revisions.Membership != 0 || entry.private.revisions.Lifecycle != 0 || token.values.RunRevision == 0 {
		entry.guard.Unlock()
		return AuthorityActivationReceipt{}, ErrAuthorityStaleToken
	}
	defer fatalOnCommitPanic()
	if noFailRemoteCommit != nil {
		noFailRemoteCommit()
	}
	values := token.values
	if values.Session.ID != "" {
		entry.session = values.Session
	}
	entry.session.PID = values.PID
	entry.session.Status = StatusRunning
	entry.session.StartedAt = values.StartedAt
	entry.private.recipe = values.Recipe
	entry.private.bindingID = values.BindingID
	entry.private.state = AuthorityRunning
	entry.private.lastActivityAt = values.LastActivityAt
	entry.private.revisions = SessionRevisions{Membership: 1, Lifecycle: 1, Run: values.RunRevision, Activity: 1}
	entry.activityAt.Store(values.LastActivityAt.UnixNano())
	entry.activityRevision.Store(1)
	entry.phase = authorityPresent
	token.committed = true
	snapshot := snapshotLocked(entry)
	entry.guard.Unlock()
	return AuthorityActivationReceipt{Snapshot: snapshot, Authority: snapshot.Handle}, nil
}

func (m *Manager) CommitPreparedExternalActivation(token *PreparedActivationToken) (AuthorityActivationReceipt, error) {
	if token == nil || token.entry == nil || token.entry.private.mode != launchplan.ModeExternal || token.entry.private.remoteEligible {
		return AuthorityActivationReceipt{}, ErrAuthorityInvalidActivation
	}
	return m.CommitPreparedActivation(token, nil)
}

func (m *Manager) AbortPreparedActivation(token *PreparedActivationToken) {
	if token == nil || token.manager != m || token.entry == nil {
		return
	}
	token.entry.guard.Lock()
	if !token.committed && token.entry.phase == authorityPending && token.entry.reservationID == token.reservationID {
		token.aborted = true
	}
	token.entry.guard.Unlock()
	if token.aborted {
		m.removePendingEntry(token.entry)
	}
}

func (m *Manager) AbortCreate(reservation *CreateReservation) {
	if reservation == nil || reservation.manager != m || reservation.entry == nil || reservation.used.Swap(true) {
		return
	}
	entry := reservation.entry
	entry.guard.Lock()
	pending := entry.phase == authorityPending && entry.reservationID == reservation.reservationID
	entry.guard.Unlock()
	if pending {
		m.removePendingEntry(entry)
	}
}

func (m *Manager) removePendingEntry(entry *authorityEntry) {
	if entry == nil {
		return
	}
	m.indexMu.Lock()
	if m.entries[entry.session.ID] == entry {
		delete(m.entries, entry.session.ID)
	}
	m.indexMu.Unlock()
}

func (m *Manager) ValidateRemoteMembership(handle AuthorityHandle) (AuthoritySnapshot, error) {
	if !handle.valid() {
		return AuthoritySnapshot{}, ErrAuthorityNotFound
	}
	entry := m.entryForID(handle.sessionID)
	if entry == nil {
		return AuthoritySnapshot{}, ErrAuthorityNotFound
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if err := validateRemoteHandleLocked(entry, handle); err != nil {
		return AuthoritySnapshot{}, err
	}
	return snapshotLocked(entry), nil
}

func (m *Manager) RemoteSnapshotByID(id string) (AuthoritySnapshot, error) {
	entry := m.entryForID(id)
	if entry == nil {
		return AuthoritySnapshot{}, ErrAuthorityNotFound
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPresent || !entry.private.remoteEligible || entry.private.mode != launchplan.ModeEmbedded {
		return AuthoritySnapshot{}, ErrAuthorityNotFound
	}
	return snapshotLocked(entry), nil
}

// LifecycleSnapshotByID resolves any present Authority entry, including
// remote-ineligible external desktop sessions. The projection carries no
// provider/model/PID/secrets.
func (m *Manager) LifecycleSnapshotByID(id string) (AuthoritySnapshot, error) {
	entry := m.entryForID(id)
	if entry == nil {
		return AuthoritySnapshot{}, ErrAuthorityNotFound
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPresent {
		return AuthoritySnapshot{}, ErrAuthorityNotFound
	}
	return snapshotLocked(entry), nil
}

func (m *Manager) ResolveRemoteHandle(id string) (ResolvedRemoteHandle, error) {
	m.indexMu.RLock()
	entry := m.entries[id]
	m.indexMu.RUnlock()
	if entry == nil {
		return ResolvedRemoteHandle{}, ErrAuthorityNotFound
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPresent || !entry.private.remoteEligible || entry.private.mode != launchplan.ModeEmbedded ||
		entry.private.pendingRemoveID != 0 || entry.private.pendingLifecycleID != 0 {
		return ResolvedRemoteHandle{}, ErrAuthorityNotFound
	}
	return ResolvedRemoteHandle{entry: entry, sessionID: id, membershipRevision: entry.private.revisions.Membership, nonce: entry.membershipNonce}, nil
}

// CommitResolvedAttach never resolves the Manager index. It acquires only the
// stable entry guard, validates exact membership, and gives the caller one
// panic-fatal final mutation block.
func (m *Manager) CommitResolvedAttach(resolved ResolvedRemoteHandle, noFail func()) error {
	if resolved.entry == nil || resolved.sessionID == "" {
		return ErrAuthorityNotFound
	}
	entry := resolved.entry
	entry.guard.Lock()
	if entry.phase != authorityPresent || entry.session.ID != resolved.sessionID || !entry.private.remoteEligible ||
		entry.private.mode != launchplan.ModeEmbedded || entry.private.revisions.Membership != resolved.membershipRevision ||
		entry.membershipNonce != resolved.nonce || entry.private.pendingRemoveID != 0 || entry.private.pendingLifecycleID != 0 {
		entry.guard.Unlock()
		return ErrAuthorityNotFound
	}
	defer fatalOnCommitPanic()
	if noFail != nil {
		noFail()
	}
	entry.guard.Unlock()
	return nil
}

func (m *Manager) WithRemoteSnapshot(handle AuthorityHandle, fn func(AuthoritySnapshot) error) error {
	entry := m.entryForID(handle.sessionID)
	if entry == nil {
		return ErrAuthorityNotFound
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if err := validateRemoteHandleLocked(entry, handle); err != nil {
		return err
	}
	if fn == nil {
		return nil
	}
	return fn(snapshotLocked(entry))
}

func (m *Manager) ListRemoteSafeSnapshots() []AuthoritySnapshot {
	m.indexMu.RLock()
	entries := make([]*authorityEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}
	m.indexMu.RUnlock()
	result := make([]AuthoritySnapshot, 0, len(entries))
	for _, entry := range entries {
		entry.guard.Lock()
		if entry.phase == authorityPresent && entry.private.remoteEligible && entry.private.mode == launchplan.ModeEmbedded {
			result = append(result, snapshotLocked(entry))
		}
		entry.guard.Unlock()
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].LastActivityAt.Equal(result[j].LastActivityAt) {
			return result[i].LastActivityAt.After(result[j].LastActivityAt)
		}
		return result[i].Handle.sessionID < result[j].Handle.sessionID
	})
	return result
}

// BindLegacyProcess upgrades an immediately-created local record with exact
// process evidence. It exists for external desktop launch paths while they keep
// their legacy public timing; remote membership remains disabled for external.
func (m *Manager) BindLegacyProcess(id string, start processcap.StartEvidence, recipe launchplan.StableRecipe) (AuthorityHandle, error) {
	if err := start.Validate(processcap.BackendExternalLauncher); err != nil {
		return AuthorityHandle{}, err
	}
	entry := m.entryForID(id)
	if entry == nil {
		return AuthorityHandle{}, ErrAuthorityNotFound
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPresent || entry.private.mode != launchplan.ModeExternal || entry.private.bindingID.Validate(0) == nil {
		return AuthorityHandle{}, ErrAuthorityStaleRevision
	}
	entry.private.bindingID = start.Binding.BindingID()
	entry.private.recipe = recipe
	entry.private.revisions.Run = start.Binding.BindingID().Generation
	entry.session.PID = start.PID
	return handleLocked(entry), nil
}

func (m *Manager) ProcessRef(handle AuthorityHandle) (AuthorityProcessRef, error) {
	entry := m.entryForID(handle.sessionID)
	if entry == nil {
		return AuthorityProcessRef{}, ErrAuthorityNotFound
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if handle.valid() {
		if err := validateHandleLocked(entry, handle); err != nil {
			return AuthorityProcessRef{}, err
		}
	} else if entry.phase != authorityPresent {
		return AuthorityProcessRef{}, ErrAuthorityNotFound
	}
	if err := entry.private.bindingID.Validate(0); err != nil || entry.private.revisions.Run == 0 {
		return AuthorityProcessRef{}, ErrAuthorityProcessUnavailable
	}
	return AuthorityProcessRef{BindingID: entry.private.bindingID, RunRevision: entry.private.revisions.Run}, nil
}

func (m *Manager) PrepareRemove(handle AuthorityHandle, expected RemoveExpected, binding processcap.BindingID) (*PreparedRemoveToken, error) {
	entry := m.entryForID(handle.sessionID)
	if entry == nil {
		return nil, ErrAuthorityNotFound
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if err := validateHandleLocked(entry, handle); err != nil {
		return nil, err
	}
	if entry.private.pendingRemoveID != 0 || entry.private.pendingLifecycleID != 0 ||
		expected.MembershipRevision != entry.private.revisions.Membership ||
		expected.LifecycleRevision != entry.private.revisions.Lifecycle ||
		expected.RunRevision != entry.private.revisions.Run || binding != entry.private.bindingID {
		return nil, ErrAuthorityStaleRevision
	}
	m.receiptSeqMu.Lock()
	m.receiptSeq++
	receiptID := m.receiptSeq
	m.receiptSeqMu.Unlock()
	if receiptID == 0 {
		return nil, ErrAuthorityRevisionOverflow
	}
	entry.private.pendingRemoveID = receiptID
	return &PreparedRemoveToken{manager: m, entry: entry, handle: handle, expected: expected, binding: binding, receiptID: receiptID}, nil
}

func (m *Manager) CommitPreparedRemove(token *PreparedRemoveToken, close processcap.ExactCloseEvidence, removedAt time.Time, noFailRemoteCommit func()) (RemoveReceipt, error) {
	if token == nil || token.manager != m || token.entry == nil {
		return RemoveReceipt{}, ErrAuthorityStaleToken
	}
	entry := token.entry
	entry.guard.Lock()
	if token.committed || token.aborted || entry.phase != authorityPresent || entry.private.pendingRemoveID != token.receiptID ||
		entry.private.revisions.Membership != token.expected.MembershipRevision ||
		entry.private.revisions.Lifecycle != token.expected.LifecycleRevision ||
		entry.private.revisions.Run != token.expected.RunRevision || entry.private.bindingID != token.binding ||
		close.BindingID() != token.binding || !close.Confirmed() {
		entry.guard.Unlock()
		return RemoveReceipt{}, ErrAuthorityStaleToken
	}
	if entry.private.revisions.Membership == math.MaxUint64 || entry.private.revisions.Lifecycle == math.MaxUint64 {
		entry.guard.Unlock()
		return RemoveReceipt{}, ErrAuthorityRevisionOverflow
	}
	if removedAt.IsZero() {
		removedAt = time.Now()
	}
	defer fatalOnCommitPanic()
	if noFailRemoteCommit != nil {
		noFailRemoteCommit()
	}
	receipt := RemoveReceipt{
		ReceiptID:          token.receiptID,
		SessionID:          entry.session.ID,
		MembershipRevision: entry.private.revisions.Membership + 1,
		LifecycleRevision:  entry.private.revisions.Lifecycle + 1,
		RemovedAt:          removedAt,
	}
	entry.private.revisions.Membership = receipt.MembershipRevision
	entry.private.revisions.Lifecycle = receipt.LifecycleRevision
	entry.private.pendingRemoveID = 0
	entry.private.removeReceipt = receipt
	entry.phase = authorityTombstoned
	token.committed = true
	entry.guard.Unlock()
	return receipt, nil
}

func (m *Manager) AbortPreparedRemove(token *PreparedRemoveToken) {
	if token == nil || token.manager != m || token.entry == nil {
		return
	}
	token.entry.guard.Lock()
	if !token.committed && token.entry.private.pendingRemoveID == token.receiptID {
		token.entry.private.pendingRemoveID = 0
		token.aborted = true
	}
	token.entry.guard.Unlock()
}

// ReclaimTombstone is the sole owner API for physical Manager GC. Exact receipt
// identity and tombstone revisions are required; usedIDs are never deleted.
func (m *Manager) ReclaimTombstone(receipt RemoveReceipt) error {
	if m == nil || receipt.ReceiptID == 0 || receipt.SessionID == "" || receipt.MembershipRevision == 0 || receipt.LifecycleRevision == 0 {
		return ErrAuthorityInvalidReceipt
	}
	m.reclaimedMu.Lock()
	if previous, ok := m.reclaimed[receipt.ReceiptID]; ok {
		m.reclaimedMu.Unlock()
		if previous == receipt {
			return nil
		}
		return ErrAuthorityInvalidReceipt
	}
	m.reclaimedMu.Unlock()
	entry := m.entryForID(receipt.SessionID)
	if entry == nil {
		return ErrAuthorityInvalidReceipt
	}
	entry.guard.Lock()
	if entry.phase != authorityTombstoned || entry.private.removeReceipt != receipt ||
		entry.private.revisions.Membership != receipt.MembershipRevision ||
		entry.private.revisions.Lifecycle != receipt.LifecycleRevision {
		entry.guard.Unlock()
		return ErrAuthorityInvalidReceipt
	}
	m.indexMu.Lock()
	if m.entries[receipt.SessionID] != entry {
		m.indexMu.Unlock()
		entry.guard.Unlock()
		return ErrAuthorityInvalidReceipt
	}
	delete(m.entries, receipt.SessionID)
	m.indexMu.Unlock()
	m.reclaimedMu.Lock()
	m.reclaimed[receipt.ReceiptID] = receipt
	m.reclaimedMu.Unlock()
	entry.guard.Unlock()
	return nil
}

func (m *Manager) PrepareLifecycle(handle AuthorityHandle, kind LifecycleKind, expected LifecycleExpected, binding processcap.BindingID) (*PreparedLifecycleToken, error) {
	if kind != LifecycleStop && kind != LifecycleRestart {
		return nil, ErrAuthorityInvalidLifecycle
	}
	entry := m.entryForID(handle.sessionID)
	if entry == nil {
		return nil, ErrAuthorityNotFound
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if err := validateHandleLocked(entry, handle); err != nil {
		return nil, err
	}
	if entry.private.pendingRemoveID != 0 || entry.private.pendingLifecycleID != 0 ||
		expected.MembershipRevision != entry.private.revisions.Membership || expected.LifecycleRevision != entry.private.revisions.Lifecycle ||
		expected.RunRevision != entry.private.revisions.Run || binding != entry.private.bindingID {
		return nil, ErrAuthorityStaleRevision
	}
	m.lifecycleSeqMu.Lock()
	m.lifecycleSeq++
	tokenID := m.lifecycleSeq
	m.lifecycleSeqMu.Unlock()
	if tokenID == 0 {
		return nil, ErrAuthorityRevisionOverflow
	}
	entry.private.pendingLifecycleID = tokenID
	return &PreparedLifecycleToken{manager: m, entry: entry, handle: handle, kind: kind, expected: expected, binding: binding, tokenID: tokenID}, nil
}

func (m *Manager) BindPreparedRestartResult(token *PreparedLifecycleToken, values PreparedRestartValues) error {
	if token == nil || token.manager != m || token.entry == nil || token.kind != LifecycleRestart || values.PID <= 0 || values.RunRevision == 0 || values.BindingID.Validate(0) != nil {
		return ErrAuthorityInvalidLifecycle
	}
	token.entry.guard.Lock()
	defer token.entry.guard.Unlock()
	if token.committed || token.aborted || token.entry.private.pendingLifecycleID != token.tokenID {
		return ErrAuthorityStaleToken
	}
	token.restart = values
	token.bound = true
	return nil
}

func (m *Manager) CommitPreparedStop(token *PreparedLifecycleToken, close processcap.ExactCloseEvidence, noFailRemoteCommit func()) (AuthorityLifecycleReceipt, error) {
	return m.commitPreparedLifecycle(token, close, AuthorityStopped, noFailRemoteCommit)
}

func (m *Manager) CommitPreparedReconcile(token *PreparedLifecycleToken, target AuthorityLifecycleState, noFailRemoteCommit func()) (AuthorityLifecycleReceipt, error) {
	if target != AuthorityUnavailable {
		return AuthorityLifecycleReceipt{}, ErrAuthorityInvalidLifecycle
	}
	return m.commitPreparedLifecycle(token, processcap.ExactCloseEvidence{}, target, noFailRemoteCommit)
}

func (m *Manager) commitPreparedLifecycle(token *PreparedLifecycleToken, close processcap.ExactCloseEvidence, target AuthorityLifecycleState, noFailRemoteCommit func()) (AuthorityLifecycleReceipt, error) {
	if token == nil || token.manager != m || token.entry == nil {
		return AuthorityLifecycleReceipt{}, ErrAuthorityStaleToken
	}
	entry := token.entry
	entry.guard.Lock()
	closeRequired := target == AuthorityStopped
	if token.committed || token.aborted || entry.phase != authorityPresent || entry.private.pendingLifecycleID != token.tokenID ||
		entry.private.revisions.Membership != token.expected.MembershipRevision || entry.private.revisions.Lifecycle != token.expected.LifecycleRevision ||
		entry.private.revisions.Run != token.expected.RunRevision || entry.private.bindingID != token.binding ||
		(closeRequired && (close.BindingID() != token.binding || !close.Confirmed())) || entry.private.revisions.Lifecycle == math.MaxUint64 {
		entry.guard.Unlock()
		return AuthorityLifecycleReceipt{}, ErrAuthorityStaleToken
	}
	defer fatalOnCommitPanic()
	if noFailRemoteCommit != nil {
		noFailRemoteCommit()
	}
	entry.private.state = target
	entry.private.revisions.Lifecycle++
	entry.private.pendingLifecycleID = 0
	entry.private.lastActivityAt = time.Now()
	incrementActivityLocked(entry)
	switch target {
	case AuthorityStopped:
		now := entry.private.lastActivityAt
		entry.session.Status = StatusStopped
		entry.session.StoppedAt = &now
	case AuthorityUnavailable:
		now := entry.private.lastActivityAt
		entry.session.Status = StatusFailed
		entry.session.StoppedAt = &now
	}
	token.committed = true
	snapshot := snapshotLocked(entry)
	entry.guard.Unlock()
	return AuthorityLifecycleReceipt{Snapshot: snapshot, Revisions: snapshot.Revisions}, nil
}

func (m *Manager) CommitPreparedRestart(token *PreparedLifecycleToken, noFailRemoteCommit func()) (AuthorityLifecycleReceipt, error) {
	if token == nil || token.manager != m || token.entry == nil || token.kind != LifecycleRestart || !token.bound {
		return AuthorityLifecycleReceipt{}, ErrAuthorityInvalidLifecycle
	}
	entry := token.entry
	entry.guard.Lock()
	if token.committed || token.aborted || entry.phase != authorityPresent || entry.private.pendingLifecycleID != token.tokenID ||
		entry.private.revisions.Membership != token.expected.MembershipRevision || entry.private.revisions.Lifecycle != token.expected.LifecycleRevision ||
		entry.private.revisions.Run != token.expected.RunRevision || entry.private.bindingID != token.binding ||
		token.restart.RunRevision <= entry.private.revisions.Run || entry.private.revisions.Lifecycle == math.MaxUint64 {
		entry.guard.Unlock()
		return AuthorityLifecycleReceipt{}, ErrAuthorityStaleToken
	}
	defer fatalOnCommitPanic()
	if noFailRemoteCommit != nil {
		noFailRemoteCommit()
	}
	entry.private.bindingID = token.restart.BindingID
	entry.private.recipe = token.restart.Recipe
	entry.private.revisions.Run = token.restart.RunRevision
	entry.private.revisions.Lifecycle++
	entry.private.pendingLifecycleID = 0
	entry.private.state = AuthorityRunning
	entry.session.PID = token.restart.PID
	entry.session.Status = StatusRunning
	entry.session.StoppedAt = nil
	entry.private.lastActivityAt = time.Now()
	incrementActivityLocked(entry)
	token.committed = true
	snapshot := snapshotLocked(entry)
	entry.guard.Unlock()
	return AuthorityLifecycleReceipt{Snapshot: snapshot, Revisions: snapshot.Revisions}, nil
}

func (m *Manager) PrepareExactExit(handle AuthorityHandle, expected LifecycleExpected) (*PreparedExitToken, error) {
	entry := m.entryForID(handle.sessionID)
	if entry == nil {
		return nil, ErrAuthorityNotFound
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if err := validateHandleLocked(entry, handle); err != nil || expected.MembershipRevision != entry.private.revisions.Membership ||
		expected.LifecycleRevision != entry.private.revisions.Lifecycle || expected.RunRevision != entry.private.revisions.Run ||
		entry.private.pendingRemoveID != 0 || entry.private.pendingLifecycleID != 0 {
		return nil, ErrAuthorityStaleRevision
	}
	m.lifecycleSeqMu.Lock()
	m.lifecycleSeq++
	tokenID := m.lifecycleSeq
	m.lifecycleSeqMu.Unlock()
	entry.private.pendingLifecycleID = tokenID
	return &PreparedExitToken{manager: m, entry: entry, handle: handle, expected: expected, tokenID: tokenID, at: time.Now()}, nil
}

func (m *Manager) CommitPreparedExit(token *PreparedExitToken, noFailRemoteCommit func()) (AuthorityLifecycleReceipt, error) {
	if token == nil || token.manager != m || token.entry == nil {
		return AuthorityLifecycleReceipt{}, ErrAuthorityStaleToken
	}
	entry := token.entry
	entry.guard.Lock()
	if token.committed || entry.phase != authorityPresent || entry.private.pendingLifecycleID != token.tokenID ||
		entry.private.revisions.Membership != token.expected.MembershipRevision || entry.private.revisions.Lifecycle != token.expected.LifecycleRevision ||
		entry.private.revisions.Run != token.expected.RunRevision || entry.private.revisions.Lifecycle == math.MaxUint64 {
		entry.guard.Unlock()
		return AuthorityLifecycleReceipt{}, ErrAuthorityStaleToken
	}
	defer fatalOnCommitPanic()
	if noFailRemoteCommit != nil {
		noFailRemoteCommit()
	}
	entry.private.revisions.Lifecycle++
	entry.private.pendingLifecycleID = 0
	entry.private.lastActivityAt = token.at
	incrementActivityLocked(entry)
	if token.failed {
		entry.private.state = AuthorityUnavailable
		entry.session.Status = StatusFailed
	} else {
		entry.private.state = AuthorityExited
		entry.session.Status = StatusExited
	}
	at := token.at
	entry.session.StoppedAt = &at
	token.committed = true
	snapshot := snapshotLocked(entry)
	entry.guard.Unlock()
	return AuthorityLifecycleReceipt{Snapshot: snapshot, Revisions: snapshot.Revisions}, nil
}

func (m *Manager) AbortPreparedLifecycle(token *PreparedLifecycleToken) {
	if token == nil || token.manager != m || token.entry == nil {
		return
	}
	token.entry.guard.Lock()
	if !token.committed && token.entry.private.pendingLifecycleID == token.tokenID {
		token.entry.private.pendingLifecycleID = 0
		token.aborted = true
	}
	token.entry.guard.Unlock()
}

func (m *Manager) TouchActivity(id string, runRevision uint64, at time.Time) bool {
	entry := m.entryForID(id)
	if entry == nil || at.IsZero() {
		return false
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPresent || entry.private.revisions.Run != runRevision || at.Before(entry.private.lastActivityAt) {
		return false
	}
	if at.Equal(entry.private.lastActivityAt) {
		return true
	}
	entry.private.lastActivityAt = at
	incrementActivityLocked(entry)
	return true
}

func (m *Manager) CommitExactRunUnavailable(id string, runRevision uint64, at time.Time) bool {
	entry := m.entryForID(id)
	if entry == nil {
		return false
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPresent || entry.private.revisions.Run != runRevision {
		return false
	}
	if at.IsZero() {
		at = time.Now()
	}
	if entry.private.revisions.Lifecycle != math.MaxUint64 {
		entry.private.revisions.Lifecycle++
	}
	entry.private.state = AuthorityUnavailable
	entry.session.Status = StatusFailed
	entry.private.lastActivityAt = at
	incrementActivityLocked(entry)
	return true
}

func (m *Manager) CommitExactRunExit(id string, runRevision uint64, failed bool, at time.Time) bool {
	entry := m.entryForID(id)
	if entry == nil {
		return false
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPresent || entry.private.revisions.Run != runRevision || entry.private.pendingRemoveID != 0 || entry.private.pendingLifecycleID != 0 ||
		entry.private.state == AuthorityExited || entry.private.state == AuthorityStopped {
		return false
	}
	if at.IsZero() {
		at = time.Now()
	}
	if entry.private.revisions.Lifecycle != math.MaxUint64 {
		entry.private.revisions.Lifecycle++
	}
	entry.private.lastActivityAt = at
	incrementActivityLocked(entry)
	entry.session.StoppedAt = &at
	if failed {
		entry.private.state = AuthorityUnavailable
		entry.session.Status = StatusFailed
	} else {
		entry.private.state = AuthorityExited
		entry.session.Status = StatusExited
	}
	return true
}

func (m *Manager) HandleForID(id string) (AuthorityHandle, error) {
	entry := m.entryForID(id)
	if entry == nil {
		return AuthorityHandle{}, ErrAuthorityNotFound
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPresent {
		return AuthorityHandle{}, ErrAuthorityNotFound
	}
	return handleLocked(entry), nil
}

func (m *Manager) entryForID(id string) *authorityEntry {
	if m == nil || id == "" {
		return nil
	}
	m.indexMu.RLock()
	entry := m.entries[id]
	m.indexMu.RUnlock()
	return entry
}

func validateHandleLocked(entry *authorityEntry, handle AuthorityHandle) error {
	if entry == nil || entry.phase != authorityPresent || entry.session.ID != handle.sessionID ||
		entry.private.revisions.Membership != handle.membershipRevision || entry.membershipNonce != handle.nonce {
		return ErrAuthorityNotFound
	}
	return nil
}

func validateRemoteHandleLocked(entry *authorityEntry, handle AuthorityHandle) error {
	if err := validateHandleLocked(entry, handle); err != nil {
		return err
	}
	if !entry.private.remoteEligible || entry.private.mode != launchplan.ModeEmbedded {
		return ErrAuthorityNotFound
	}
	return nil
}

func handleLocked(entry *authorityEntry) AuthorityHandle {
	return AuthorityHandle{sessionID: entry.session.ID, membershipRevision: entry.private.revisions.Membership, nonce: entry.membershipNonce}
}

func snapshotLocked(entry *authorityEntry) AuthoritySnapshot {
	return AuthoritySnapshot{
		Handle:         handleLocked(entry),
		CLIType:        launchplan.CLIType(entry.session.AppType),
		Mode:           entry.private.mode,
		RemoteEligible: entry.private.remoteEligible,
		SafeTitle:      entry.private.safeTitle,
		Workdir:        entry.session.WorkDir,
		State:          entry.private.state,
		StartedAt:      entry.session.StartedAt,
		LastActivityAt: entry.private.lastActivityAt,
		Revisions:      entry.private.revisions,
	}
}

func incrementActivityLocked(entry *authorityEntry) {
	if entry.private.revisions.Activity < math.MaxUint64 {
		entry.private.revisions.Activity++
	}
	entry.activityAt.Store(entry.private.lastActivityAt.UnixNano())
	entry.activityRevision.Store(entry.private.revisions.Activity)
}

func launchModeFromAuthorityMode(mode launchplan.Mode) (LaunchMode, error) {
	switch mode {
	case launchplan.ModeEmbedded:
		return ModeEmbedded, nil
	case launchplan.ModeExternal:
		return ModeTerminal, nil
	default:
		return "", ErrAuthorityInvalidCreate
	}
}

func authorityModeFromLaunchMode(mode LaunchMode) (launchplan.Mode, error) {
	switch mode {
	case ModeEmbedded:
		return launchplan.ModeEmbedded, nil
	case ModeTerminal:
		return launchplan.ModeExternal, nil
	default:
		return 0, ErrAuthorityInvalidCreate
	}
}

func authoritySafeTitle(appType AppType, workdir string) string {
	label := string(appType)
	switch appType {
	case AppTypeClaudeCode:
		label = "Claude Code"
	case AppTypeOpenCode:
		label = "OpenCode"
	case AppTypeCodex:
		label = "Codex"
	case AppTypePi:
		label = "Pi"
	case AppTypeOhMyPi:
		label = "Oh My Pi"
	}
	base := workdir
	for len(base) > 1 && (base[len(base)-1] == '/' || base[len(base)-1] == '\\') {
		base = base[:len(base)-1]
	}
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '/' || base[i] == '\\' {
			base = base[i+1:]
			break
		}
	}
	if base == "" {
		return label
	}
	return label + " · " + base
}

func fatalOnCommitPanic() {
	if recover() != nil {
		os.Exit(70)
	}
}

var (
	ErrAuthorityNotFound           = errors.New("session authority: not found")
	ErrAuthorityIDCollision        = errors.New("session authority: id collision")
	ErrAuthorityEntropy            = errors.New("session authority: entropy unavailable")
	ErrAuthorityInvalidCreate      = errors.New("session authority: invalid create")
	ErrAuthorityInvalidActivation  = errors.New("session authority: invalid activation")
	ErrAuthorityInvalidLifecycle   = errors.New("session authority: invalid lifecycle")
	ErrAuthorityInvalidReceipt     = errors.New("session authority: invalid remove receipt")
	ErrAuthorityStaleToken         = errors.New("session authority: stale token")
	ErrAuthorityStaleRevision      = errors.New("session authority: stale revision")
	ErrAuthorityRevisionOverflow   = errors.New("session authority: revision overflow")
	ErrAuthorityProcessUnavailable = errors.New("session authority: process capability unavailable")
)
