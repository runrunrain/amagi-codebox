package main

// proxy_headroom_facade.go — M3-A2 App-level desktop facade for the Proxy and
// Headroom shared singletons (design §4.1, §6.3, §6.7).
//
// These raw services are REMOVED from the Wails Bind list (C-01); the frontend
// now reaches them only through these App-level methods. Mutation methods
// (Start/Stop/reconfigure) are lease-guarded by the SharedServiceCoordinator
// (design §6.7 N-01): while any active/pending run lease exists, they are
// stably rejected with raw call = 0. Read methods (GetRules/GetStatus/etc.)
// pass through unconditionally.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"amagi-codebox/internal/headroom"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/proxy"
	"amagi-codebox/internal/remote"
)

var (
	ErrExternalCleanupConfirmationRequired = errors.New("external cleanup recovery: explicit confirmation required")
	ErrExternalCleanupStillRunning         = errors.New("external cleanup recovery: process is still running")
	ErrExternalCleanupRecoveryNotFound     = errors.New("external cleanup recovery: item not found")
	ErrExternalCleanupRecheckUnavailable   = errors.New("external cleanup recovery: process recheck unavailable")
)

// checkSharedLease returns ErrSharedServiceInUse if the given mutation is
// blocked by an active run lease (design §6.7.2). M-006: in production the
// coordinator is always wired (constructed in the App factory); a nil
// coordinator means the App is not yet initialized, so we fail closed (reject)
// rather than silently allowing a mutation that could break active sessions.
func (a *App) checkSharedLease(kind remote.SharedServiceKind, mutation remote.SharedServiceMutationKind) error {
	if a.isExternalCleanupRecoveryBlocked() && isHeadroomSharedKind(kind) {
		return remote.ErrSharedServiceInUse
	}
	if a.sharedCoord == nil {
		return remote.ErrSharedServiceInUse
	}
	return a.sharedCoord.CheckMutation(kind, mutation, a.sharedFingerprint(kind))
}

// acquireSharedMutation owns the check→raw-I/O window for a singleton mutation.
// Unlike a one-shot check, the exact token remains visible to launch admissions
// and uninstall drains until the caller releases it.
func (a *App) acquireSharedMutation(kind remote.SharedServiceKind, mutation remote.SharedServiceMutationKind) (*remote.SharedMutationAdmission, error) {
	if a.isExternalCleanupRecoveryBlocked() && isHeadroomSharedKind(kind) {
		return nil, remote.ErrSharedServiceInUse
	}
	if a.sharedCoord == nil {
		return nil, remote.ErrSharedServiceInUse
	}
	return a.sharedCoord.AcquireMutationAdmission(kind, mutation)
}

// sharedFingerprint computes a non-secret config fingerprint for a shared
// singleton from its current running state (M-006). It is only used to detect
// incompatible-config launches while a lease exists.
func (a *App) sharedFingerprint(kind remote.SharedServiceKind) [32]byte {
	switch kind {
	case remote.SharedServiceClaudeProxy:
		st := a.Proxy.GetStatus()
		return sharedFingerprintForProxy(st.BackendURL, st.Port)
	case remote.SharedServiceClaudeHeadroom:
		st := a.Headroom.GetStatus()
		return sharedFingerprintForHeadroom(st.BackendURL, st.Port)
	case remote.SharedServiceCodexHeadroom:
		if a.CodexHeadroom != nil {
			st := a.CodexHeadroom.GetStatus()
			return sharedFingerprintForHeadroom(st.BackendURL, st.Port)
		}
	}
	return [32]byte{}
}

// rememberSharedLease records a shared-service lease for a session (M-006).
func (a *App) rememberSharedLease(sessionID string, lease *remote.SharedDependencyLease) {
	if lease == nil {
		return
	}
	a.sharedLeaseMu.Lock()
	defer a.sharedLeaseMu.Unlock()
	if a.sharedLeases == nil {
		a.sharedLeases = make(map[string][]*remote.SharedDependencyLease)
	}
	a.sharedLeases[sessionID] = append(a.sharedLeases[sessionID], lease)
}

// acquireAndRememberExternalSharedLease atomically promotes one startup
// admission and records the resulting lease under the App registry lock. The
// lock order is App.sharedLeaseMu → coordinator.mu; shutdown uses the same order
// so it cannot clear the coordinator between promotion and registration.
func (a *App) acquireAndRememberExternalSharedLease(
	sessionID string,
	identity *remote.ExternalRunIdentity,
	kind remote.SharedServiceKind,
	admission *remote.SharedLaunchAdmission,
) error {
	if a.sharedCoord == nil {
		return remote.ErrSharedServiceInUse
	}
	fingerprint := a.sharedFingerprint(kind)
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	a.sharedLeaseMu.Lock()
	defer a.sharedLeaseMu.Unlock()
	lease, err := a.sharedCoord.AcquireForExternalRunWithAdmission(
		ctx, identity, kind, fingerprint, admission,
	)
	if err != nil {
		return err
	}
	if a.sharedLeases == nil {
		a.sharedLeases = make(map[string][]*remote.SharedDependencyLease)
	}
	a.sharedLeases[sessionID] = append(a.sharedLeases[sessionID], lease)
	return nil
}

func isHeadroomSharedKind(kind remote.SharedServiceKind) bool {
	return kind == remote.SharedServiceClaudeHeadroom || kind == remote.SharedServiceCodexHeadroom
}

func (a *App) isExternalCleanupRecoveryBlocked() bool {
	a.externalCleanupMu.Lock()
	defer a.externalCleanupMu.Unlock()
	return a.externalCleanupRecoveryBlocked
}

