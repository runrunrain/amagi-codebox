package remote

// m2a_error_mapper_test.go — Tests for v1ErrorMapper (design §8.1/§8.2) and
// v1 path matching (design §5.1).

import (
	"net/http"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Error mapper tests
// ---------------------------------------------------------------------------

func TestV1ErrorMapper_GateDenyBusy(t *testing.T) {
	m := newV1ErrorMapper()
	re, ok := m.mapGateError("req1", &ControlGateError{Kind: DenyBusy})
	if !ok {
		t.Fatal("expected mapped")
	}
	if re.status != http.StatusConflict {
		t.Fatalf("DenyBusy: expected 409, got %d", re.status)
	}
	if re.body.Code != contract.ErrorCodeControlBusy {
		t.Fatalf("DenyBusy: expected control.busy, got %s", re.body.Code)
	}
}

func TestV1ErrorMapper_GateDenyNotController(t *testing.T) {
	m := newV1ErrorMapper()
	re, ok := m.mapGateError("req1", &ControlGateError{Kind: DenyNotController})
	if !ok {
		t.Fatal("expected mapped")
	}
	if re.status != http.StatusForbidden {
		t.Fatalf("DenyNotController: expected 403, got %d", re.status)
	}
	if re.body.Code != contract.ErrorCodeControlForbidden {
		t.Fatalf("expected control.forbidden, got %s", re.body.Code)
	}
}

func TestV1ErrorMapper_GateDenySessionNotFound(t *testing.T) {
	m := newV1ErrorMapper()
	re, ok := m.mapGateError("req1", &ControlGateError{Kind: DenySessionNotFound})
	if !ok {
		t.Fatal("expected mapped")
	}
	if re.status != http.StatusNotFound {
		t.Fatalf("DenySessionNotFound: expected 404, got %d", re.status)
	}
	if re.body.Code != contract.ErrorCodeSessionNotFound {
		t.Fatalf("expected session.not_found, got %s", re.body.Code)
	}
}

func TestV1ErrorMapper_GateDenyDeviceRevoked(t *testing.T) {
	m := newV1ErrorMapper()
	re, ok := m.mapGateError("req1", &ControlGateError{Kind: DenyDeviceRevoked})
	if !ok {
		t.Fatal("expected mapped")
	}
	if re.status != http.StatusUnauthorized {
		t.Fatalf("DenyDeviceRevoked: expected 401, got %d", re.status)
	}
	if re.body.Code != contract.ErrorCodeAuthRevoked {
		t.Fatalf("expected auth.revoked, got %s", re.body.Code)
	}
}

func TestV1ErrorMapper_LaunchFailureWorkdir(t *testing.T) {
	m := newV1ErrorMapper()
	re := m.mapLaunchFailure("req1", newLaunchResolveFailure(LaunchResolveFailureWorkdir, contract.CLITypeClaudeCode))
	if re.status != http.StatusBadRequest {
		t.Fatalf("workdir failure: expected 400, got %d", re.status)
	}
	if re.body.Code != contract.ErrorCodeBadRequest {
		t.Fatalf("expected bad_request, got %s", re.body.Code)
	}
}

func TestV1ErrorMapper_LaunchFailureContext(t *testing.T) {
	m := newV1ErrorMapper()
	re := m.mapLaunchFailure("req1", newLaunchResolveFailure(LaunchResolveFailureContext, contract.CLITypeOpenCode))
	if re.status != http.StatusUnprocessableEntity {
		t.Fatalf("context failure: expected 422, got %d", re.status)
	}
	if re.body.Code != contract.ErrorCodeSessionLaunchFailed {
		t.Fatalf("expected session.launch_failed, got %s", re.body.Code)
	}
	// Should include cliType detail.
	if re.body.Details[contract.DetailKeyCLIType] != "opencode" {
		t.Fatalf("expected cliType=opencode in details, got %v", re.body.Details[contract.DetailKeyCLIType])
	}
}

func TestV1ErrorMapper_LaunchFailureCapability(t *testing.T) {
	m := newV1ErrorMapper()
	re := m.mapLaunchFailure("req1", newLaunchResolveFailure(LaunchResolveFailureCapability, contract.CLITypeCodex))
	if re.status != http.StatusUnprocessableEntity {
		t.Fatalf("capability failure: expected 422, got %d", re.status)
	}
	if re.body.Details[contract.DetailKeyCLIType] != "codex" {
		t.Fatalf("expected cliType=codex, got %v", re.body.Details[contract.DetailKeyCLIType])
	}
}

func TestV1ErrorMapper_GenericError(t *testing.T) {
	m := newV1ErrorMapper()
	re := m.mapGenericError("req1")
	if re.status != http.StatusServiceUnavailable {
		t.Fatalf("generic: expected 503, got %d", re.status)
	}
}

func TestV1ErrorMapper_NonGateError(t *testing.T) {
	m := newV1ErrorMapper()
	_, ok := m.mapGateError("req1", nil)
	if ok {
		t.Fatal("nil error should not map")
	}
}

// ---------------------------------------------------------------------------
// Path matching tests (design §5.1)
// ---------------------------------------------------------------------------

func TestMatchV1Path_StaticExact(t *testing.T) {
	sid, ok := matchV1Path("/api/remote/v1/sessions", "/api/remote/v1/sessions")
	if !ok {
		t.Fatal("exact static match should succeed")
	}
	if sid != "" {
		t.Fatalf("static match should have empty sessionID, got %s", sid)
	}
}

func TestMatchV1Path_DynamicID(t *testing.T) {
	sid, ok := matchV1Path("/api/remote/v1/sessions/{id}", "/api/remote/v1/sessions/abc123")
	if !ok {
		t.Fatal("dynamic match should succeed")
	}
	if sid != "abc123" {
		t.Fatalf("expected sessionID=abc123, got %s", sid)
	}
}

func TestMatchV1Path_DynamicIDWithSuffix(t *testing.T) {
	sid, ok := matchV1Path("/api/remote/v1/sessions/{id}/stop", "/api/remote/v1/sessions/abc123/stop")
	if !ok {
		t.Fatal("dynamic match with suffix should succeed")
	}
	if sid != "abc123" {
		t.Fatalf("expected sessionID=abc123, got %s", sid)
	}
}

func TestMatchV1Path_ControlAcquire(t *testing.T) {
	sid, ok := matchV1Path("/api/remote/v1/sessions/{id}/control/acquire", "/api/remote/v1/sessions/sess-xyz/control/acquire")
	if !ok {
		t.Fatal("control/acquire match should succeed")
	}
	if sid != "sess-xyz" {
		t.Fatalf("expected sessionID=sess-xyz, got %s", sid)
	}
}

func TestMatchV1Path_EncodedSlash(t *testing.T) {
	// Percent-encoded slash should be rejected.
	_, ok := matchV1Path("/api/remote/v1/sessions/{id}", "/api/remote/v1/sessions/foo%2Fbar")
	if ok {
		t.Fatal("encoded slash in ID should be rejected")
	}
}

func TestMatchV1Path_EmptyID(t *testing.T) {
	_, ok := matchV1Path("/api/remote/v1/sessions/{id}", "/api/remote/v1/sessions/")
	if ok {
		t.Fatal("empty ID should not match")
	}
}

func TestMatchV1Path_WrongPrefix(t *testing.T) {
	_, ok := matchV1Path("/api/remote/v1/sessions/{id}", "/api/remote/v1/other/abc")
	if ok {
		t.Fatal("wrong prefix should not match")
	}
}

func TestV1SpecByPath_SessionRoutes(t *testing.T) {
	specs := []v1RouteSpec{
		{endpointIndex: 2},
		{endpointIndex: 3},
		{endpointIndex: 5},
	}
	// /sessions
	m := v1SpecByPath(specs, "/api/remote/v1/sessions")
	if m == nil || m.spec.endpointIndex != 2 {
		t.Fatal("sessions list not matched")
	}
	// /sessions/{id}
	m = v1SpecByPath(specs, "/api/remote/v1/sessions/abc")
	if m == nil || m.spec.endpointIndex != 3 {
		t.Fatalf("sessions detail not matched: %v", m)
	}
	if m.sessionID != "abc" {
		t.Fatalf("expected sessionID=abc, got %s", m.sessionID)
	}
	// /sessions/{id}/stop
	m = v1SpecByPath(specs, "/api/remote/v1/sessions/abc/stop")
	if m == nil || m.spec.endpointIndex != 5 {
		t.Fatalf("sessions stop not matched: %v", m)
	}
}

func TestValidV1SessionID(t *testing.T) {
	if !validV1SessionID("abc") {
		t.Fatal("short valid ID rejected")
	}
	if validV1SessionID("") {
		t.Fatal("empty ID should be invalid")
	}
	// Long ID over limit.
	longID := make([]byte, MaxV1SessionIDBytes+1)
	for i := range longID {
		longID[i] = 'a'
	}
	if validV1SessionID(string(longID)) {
		t.Fatal("over-limit ID should be invalid")
	}
}
