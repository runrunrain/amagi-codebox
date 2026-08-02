package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fixturePath = "../../../mobile/src/lib/contract/testdata/v1-wire-fixtures.json"

type fixtureEnvelope struct {
	Manifest struct {
		APIVersion       string         `json:"apiVersion"`
		RequestIDHeader  string         `json:"requestIdHeader"`
		RESTBasePath     string         `json:"restBasePath"`
		WebSocketV1Path  string         `json:"webSocketV1Path"`
		RestEndpoints    []RestEndpoint `json:"restEndpoints"`
		ClientFrameTypes []string       `json:"clientFrameTypes"`
		ServerEventTypes []string       `json:"serverEventTypes"`
		ErrorCodes       []string       `json:"errorCodes"`
		CLITypes         []string       `json:"cliTypes"`
		SessionStates    []string       `json:"sessionStates"`
		ControlStates    []string       `json:"controlStates"`
		HistoryStates    []string       `json:"historyStates"`
		ErrorLayers      []string       `json:"errorLayers"`
		ActionHints      []string       `json:"actionHints"`
	} `json:"manifest"`
	REST struct {
		PairingCompleteRequest          json.RawMessage `json:"pairingCompleteRequest"`
		PairingCompleteResponse         json.RawMessage `json:"pairingCompleteResponse"`
		HostSummary                     json.RawMessage `json:"hostSummary"`
		SessionListEmpty                json.RawMessage `json:"sessionListEmpty"`
		SessionSummary                  json.RawMessage `json:"sessionSummary"`
		SessionDetail                   json.RawMessage `json:"sessionDetail"`
		CreateSessionRequest            json.RawMessage `json:"createSessionRequest"`
		CreateSessionRequestWithWorkdir json.RawMessage `json:"createSessionRequestWithWorkdir"`
		ConfirmActionRequest            json.RawMessage `json:"confirmActionRequest"`
		ControlSnapshotNone             json.RawMessage `json:"controlSnapshotNone"`
		ControlSnapshotYou              json.RawMessage `json:"controlSnapshotYou"`
		ControlSnapshotOther            json.RawMessage `json:"controlSnapshotOther"`
		ControlSnapshotDesktop          json.RawMessage `json:"controlSnapshotDesktop"`
	} `json:"rest"`
	ClientFrames map[string]json.RawMessage `json:"clientFrames"`
	ServerEvents map[string]json.RawMessage `json:"serverEvents"`
	Errors       map[string]json.RawMessage `json:"errors"`
	Invalid      map[string]json.RawMessage `json:"invalid"`
}

func loadFixture(t *testing.T) *fixtureEnvelope {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(wd, fixturePath))
	if err != nil {
		t.Fatalf("read shared fixture: %v", err)
	}
	var fx fixtureEnvelope
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return &fx
}

func normalize(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("normalize %q: %v", string(b), err)
	}
	return v
}

func assertJSONEqual(t *testing.T, got []byte, want json.RawMessage) {
	t.Helper()
	if !reflect.DeepEqual(normalize(t, got), normalize(t, want)) {
		t.Fatalf("JSON mismatch\n got: %s\nwant: %s", string(got), string(want))
	}
}

func strPtr(s string) *string     { return &s }
func boolPtr(b bool) *bool        { return &b }
func gapPtr(g GapRange) *GapRange { return &g }

func stringsEqualExact(a, b []string) bool {
	return reflect.DeepEqual(a, b)
}

// ---------------------------------------------------------------------------
// (1) Full manifest parity — endpoint objects + every enum array.
// ---------------------------------------------------------------------------