func (a *App) setExternalCleanupRecoveryBlocked(blocked bool) {
	a.externalCleanupMu.Lock()
	a.externalCleanupRecoveryBlocked = blocked
	a.externalCleanupMu.Unlock()
}

// captureExternalProcessStartGeneration snapshots the one-shot App start epoch
// before a potentially slow reservation. Even values are open; Shutdown
// atomically changes the epoch to odd before reporting or StopAll.
func (a *App) captureExternalProcessStartGeneration() (uint64, error) {
	generation := a.externalStartGeneration.Load()
	if generation&1 != 0 || a.externalShutdown.Load() {
		return 0, remote.ErrSharedCoordinatorClosed
	}
	return generation, nil
}

// fenceExternalProcessStarts is the linearization point for graceful Shutdown.
// A reservation that finishes after this transition cannot commit raw OS Start.
func (a *App) fenceExternalProcessStarts() {
	for {
		generation := a.externalStartGeneration.Load()
		if generation&1 != 0 || a.externalStartGeneration.CompareAndSwap(generation, generation+1) {
			break
		}
	}
	// Keep the historical bool as the cheap public-path fence and test seam. The
	// generation above is authoritative for reserve->start commit ordering.
	a.externalShutdown.Store(true)
}

func (a *App) beginExternalOwnershipAttempt(sessionID string, kind remote.SharedServiceKind, generation uint64) *externalOwnershipAttempt {
	attempt := &externalOwnershipAttempt{sessionID: sessionID, kind: kind, startGeneration: generation}
	a.externalCleanupMu.Lock()
	a.externalOwnershipAttempt = attempt
	a.externalCleanupMu.Unlock()
	return attempt
}

// commitExternalProcessStart validates the same generation captured before
// Reserve. The CAS is the start-side linearization against Shutdown's odd-epoch
// transition; repeated checks conservatively let a concurrent fence win until
// the attempt is marked committed.
func (a *App) commitExternalProcessStart(attempt *externalOwnershipAttempt, generation uint64) bool {
	if generation&1 != 0 || a.externalShutdown.Load() {
		return false
	}
	if !a.externalStartGeneration.CompareAndSwap(generation, generation) {
		return false
	}
	if a.externalStartGeneration.Load() != generation || a.externalShutdown.Load() {
		return false
	}
	if attempt == nil {
		return false
	}
	a.externalCleanupMu.Lock()
	defer a.externalCleanupMu.Unlock()
	if a.externalOwnershipAttempt != attempt || attempt.startGeneration != generation ||
		a.externalStartGeneration.Load() != generation || a.externalShutdown.Load() {
		return false
	}
	attempt.startCommitted = true
	return true
}

// authorizeExternalRawStart runs inside Launcher immediately before cmd.Start.
// It closes preparation-delay races: once Shutdown advances the generation, an
// attempt that had committed but not reached this callback cannot start an OS
// process. The attempt remains published until raw Start and post-start
// ownership transfer complete.
func (a *App) authorizeExternalRawStart(attempt *externalOwnershipAttempt, generation uint64) error {
	a.externalCleanupMu.Lock()
	defer a.externalCleanupMu.Unlock()
	if attempt == nil || a.externalOwnershipAttempt != attempt || !attempt.startCommitted ||
		attempt.startGeneration != generation || attempt.rawStartAuthorized ||
		a.externalStartGeneration.Load() != generation || a.externalShutdown.Load() {
		return remote.ErrSharedCoordinatorClosed
	}
	attempt.rawStartAuthorized = true
	return nil
}

func (a *App) markExternalOwnershipStarted(attempt *externalOwnershipAttempt) {
	a.externalCleanupMu.Lock()
	if attempt != nil && a.externalOwnershipAttempt == attempt && attempt.rawStartAuthorized {
		attempt.processStarted = true
	}
	a.externalCleanupMu.Unlock()
}

