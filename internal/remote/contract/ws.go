package contract

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Client frame and server event type discriminant constants. These are the
// sole legal values for the top-level "type" field.
const (
	ClientFrameTypeAttach   = "attach"
	ClientFrameTypeInput    = "input"
	ClientFrameTypeResize   = "resize"
	ClientFrameTypeBackfill = "backfill"
	ClientFrameTypePing     = "ping"

	ServerEventTypeSessionAttached = "session.attached"
	ServerEventTypeOutput          = "output"
	ServerEventTypeBackfillResult  = "backfill.result"
	ServerEventTypeSessionState    = "session.state"
	ServerEventTypeControlState    = "control.state"
	ServerEventTypeAuthRevoked     = "auth.revoked"
	ServerEventTypeError           = "error"
	ServerEventTypeInputAck        = "input.ack"
)

// KnownClientFrameTypes is the complete set of 5 v1 client frame types. An
// unknown client frame type is bad_request (design §6.4); clients cannot make
// an old server execute unknown write operations.
var KnownClientFrameTypes = []string{
	ClientFrameTypeAttach,
	ClientFrameTypeInput,
	ClientFrameTypeResize,
	ClientFrameTypeBackfill,
	ClientFrameTypePing,
}

// KnownServerEventTypes is the complete set of 8 v1 server event type
// categories. Unknown server types are forward-compatible: clients keep the
// connection, ignore business updates and record a sanitized diagnostic.
// input.ack (CG-03, contract-addendum-cg03.md §3) is additive: an old client
// that does not know it normalizes the event to a sanitized Unknown and keeps
// the connection (it never produced canonical IDs, so it never waits on it).
var KnownServerEventTypes = []string{
	ServerEventTypeSessionAttached,
	ServerEventTypeOutput,
	ServerEventTypeBackfillResult,
	ServerEventTypeSessionState,
	ServerEventTypeControlState,
	ServerEventTypeAuthRevoked,
	ServerEventTypeError,
	ServerEventTypeInputAck,
}

// ---------------------------------------------------------------------------
// auth.revoked reason manifest + close directive (CG-01 contract addendum).
//
// The reason is a closed one-value enum for server PRODUCERS; the close code
// is a transport directive, NOT an 8th server event type or a 13th error code.
// Unknown/missing/null/malformed reasons produce no bytes (producer strict).
// Consumers MUST treat any auth.revoked event (known/unknown/malformed reason)
// as force-unauthorized: the client revokes on the event TYPE, never on the
// reason value. 1008 is a generic policy code — other policy closes may also
// use 1008, so a client MUST NOT infer device-revoke from the close code alone.
// ---------------------------------------------------------------------------

// AuthRevokedReason is the closed enum of canonical auth.revoked reason values.
// It is opaque: clients MUST NOT switch client behavior on its value (the
// event type already forces unauthorized); it exists for producer narrowing
// and forward-safe strict decode.
type AuthRevokedReason string

// AuthRevokedReasonDeviceRevoked is the sole v1 canonical reason: the device's
// persistent authorization was revoked.
const AuthRevokedReasonDeviceRevoked AuthRevokedReason = "device_revoked"

// KnownAuthRevokedReasons is the complete set of v1 canonical auth.revoked
// reasons (independent of KnownServerEventTypes/KnownErrorCodes). Server
// producers MUST construct Reason only from this set; ValidateServerEvent and
// DecodeKnownServerEvent reject any other value with no bytes produced.
var KnownAuthRevokedReasons = []AuthRevokedReason{
	AuthRevokedReasonDeviceRevoked,
}

// AuthRevokedCloseCode is the WS close code that follows a canonical
// auth.revoked event (RFC 6455 1008 Policy Violation). It lives in the contract
// package without importing any WebSocket implementation: the future producer
// hands it to the existing close API. This constant does not change the frozen
// event/error/type counts.
const AuthRevokedCloseCode = 1008

// ---------------------------------------------------------------------------
// CG-03 input confirmation capability (contract-addendum-cg03.md §3).
//
// input.ack is an additive server event: it confirms a logical input MessageID
// was committed (exactly-once raw PTY write or a duplicate of an already-
// committed key). It carries no seq/status/time/data/details — its sole meaning
// is "the (DeviceID, MessageID) key is committed". It is never broadcast, never
// enters replay history/backfill, and is delivered only to the requesting
// connection's sole outbound writer. A malformed ACK is sanitized to Unknown +
// force-read-only (no settlement, no raw ID retained).
//
// The capability is negotiated on session.attached via an optional inputAckMode:
// absent = capability unavailable (read-only for new clients); present = the
// sole canonical value session-window-v1. A new server sets it; an old server
// omits it. A new client MUST NOT send input when the mode is absent/unknown.
// ---------------------------------------------------------------------------

// InputAckMode is the optional session.attached capability marker (CG-03). The
// pointer carries optionality: nil = absent (capability unavailable).
type InputAckMode string

// InputAckModeSessionWindowV1 is the sole v1 input-ack capability value: the
// per-session, bounded, non-evicting input ledger with ACK confirmation.
const InputAckModeSessionWindowV1 InputAckMode = "session-window-v1"