func TestManifests_FullParity(t *testing.T) {
	fx := loadFixture(t)
	m := fx.Manifest

	if string(APIVersionV1) != m.APIVersion {
		t.Errorf("APIVersionV1 = %q, want %q", APIVersionV1, m.APIVersion)
	}
	if RequestIDHeader != m.RequestIDHeader {
		t.Errorf("RequestIDHeader mismatch")
	}
	if RESTBasePath != m.RESTBasePath {
		t.Errorf("RESTBasePath mismatch")
	}
	if WebSocketV1Path != m.WebSocketV1Path {
		t.Errorf("WebSocketV1Path mismatch")
	}
	// Endpoint full-object comparison (method + path + status), not path-only.
	if len(V1RestEndpoints) != len(m.RestEndpoints) {
		t.Fatalf("V1RestEndpoints count = %d, want %d", len(V1RestEndpoints), len(m.RestEndpoints))
	}
	for i := range V1RestEndpoints {
		if V1RestEndpoints[i] != m.RestEndpoints[i] {
			t.Errorf("endpoint[%d] = %+v, want %+v", i, V1RestEndpoints[i], m.RestEndpoints[i])
		}
	}
	// Enum arrays.
	check := func(got []string, want []string, name string) {
		t.Helper()
		if !stringsEqualExact(got, want) {
			t.Errorf("%s mismatch (order-sensitive)\n got: %v\nwant: %v", name, got, want)
		}
	}
	check(KnownClientFrameTypes, m.ClientFrameTypes, "clientFrameTypes")
	check(KnownServerEventTypes, m.ServerEventTypes, "serverEventTypes")
	check(codesToStrings(KnownErrorCodes), m.ErrorCodes, "errorCodes")
	check(cliToStrings(KnownCLITypes), m.CLITypes, "cliTypes")
	check(sessionStateStrings(KnownSessionStates), m.SessionStates, "sessionStates")
	check(controlStateStrings(KnownControlStates), m.ControlStates, "controlStates")
	check(historyStateStrings(KnownHistoryStates), m.HistoryStates, "historyStates")
	check(layerStrings(KnownErrorLayers), m.ErrorLayers, "errorLayers")
	check(hintStrings(KnownActionHints), m.ActionHints, "actionHints")

	if len(V1RestEndpoints) != 10 || len(KnownClientFrameTypes) != 5 || len(KnownServerEventTypes) != 7 || len(KnownErrorCodes) != 12 {
		t.Errorf("frozen counts changed: endpoints=%d client=%d server=%d errors=%d", len(V1RestEndpoints), len(KnownClientFrameTypes), len(KnownServerEventTypes), len(KnownErrorCodes))
	}
}

func codesToStrings(c []ErrorCode) []string         { return anyStrings(c) }
func cliToStrings(c []CLIType) []string             { return anyStrings(c) }
func sessionStateStrings(c []SessionState) []string { return anyStrings(c) }
func controlStateStrings(c []ControlState) []string { return anyStrings(c) }
func historyStateStrings(c []HistoryState) []string { return anyStrings(c) }
func layerStrings(c []ErrorLayer) []string          { return anyStrings(c) }
func hintStrings(c []ActionHint) []string           { return anyStrings(c) }

func anyStrings[T ~string](c []T) []string {
	out := make([]string, len(c))
	for i, v := range c {
		out[i] = string(v)
	}
	return out
}

// ---------------------------------------------------------------------------
// (2) REST request Decode (production ingress) + response Marshal (egress).
// ---------------------------------------------------------------------------

func TestREST_DecodeRequests(t *testing.T) {
	fx := loadFixture(t)
	pcr, err := DecodePairingCompleteRequest(fx.REST.PairingCompleteRequest)
	if err != nil {
		t.Fatalf("DecodePairingCompleteRequest: %v", err)
	}
	if pcr.Code == "" || pcr.DeviceName == "" {
		t.Fatalf("decoded pairing request has empty field: %+v", pcr)
	}
	if _, err := DecodeCreateSessionRequest(fx.REST.CreateSessionRequest); err != nil {
		t.Fatalf("DecodeCreateSessionRequest (no workdir): %v", err)
	}
	if cs, err := DecodeCreateSessionRequest(fx.REST.CreateSessionRequestWithWorkdir); err != nil {
		t.Fatalf("DecodeCreateSessionRequest (workdir): %v", err)
	} else if cs.Workdir == nil {
		t.Fatalf("workdir not decoded")
	}
	if _, err := DecodeConfirmActionRequest(fx.REST.ConfirmActionRequest); err != nil {
		t.Fatalf("DecodeConfirmActionRequest: %v", err)
	}
}