// handoffPostCommitShutdownStart owns the rare case where Shutdown linearizes
// after Launcher authorization but cmd.Start returns after StopAll's snapshot.
// The process is registered in the exact in-memory cleanup registry before a
// compensating Stop; failures remain in the bounded reaper's view.
func (a *App) handoffPostCommitShutdownStart(
	sessionID string,
	pid int,
	kind remote.SharedServiceKind,
	reservation externalCleanupReservation,
	admission *remote.SharedLaunchAdmission,
	lifecycle externalLauncherPort,
) error {
	record := externalCleanupRecord{
		Version:      externalCleanupJournalVersion,
		SessionID:    sessionID,
		PID:          pid,
		Kind:         kind,
		RegisteredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	claim := a.registerExternalCleanup(record, reservation, false, admission, lifecycle)
	stopErr := a.compensateExternalCleanup(claim)
	a.recordExternalCleanupAbandonment(remote.ExternalCleanupAbandonmentEvent{
		SessionID:          sessionID,
		Kind:               kind,
		Reason:             remote.ExternalCleanupAbandonmentPostCommitShutdown,
		DurableReservation: reservation.SessionID != "",
	})
	if stopErr != nil {
		return errors.Join(remote.ErrSharedCoordinatorClosed, fmt.Errorf("stop post-commit external process: %w", stopErr))
	}
	return remote.ErrSharedCoordinatorClosed
}

// rejectExternalProcessStartAfterFence exactly retires the now-durable pre-start
// reservation and emits a typed receipt. No raw Launcher method has run.
func (a *App) rejectExternalProcessStartAfterFence(
	sessionID string,
	kind remote.SharedServiceKind,
	reservation externalCleanupReservation,
) error {
	completionErr := a.completeExternalProcessReservation(reservation)
	if completionErr == nil && reservation.SessionID != "" {
		a.externalCleanupMu.Lock()
		if attempt := a.externalOwnershipAttempt; attempt != nil && attempt.sessionID == sessionID {
			attempt.durableReservation = false
		}
		a.externalCleanupMu.Unlock()
	}
	a.recordExternalCleanupAbandonment(remote.ExternalCleanupAbandonmentEvent{
		SessionID:          sessionID,
		Kind:               kind,
		Reason:             remote.ExternalCleanupAbandonmentShutdownStartFenced,
		DurableReservation: completionErr != nil && reservation.SessionID != "",
	})
	if completionErr != nil {
		return errors.Join(remote.ErrSharedCoordinatorClosed, completionErr)
	}
	return remote.ErrSharedCoordinatorClosed
}

func (a *App) markExternalOwnershipReservation(attempt *externalOwnershipAttempt) {
	a.externalCleanupMu.Lock()
	if a.externalOwnershipAttempt == attempt {
		attempt.durableReservation = true
	}
	a.externalCleanupMu.Unlock()
}

func (a *App) endExternalOwnershipAttempt(attempt *externalOwnershipAttempt) {
	a.externalCleanupMu.Lock()
	if a.externalOwnershipAttempt == attempt {
		a.externalOwnershipAttempt = nil
	}
	a.externalCleanupMu.Unlock()
}

func (a *App) recordExternalCleanupAbandonment(event remote.ExternalCleanupAbandonmentEvent) {
	if event.Version == 0 {
		event.Version = remote.ExternalCleanupAbandonmentEventVersion
	}
	if event.OccurredAt == "" {
		event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	a.externalCleanupMu.Lock()
	a.externalCleanupEvents = append(a.externalCleanupEvents, event)
	sink := a.externalCleanupEventSink
	a.externalCleanupMu.Unlock()
	if sink != nil {
		sink(event)
	}
	if a.Log != nil {
		a.Log.Error("session", "外部进程清理未确认", fmt.Sprintf(
			"sessionID=%s kind=%d reason=%s durableReservation=%t",
			event.SessionID, event.Kind, event.Reason, event.DurableReservation,
		))
	}
}

func (a *App) recordExternalCleanupRecoveryAudit(event remote.ExternalCleanupRecoveryAuditEvent) {
	if event.Version == 0 {
		event.Version = remote.ExternalCleanupRecoveryContractVersion
	}
	if event.OccurredAt == "" {
		event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	a.externalCleanupMu.Lock()
	a.externalCleanupRecoveryAuditEvents = append(a.externalCleanupRecoveryAuditEvents, event)
	sink := a.externalCleanupRecoveryAuditEventSink
	a.externalCleanupMu.Unlock()
	if sink != nil {
		sink(event)
	}
	if a.Log != nil {
		a.Log.Info("session", "外部进程恢复确认审计", fmt.Sprintf(
			"sessionID=%s kind=%d reason=%s outcome=%s fenceReleased=%t",
			event.SessionID, event.Kind, event.Reason, event.Outcome, event.FenceReleased,
		))
	}
}

// GetExternalCleanupRecoveryStatus is the privacy-minimal product status for
// legacy/uncertain durable owners. It rechecks process liveness but exposes no
// PID, command, environment, provider, path or terminal data.
func (a *App) GetExternalCleanupRecoveryStatus() remote.ExternalCleanupRecoveryStatus {
	a.externalCleanupMu.Lock()
	claims := make([]*externalCleanupClaim, 0, len(a.externalCleanups))
	for _, claim := range a.externalCleanups {
		if claim.recoveryReason != "" {
			claims = append(claims, claim)
		}
	}
	a.externalCleanupMu.Unlock()

	items := make([]remote.ExternalCleanupRecoveryItem, 0, len(claims))
	for _, claim := range claims {
		running := true
		if claim.lifecycle != nil {
			running = claim.lifecycle.IsRunning(claim.sessionID)
		}
		a.externalCleanupMu.Lock()
		if a.externalCleanups[claim.sessionID] != claim {
			a.externalCleanupMu.Unlock()
			continue
		}
		if !running {
			claim.terminalObserved = true
		}
		canConfirm := claim.terminalObserved
		kind := claim.record.Kind
		if kind == 0 {
			kind = claim.reservation.Kind
		}
		item := remote.ExternalCleanupRecoveryItem{
			SessionID:  claim.sessionID,
			Kind:       kind,
			Reason:     claim.recoveryReason,
			State:      remote.ExternalCleanupRecoveryRunning,
			CanConfirm: canConfirm,
		}
		if canConfirm {
			item.State = remote.ExternalCleanupRecoveryAwaitingConfirmation
		}
		a.externalCleanupMu.Unlock()
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].SessionID < items[j].SessionID })
	return remote.ExternalCleanupRecoveryStatus{
		Version: remote.ExternalCleanupRecoveryContractVersion,
		Blocked: a.isExternalCleanupRecoveryBlocked(),
		Items:   items,
	}
}

func (a *App) recomputeExternalCleanupRecoveryFence() (bool, error) {
	if a.externalCleanupStore == nil || !a.externalCleanupStore.IsReady() {
		a.setExternalCleanupRecoveryBlocked(true)
		return true, errExternalCleanupStoreNotReady
	}
	pending, err := a.externalCleanupStore.LoadPending()
	if err != nil {
		a.setExternalCleanupRecoveryBlocked(true)
		return true, err
	}
	active, err := a.externalCleanupStore.LoadActive()
	if err != nil {
		a.setExternalCleanupRecoveryBlocked(true)
		return true, err
	}

	a.externalCleanupMu.Lock()
	knownActive := make(map[string]struct{}, len(a.externalCleanups)+len(a.externalDurableRuns))
	blocked := len(pending) > 0
	for id, claim := range a.externalCleanups {
		if claim.recordDurable {
			knownActive[id] = struct{}{}
		}
		if claim.recoveryReason != "" {
			blocked = true
		}
	}
	for id := range a.externalDurableRuns {
		knownActive[id] = struct{}{}
	}
	for _, record := range active {
		if _, known := knownActive[record.SessionID]; !known {
			blocked = true
		}
	}
	a.externalCleanupRecoveryBlocked = blocked
	a.externalCleanupMu.Unlock()
	return blocked, nil
}

// ConfirmExternalCleanupRecovery is the only product repair path for an
// identity-uncertain durable owner. confirmed must be true, the Launcher must
// prove OS absence, and exact journal completion must succeed before the claim,
// admission or global launch fence can be released. There is no force-clear.
func (a *App) ConfirmExternalCleanupRecovery(sessionID string, confirmed bool) (remote.ExternalCleanupRecoveryResult, error) {
	result := remote.ExternalCleanupRecoveryResult{SessionID: sessionID}
	a.externalCleanupMu.Lock()
	claim := a.externalCleanups[sessionID]
	a.externalCleanupMu.Unlock()
	if claim == nil || claim.recoveryReason == "" {
		a.recordExternalCleanupRecoveryAudit(remote.ExternalCleanupRecoveryAuditEvent{
			SessionID: sessionID,
			Outcome:   remote.ExternalCleanupRecoveryAuditNotFound,
		})
		return result, ErrExternalCleanupRecoveryNotFound
	}
	kind := claim.record.Kind
	if kind == 0 {
		kind = claim.reservation.Kind
	}
	audit := remote.ExternalCleanupRecoveryAuditEvent{
		SessionID: sessionID,
		Kind:      kind,
		Reason:    claim.recoveryReason,
	}
	if !confirmed {
		audit.Outcome = remote.ExternalCleanupRecoveryAuditConfirmationRequired
		a.recordExternalCleanupRecoveryAudit(audit)
		return result, ErrExternalCleanupConfirmationRequired
	}

	claim.completionMu.Lock()
	defer claim.completionMu.Unlock()
	a.externalCleanupMu.Lock()
	current := a.externalCleanups[sessionID]
	a.externalCleanupMu.Unlock()
	if current != claim {
		audit.Outcome = remote.ExternalCleanupRecoveryAuditNotFound
		a.recordExternalCleanupRecoveryAudit(audit)
		return result, ErrExternalCleanupRecoveryNotFound
	}
	if claim.lifecycle == nil {
		audit.Outcome = remote.ExternalCleanupRecoveryAuditPersistenceFailed
		a.recordExternalCleanupRecoveryAudit(audit)
		return result, ErrExternalCleanupRecheckUnavailable
	}
	if claim.lifecycle.IsRunning(sessionID) {
		audit.Outcome = remote.ExternalCleanupRecoveryAuditStillRunning
		a.recordExternalCleanupRecoveryAudit(audit)
		return result, ErrExternalCleanupStillRunning
	}
	a.externalCleanupMu.Lock()
	if a.externalCleanups[sessionID] == claim {
		claim.terminalObserved = true
	}
	a.externalCleanupMu.Unlock()

	var persistenceErr error
	if a.externalCleanupStore == nil {
		persistenceErr = errExternalCleanupStoreNotReady
	} else {
		switch {
		case claim.recordDurable && claim.record.ProcessIdentity != "":
			persistenceErr = a.externalCleanupStore.Complete(claim.record)
		case claim.reservation.SessionID != "":
			persistenceErr = a.externalCleanupStore.CompleteReservation(claim.reservation)
		default:
			persistenceErr = errExternalCleanupInvalidRecord
		}
	}
	if persistenceErr != nil {
		a.setExternalCleanupRecoveryBlocked(true)
		audit.Outcome = remote.ExternalCleanupRecoveryAuditPersistenceFailed
		a.recordExternalCleanupRecoveryAudit(audit)
		return result, fmt.Errorf("confirm external cleanup persistence: %w", persistenceErr)
	}

	a.externalCleanupMu.Lock()
	if a.externalCleanups[sessionID] != claim {
		a.externalCleanupMu.Unlock()
		audit.Outcome = remote.ExternalCleanupRecoveryAuditNotFound
		a.recordExternalCleanupRecoveryAudit(audit)
		return result, ErrExternalCleanupRecoveryNotFound
	}
	delete(a.externalCleanups, sessionID)
	close(claim.done)
	a.externalCleanupMu.Unlock()
	if a.sharedCoord != nil {
		a.sharedCoord.ReleaseLaunchAdmission(claim.admission)
	}
	result.Cleared = true
	blocked, reconcileErr := a.recomputeExternalCleanupRecoveryFence()
	result.FenceReleased = !blocked
	if reconcileErr != nil {
		audit.Outcome = remote.ExternalCleanupRecoveryAuditPersistenceFailed
		a.recordExternalCleanupRecoveryAudit(audit)
		return result, fmt.Errorf("recompute external cleanup recovery fence: %w", reconcileErr)
	}
	audit.Outcome = remote.ExternalCleanupRecoveryAuditCompleted
	audit.FenceReleased = result.FenceReleased
	a.recordExternalCleanupRecoveryAudit(audit)
	return result, nil
}

func (a *App) externalShutdownBudget() time.Duration {
	if a.externalShutdownCleanupBudget > 0 {
		return a.externalShutdownCleanupBudget
	}
	return defaultExternalShutdownCleanupBudget
}

func (a *App) snapshotExternalCleanupAbandonments(reason remote.ExternalCleanupAbandonmentReason) []remote.ExternalCleanupAbandonmentEvent {
	a.externalCleanupMu.Lock()
	attempt := a.externalOwnershipAttempt
	claims := make([]*externalCleanupClaim, 0, len(a.externalCleanups))
	for _, claim := range a.externalCleanups {
		claims = append(claims, claim)
	}
	durableRuns := make([]externalCleanupRecord, 0, len(a.externalDurableRuns))
	for _, record := range a.externalDurableRuns {
		durableRuns = append(durableRuns, record)
	}
	a.externalCleanupMu.Unlock()

	bySession := make(map[string]remote.ExternalCleanupAbandonmentEvent, len(claims)+len(durableRuns)+1)
	if attempt != nil {
		bySession[attempt.sessionID] = remote.ExternalCleanupAbandonmentEvent{
			SessionID:          attempt.sessionID,
			Kind:               attempt.kind,
			Reason:             remote.ExternalCleanupAbandonmentShutdownHandoff,
			DurableReservation: attempt.durableReservation,
		}
	}
	for _, claim := range claims {
		kind := claim.record.Kind
		if kind == 0 {
			kind = claim.reservation.Kind
		}
		bySession[claim.sessionID] = remote.ExternalCleanupAbandonmentEvent{
			SessionID:          claim.sessionID,
			Kind:               kind,
			Reason:             reason,
			DurableReservation: claim.recordDurable || claim.reservation.SessionID != "",
		}
	}
	for _, record := range durableRuns {
		bySession[record.SessionID] = remote.ExternalCleanupAbandonmentEvent{
			SessionID:          record.SessionID,
			Kind:               record.Kind,
			Reason:             reason,
			DurableReservation: true,
		}
	}
	ids := make([]string, 0, len(bySession))
	for id := range bySession {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]remote.ExternalCleanupAbandonmentEvent, 0, len(ids))
	for _, id := range ids {
		event := bySession[id]
		event.Version = remote.ExternalCleanupAbandonmentEventVersion
		event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
		out = append(out, event)
	}
	return out
}

