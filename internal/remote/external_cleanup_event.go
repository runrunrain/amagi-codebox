package remote

// ExternalCleanupAbandonmentEvent is the typed, privacy-minimal receipt emitted
// when a graceful host fence abandons a launch or cannot prove an external
// process terminal within its bounded lifecycle window. It is deliberately not
// a wire control event: the desktop host records/logs it after the control
// authority fence has closed. DurableReservation says whether a pre-start
// fsynced reservation remains as cross-App fail-closed authority.
type ExternalCleanupAbandonmentEvent struct {
	Version            uint8                            `json:"version"`
	SessionID          string                           `json:"sessionId"`
	Kind               SharedServiceKind                `json:"kind"`
	Reason             ExternalCleanupAbandonmentReason `json:"reason"`
	DurableReservation bool                             `json:"durableReservation"`
	OccurredAt         string                           `json:"occurredAt"`
}

// ExternalCleanupAbandonmentReason is a closed set so tests, logs and support
// tooling can distinguish a failed durability upgrade from a bounded shutdown
// timeout without parsing prose.
type ExternalCleanupAbandonmentReason string

const (
	ExternalCleanupAbandonmentEventVersion        uint8                            = 1
	ExternalCleanupAbandonmentDurabilityHandoff   ExternalCleanupAbandonmentReason = "durability_handoff_failed"
	ExternalCleanupAbandonmentShutdownHandoff     ExternalCleanupAbandonmentReason = "shutdown_handoff_timeout"
	ExternalCleanupAbandonmentShutdownUnconfirmed ExternalCleanupAbandonmentReason = "shutdown_terminal_unconfirmed"
	ExternalCleanupAbandonmentShutdownStopTimeout ExternalCleanupAbandonmentReason = "shutdown_stop_all_timeout"
	ExternalCleanupAbandonmentShutdownStartFenced ExternalCleanupAbandonmentReason = "shutdown_start_fenced"
	ExternalCleanupAbandonmentPostCommitShutdown  ExternalCleanupAbandonmentReason = "shutdown_post_commit_start"
)

// ExternalCleanupShutdownReport is the exact bounded external-process phase
// result retained by App.Shutdown. Unrecovered is never omitted or converted to
// success merely because a durable reservation will fail-close the next App.
type ExternalCleanupShutdownReport struct {
	BudgetMillis    int64                             `json:"budgetMillis"`
	ElapsedMillis   int64                             `json:"elapsedMillis"`
	StopAllTimedOut bool                              `json:"stopAllTimedOut"`
	HandoffTimedOut bool                              `json:"handoffTimedOut"`
	Unrecovered     []ExternalCleanupAbandonmentEvent `json:"unrecovered"`
}

// ExternalCleanupRecoveryReason is the privacy-minimal reason an adopted
// durable process requires explicit user confirmation after terminal proof.
type ExternalCleanupRecoveryReason string

const (
	ExternalCleanupRecoveryLegacyIdentity     ExternalCleanupRecoveryReason = "legacy_process_identity"
	ExternalCleanupRecoveryIdentityInspection ExternalCleanupRecoveryReason = "identity_inspection_uncertain"
)

// ExternalCleanupRecoveryState intentionally exposes no PID, argv, env, path,
// provider or output. The user only needs to know whether the old external
// terminal is still live or can now be safely confirmed as gone.
type ExternalCleanupRecoveryState string

const (
	ExternalCleanupRecoveryRunning              ExternalCleanupRecoveryState = "running"
	ExternalCleanupRecoveryAwaitingConfirmation ExternalCleanupRecoveryState = "awaiting_confirmation"
)

type ExternalCleanupRecoveryItem struct {
	SessionID  string                        `json:"sessionId"`
	Kind       SharedServiceKind             `json:"kind"`
	Reason     ExternalCleanupRecoveryReason `json:"reason"`
	State      ExternalCleanupRecoveryState  `json:"state"`
	CanConfirm bool                          `json:"canConfirm"`
}

type ExternalCleanupRecoveryStatus struct {
	Version uint8                         `json:"version"`
	Blocked bool                          `json:"blocked"`
	Items   []ExternalCleanupRecoveryItem `json:"items"`
}

type ExternalCleanupRecoveryResult struct {
	SessionID     string `json:"sessionId"`
	Cleared       bool   `json:"cleared"`
	FenceReleased bool   `json:"fenceReleased"`
}

type ExternalCleanupRecoveryAuditOutcome string

const (
	ExternalCleanupRecoveryAuditConfirmationRequired ExternalCleanupRecoveryAuditOutcome = "confirmation_required"
	ExternalCleanupRecoveryAuditStillRunning         ExternalCleanupRecoveryAuditOutcome = "still_running"
	ExternalCleanupRecoveryAuditNotFound             ExternalCleanupRecoveryAuditOutcome = "not_found"
	ExternalCleanupRecoveryAuditPersistenceFailed    ExternalCleanupRecoveryAuditOutcome = "persistence_failed"
	ExternalCleanupRecoveryAuditCompleted            ExternalCleanupRecoveryAuditOutcome = "completed"
)

// ExternalCleanupRecoveryAuditEvent records each explicit repair decision
// without process details or credentials. It is host-local, not a wire control
// event, and is suitable for support logs and deterministic tests.
type ExternalCleanupRecoveryAuditEvent struct {
	Version       uint8                               `json:"version"`
	SessionID     string                              `json:"sessionId"`
	Kind          SharedServiceKind                   `json:"kind"`
	Reason        ExternalCleanupRecoveryReason       `json:"reason"`
	Outcome       ExternalCleanupRecoveryAuditOutcome `json:"outcome"`
	FenceReleased bool                                `json:"fenceReleased"`
	OccurredAt    string                              `json:"occurredAt"`
}

const ExternalCleanupRecoveryContractVersion uint8 = 1