func TestREST_MarshalResponses(t *testing.T) {
	fx := loadFixture(t)
	fourCLI := []CLIAvailability{
		{CLIType: CLITypeClaudeCode, Available: true},
		{CLIType: CLITypeOpenCode, Available: true},
		{CLIType: CLITypeCodex, Available: false},
		{CLIType: CLITypePi, Available: true},
	}
	pairing := PairingCompleteResponse{
		Device: DeviceSummary{ID: "dev_opaque_1", Name: "Maorun iPhone", PairedAt: "2026-08-02T01:02:03Z"},
		Host:   HostSummary{APIVersion: APIVersionV1, ServerVersion: "1.2.60", CLIAvailability: fourCLI},
	}
	b, err := MarshalRESTResponse(pairing)
	if err != nil {
		t.Fatalf("MarshalRESTResponse pairing: %v", err)
	}
	assertJSONEqual(t, b, fx.REST.PairingCompleteResponse)

	host := HostSummary{APIVersion: APIVersionV1, ServerVersion: "1.2.60", CLIAvailability: fourCLI}
	bh, err := MarshalRESTResponse(host)
	if err != nil {
		t.Fatalf("MarshalRESTResponse host: %v", err)
	}
	assertJSONEqual(t, bh, fx.REST.HostSummary)

	// SessionList empty marshals as [].
	le, err := MarshalRESTResponse(SessionList{})
	if err != nil {
		t.Fatalf("MarshalRESTResponse SessionList{}: %v", err)
	}
	assertJSONEqual(t, le, fx.REST.SessionListEmpty)

	// SessionDetail.
	summary := SessionSummary{
		ID: "sess_opaque_1", Title: "Fix remote contract", CLIType: CLITypeClaudeCode,
		State: SessionStateRunning, Control: ControlSnapshot{State: ControlStateYou},
		LastActivityAt: "2026-08-02T01:03:00Z",
	}
	detail := SessionDetail{SessionSummary: summary, Workdir: "/workspace/project", StartedAt: "2026-08-02T01:00:00Z", EarliestSeq: 41, LatestSeq: 77}
	bd, err := MarshalRESTResponse(detail)
	if err != nil {
		t.Fatalf("MarshalRESTResponse detail: %v", err)
	}
	assertJSONEqual(t, bd, fx.REST.SessionDetail)

	// ControlSnapshot four variants via RESTResponse marshal.
	for _, c := range []struct {
		name string
		val  ControlSnapshot
		raw  json.RawMessage
	}{
		{"none", ControlSnapshot{State: ControlStateNone}, fx.REST.ControlSnapshotNone},
		{"you", ControlSnapshot{State: ControlStateYou}, fx.REST.ControlSnapshotYou},
		{"other", ControlSnapshot{State: ControlStateOther, DeviceName: strPtr("iPad")}, fx.REST.ControlSnapshotOther},
		{"desktop", ControlSnapshot{State: ControlStateDesktop}, fx.REST.ControlSnapshotDesktop},
	} {
		b, err := MarshalRESTResponse(c.val)
		if err != nil {
			t.Fatalf("MarshalRESTResponse control %s: %v", c.name, err)
		}
		assertJSONEqual(t, b, c.raw)
	}
}

// ---------------------------------------------------------------------------
// (3) Client frames Decode (production ingress).
// ---------------------------------------------------------------------------

func TestClientFrames_Decode(t *testing.T) {
	fx := loadFixture(t)
	for _, key := range []string{"attach", "attachWithLastSeq", "input", "resize", "backfill", "ping"} {
		raw, ok := fx.ClientFrames[key]
		if !ok {
			t.Fatalf("missing client frame %q", key)
		}
		f, err := DecodeClientFrame(raw)
		if err != nil {
			t.Fatalf("DecodeClientFrame %s: %v", key, err)
		}
		if rid := clientRequestID(f); rid == "" {
			t.Fatalf("client frame %s has empty requestId", key)
		}
	}
	// attachWithLastSeq surfaces the optional cursor.
	af, err := DecodeClientFrame(fx.ClientFrames["attachWithLastSeq"])
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := af.(AttachFrame); !ok || a.LastSeq == nil || *a.LastSeq != 40 {
		t.Fatalf("attachWithLastSeq lastSeq = %v", a.LastSeq)
	}
}

func clientRequestID(f ClientFrame) RequestID {
	switch v := f.(type) {
	case AttachFrame:
		return v.RequestID
	case InputFrame:
		return v.RequestID
	case ResizeFrame:
		return v.RequestID
	case BackfillFrame:
		return v.RequestID
	case PingFrame:
		return v.RequestID
	}
	return ""
}

// ---------------------------------------------------------------------------
// (4) Server events Decode/Validate/Marshal (production) — all 9 concrete
// categories + the attached-gap sample.
// ---------------------------------------------------------------------------