// IsCanonicalMessageID reports whether id is the CG-03 canonical producer
// format: "msg-v1-" + 32 lowercase hex (39 ASCII bytes, 128 random bits). It is
// the opt-in discriminator for the session input ledger + ACK path; any other
// non-empty opaque ID keeps the legacy per-connection dedupe + silent-success
// path and MUST NOT be suppressed across connections. This is a pure byte
// classifier (no regex/imports) and is the exact canonical classifier used by
// the server producer to route canonical vs legacy IDs.
func IsCanonicalMessageID(id MessageID) bool {
	const prefix = "msg-v1-"
	s := string(id)
	if len(s) != len(prefix)+32 {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if s[i] != prefix[i] {
			return false
		}
	}
	for i := len(prefix); i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// IsCanonicalRequestID reports whether rid is the CG-03 canonical attempt ID
// format: "req-v1-" + 32 lowercase hex (39 ASCII bytes). Each wire attempt of a
// canonical input appends a fresh canonical RequestID to the entry's all-attempt
// set before sending; the ACK matches the entry by MessageID OR any all-attempt
// RequestID.
func IsCanonicalRequestID(rid RequestID) bool {
	const prefix = "req-v1-"
	s := string(rid)
	if len(s) != len(prefix)+32 {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if s[i] != prefix[i] {
			return false
		}
	}
	for i := len(prefix); i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Snapshot layers (design §8.3 session.attached)
// ---------------------------------------------------------------------------

// ConnectionSnapshot at attach time: state is always "connected".
type ConnectionSnapshot struct {
	State string `json:"state"`
}

// AuthSnapshot at attach time: state is always "authorized".
type AuthSnapshot struct {
	State string `json:"state"`
}

// SessionSnapshot carries the session lifecycle state.
type SessionSnapshot struct {
	State SessionState `json:"state"`
}

// HistorySnapshot carries the history/window state. Gap is REQUIRED when State
// is "gap" (carrying the lost replay range) and MUST be omitted for
// continuous/backfilled (addendum §1.2). The pointer carries the conditional;
// the production validator enforces all combinations.
type HistorySnapshot struct {
	State HistoryState `json:"state"`
	Gap   *GapRange    `json:"gap,omitempty"`
}

// GapRange marks an unavailable closed replay range. code is always
// ErrorCodeHistoryGap.
type GapRange struct {
	Code    ErrorCode `json:"code"`
	FromSeq Seq       `json:"fromSeq"`
	ToSeq   Seq       `json:"toSeq"`
}

// FiveLayerSnapshot is the full state snapshot returned by session.attached.
// All five layers are required and never null.
type FiveLayerSnapshot struct {
	Connection ConnectionSnapshot `json:"connection"`
	Auth       AuthSnapshot       `json:"auth"`
	Session    SessionSnapshot    `json:"session"`
	Control    ControlSnapshot    `json:"control"`
	History    HistorySnapshot    `json:"history"`
}

// ---------------------------------------------------------------------------
// Client frames — every frame has a required, non-null top-level requestId.
// ---------------------------------------------------------------------------

// ClientFrame is the marker interface for the 5 v1 client frame variants.
type ClientFrame interface {
	isClientFrame()
}

// AttachFrame selects a session. It MUST be the first business frame on a
// connection. apiVersion is required and echoed by session.attached. lastSeq
// is optional: omitted = first attach, 0 = client has no replay frame.
type AttachFrame struct {
	Type       string     `json:"type"`
	RequestID  RequestID  `json:"requestId"`
	APIVersion APIVersion `json:"apiVersion"`
	SessionID  SessionID  `json:"sessionId"`
	LastSeq    *Seq       `json:"lastSeq,omitempty"`
}

func (AttachFrame) isClientFrame() {}

// InputFrame writes to the PTY. Only the current controller may send it; an
// observer gets a correlated control.forbidden error. id is the per-connection
// idempotency key; a repeated id is silently dropped and NOT re-written. There
// is no JSON success ACK. data is non-empty RFC4648 Base64.
type InputFrame struct {
	Type      string    `json:"type"`
	RequestID RequestID `json:"requestId"`
	ID        MessageID `json:"id"`
	Data      string    `json:"data"`
}

func (InputFrame) isClientFrame() {}

// ResizeFrame changes PTY dimensions. cols/rows are positive integers; only
// the current controller's resize takes effect. No JSON ACK.
type ResizeFrame struct {
	Type      string    `json:"type"`
	RequestID RequestID `json:"requestId"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
}

func (ResizeFrame) isClientFrame() {}

// BackfillFrame requests a closed replay range [fromSeq, toSeq] with
// 1 <= fromSeq <= toSeq. The server replies with one correlated backfill.result
// (frames or gap variant), never both.
type BackfillFrame struct {
	Type      string    `json:"type"`
	RequestID RequestID `json:"requestId"`
	FromSeq   Seq       `json:"fromSeq"`
	ToSeq     Seq       `json:"toSeq"`
}

func (BackfillFrame) isClientFrame() {}

// PingFrame refreshes application-layer liveness only. It has no business
// payload and no JSON ACK, and cannot substitute for attach/auth.
type PingFrame struct {
	Type      string    `json:"type"`
	RequestID RequestID `json:"requestId"`
}

func (PingFrame) isClientFrame() {}

// ---------------------------------------------------------------------------
// Server events — 7 wire categories. backfill.result and session.state each
// have two concrete variants (frames/gap; normal/restart-boundary).
// ---------------------------------------------------------------------------

// KnownServerEvent is the marker interface for the 7 frozen server event
// categories. Unknown server types are normalized by clients to an
// UnknownServerEvent-equivalent and MUST NOT terminate the stream.
type KnownServerEvent interface {
	isKnownServerEvent()
}

// ReplayFrame is a server event that occupies a seq position in replay history
// (output and the restart-boundary variant of session.state). Normal
// session.state broadcasts do NOT occupy a seq and are not ReplayFrames.
type ReplayFrame interface {
	isReplayFrame()
	ReplaySeq() Seq
}

// SessionAttachedEvent is the reply to attach. requestId MUST equal the attach
// requestId. history is the retained replay frames with seq > lastSeq (or all
// retained when lastSeq omitted), ascending by seq, empty as []. earliestSeq
// and latestSeq are required even when 0.
type SessionAttachedEvent struct {
	Type         string            `json:"type"`
	RequestID    RequestID         `json:"requestId"`
	APIVersion   APIVersion        `json:"apiVersion"`
	SessionID    SessionID         `json:"sessionId"`
	History      []ReplayFrame     `json:"history"`
	EarliestSeq  Seq               `json:"earliestSeq"`
	LatestSeq    Seq               `json:"latestSeq"`
	Snapshot     FiveLayerSnapshot `json:"snapshot"`
	InputAckMode *InputAckMode     `json:"inputAckMode,omitempty"`
}

func (SessionAttachedEvent) isKnownServerEvent() {}

// OutputEvent is a replayable PTY output frame. chunk is RFC4648 Base64 of the
// raw bytes. structuredExpected is an optional parse hint; it does NOT
// guarantee a subsequent structured event (design TD-10). v1 does not send the
// legacy "structured-part".
type OutputEvent struct {
	Type               string    `json:"type"`
	SessionID          SessionID `json:"sessionId"`
	Seq                Seq       `json:"seq"`
	Chunk              string    `json:"chunk"`
	StructuredExpected *bool     `json:"structuredExpected,omitempty"`
}

func (OutputEvent) isKnownServerEvent() {}
func (OutputEvent) isReplayFrame()      {}
func (e OutputEvent) ReplaySeq() Seq    { return e.Seq }

// BackfillFramesResultEvent is the frames variant of backfill.result: the
// requested range was fully retained. frames is non-empty and ascending.
type BackfillFramesResultEvent struct {
	Type        string        `json:"type"`
	RequestID   RequestID     `json:"requestId"`
	SessionID   SessionID     `json:"sessionId"`
	FromSeq     Seq           `json:"fromSeq"`
	ToSeq       Seq           `json:"toSeq"`
	EarliestSeq Seq           `json:"earliestSeq"`
	LatestSeq   Seq           `json:"latestSeq"`
	Frames      []ReplayFrame `json:"frames"`
}

func (BackfillFramesResultEvent) isKnownServerEvent() {}

// BackfillGapResultEvent is the gap variant of backfill.result: the requested
// range is unavailable. gap is a normal, displayable result and does NOT replace
// a connection drop. Exactly one of frames/gap is present. v1 has NO partial-gap
// representation (design §4.3.6: "fully retained ⇒ frames variant; start before
// earliest ⇒ gap variant (no mixing)"): the gap MUST cover the FULL requested
// range [FromSeq, ToSeq] (Gap.FromSeq == FromSeq && Gap.ToSeq == ToSeq). The
// server producer emits the whole requested range as the gap; the validator
// enforces it so a client can safely advance its frontier across the whole
// range without crossing positions that were never held nor adjudicated.
type BackfillGapResultEvent struct {
	Type        string    `json:"type"`
	RequestID   RequestID `json:"requestId"`
	SessionID   SessionID `json:"sessionId"`
	FromSeq     Seq       `json:"fromSeq"`
	ToSeq       Seq       `json:"toSeq"`
	EarliestSeq Seq       `json:"earliestSeq"`
	LatestSeq   Seq       `json:"latestSeq"`
	Gap         GapRange  `json:"gap"`
}

func (BackfillGapResultEvent) isKnownServerEvent() {}

// BackfillResultEvent is the frames|gap discriminated union. Exactly one
// variant applies; the other's field MUST be omitted.
type BackfillResultEvent interface {
	isBackfillResult()
}

func (BackfillFramesResultEvent) isBackfillResult() {}
func (BackfillGapResultEvent) isBackfillResult()    {}

// SessionStateEvent is the normal session lifecycle broadcast. It does NOT
// occupy a seq and is not replayable; seq/restartBoundary MUST be omitted.
type SessionStateEvent struct {
	Type       string       `json:"type"`
	SessionID  SessionID    `json:"sessionId"`
	State      SessionState `json:"state"`
	OccurredAt string       `json:"occurredAt"`
}

func (SessionStateEvent) isKnownServerEvent() {}

// SessionRestartBoundaryEvent is the replayable restart-boundary variant of
// session.state. restartBoundary is always true (required, no omitempty) and
// seq is required; it occupies a seq position in replay history. There is no
// separate "session.restartBoundary" wire type (design C-06).
type SessionRestartBoundaryEvent struct {
	Type            string       `json:"type"`
	SessionID       SessionID    `json:"sessionId"`
	State           SessionState `json:"state"`
	RestartBoundary bool         `json:"restartBoundary"`
	Seq             Seq          `json:"seq"`
	OccurredAt      string       `json:"occurredAt"`
}

func (SessionRestartBoundaryEvent) isKnownServerEvent() {}
func (SessionRestartBoundaryEvent) isReplayFrame()      {}
func (e SessionRestartBoundaryEvent) ReplaySeq() Seq    { return e.Seq }

// ControlStateEvent projects the control state. reason is an opaque action
// reason (not user copy). deviceName is REQUIRED only when state == other and
// MUST be omitted otherwise; deviceId/credential are never sent.
type ControlStateEvent struct {
	Type       string       `json:"type"`
	SessionID  SessionID    `json:"sessionId"`
	State      ControlState `json:"state"`
	DeviceName *string      `json:"deviceName,omitempty"`
	Reason     string       `json:"reason"`
	OccurredAt string       `json:"occurredAt"`
}

func (ControlStateEvent) isKnownServerEvent() {}

// AuthRevokedEvent precedes a close 1008 (AuthRevokedCloseCode). Reason is the
// closed AuthRevokedReason enum (CG-01): producers cannot assign a free string
// without a compile-time cast, and ValidateServerEvent rejects non-members. The
// client returns to unauthorized state but does NOT auto-clear local drafts
// (design E-03/E-04).
type AuthRevokedEvent struct {
	Type       string            `json:"type"`
	Reason     AuthRevokedReason `json:"reason"`
	OccurredAt string            `json:"occurredAt"`
}

func (AuthRevokedEvent) isKnownServerEvent() {}

// ErrorEvent is the WS error event. requestId is conditional: required and
// echoed when the error derives from a parseable command, omitted otherwise.
// sessionId is optional. code/layer/message/actionHint are required.
type ErrorEvent struct {
	Type       string     `json:"type"`
	RequestID  RequestID  `json:"requestId,omitempty"`
	SessionID  SessionID  `json:"sessionId,omitempty"`
	Code       ErrorCode  `json:"code"`
	Layer      ErrorLayer `json:"layer"`
	Message    string     `json:"message"`
	ActionHint ActionHint `json:"actionHint"`
	Details    Details    `json:"details,omitempty"`
}

func (ErrorEvent) isKnownServerEvent() {}

// InputAckEvent (CG-03) confirms a canonical input MessageID was committed by
// the per-session ledger: exactly-once raw PTY write, or a duplicate of an
// already-committed (DeviceID, MessageID) key (re-ACK). It carries no
// seq/status/time/data/details. requestId equals the triggering wire attempt;
// id is the stable canonical MessageID key. It is not a ReplayFrame (no seq),
// is never broadcast, and never enters history/backfill.
type InputAckEvent struct {
	Type      string    `json:"type"`
	RequestID RequestID `json:"requestId"`
	SessionID SessionID `json:"sessionId"`
	ID        MessageID `json:"id"`
}

func (InputAckEvent) isKnownServerEvent() {}

// ===========================================================================
// Production validators (pure). Addendum §5.3.
// ===========================================================================

func validateGapRange(g GapRange) error {
	if g.Code != ErrorCodeHistoryGap {
		return fmt.Errorf("contract: GapRange.Code must be %q", ErrorCodeHistoryGap)
	}
	if err := validateReplaySeq(g.FromSeq); err != nil {
		return err
	}
	if g.ToSeq < g.FromSeq {
		return errors.New("contract: GapRange.ToSeq must be >= FromSeq")
	}
	return validateSeqRange(g.ToSeq)
}

func validateHistorySnapshot(h HistorySnapshot) error {
	switch h.State {
	case HistoryStateContinuous, HistoryStateBackfilled:
		if h.Gap != nil {
			return fmt.Errorf("contract: HistorySnapshot.Gap must be omitted for state %q", h.State)
		}
	case HistoryStateGap:
		if h.Gap == nil {
			return errors.New("contract: HistorySnapshot.Gap required for state gap")
		}
		if err := validateGapRange(*h.Gap); err != nil {
			return err
		}
	default:
		return fmt.Errorf("contract: HistorySnapshot.State %q is not a known history state", h.State)
	}
	return nil
}

func validateConnectionSnapshot(c ConnectionSnapshot) error {
	if c.State != AttachedConnectionState {
		return fmt.Errorf("contract: ConnectionSnapshot.State must be %q at attach", AttachedConnectionState)
	}
	return nil
}

func validateAuthSnapshot(a AuthSnapshot) error {
	if a.State != AttachedAuthState {
		return fmt.Errorf("contract: AuthSnapshot.State must be %q at attach", AttachedAuthState)
	}
	return nil
}

func validateSessionSnapshot(s SessionSnapshot) error {
	if !isKnownSessionState(s.State) {
		return fmt.Errorf("contract: SessionSnapshot.State %q is not a known session state", s.State)
	}
	return nil
}

func isKnownControlState(s ControlState) bool {
	for _, k := range KnownControlStates {
		if k == s {
			return true
		}
	}
	return false
}

// isKnownAuthRevokedReason reports whether r is a canonical v1 auth.revoked
// reason. An empty or future/non-canonical value returns false, so the
// validator rejects it (CG-01 closed producer enum).
func isKnownAuthRevokedReason(r AuthRevokedReason) bool {
	for _, k := range KnownAuthRevokedReasons {
		if k == r {
			return true
		}
	}
	return false
}

func validateFiveLayerSnapshot(s FiveLayerSnapshot) error {
	if err := validateConnectionSnapshot(s.Connection); err != nil {
		return err
	}
	if err := validateAuthSnapshot(s.Auth); err != nil {
		return err
	}
	if err := validateSessionSnapshot(s.Session); err != nil {
		return err
	}
	if err := validateControlSnapshot(s.Control); err != nil {
		return err
	}
	return validateHistorySnapshot(s.History)
}

func validateOutputEvent(e OutputEvent) error {
	if e.Type != ServerEventTypeOutput {
		return fmt.Errorf("contract: OutputEvent.Type must be %q", ServerEventTypeOutput)
	}
	if e.SessionID == "" {
		return errors.New("contract: OutputEvent.SessionID must be non-empty")
	}
	if err := validateReplaySeq(e.Seq); err != nil {
		return err
	}
	return validateBase64(e.Chunk)
}

func validateReplayFrames(frames []ReplayFrame) error {
	for i, fr := range frames {
		switch rf := fr.(type) {
		case OutputEvent:
			if err := validateOutputEvent(rf); err != nil {
				return fmt.Errorf("contract: replay frame[%d]: %w", i, err)
			}
		case SessionRestartBoundaryEvent:
			if err := validateRestartBoundary(rf); err != nil {
				return fmt.Errorf("contract: replay frame[%d]: %w", i, err)
			}
		default:
			return fmt.Errorf("contract: replay frame[%d]: unknown ReplayFrame type %T", i, fr)
		}
	}
	return nil
}

// replayFrameSeq returns the occupied seq of a ReplayFrame concrete value.
func replayFrameSeq(fr ReplayFrame) Seq {
	switch rf := fr.(type) {
	case OutputEvent:
		return rf.Seq
	case SessionRestartBoundaryEvent:
		return rf.Seq
	}
	return 0
}

func validateAttached(a SessionAttachedEvent) error {
	if a.Type != ServerEventTypeSessionAttached {
		return fmt.Errorf("contract: SessionAttachedEvent.Type must be %q", ServerEventTypeSessionAttached)
	}
	if a.RequestID == "" {
		return errors.New("contract: SessionAttachedEvent.RequestID must be non-empty")
	}
	if a.APIVersion != APIVersionV1 {
		return fmt.Errorf("contract: SessionAttachedEvent.APIVersion must be %q", APIVersionV1)
	}
	if a.SessionID == "" {
		return errors.New("contract: SessionAttachedEvent.SessionID must be non-empty")
	}
	if a.History == nil {
		return errors.New("contract: SessionAttachedEvent.History must not be nil")
	}
	if err := validateReplayFrames(a.History); err != nil {
		return err
	}
	if err := validateSeqRange(a.EarliestSeq); err != nil {
		return err
	}
	if err := validateSeqRange(a.LatestSeq); err != nil {
		return err
	}
	if a.EarliestSeq > a.LatestSeq {
		return errors.New("contract: SessionAttachedEvent.EarliestSeq must be <= LatestSeq")
	}
	if err := validateFiveLayerSnapshot(a.Snapshot); err != nil {
		return err
	}
	// Attached gap relation (addendum §1.2): when history.state is gap, the gap
	// range must connect to the retained window — gap.ToSeq+1 == EarliestSeq.
	// ToSeq <= MaxSeqSafeInteger (validated), so +1 cannot overflow uint64.
	if a.Snapshot.History.State == HistoryStateGap && a.Snapshot.History.Gap != nil {
		if a.Snapshot.History.Gap.ToSeq+1 != a.EarliestSeq {
			return errors.New("contract: attached history gap.ToSeq+1 must equal EarliestSeq")
		}
	}
	// CG-03 inputAckMode is an optional additive field; the decoder only ever
	// sets it to the canonical value (unknown values are treated as absent), so
	// no validation is needed here. The producer constructs it from the typed
	// constant InputAckModeSessionWindowV1 only.
	return nil
}

func validateBackfillFrames(b BackfillFramesResultEvent) error {
	if b.Type != ServerEventTypeBackfillResult {
		return fmt.Errorf("contract: BackfillFramesResultEvent.Type must be %q", ServerEventTypeBackfillResult)
	}
	if b.RequestID == "" {
		return errors.New("contract: BackfillFramesResultEvent.RequestID must be non-empty")
	}
	if b.SessionID == "" {
		return errors.New("contract: BackfillFramesResultEvent.SessionID must be non-empty")
	}
	if err := validateReplaySeq(b.FromSeq); err != nil {
		return err
	}
	if b.ToSeq < b.FromSeq {
		return errors.New("contract: BackfillFramesResultEvent.ToSeq must be >= FromSeq")
	}
	if err := validateSeqRange(b.ToSeq); err != nil {
		return err
	}
	if err := validateSeqRange(b.EarliestSeq); err != nil {
		return err
	}
	if err := validateSeqRange(b.LatestSeq); err != nil {
		return err
	}
	if b.Frames == nil {
		return errors.New("contract: BackfillFramesResultEvent.Frames must not be nil")
	}
	if len(b.Frames) == 0 {
		return errors.New("contract: BackfillFramesResultEvent.Frames must be non-empty")
	}
	if err := validateReplayFrames(b.Frames); err != nil {
		return err
	}
	// M3-004: the frames variant claims the FULL closed range [FromSeq, ToSeq] is
	// retained (a partial range must use the gap variant). Require contiguous,
	// exhaustive cover: first==FromSeq, every adjacent pair differs by exactly 1,
	// last==ToSeq. Without this a partial [seq2] for [2,3] passes the old
	// in-range+ascending check and the store would wrongly mark seq3 settled.
	var prev Seq
	for i, fr := range b.Frames {
		s := replayFrameSeq(fr)
		if s < b.FromSeq || s > b.ToSeq {
			return fmt.Errorf("contract: BackfillFramesResultEvent.Frames[%d] seq %d outside [%d,%d]", i, s, b.FromSeq, b.ToSeq)
		}
		if i == 0 {
			if s != b.FromSeq {
				return fmt.Errorf("contract: BackfillFramesResultEvent.Frames[0] seq %d != FromSeq %d (non-contiguous cover; use gap variant for partial range)", s, b.FromSeq)
			}
		} else {
			if s != prev+1 {
				return fmt.Errorf("contract: BackfillFramesResultEvent.Frames[%d] seq %d not contiguous with prev %d (non-exhaustive cover; use gap variant for partial range)", i, s, prev)
			}
		}
		if i == len(b.Frames)-1 && s != b.ToSeq {
			return fmt.Errorf("contract: BackfillFramesResultEvent.Frames last seq %d != ToSeq %d (non-exhaustive cover; use gap variant for partial range)", s, b.ToSeq)
		}
		prev = s
	}
	return nil
}

func validateBackfillGap(b BackfillGapResultEvent) error {
	if b.Type != ServerEventTypeBackfillResult {
		return fmt.Errorf("contract: BackfillGapResultEvent.Type must be %q", ServerEventTypeBackfillResult)
	}
	if b.RequestID == "" {
		return errors.New("contract: BackfillGapResultEvent.RequestID must be non-empty")
	}
	if b.SessionID == "" {
		return errors.New("contract: BackfillGapResultEvent.SessionID must be non-empty")
	}
	if err := validateReplaySeq(b.FromSeq); err != nil {
		return err
	}
	if b.ToSeq < b.FromSeq {
		return errors.New("contract: BackfillGapResultEvent.ToSeq must be >= FromSeq")
	}
	if err := validateSeqRange(b.ToSeq); err != nil {
		return err
	}
	if err := validateSeqRange(b.EarliestSeq); err != nil {
		return err
	}
	if err := validateSeqRange(b.LatestSeq); err != nil {
		return err
	}
	if err := validateGapRange(b.Gap); err != nil {
		return err
	}
	// M3-004 (R3): the gap variant must cover the FULL requested range. v1 has no
	// partial-gap representation (design §4.3.6: "fully retained ⇒ frames; start
	// before earliest ⇒ gap variant (no mixing)"; the server producer emits
	// Gap{FromSeq: frame.FromSeq, ToSeq: frame.ToSeq}). Without this check a partial
	// inner gap [2,2] for outer [2,4] would pass the inner-only validation and the
	// store would advance its frontier to event.toSeq=4, crossing Seq3/4 which were
	// never held nor authoritatively adjudicated — violating design §3 (frontier
	// advances only across held or authoritatively-unavailable positions).
	if b.Gap.FromSeq != b.FromSeq || b.Gap.ToSeq != b.ToSeq {
		return fmt.Errorf("contract: BackfillGapResultEvent.Gap must cover the full requested range [FromSeq=%d, ToSeq=%d]; got gap [%d, %d] (v1 has no partial-gap representation)", b.FromSeq, b.ToSeq, b.Gap.FromSeq, b.Gap.ToSeq)
	}
	return nil
}

func validateSessionStateNormal(e SessionStateEvent) error {
	if e.Type != ServerEventTypeSessionState {
		return fmt.Errorf("contract: SessionStateEvent.Type must be %q", ServerEventTypeSessionState)
	}
	if e.SessionID == "" {
		return errors.New("contract: SessionStateEvent.SessionID must be non-empty")
	}
	if !isKnownSessionState(e.State) {
		return fmt.Errorf("contract: SessionStateEvent.State %q is not a known session state", e.State)
	}
	if e.OccurredAt == "" {
		return errors.New("contract: SessionStateEvent.OccurredAt must be non-empty")
	}
	return nil
}

func validateRestartBoundary(e SessionRestartBoundaryEvent) error {
	if e.Type != ServerEventTypeSessionState {
		return fmt.Errorf("contract: SessionRestartBoundaryEvent.Type must be %q", ServerEventTypeSessionState)
	}
	if e.SessionID == "" {
		return errors.New("contract: SessionRestartBoundaryEvent.SessionID must be non-empty")
	}
	if !isKnownSessionState(e.State) {
		return fmt.Errorf("contract: SessionRestartBoundaryEvent.State %q is not a known session state", e.State)
	}
	if !e.RestartBoundary {
		return errors.New("contract: SessionRestartBoundaryEvent.RestartBoundary must be true")
	}
	if err := validateReplaySeq(e.Seq); err != nil {
		return err
	}
	if e.OccurredAt == "" {
		return errors.New("contract: SessionRestartBoundaryEvent.OccurredAt must be non-empty")
	}
	return nil
}

func validateControlStateEvent(e ControlStateEvent) error {
	if e.Type != ServerEventTypeControlState {
		return fmt.Errorf("contract: ControlStateEvent.Type must be %q", ServerEventTypeControlState)
	}
	if e.SessionID == "" {
		return errors.New("contract: ControlStateEvent.SessionID must be non-empty")
	}
	if !isKnownControlState(e.State) {
		return fmt.Errorf("contract: ControlStateEvent.State %q is not a known control state", e.State)
	}
	switch e.State {
	case ControlStateOther:
		if e.DeviceName == nil || *e.DeviceName == "" {
			return errors.New("contract: ControlStateEvent.DeviceName required for state other")
		}
	case ControlStateNone, ControlStateYou, ControlStateDesktop:
		if e.DeviceName != nil {
			return fmt.Errorf("contract: ControlStateEvent.DeviceName must be omitted for state %q", e.State)
		}
	}
	if e.Reason == "" {
		return errors.New("contract: ControlStateEvent.Reason must be non-empty")
	}
	if e.OccurredAt == "" {
		return errors.New("contract: ControlStateEvent.OccurredAt must be non-empty")
	}
	return nil
}

func validateAuthRevokedEvent(e AuthRevokedEvent) error {
	if e.Type != ServerEventTypeAuthRevoked {
		return fmt.Errorf("contract: AuthRevokedEvent.Type must be %q", ServerEventTypeAuthRevoked)
	}
	// CG-01 closed reason enum: unknown/empty reasons are rejected with no bytes
	// produced (producer strict). isKnownAuthRevokedReason implicitly rejects "".
	if !isKnownAuthRevokedReason(e.Reason) {
		return fmt.Errorf("contract: AuthRevokedEvent.Reason %q is not a known auth revoked reason", e.Reason)
	}
	if e.OccurredAt == "" {
		return errors.New("contract: AuthRevokedEvent.OccurredAt must be non-empty")
	}
	return nil
}

func validateErrorEvent(e ErrorEvent) error {
	if e.Type != ServerEventTypeError {
		return fmt.Errorf("contract: ErrorEvent.Type must be %q", ServerEventTypeError)
	}
	if e.Code == "" {
		return errors.New("contract: ErrorEvent.Code must be non-empty")
	}
	if !isKnownErrorLayer(e.Layer) {
		return fmt.Errorf("contract: ErrorEvent.Layer %q is not a known error layer", e.Layer)
	}
	if e.Message == "" {
		return errors.New("contract: ErrorEvent.Message must be non-empty")
	}
	if e.ActionHint == "" {
		return errors.New("contract: ErrorEvent.ActionHint must be non-empty")
	}
	return nil
}

// validateInputAck validates a CG-03 input.ack event. The ACK has exactly four
// required fields (type/requestId/sessionId/id) and no seq/status/time/data/
// details. The id accepts any non-empty opaque value: the canonical 39-byte
// discriminator is a producer-side opt-in, not a consumer requirement (legacy
// non-empty ACKs still decode), so the validator only requires non-empty id.
func validateInputAck(e InputAckEvent) error {
	if e.Type != ServerEventTypeInputAck {
		return fmt.Errorf("contract: InputAckEvent.Type must be %q", ServerEventTypeInputAck)
	}
	if e.RequestID == "" {
		return errors.New("contract: InputAckEvent.RequestID must be non-empty")
	}
	if e.SessionID == "" {
		return errors.New("contract: InputAckEvent.SessionID must be non-empty")
	}
	if e.ID == "" {
		return errors.New("contract: InputAckEvent.ID must be non-empty")
	}
	return nil
}

// ValidateServerEvent validates a known server event by its concrete type. It
// enforces required fields, closed enums, safe seq, conditional/XOR, replay
// frames and the attached-time snapshot constraints.
func ValidateServerEvent(e KnownServerEvent) error {
	switch v := e.(type) {
	case SessionAttachedEvent:
		return validateAttached(v)
	case OutputEvent:
		return validateOutputEvent(v)
	case BackfillFramesResultEvent:
		return validateBackfillFrames(v)
	case BackfillGapResultEvent:
		return validateBackfillGap(v)
	case SessionStateEvent:
		return validateSessionStateNormal(v)
	case SessionRestartBoundaryEvent:
		return validateRestartBoundary(v)
	case ControlStateEvent:
		return validateControlStateEvent(v)
	case AuthRevokedEvent:
		return validateAuthRevokedEvent(v)
	case ErrorEvent:
		return validateErrorEvent(v)
	case InputAckEvent:
		return validateInputAck(v)
	default:
		return fmt.Errorf("contract: unknown KnownServerEvent type %T", e)
	}
}

// MarshalServerEvent validates then marshals a known server event. It produces
// no bytes on validation failure.
func MarshalServerEvent(e KnownServerEvent) ([]byte, error) {
	if err := ValidateServerEvent(e); err != nil {
		return nil, err
	}
	return json.Marshal(e)
}

// ===========================================================================
// Production Decode (ingress). Addendum §5.1/§5.2.
// ===========================================================================

// DecodeClientFrame strictly decodes a v1 WS client frame: rejects unknown
// fields/types, null/missing required, trailing JSON and type errors, then
// validates conditional/range/safe-seq/Base64.
func DecodeClientFrame(raw []byte) (ClientFrame, error) {
	f, err := strictFields(raw)
	if err != nil {
		return nil, err
	}
	typeRaw, err := requireField(f, "type")
	if err != nil {
		return nil, err
	}
	var ftype string
	if err := json.Unmarshal(typeRaw, &ftype); err != nil {
		return nil, fmt.Errorf("contract: field type: %w", err)
	}
	switch ftype {
	case ClientFrameTypeAttach:
		return decodeAttachFrame(f)
	case ClientFrameTypeInput:
		return decodeInputFrame(f)
	case ClientFrameTypeResize:
		return decodeResizeFrame(f)
	case ClientFrameTypeBackfill:
		return decodeBackfillFrame(f)
	case ClientFrameTypePing:
		return decodePingFrame(f)
	default:
		return nil, fmt.Errorf("contract: unknown client frame type %q", ftype)
	}
}

func decodeAttachFrame(f map[string]json.RawMessage) (AttachFrame, error) {
	if err := rejectUnknown(f, "type", "requestId", "apiVersion", "sessionId", "lastSeq"); err != nil {
		return AttachFrame{}, err
	}
	rid, err := reqNonEmptyString(f, "requestId")
	if err != nil {
		return AttachFrame{}, err
	}
	apiVer, err := reqField[string](f, "apiVersion")
	if err != nil {
		return AttachFrame{}, err
	}
	if apiVer != string(APIVersionV1) {
		return AttachFrame{}, fmt.Errorf("contract: apiVersion must be %q", APIVersionV1)
	}
	sid, err := reqNonEmptyString(f, "sessionId")
	if err != nil {
		return AttachFrame{}, err
	}
	out := AttachFrame{Type: ClientFrameTypeAttach, RequestID: RequestID(rid), APIVersion: APIVersion(apiVer), SessionID: SessionID(sid)}
	if ls, ok, err := optFieldT[Seq](f, "lastSeq"); err != nil {
		return out, err
	} else if ok {
		if err := validateSeqRange(ls); err != nil {
			return out, err
		}
		out.LastSeq = &ls
	}
	return out, nil
}

func decodeInputFrame(f map[string]json.RawMessage) (InputFrame, error) {
	if err := rejectUnknown(f, "type", "requestId", "id", "data"); err != nil {
		return InputFrame{}, err
	}
	rid, err := reqNonEmptyString(f, "requestId")
	if err != nil {
		return InputFrame{}, err
	}
	id, err := reqNonEmptyString(f, "id")
	if err != nil {
		return InputFrame{}, err
	}
	data, err := reqField[string](f, "data")
	if err != nil {
		return InputFrame{}, err
	}
	out := InputFrame{Type: ClientFrameTypeInput, RequestID: RequestID(rid), ID: MessageID(id), Data: data}
	if err := validateBase64(data); err != nil {
		return out, err
	}
	return out, nil
}

func decodeResizeFrame(f map[string]json.RawMessage) (ResizeFrame, error) {
	if err := rejectUnknown(f, "type", "requestId", "cols", "rows"); err != nil {
		return ResizeFrame{}, err
	}
	rid, err := reqNonEmptyString(f, "requestId")
	if err != nil {
		return ResizeFrame{}, err
	}
	cols, err := reqField[int](f, "cols")
	if err != nil {
		return ResizeFrame{}, err
	}
	rows, err := reqField[int](f, "rows")
	if err != nil {
		return ResizeFrame{}, err
	}
	if cols < 1 {
		return ResizeFrame{}, errors.New("contract: cols must be a positive integer")
	}
	if rows < 1 {
		return ResizeFrame{}, errors.New("contract: rows must be a positive integer")
	}
	return ResizeFrame{Type: ClientFrameTypeResize, RequestID: RequestID(rid), Cols: cols, Rows: rows}, nil
}

func decodeBackfillFrame(f map[string]json.RawMessage) (BackfillFrame, error) {
	if err := rejectUnknown(f, "type", "requestId", "fromSeq", "toSeq"); err != nil {
		return BackfillFrame{}, err
	}
	rid, err := reqNonEmptyString(f, "requestId")
	if err != nil {
		return BackfillFrame{}, err
	}
	from, err := reqField[Seq](f, "fromSeq")
	if err != nil {
		return BackfillFrame{}, err
	}
	to, err := reqField[Seq](f, "toSeq")
	if err != nil {
		return BackfillFrame{}, err
	}
	if err := validateReplaySeq(from); err != nil {
		return BackfillFrame{}, err
	}
	if to < from {
		return BackfillFrame{}, errors.New("contract: toSeq must be >= fromSeq")
	}
	if err := validateSeqRange(to); err != nil {
		return BackfillFrame{}, err
	}
	return BackfillFrame{Type: ClientFrameTypeBackfill, RequestID: RequestID(rid), FromSeq: from, ToSeq: to}, nil
}

func decodePingFrame(f map[string]json.RawMessage) (PingFrame, error) {
	if err := rejectUnknown(f, "type", "requestId"); err != nil {
		return PingFrame{}, err
	}
	rid, err := reqNonEmptyString(f, "requestId")
	if err != nil {
		return PingFrame{}, err
	}
	return PingFrame{Type: ClientFrameTypePing, RequestID: RequestID(rid)}, nil
}

// DecodeKnownServerEvent strictly decodes a known v1 server event for contract
// parity/validation. It allows additive unknown fields on known events (forward
// compatibility) but rejects missing/null required, wrong types, conditional
// failures, unsafe seq and unknown top-level type. It returns error (not a
// normalized Unknown) for unknown type: the TS-side forward-compatible
// normalization is the client's responsibility, not this contract-parity API.
func DecodeKnownServerEvent(raw []byte) (KnownServerEvent, error) {
	f, err := strictFields(raw)
	if err != nil {
		return nil, err
	}
	typeRaw, err := requireField(f, "type")
	if err != nil {
		return nil, err
	}
	var ftype string
	if err := json.Unmarshal(typeRaw, &ftype); err != nil {
		return nil, fmt.Errorf("contract: field type: %w", err)
	}
	switch ftype {
	case ServerEventTypeSessionAttached:
		ev, err := decodeAttached(f)
		if err != nil {
			return nil, err
		}
		return ev, validateAttached(ev)
	case ServerEventTypeOutput:
		ev, err := decodeOutput(f)
		if err != nil {
			return nil, err
		}
		return ev, validateOutputEvent(ev)
	case ServerEventTypeBackfillResult:
		return decodeBackfillResult(f)
	case ServerEventTypeSessionState:
		return decodeSessionState(f)
	case ServerEventTypeControlState:
		ev, err := decodeControlState(f)
		if err != nil {
			return nil, err
		}
		return ev, validateControlStateEvent(ev)
	case ServerEventTypeAuthRevoked:
		ev, err := decodeAuthRevoked(f)
		if err != nil {
			return nil, err
		}
		return ev, validateAuthRevokedEvent(ev)
	case ServerEventTypeError:
		ev, err := decodeErrorEvent(f)
		if err != nil {
			return nil, err
		}
		return ev, validateErrorEvent(ev)
	case ServerEventTypeInputAck:
		ev, err := decodeInputAck(f)
		if err != nil {
			return nil, err
		}
		return ev, validateInputAck(ev)
	default:
		return nil, fmt.Errorf("contract: unknown server event type %q", ftype)
	}
}

// decodeAttached builds a SessionAttachedEvent from fields (additive unknown
// fields allowed/discarded).
func decodeAttached(f map[string]json.RawMessage) (SessionAttachedEvent, error) {
	rid, err := reqNonEmptyString(f, "requestId")
	if err != nil {
		return SessionAttachedEvent{}, err
	}
	apiVer, err := reqField[string](f, "apiVersion")
	if err != nil {
		return SessionAttachedEvent{}, err
	}
	if apiVer != string(APIVersionV1) {
		return SessionAttachedEvent{}, fmt.Errorf("contract: apiVersion must be %q", APIVersionV1)
	}
	sid, err := reqNonEmptyString(f, "sessionId")
	if err != nil {
		return SessionAttachedEvent{}, err
	}
	history, err := decodeReplayFrames(f, "history")
	if err != nil {
		return SessionAttachedEvent{}, err
	}
	earliest, err := reqField[Seq](f, "earliestSeq")
	if err != nil {
		return SessionAttachedEvent{}, err
	}
	latest, err := reqField[Seq](f, "latestSeq")
	if err != nil {
		return SessionAttachedEvent{}, err
	}
	snap, err := decodeFiveLayer(f, "snapshot")
	if err != nil {
		return SessionAttachedEvent{}, err
	}
	out := SessionAttachedEvent{
		Type: ServerEventTypeSessionAttached, RequestID: RequestID(rid), APIVersion: APIVersion(apiVer),
		SessionID: SessionID(sid), History: history, EarliestSeq: earliest, LatestSeq: latest, Snapshot: snap,
	}
	// CG-03 input ack capability (contract-addendum-cg03.md §3/§4): inputAckMode
	// is an optional additive field. Only the sole canonical value enables the
	// capability; absent OR unknown value = capability unavailable (read
	// projection preserved, input disabled). An unknown future value is treated
	// as absent (forward-compat: "known event unknown optional field: ignored"),
	// NOT a malformed event.
	if mode, ok, err := optFieldT[InputAckMode](f, "inputAckMode"); err != nil {
		return SessionAttachedEvent{}, err
	} else if ok && mode == InputAckModeSessionWindowV1 {
		out.InputAckMode = &mode
	}
	return out, nil
}

// decodeReplayFrames decodes the named array field into ReplayFrame concrete
// values. The field is required and non-null; the array may be empty.
func decodeReplayFrames(f map[string]json.RawMessage, key string) ([]ReplayFrame, error) {
	v, err := requireField(f, key)
	if err != nil {
		return nil, err
	}
	var rawArr []json.RawMessage
	if err := json.Unmarshal(v, &rawArr); err != nil {
		return nil, fmt.Errorf("contract: field %q: %w", key, err)
	}
	out := make([]ReplayFrame, 0, len(rawArr))
	for i, item := range rawArr {
		ev, err := DecodeKnownServerEvent(item)
		if err != nil {
			return nil, fmt.Errorf("contract: %s[%d]: %w", key, i, err)
		}
		rf, ok := ev.(ReplayFrame)
		if !ok {
			return nil, fmt.Errorf("contract: %s[%d]: not a replay frame (type %T)", key, i, ev)
		}
		out = append(out, rf)
	}
	return out, nil
}

func decodeOutput(f map[string]json.RawMessage) (OutputEvent, error) {
	sid, err := reqNonEmptyString(f, "sessionId")
	if err != nil {
		return OutputEvent{}, err
	}
	seq, err := reqField[Seq](f, "seq")
	if err != nil {
		return OutputEvent{}, err
	}
	chunk, err := reqField[string](f, "chunk")
	if err != nil {
		return OutputEvent{}, err
	}
	out := OutputEvent{Type: ServerEventTypeOutput, SessionID: SessionID(sid), Seq: seq, Chunk: chunk}
	if se, ok, err := optFieldT[bool](f, "structuredExpected"); err != nil {
		return out, err
	} else if ok {
		out.StructuredExpected = &se
	}
	return out, nil
}

func decodeBackfillResult(f map[string]json.RawMessage) (KnownServerEvent, error) {
	common, err := decodeBackfillCommon(f)
	if err != nil {
		return nil, err
	}
	hasFrames := fieldPresent(f, "frames")
	hasGap := fieldPresent(f, "gap")
	if hasFrames == hasGap {
		// both present or both absent -> XOR violation
		return nil, errors.New("contract: backfill.result must have exactly one of frames or gap")
	}
	if hasGap {
		gap, err := decodeGap(f, "gap")
		if err != nil {
			return nil, err
		}
		ev := BackfillGapResultEvent{Type: ServerEventTypeBackfillResult, RequestID: common.requestID, SessionID: common.sessionID,
			FromSeq: common.fromSeq, ToSeq: common.toSeq, EarliestSeq: common.earliestSeq, LatestSeq: common.latestSeq, Gap: gap}
		return ev, validateBackfillGap(ev)
	}
	frames, err := decodeReplayFrames(f, "frames")
	if err != nil {
		return nil, err
	}
	ev := BackfillFramesResultEvent{Type: ServerEventTypeBackfillResult, RequestID: common.requestID, SessionID: common.sessionID,
		FromSeq: common.fromSeq, ToSeq: common.toSeq, EarliestSeq: common.earliestSeq, LatestSeq: common.latestSeq, Frames: frames}
	return ev, validateBackfillFrames(ev)
}

type backfillCommon struct {
	requestID   RequestID
	sessionID   SessionID
	fromSeq     Seq
	toSeq       Seq
	earliestSeq Seq
	latestSeq   Seq
}

func decodeBackfillCommon(f map[string]json.RawMessage) (backfillCommon, error) {
	rid, err := reqNonEmptyString(f, "requestId")
	if err != nil {
		return backfillCommon{}, err
	}
	sid, err := reqNonEmptyString(f, "sessionId")
	if err != nil {
		return backfillCommon{}, err
	}
	from, err := reqField[Seq](f, "fromSeq")
	if err != nil {
		return backfillCommon{}, err
	}
	to, err := reqField[Seq](f, "toSeq")
	if err != nil {
		return backfillCommon{}, err
	}
	earliest, err := reqField[Seq](f, "earliestSeq")
	if err != nil {
		return backfillCommon{}, err
	}
	latest, err := reqField[Seq](f, "latestSeq")
	if err != nil {
		return backfillCommon{}, err
	}
	return backfillCommon{requestID: RequestID(rid), sessionID: SessionID(sid), fromSeq: from, toSeq: to, earliestSeq: earliest, latestSeq: latest}, nil
}

// fieldPresent reports whether key exists in f (regardless of null). Used for
// XOR presence checks where the field value is itself validated separately.
func fieldPresent(f map[string]json.RawMessage, key string) bool {
	_, ok := f[key]
	return ok
}

func decodeGap(f map[string]json.RawMessage, key string) (GapRange, error) {
	v, err := requireField(f, key)
	if err != nil {
		return GapRange{}, err
	}
	var g GapRange
	if err := json.Unmarshal(v, &g); err != nil {
		return GapRange{}, fmt.Errorf("contract: field %q: %w", key, err)
	}
	return g, nil
}

func decodeSessionState(f map[string]json.RawMessage) (KnownServerEvent, error) {
	sid, err := reqNonEmptyString(f, "sessionId")
	if err != nil {
		return nil, err
	}
	state, err := reqField[SessionState](f, "state")
	if err != nil {
		return nil, err
	}
	occurred, err := reqNonEmptyString(f, "occurredAt")
	if err != nil {
		return nil, err
	}
	hasRestart := fieldPresent(f, "restartBoundary")
	hasSeq := fieldPresent(f, "seq")
	if hasRestart {
		rb, err := reqField[bool](f, "restartBoundary")
		if err != nil {
			return nil, err
		}
		if !rb {
			return nil, errors.New("contract: session.state restartBoundary must be true when present")
		}
		seq, err := reqField[Seq](f, "seq")
		if err != nil {
			return nil, err
		}
		ev := SessionRestartBoundaryEvent{Type: ServerEventTypeSessionState, SessionID: SessionID(sid), State: state, RestartBoundary: true, Seq: seq, OccurredAt: occurred}
		return ev, validateRestartBoundary(ev)
	}
	if hasSeq {
		return nil, errors.New("contract: session.state normal event must omit seq")
	}
	ev := SessionStateEvent{Type: ServerEventTypeSessionState, SessionID: SessionID(sid), State: state, OccurredAt: occurred}
	return ev, validateSessionStateNormal(ev)
}

func decodeControlState(f map[string]json.RawMessage) (ControlStateEvent, error) {
	sid, err := reqNonEmptyString(f, "sessionId")
	if err != nil {
		return ControlStateEvent{}, err
	}
	state, err := reqField[ControlState](f, "state")
	if err != nil {
		return ControlStateEvent{}, err
	}
	reason, err := reqNonEmptyString(f, "reason")
	if err != nil {
		return ControlStateEvent{}, err
	}
	occurred, err := reqNonEmptyString(f, "occurredAt")
	if err != nil {
		return ControlStateEvent{}, err
	}
	out := ControlStateEvent{Type: ServerEventTypeControlState, SessionID: SessionID(sid), State: state, Reason: reason, OccurredAt: occurred}
	if dn, ok, err := optFieldT[string](f, "deviceName"); err != nil {
		return out, err
	} else if ok {
		out.DeviceName = &dn
	}
	return out, nil
}

func decodeAuthRevoked(f map[string]json.RawMessage) (AuthRevokedEvent, error) {
	// reqNonEmptyString rejects missing/null/empty/wrong-type reason at decode
	// time; the membership check runs in validateAuthRevokedEvent.
	reasonStr, err := reqNonEmptyString(f, "reason")
	if err != nil {
		return AuthRevokedEvent{}, err
	}
	occurred, err := reqNonEmptyString(f, "occurredAt")
	if err != nil {
		return AuthRevokedEvent{}, err
	}
	return AuthRevokedEvent{Type: ServerEventTypeAuthRevoked, Reason: AuthRevokedReason(reasonStr), OccurredAt: occurred}, nil
}

// decodeInputAck decodes a CG-03 input.ack event (additive: unknown fields are
// discarded, matching the forward-compatible server-event decode contract). The
// id accepts any non-empty opaque value; the canonical 39-byte discriminator is
// validated only when the server producer classifies the canonical ledger path.
func decodeInputAck(f map[string]json.RawMessage) (InputAckEvent, error) {
	rid, err := reqNonEmptyString(f, "requestId")
	if err != nil {
		return InputAckEvent{}, err
	}
	sid, err := reqNonEmptyString(f, "sessionId")
	if err != nil {
		return InputAckEvent{}, err
	}
	id, err := reqNonEmptyString(f, "id")
	if err != nil {
		return InputAckEvent{}, err
	}
	return InputAckEvent{Type: ServerEventTypeInputAck, RequestID: RequestID(rid), SessionID: SessionID(sid), ID: MessageID(id)}, nil
}

func decodeErrorEvent(f map[string]json.RawMessage) (ErrorEvent, error) {
	code, err := reqField[ErrorCode](f, "code")
	if err != nil {
		return ErrorEvent{}, err
	}
	if code == "" {
		return ErrorEvent{}, errors.New("contract: code must be non-empty")
	}
	layer, err := reqField[ErrorLayer](f, "layer")
	if err != nil {
		return ErrorEvent{}, err
	}
	message, err := reqNonEmptyString(f, "message")
	if err != nil {
		return ErrorEvent{}, err
	}
	hint, err := reqField[ActionHint](f, "actionHint")
	if err != nil {
		return ErrorEvent{}, err
	}
	if hint == "" {
		return ErrorEvent{}, errors.New("contract: actionHint must be non-empty")
	}
	out := ErrorEvent{Type: ServerEventTypeError, Code: code, Layer: layer, Message: message, ActionHint: hint}
	if rid, ok, err := optFieldT[string](f, "requestId"); err != nil {
		return out, err
	} else if ok {
		out.RequestID = RequestID(rid)
	}
	if sid, ok, err := optFieldT[string](f, "sessionId"); err != nil {
		return out, err
	} else if ok {
		out.SessionID = SessionID(sid)
	}
	if dn, ok, err := optField(f, "details"); err != nil {
		return out, err
	} else if ok {
		var d Details
		if err := json.Unmarshal(dn, &d); err != nil {
			return out, fmt.Errorf("contract: details: %w", err)
		}
		out.Details = d
	}
	return out, nil
}

func decodeFiveLayer(f map[string]json.RawMessage, key string) (FiveLayerSnapshot, error) {
	v, err := requireField(f, key)
	if err != nil {
		return FiveLayerSnapshot{}, err
	}
	sub, err := strictFields(v)
	if err != nil {
		return FiveLayerSnapshot{}, fmt.Errorf("contract: field %q: %w", key, err)
	}
	connState, err := decodeLayerStateString(sub, "connection")
	if err != nil {
		return FiveLayerSnapshot{}, err
	}
	authState, err := decodeLayerStateString(sub, "auth")
	if err != nil {
		return FiveLayerSnapshot{}, err
	}
	sessionSnap, err := decodeSessionLayer(sub)
	if err != nil {
		return FiveLayerSnapshot{}, err
	}
	ctrl, err := decodeControlLayer(sub)
	if err != nil {
		return FiveLayerSnapshot{}, err
	}
	hist, err := decodeHistoryLayer(sub)
	if err != nil {
		return FiveLayerSnapshot{}, err
	}
	return FiveLayerSnapshot{Connection: ConnectionSnapshot{State: connState}, Auth: AuthSnapshot{State: authState}, Session: sessionSnap, Control: ctrl, History: hist}, nil
}

// decodeLayerStateString decodes a layer that is {state: string}
// (connection/auth share this shape).
func decodeLayerStateString(sub map[string]json.RawMessage, key string) (string, error) {
	v, err := requireField(sub, key)
	if err != nil {
		return "", err
	}
	inner, err := strictFields(v)
	if err != nil {
		return "", fmt.Errorf("contract: field %q: %w", key, err)
	}
	return reqNonEmptyString(inner, "state")
}

func decodeSessionLayer(sub map[string]json.RawMessage) (SessionSnapshot, error) {
	v, err := requireField(sub, "session")
	if err != nil {
		return SessionSnapshot{}, err
	}
	inner, err := strictFields(v)
	if err != nil {
		return SessionSnapshot{}, err
	}
	st, err := reqField[SessionState](inner, "state")
	return SessionSnapshot{State: st}, err
}

func decodeControlLayer(sub map[string]json.RawMessage) (ControlSnapshot, error) {
	v, err := requireField(sub, "control")
	if err != nil {
		return ControlSnapshot{}, err
	}
	inner, err := strictFields(v)
	if err != nil {
		return ControlSnapshot{}, err
	}
	state, err := reqField[ControlState](inner, "state")
	if err != nil {
		return ControlSnapshot{}, err
	}
	out := ControlSnapshot{State: state}
	if dn, ok, err := optFieldT[string](inner, "deviceName"); err != nil {
		return out, err
	} else if ok {
		out.DeviceName = &dn
	}
	return out, nil
}

func decodeHistoryLayer(sub map[string]json.RawMessage) (HistorySnapshot, error) {
	v, err := requireField(sub, "history")
	if err != nil {
		return HistorySnapshot{}, err
	}
	inner, err := strictFields(v)
	if err != nil {
		return HistorySnapshot{}, err
	}
	state, err := reqField[HistoryState](inner, "state")
	if err != nil {
		return HistorySnapshot{}, err
	}
	out := HistorySnapshot{State: state}
	if g, ok, err := optField(inner, "gap"); err != nil {
		return out, err
	} else if ok {
		var gap GapRange
		if err := json.Unmarshal(g, &gap); err != nil {
			return out, fmt.Errorf("contract: history.gap: %w", err)
		}
		out.Gap = &gap
	}
	return out, nil
}