func (a *App) requireExternalCleanupStore() error {
	if a.isExternalCleanupRecoveryBlocked() || a.externalCleanupStore == nil || !a.externalCleanupStore.IsReady() {
		return errExternalCleanupStoreNotReady
	}
	return nil
}

// reserveExternalProcessOwnership fsyncs a fixed-schema intent before OS Start.
// A later PID/identity registration consumes it atomically in journal replay;
// if that upgrade fails, the reservation remains durable and a fresh App must
// fail closed rather than infer that no process exists.
func (a *App) reserveExternalProcessOwnership(sessionID string, kind remote.SharedServiceKind) (externalCleanupReservation, error) {
	reservation := externalCleanupReservation{
		Version:    externalCleanupJournalVersion,
		SessionID:  sessionID,
		Kind:       kind,
		ReservedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if a.externalCleanupStore == nil {
		return reservation, errExternalCleanupStoreNotReady
	}
	if err := a.externalCleanupStore.Reserve(reservation); err != nil {
		return reservation, fmt.Errorf("reserve external process ownership: %w", err)
	}
	return reservation, nil
}

func (a *App) completeExternalProcessReservation(reservation externalCleanupReservation) error {
	if reservation.SessionID == "" || a.externalCleanupStore == nil {
		return nil
	}
	if err := a.externalCleanupStore.CompleteReservation(reservation); err != nil {
		a.setExternalCleanupRecoveryBlocked(true)
		return fmt.Errorf("complete external process reservation: %w", err)
	}
	return nil
}

// persistExternalProcessOwnership writes the OS identity before lease
// promotion. Thus both successful external runs and promotion-failure cleanup
// claims survive a concurrent graceful Shutdown/host boundary.
func (a *App) persistExternalProcessOwnership(
	sessionID string,
	pid int,
	kind remote.SharedServiceKind,
	lifecycle externalLauncherPort,
) (externalCleanupRecord, error) {
	identity, identityErr := lifecycle.CaptureProcessIdentity(sessionID)
	record := externalCleanupRecord{
		Version:         externalCleanupJournalVersion,
		SessionID:       sessionID,
		PID:             pid,
		ProcessIdentity: identity,
		Kind:            kind,
		RegisteredAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}
	var ownershipErr error
	if identityErr != nil {
		ownershipErr = fmt.Errorf("capture external process identity: %w", identityErr)
	}
	if a.externalCleanupStore == nil {
		ownershipErr = errors.Join(ownershipErr, errExternalCleanupStoreNotReady)
	} else if err := a.externalCleanupStore.Register(record); err != nil {
		ownershipErr = errors.Join(ownershipErr, fmt.Errorf("persist external process ownership: %w", err))
	}
	return record, ownershipErr
}

func (a *App) rememberExternalDurableRun(record externalCleanupRecord) {
	a.externalCleanupMu.Lock()
	if a.externalDurableRuns == nil {
		a.externalDurableRuns = make(map[string]externalCleanupRecord)
	}
	a.externalDurableRuns[record.SessionID] = record
	a.externalCleanupMu.Unlock()
}

// registerExternalCleanup transfers an already-started process (durable record,
// durable reservation, or transient no-Headroom PID) and any unpromoted
// admission into the exact in-memory reaper owner before Stop.
func (a *App) registerExternalCleanup(
	record externalCleanupRecord,
	reservation externalCleanupReservation,
	recordDurable bool,
	admission *remote.SharedLaunchAdmission,
	lifecycle externalLauncherPort,
) *externalCleanupClaim {
	claim := &externalCleanupClaim{
		sessionID:     record.SessionID,
		admission:     admission,
		lifecycle:     lifecycle,
		record:        record,
		reservation:   reservation,
		recordDurable: recordDurable,
		done:          make(chan struct{}),
	}
	a.externalCleanupMu.Lock()
	if a.externalCleanups == nil {
		a.externalCleanups = make(map[string]*externalCleanupClaim)
	}
	a.externalCleanups[record.SessionID] = claim
	a.externalCleanupMu.Unlock()
	return claim
}

// recoverExternalCleanups replays durable active records into a fresh
// coordinator + Launcher instance. Each identity-verified live process receives
// a new startup admission before its reaper starts; terminal or PID-reused
// records are completed without signalling the new process.
func (a *App) recoverExternalCleanups() error {
	if a.externalCleanupStore == nil {
		return nil
	}
	records, err := a.externalCleanupStore.LoadActive()
	if err != nil {
		a.setExternalCleanupRecoveryBlocked(true)
		return err
	}
	pending, err := a.externalCleanupStore.LoadPending()
	if err != nil {
		a.setExternalCleanupRecoveryBlocked(true)
		return err
	}
	// A reservation has no PID by design because it precedes OS Start. After an
	// App boundary its process existence is indeterminate, so retain a global
	// Headroom recovery fence until an operator/repair path resolves the journal.
	hardBlocked := len(pending) > 0
	var recoveryErr error
	if hardBlocked {
		recoveryErr = fmt.Errorf("recover external cleanup: %d unresolved pre-start reservation(s)", len(pending))
	}
	if len(records) == 0 {
		a.setExternalCleanupRecoveryBlocked(hardBlocked)
		return recoveryErr
	}
	if a.sharedCoord == nil {
		a.setExternalCleanupRecoveryBlocked(true)
		return errors.New("recover external cleanup: shared coordinator unavailable")
	}
	lifecycle := a.externalSessionLauncher()
	if lifecycle == nil {
		a.setExternalCleanupRecoveryBlocked(true)
		return errors.New("recover external cleanup: launcher unavailable")
	}

	for _, record := range records {
		admission, admissionErr := a.sharedCoord.AcquireLaunchAdmission(record.Kind)
		if admissionErr != nil {
			hardBlocked = true
			recoveryErr = errors.Join(recoveryErr, fmt.Errorf("recover %s admission: %w", record.SessionID, admissionErr))
			continue
		}
		running, adoptErr := lifecycle.RecoverProcess(record.SessionID, record.PID, record.ProcessIdentity)
		if !running {
			a.sharedCoord.ReleaseLaunchAdmission(admission)
			if completeErr := a.externalCleanupStore.Complete(record); completeErr != nil {
				recoveryErr = errors.Join(recoveryErr, fmt.Errorf("complete terminal recovery %s: %w", record.SessionID, completeErr))
			}
			if adoptErr != nil {
				recoveryErr = errors.Join(recoveryErr, fmt.Errorf("inspect recovered process %s: %w", record.SessionID, adoptErr))
			}
			continue
		}
		recoveryReason := remote.ExternalCleanupRecoveryReason("")
		if adoptErr != nil {
			// A live process whose identity could not be authoritatively adopted
			// keeps both its exact admission and the global Headroom recovery fence.
			// It is completed only through the explicit terminal-confirmation API.
			hardBlocked = true
			a.setExternalCleanupRecoveryBlocked(true)
			if errors.Is(adoptErr, launcher.ErrLegacyProcFSIdentity) {
				recoveryReason = remote.ExternalCleanupRecoveryLegacyIdentity
			} else {
				recoveryReason = remote.ExternalCleanupRecoveryIdentityInspection
			}
			recoveryErr = errors.Join(recoveryErr, fmt.Errorf("recover process %s: %w", record.SessionID, adoptErr))
		}
		claim := &externalCleanupClaim{
			sessionID:      record.SessionID,
			admission:      admission,
			lifecycle:      lifecycle,
			record:         record,
			recordDurable:  true,
			recoveryReason: recoveryReason,
			done:           make(chan struct{}),
		}
		a.externalCleanupMu.Lock()
		if a.externalCleanups == nil {
			a.externalCleanups = make(map[string]*externalCleanupClaim)
		}
		a.externalCleanups[record.SessionID] = claim
		a.externalCleanupMu.Unlock()
		a.startExternalCleanupReaper(claim)
	}
	a.setExternalCleanupRecoveryBlocked(hardBlocked)
	return recoveryErr
}

// compensateExternalCleanup performs the first synchronous Stop after
// ownership registration. A successful and observed terminal completes inline;
// every uncertain result transfers to the bounded-rate reaper.
func (a *App) compensateExternalCleanup(claim *externalCleanupClaim) error {
	if claim == nil || claim.lifecycle == nil {
		return nil
	}
	stopErr := claim.lifecycle.Stop(claim.sessionID)
	if stopErr == nil && !claim.lifecycle.IsRunning(claim.sessionID) {
		a.completeExternalCleanup(claim)
		return nil
	}
	a.startExternalCleanupReaper(claim)
	return stopErr
}

// startExternalCleanupReaper retries at the same bounded cadence as external
// terminal observation. It intentionally does not select on App ctx: ctx
// cancellation is a shutdown signal, not proof that the child is terminal.
func (a *App) startExternalCleanupReaper(claim *externalCleanupClaim) {
	if claim == nil || claim.lifecycle == nil {
		return
	}
	claim.reaperOnce.Do(func() {
		go func() {
			interval := a.externalSessionPollInterval()
			for {
				select {
				case <-claim.done:
					return
				default:
				}
				if !claim.lifecycle.IsRunning(claim.sessionID) {
					a.completeExternalCleanup(claim)
					return
				}
				timer := time.NewTimer(interval)
				select {
				case <-timer.C:
				case <-claim.done:
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					return
				}
				if !claim.lifecycle.IsRunning(claim.sessionID) {
					a.completeExternalCleanup(claim)
					return
				}
				_ = claim.lifecycle.Stop(claim.sessionID)
			}
		}()
	})
}

// completeExternalCleanup is exact-pointer and idempotent across reaper,
// explicit Stop, natural terminal, and shutdown StopAll races.
func (a *App) completeExternalCleanup(claim *externalCleanupClaim) {
	if claim == nil {
		return
	}
	claim.completionMu.Lock()
	defer claim.completionMu.Unlock()
	a.externalCleanupMu.Lock()
	current := a.externalCleanups[claim.sessionID]
	if current != claim {
		a.externalCleanupMu.Unlock()
		return
	}
	if claim.recoveryReason != "" {
		// Uncertain recovered identities require an explicit user confirmation.
		// Automatic reapers may prove terminal and expose CanConfirm, but never
		// remove the journal/admission or unlock the global recovery fence.
		claim.terminalObserved = true
		a.externalCleanupMu.Unlock()
		return
	}
	delete(a.externalCleanups, claim.sessionID)
	close(claim.done)
	a.externalCleanupMu.Unlock()
	// Terminal is already proven. Complete whichever durable authority exists:
	// the upgraded PID/identity record, or the pre-start reservation retained
	// after a failed upgrade. A write failure leaves conservative stale authority
	// and latches the recovery fence; it never fabricates successful cleanup.
	var persistenceErr error
	if a.externalCleanupStore != nil {
		switch {
		case claim.recordDurable && claim.record.ProcessIdentity != "":
			persistenceErr = a.externalCleanupStore.Complete(claim.record)
		case claim.reservation.SessionID != "":
			persistenceErr = a.externalCleanupStore.CompleteReservation(claim.reservation)
		}
	}
	if persistenceErr != nil {
		a.setExternalCleanupRecoveryBlocked(true)
		if a.Log != nil {
			a.Log.Warn("session", "持久清理登记完成写入失败", fmt.Sprintf("id=%s err=%v", claim.sessionID, persistenceErr))
		}
	}
	if a.sharedCoord != nil {
		a.sharedCoord.ReleaseLaunchAdmission(claim.admission)
	}
}

func (a *App) completeExternalDurableRun(sessionID string) {
	a.externalCleanupMu.Lock()
	record, ok := a.externalDurableRuns[sessionID]
	if ok {
		delete(a.externalDurableRuns, sessionID)
	}
	a.externalCleanupMu.Unlock()
	if !ok || a.externalCleanupStore == nil {
		return
	}
	if err := a.externalCleanupStore.Complete(record); err != nil && a.Log != nil {
		a.Log.Warn("session", "持久外部进程登记完成写入失败", fmt.Sprintf("id=%s err=%v", sessionID, err))
	}
}

func (a *App) completeExternalCleanupForSession(sessionID string) {
	a.externalCleanupMu.Lock()
	claim := a.externalCleanups[sessionID]
	a.externalCleanupMu.Unlock()
	if claim == nil || (claim.lifecycle != nil && claim.lifecycle.IsRunning(sessionID)) {
		return
	}
	a.completeExternalCleanup(claim)
}

// completeTerminatedExternalCleanups gives shutdown StopAll an immediate exact
// handoff receipt without clearing claims whose Launcher process remains live.
func (a *App) completeTerminatedExternalCleanups() {
	a.externalCleanupMu.Lock()
	claims := make([]*externalCleanupClaim, 0, len(a.externalCleanups))
	for _, claim := range a.externalCleanups {
		claims = append(claims, claim)
	}
	durableRuns := make([]externalCleanupRecord, 0, len(a.externalDurableRuns))
	for _, record := range a.externalDurableRuns {
		durableRuns = append(durableRuns, record)
	}
	a.externalCleanupMu.Unlock()
	for _, claim := range claims {
		if claim.lifecycle == nil || !claim.lifecycle.IsRunning(claim.sessionID) {
			a.completeExternalCleanup(claim)
		}
	}
	lifecycle := a.externalSessionLauncher()
	if lifecycle == nil {
		return
	}
	for _, record := range durableRuns {
		if !lifecycle.IsRunning(record.SessionID) {
			a.completeExternalDurableRun(record.SessionID)
		}
	}
}

// closeSharedLeasesForShutdown is the terminal App/coordinator fence. Holding
// sharedLeaseMu through ClearAll makes external promotion vs shutdown atomic:
// promotion is either registered and released here, or observes the closed
// coordinator and transfers its just-started process to externalCleanups. That
// registry is deliberately not cleared here: only terminal receipt may remove
// a promotion-failure cleanup owner.
func (a *App) closeSharedLeasesForShutdown() {
	a.sharedLeaseMu.Lock()
	defer a.sharedLeaseMu.Unlock()
	if a.sharedCoord != nil {
		ctx := context.Background()
		for _, leases := range a.sharedLeases {
			for _, lease := range leases {
				_ = a.sharedCoord.ReleaseExact(ctx, lease)
			}
		}
		a.sharedCoord.ClearAll()
	}
	a.sharedLeases = make(map[string][]*remote.SharedDependencyLease)
}

// releaseSharedLeases releases all shared-service leases for a session (M-006).
// Called on run terminal / stop / remove. Idempotent.
func (a *App) releaseSharedLeases(sessionID string) {
	a.sharedLeaseMu.Lock()
	leases := a.sharedLeases[sessionID]
	delete(a.sharedLeases, sessionID)
	a.sharedLeaseMu.Unlock()
	a.completeExternalDurableRun(sessionID)
	if a.sharedCoord == nil {
		return
	}
	ctx := context.Background()
	for _, lease := range leases {
		_ = a.sharedCoord.ReleaseExact(ctx, lease)
	}
}

// ---------------------------------------------------------------------------
// Proxy facade
// ---------------------------------------------------------------------------

// ProxyGetRules returns the current injection rules (read; no lease guard).
func (a *App) ProxyGetRules() []proxy.InjectionRule { return a.Proxy.GetRules() }

// ProxySetRules replaces all injection rules (mutation; lease-guarded).
func (a *App) ProxySetRules(rules []proxy.InjectionRule) error {
	if err := a.checkSharedLease(remote.SharedServiceClaudeProxy, remote.MutationReconfigure); err != nil {
		return err
	}
	a.Proxy.SetRules(rules)
	return nil
}

// ProxyAddRule adds a single injection rule (mutation; lease-guarded).
func (a *App) ProxyAddRule(rule proxy.InjectionRule) error {
	if err := a.checkSharedLease(remote.SharedServiceClaudeProxy, remote.MutationReconfigure); err != nil {
		return err
	}
	return a.Proxy.AddRule(rule)
}

// ProxyUpdateRule updates an injection rule (mutation; lease-guarded).
func (a *App) ProxyUpdateRule(rule proxy.InjectionRule) error {
	if err := a.checkSharedLease(remote.SharedServiceClaudeProxy, remote.MutationReconfigure); err != nil {
		return err
	}
	return a.Proxy.UpdateRule(rule)
}

// ProxyDeleteRule deletes an injection rule (mutation; lease-guarded).
func (a *App) ProxyDeleteRule(id string) error {
	if err := a.checkSharedLease(remote.SharedServiceClaudeProxy, remote.MutationReconfigure); err != nil {
		return err
	}
	return a.Proxy.DeleteRule(id)
}

// ProxyLoadRules loads rules from the config dir (read; no lease guard).
func (a *App) ProxyLoadRules(configDir string) error { return a.Proxy.LoadRules(configDir) }

// ProxySaveRules persists rules to the config dir (mutation; lease-guarded).
func (a *App) ProxySaveRules(configDir string) error {
	if err := a.checkSharedLease(remote.SharedServiceClaudeProxy, remote.MutationReconfigure); err != nil {
		return err
	}
	return a.Proxy.SaveRules(configDir)
}

// ProxyStart starts the injection proxy (mutation; lease-guarded).
func (a *App) ProxyStart(port int, backendURL string) error {
	if err := a.checkSharedLease(remote.SharedServiceClaudeProxy, remote.MutationStartDifferentConfig); err != nil {
		return err
	}
	return a.Proxy.Start(port, backendURL)
}

// ProxyStop stops the injection proxy (mutation; lease-guarded).
func (a *App) ProxyStop() error {
	if err := a.checkSharedLease(remote.SharedServiceClaudeProxy, remote.MutationStop); err != nil {
		return err
	}
	return a.Proxy.Stop()
}

// ProxyIsRunning reports whether the proxy is running (read).
func (a *App) ProxyIsRunning() bool { return a.Proxy.IsRunning() }

// ProxyGetStatus returns the proxy status (read).
func (a *App) ProxyGetStatus() proxy.ProxyStatus { return a.Proxy.GetStatus() }

// ProxyGetLogs returns recent injection logs (read).
func (a *App) ProxyGetLogs() []proxy.InjectionLog { return a.Proxy.GetLogs() }

// ProxyGetPort returns the proxy port (read).
func (a *App) ProxyGetPort() int { return a.Proxy.GetPort() }

// ---------------------------------------------------------------------------
// Headroom facade
// ---------------------------------------------------------------------------

// HeadroomStart starts the Claude headroom proxy (mutation; lease-guarded).
func (a *App) HeadroomStart(realBackendURL string) error {
	admission, err := a.acquireSharedMutation(remote.SharedServiceClaudeHeadroom, remote.MutationStartDifferentConfig)
	if err != nil {
		return err
	}
	defer a.sharedCoord.ReleaseMutationAdmission(admission)
	return a.Headroom.Start(realBackendURL)
}

// HeadroomStop stops the Claude headroom proxy (mutation; lease-guarded).
func (a *App) HeadroomStop() error {
	admission, err := a.acquireSharedMutation(remote.SharedServiceClaudeHeadroom, remote.MutationStop)
	if err != nil {
		return err
	}
	defer a.sharedCoord.ReleaseMutationAdmission(admission)
	return a.Headroom.Stop()
}

// HeadroomIsRunning reports whether headroom is running (read).
func (a *App) HeadroomIsRunning() bool { return a.Headroom.IsRunning() }

// HeadroomGetStatus returns the headroom status (read).
func (a *App) HeadroomGetStatus() headroom.HeadroomStatus { return a.Headroom.GetStatus() }

// HeadroomGetPort returns the headroom port (read).
func (a *App) HeadroomGetPort() int { return a.Headroom.GetPort() }