func TestServerEvents_DecodeValidateMarshal(t *testing.T) {
	fx := loadFixture(t)
	knownKeys := []string{
		"sessionAttachedEmptyHistory", "sessionAttachedWithHistory", "sessionAttachedGap",
		"output", "backfillResultFrames", "backfillResultGap",
		"sessionStateExited", "sessionStateRestartBoundary",
		"controlStateOther", "controlStateYou", "controlStateNone", "controlStateDesktop",
		"authRevoked", "error",
	}
	for _, key := range knownKeys {
		raw := fx.ServerEvents[key]
		ev, err := DecodeKnownServerEvent(raw)
		if err != nil {
			t.Fatalf("DecodeKnownServerEvent %s: %v", key, err)
		}
		if err := ValidateServerEvent(ev); err != nil {
			t.Fatalf("ValidateServerEvent %s: %v", key, err)
		}
		// Re-marshal and compare semantically (producer round-trip).
		b, err := MarshalServerEvent(ev)
		if err != nil {
			t.Fatalf("MarshalServerEvent %s: %v", key, err)
		}
		assertJSONEqual(t, b, raw)
	}
}

// ---------------------------------------------------------------------------
// (5) Errors Marshal (production egress) + version mismatch details.
// ---------------------------------------------------------------------------

func TestErrors_Marshal(t *testing.T) {
	fx := loadFixture(t)
	cases := []struct {
		key string
		val APIError
	}{
		{"netUnreachable", APIError{RequestID: "req_http_1", Code: ErrorCodeNetUnreachable, Layer: ErrorLayerConnection, Message: "network unreachable", ActionHint: ActionHintRetry}},
		{"sessionLaunchFailed", APIError{RequestID: "req_http_7", Code: ErrorCodeSessionLaunchFailed, Layer: ErrorLayerSession, Message: "session launch failed", ActionHint: ActionHintCheckDesktop, Details: Details{DetailKeyCLIType: string(CLITypePi)}}},
		{"versionMismatch", APIError{RequestID: "req_attach_3", Code: ErrorCodeBadRequest, Layer: ErrorLayerConnection, Message: "unsupported api version", ActionHint: ActionHintUpgradeClient, Details: Details{DetailKeyReason: DetailReasonUnsupportedAPIVersion, "receivedApiVersion": "v2", "supportedApiVersions": []string{"v1"}}}},
	}
	for _, c := range cases {
		b, err := MarshalAPIError(c.val)
		if err != nil {
			t.Fatalf("MarshalAPIError %s: %v", c.key, err)
		}
		assertJSONEqual(t, b, fx.Errors[c.key])
	}
}

// ---------------------------------------------------------------------------
// (6) Required zero values survive (no omitempty) + required-slice nil rejected.
// ---------------------------------------------------------------------------

func TestRequiredZeroValues_NotOmitted(t *testing.T) {
	attached := SessionAttachedEvent{
		Type: ServerEventTypeSessionAttached, RequestID: "r", APIVersion: APIVersionV1, SessionID: "s",
		History: []ReplayFrame{}, EarliestSeq: 0, LatestSeq: 0,
		Snapshot: FiveLayerSnapshot{
			Connection: ConnectionSnapshot{State: AttachedConnectionState}, Auth: AuthSnapshot{State: AttachedAuthState},
			Session: SessionSnapshot{State: SessionStateRunning}, Control: ControlSnapshot{State: ControlStateNone},
			History: HistorySnapshot{State: HistoryStateContinuous},
		},
	}
	b, err := MarshalServerEvent(attached)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"earliestSeq":0`, `"latestSeq":0`, `"history":[]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("zero-value field lost: %q missing from %s", want, string(b))
		}
	}
}

func TestRequiredSlices_NilRejected(t *testing.T) {
	// HostSummary.CLIAvailability nil.
	if _, err := MarshalRESTResponse(HostSummary{APIVersion: APIVersionV1, ServerVersion: "v", CLIAvailability: nil}); err == nil {
		t.Errorf("HostSummary with nil CLIAvailability must be rejected")
	}
	// SessionList nil.
	if _, err := MarshalRESTResponse(SessionList(nil)); err == nil {
		t.Errorf("SessionList(nil) must be rejected")
	}
	// SessionAttachedEvent.History nil.
	if _, err := MarshalServerEvent(SessionAttachedEvent{Type: ServerEventTypeSessionAttached, RequestID: "r", APIVersion: APIVersionV1, SessionID: "s", History: nil, EarliestSeq: 0, LatestSeq: 0, Snapshot: minimalSnapshot()}); err == nil {
		t.Errorf("SessionAttachedEvent with nil History must be rejected")
	}
	// BackfillFramesResultEvent.Frames nil.
	bf := BackfillFramesResultEvent{Type: ServerEventTypeBackfillResult, RequestID: "r", SessionID: "s", FromSeq: 1, ToSeq: 2, EarliestSeq: 1, LatestSeq: 5, Frames: nil}
	if _, err := MarshalServerEvent(bf); err == nil {
		t.Errorf("BackfillFramesResultEvent with nil Frames must be rejected")
	}
	// non-nil empty Frames also rejected (success range must have >= 1 frame).
	bf.Frames = []ReplayFrame{}
	if _, err := MarshalServerEvent(bf); err == nil {
		t.Errorf("BackfillFramesResultEvent with empty Frames must be rejected")
	}
}

func minimalSnapshot() FiveLayerSnapshot {
	return FiveLayerSnapshot{
		Connection: ConnectionSnapshot{State: AttachedConnectionState}, Auth: AuthSnapshot{State: AttachedAuthState},
		Session: SessionSnapshot{State: SessionStateRunning}, Control: ControlSnapshot{State: ControlStateNone},
		History: HistorySnapshot{State: HistoryStateContinuous},
	}
}

// ---------------------------------------------------------------------------
// (7) Seq boundaries: max ok; max+1 / 0 replay / negative / fractional reject.
// ---------------------------------------------------------------------------

func TestSeqBoundaries(t *testing.T) {
	// max is valid (replay seq).
	maxOut := OutputEvent{Type: ServerEventTypeOutput, SessionID: "s", Seq: MaxSeqSafeInteger, Chunk: "YQ=="}
	if err := ValidateServerEvent(maxOut); err != nil {
		t.Errorf("seq = MaxSeqSafeInteger should be valid: %v", err)
	}
	// max+1 invalid.
	if err := ValidateServerEvent(OutputEvent{Type: ServerEventTypeOutput, SessionID: "s", Seq: MaxSeqSafeInteger + 1, Chunk: "YQ=="}); err == nil {
		t.Errorf("seq = MaxSeqSafeInteger+1 should be rejected")
	}
	// replay seq 0 invalid.
	if err := ValidateServerEvent(OutputEvent{Type: ServerEventTypeOutput, SessionID: "s", Seq: 0, Chunk: "YQ=="}); err == nil {
		t.Errorf("replay seq = 0 should be rejected")
	}
}

// ---------------------------------------------------------------------------
// (8) Invalid fixture: every key rejected by production API; consumed-set parity.
// ---------------------------------------------------------------------------

func TestInvalid_AllRejected(t *testing.T) {
	fx := loadFixture(t)

	// Map each invalid key to the production decoder that must reject it.
	// Server-event invalids → DecodeKnownServerEvent; client → DecodeClientFrame;
	// confirm → DecodeConfirmActionRequest.
	serverInvalid := map[string]bool{
		"framesAndGapBothPresent": true, "backfillNeitherFramesNorGap": true,
		"controlOtherMissingDeviceName": true, "controlNoneWithDeviceName": true,
		"controlYouWithDeviceName": true, "controlDesktopWithDeviceName": true,
		"knownOutputNullChunk": true, "knownSessionUnknownState": true,
		"knownAttachedUnknownAuthState": true, "knownAttachedUnknownHistoryState": true,
		"knownControlUnknownState": true,
		"historyGapMissingRange":   true, "historyGapNullRange": true,
		"historyContinuousWithRange": true, "historyBackfilledWithRange": true,
		"unsafeSeqAboveMax": true, "unsafeSeqFractional": true,
		"unsafeSeqNegative": true, "replaySeqZero": true,
		"attachedGapRangeMismatch":     true,
		"outputStructuredExpectedNull": true, "errorRequestIdNull": true, "errorDetailsNull": true,
		"sessionStateRestartFalse": true, "sessionStateSeqAlone": true,
		"backfillFrameOutOfRange": true, "backfillFrameNonAscending": true,
	}
	clientInvalid := map[string]bool{
		"nullRequiredField": true, "missingRequiredField": true,
		"unknownClientFrame": true, "unknownClientField": true,
	}
	confirmInvalid := map[string]bool{
		"confirmFalse": true, "confirmNull": true, "confirmMissing": true,
	}

	// Consumed-set parity: every fixture invalid key must be mapped.
	consumed := make(map[string]bool, len(fx.Invalid))
	for k := range serverInvalid {
		consumed[k] = true
	}
	for k := range clientInvalid {
		consumed[k] = true
	}
	for k := range confirmInvalid {
		consumed[k] = true
	}
	if len(consumed) != len(fx.Invalid) {
		var missing []string
		for k := range fx.Invalid {
			if !consumed[k] {
				missing = append(missing, k)
			}
		}
		t.Fatalf("invalid fixture keys not consumed by any test: %v", missing)
	}

	for key := range serverInvalid {
		if _, err := DecodeKnownServerEvent(fx.Invalid[key]); err == nil {
			t.Errorf("server invalid %q must be rejected by DecodeKnownServerEvent", key)
		}
	}
	for key := range clientInvalid {
		if _, err := DecodeClientFrame(fx.Invalid[key]); err == nil {
			t.Errorf("client invalid %q must be rejected by DecodeClientFrame", key)
		}
	}
	for key := range confirmInvalid {
		if _, err := DecodeConfirmActionRequest(fx.Invalid[key]); err == nil {
			t.Errorf("confirm invalid %q must be rejected by DecodeConfirmActionRequest", key)
		}
	}
}

// ---------------------------------------------------------------------------
// (9) Marker interfaces compile + ReplaySeq.
// ---------------------------------------------------------------------------

func TestMarkerInterfaces(t *testing.T) {
	var _ ClientFrame = AttachFrame{}
	var _ ClientFrame = InputFrame{}
	var _ ClientFrame = ResizeFrame{}
	var _ ClientFrame = BackfillFrame{}
	var _ ClientFrame = PingFrame{}

	var _ KnownServerEvent = SessionAttachedEvent{}
	var _ KnownServerEvent = OutputEvent{}
	var _ KnownServerEvent = BackfillFramesResultEvent{}
	var _ KnownServerEvent = BackfillGapResultEvent{}
	var _ KnownServerEvent = SessionStateEvent{}
	var _ KnownServerEvent = SessionRestartBoundaryEvent{}
	var _ KnownServerEvent = ControlStateEvent{}
	var _ KnownServerEvent = AuthRevokedEvent{}
	var _ KnownServerEvent = ErrorEvent{}

	var _ ReplayFrame = OutputEvent{}
	var _ ReplayFrame = SessionRestartBoundaryEvent{}
	var _ BackfillResultEvent = BackfillFramesResultEvent{}
	var _ BackfillResultEvent = BackfillGapResultEvent{}
	var _ RESTResponse = SessionList{}

	if (OutputEvent{Seq: 7}).ReplaySeq() != 7 {
		t.Fatal("OutputEvent.ReplaySeq")
	}
	if (SessionRestartBoundaryEvent{Seq: 9}).ReplaySeq() != 9 {
		t.Fatal("SessionRestartBoundaryEvent.ReplaySeq")
	}
}

// ---------------------------------------------------------------------------
// (10) Trailing JSON after a valid object must be rejected (round2 probe 4).
// strictFields must Decode again after the first object and require io.EOF,
// not merely dec.More() (which misses `]`/extra-value edge cases).
// ---------------------------------------------------------------------------

func TestTrailingJSON_Rejected(t *testing.T) {
	// A valid ping frame followed by a trailing `]` must be rejected.
	raw := []byte(`{"type":"ping","requestId":"r"}]`)
	if _, err := DecodeClientFrame(raw); err == nil {
		t.Errorf("trailing `]` after valid object must be rejected")
	}
	// Two concatenated JSON objects must be rejected.
	raw2 := []byte(`{"type":"ping","requestId":"r"}{"type":"ping","requestId":"r2"}`)
	if _, err := DecodeClientFrame(raw2); err == nil {
		t.Errorf("second JSON object after valid object must be rejected")
	}
	// A valid object alone still decodes (regression guard).
	if _, err := DecodeClientFrame([]byte(`{"type":"ping","requestId":"r"}`)); err != nil {
		t.Errorf("valid single object must decode: %v", err)
	}
}

// ---------------------------------------------------------------------------
// (11) Manifest order sensitivity (round2 probe 7). A reordered enum array
// must NOT compare equal to the canonical order.
// ---------------------------------------------------------------------------

func TestManifest_OrderSensitive(t *testing.T) {
	canonical := KnownClientFrameTypes
	reordered := []string{canonical[1], canonical[0], canonical[2], canonical[3], canonical[4]}
	if reflect.DeepEqual(canonical, reordered) {
		t.Fatalf("reorder negative broken: reordered equal to canonical (test wiring)")
	}
	if stringsEqualExact(canonical, reordered) {
		t.Errorf("stringsEqualExact must be order-sensitive; reordered array must NOT equal canonical")
	}
	// Sanity: identical order compares equal.
	if !stringsEqualExact(canonical, append([]string(nil), canonical...)) {
		t.Errorf("stringsEqualExact must equal identical-order array")
	}
}
