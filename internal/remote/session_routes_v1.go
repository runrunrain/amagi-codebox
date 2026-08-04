package remote

// session_routes_v1.go — M2-A session REST endpoint handlers (design §5).
//
// These handlers are registered by registerV1Routes (indices 2-9) when the
// sessionAdapter is wired. Each handler does ONLY:
//   - read the body / sessionID (already extracted + validated by the dispatcher);
//   - call the adapter (which encapsulates all gate/raw/journal logic);
//   - marshal + write the response (or error).
//
// Handlers NEVER call raw ports, construct permits, or leak internal errors.

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Body caps (design §5.1)
// ---------------------------------------------------------------------------

const (
	// createBodyCap bounds the create-session request body (4 KiB).
	createBodyCap = 4 << 10
	// confirmBodyCap bounds the stop/restart/remove request body (1 KiB).
	confirmBodyCap = 1 << 10
)

// ---------------------------------------------------------------------------
// Sessions List (endpoint 2: GET /sessions)
// ---------------------------------------------------------------------------

func (s *Server) handleV1SessionsList(w http.ResponseWriter, r *http.Request, ep contract.RestEndpoint, reqID contract.RequestID, corsAllowed bool, principal v1Principal, sessionID contract.SessionID) {
	adapter := s.sessionAdapter
	result, aerr := adapter.ListSessions(r.Context(), principal.DeviceID)
	if aerr != nil {
		aerr.Rest.body.RequestID = reqID
		aerr.Rest.write(w)
		return
	}
	body, merr := contract.MarshalRESTResponse(result.List)
	if merr != nil {
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "service unavailable", contract.ActionHintCheckDesktop)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ep.SuccessStatus)
	_, _ = w.Write(body)
}

// ---------------------------------------------------------------------------
// Session Detail (endpoint 3: GET /sessions/{id})
// ---------------------------------------------------------------------------

func (s *Server) handleV1SessionDetail(w http.ResponseWriter, r *http.Request, ep contract.RestEndpoint, reqID contract.RequestID, corsAllowed bool, principal v1Principal, sessionID contract.SessionID) {
	adapter := s.sessionAdapter
	result, aerr := adapter.SessionDetail(r.Context(), reqID, sessionID, principal.DeviceID)
	if aerr != nil {
		aerr.Rest.body.RequestID = reqID
		aerr.Rest.write(w)
		return
	}
	body, merr := contract.MarshalRESTResponse(result.Detail)
	if merr != nil {
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "service unavailable", contract.ActionHintCheckDesktop)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ep.SuccessStatus)
	_, _ = w.Write(body)
}

// ---------------------------------------------------------------------------
// Session Create (endpoint 4: POST /sessions)
// ---------------------------------------------------------------------------

func (s *Server) handleV1SessionCreate(w http.ResponseWriter, r *http.Request, ep contract.RestEndpoint, reqID contract.RequestID, corsAllowed bool, principal v1Principal, sessionID contract.SessionID) {
	adapter := s.sessionAdapter
	// Content-Type + body cap.
	ct := strings.TrimSpace(r.Header.Get("Content-Type"))
	media := strings.SplitN(ct, ";", 2)[0]
	if media != "application/json" {
		writeV1Error(w, reqID, http.StatusUnsupportedMediaType, contract.ErrorCodeBadRequest,
			contract.ErrorLayerSession, "invalid request", contract.ActionHintRetry)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, createBodyCap)
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(r.Body); err != nil {
		writeV1Error(w, reqID, http.StatusRequestEntityTooLarge, contract.ErrorCodeBadRequest,
			contract.ErrorLayerSession, "invalid request", contract.ActionHintRetry)
		return
	}
	req, derr := contract.DecodeCreateSessionRequest(raw.Bytes())
	if derr != nil {
		writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
			contract.ErrorLayerSession, "invalid request", contract.ActionHintRetry)
		return
	}
	result, aerr := adapter.CreateSession(r.Context(), reqID, principal, req)
	if aerr != nil {
		aerr.Rest.body.RequestID = reqID
		aerr.Rest.write(w)
		return
	}
	body, merr := contract.MarshalRESTResponse(result.Detail)
	if merr != nil {
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "service unavailable", contract.ActionHintCheckDesktop)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ep.SuccessStatus)
	_, _ = w.Write(body)
}

// ---------------------------------------------------------------------------
// Session Stop (endpoint 5: POST /sessions/{id}/stop)
// ---------------------------------------------------------------------------

func (s *Server) handleV1SessionStop(w http.ResponseWriter, r *http.Request, ep contract.RestEndpoint, reqID contract.RequestID, corsAllowed bool, principal v1Principal, sessionID contract.SessionID) {
	s.handleLifecycleAction(w, r, ep, reqID, principal, sessionID, func(ctx context.Context) (SessionDetailResult, *AdapterError) {
		return s.sessionAdapter.StopSession(ctx, reqID, principal, sessionID)
	})
}

// ---------------------------------------------------------------------------
// Session Restart (endpoint 6: POST /sessions/{id}/restart)
// ---------------------------------------------------------------------------

func (s *Server) handleV1SessionRestart(w http.ResponseWriter, r *http.Request, ep contract.RestEndpoint, reqID contract.RequestID, corsAllowed bool, principal v1Principal, sessionID contract.SessionID) {
	s.handleLifecycleAction(w, r, ep, reqID, principal, sessionID, func(ctx context.Context) (SessionDetailResult, *AdapterError) {
		return s.sessionAdapter.RestartSession(ctx, reqID, principal, sessionID)
	})
}

// handleLifecycleAction is the shared stop/restart handler: decode confirm
// body, call the adapter action, write the response.
func (s *Server) handleLifecycleAction(w http.ResponseWriter, r *http.Request, ep contract.RestEndpoint, reqID contract.RequestID, principal v1Principal, sessionID contract.SessionID, action func(context.Context) (SessionDetailResult, *AdapterError)) {
	// Content-Type + body cap.
	ct := strings.TrimSpace(r.Header.Get("Content-Type"))
	media := strings.SplitN(ct, ";", 2)[0]
	if media != "application/json" {
		writeV1Error(w, reqID, http.StatusUnsupportedMediaType, contract.ErrorCodeBadRequest,
			contract.ErrorLayerSession, "invalid request", contract.ActionHintRetry)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, confirmBodyCap)
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(r.Body); err != nil {
		writeV1Error(w, reqID, http.StatusRequestEntityTooLarge, contract.ErrorCodeBadRequest,
			contract.ErrorLayerSession, "invalid request", contract.ActionHintRetry)
		return
	}
	// Decode confirm body.
	_, derr := contract.DecodeConfirmActionRequest(raw.Bytes())
	if derr != nil {
		writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
			contract.ErrorLayerSession, "invalid request", contract.ActionHintRetry)
		return
	}
	result, aerr := action(r.Context())
	if aerr != nil {
		aerr.Rest.body.RequestID = reqID
		aerr.Rest.write(w)
		return
	}
	body, merr := contract.MarshalRESTResponse(result.Detail)
	if merr != nil {
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "service unavailable", contract.ActionHintCheckDesktop)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ep.SuccessStatus)
	_, _ = w.Write(body)
}

// ---------------------------------------------------------------------------
// Session Remove (endpoint 7: DELETE /sessions/{id})
// ---------------------------------------------------------------------------

func (s *Server) handleV1SessionRemove(w http.ResponseWriter, r *http.Request, ep contract.RestEndpoint, reqID contract.RequestID, corsAllowed bool, principal v1Principal, sessionID contract.SessionID) {
	// Content-Type + body cap.
	ct := strings.TrimSpace(r.Header.Get("Content-Type"))
	media := strings.SplitN(ct, ";", 2)[0]
	if media != "application/json" {
		writeV1Error(w, reqID, http.StatusUnsupportedMediaType, contract.ErrorCodeBadRequest,
			contract.ErrorLayerSession, "invalid request", contract.ActionHintRetry)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, confirmBodyCap)
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(r.Body); err != nil {
		writeV1Error(w, reqID, http.StatusRequestEntityTooLarge, contract.ErrorCodeBadRequest,
			contract.ErrorLayerSession, "invalid request", contract.ActionHintRetry)
		return
	}
	// Decode confirm body.
	_, derr := contract.DecodeConfirmActionRequest(raw.Bytes())
	if derr != nil {
		writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
			contract.ErrorLayerSession, "invalid request", contract.ActionHintRetry)
		return
	}
	aerr := s.sessionAdapter.RemoveSession(r.Context(), reqID, principal, sessionID)
	if aerr != nil {
		aerr.Rest.body.RequestID = reqID
		aerr.Rest.write(w)
		return
	}
	// 204 No Content.
	w.WriteHeader(ep.SuccessStatus)
}

// ---------------------------------------------------------------------------
// Control Acquire (endpoint 8: POST /sessions/{id}/control/acquire)
// ---------------------------------------------------------------------------

func (s *Server) handleV1ControlAcquire(w http.ResponseWriter, r *http.Request, ep contract.RestEndpoint, reqID contract.RequestID, corsAllowed bool, principal v1Principal, sessionID contract.SessionID) {
	// Acquire requires empty body (design §5.1).
	r.Body = http.MaxBytesReader(w, r.Body, 1)
	var peek [1]byte
	if n, _ := r.Body.Read(peek[:]); n > 0 {
		writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
			contract.ErrorLayerControl, "request body must be empty", contract.ActionHintRetry)
		return
	}
	// Get the current live lease for this device+session.
	var lease *ControlConnectionLease
	if s.sessionAdapter.Runtime() != nil {
		lease = s.sessionAdapter.Runtime().Directory().CurrentLease(principal.DeviceID, sessionID)
	}
	result, aerr := s.sessionAdapter.AcquireControl(r.Context(), reqID, principal, sessionID, lease)
	if aerr != nil {
		aerr.Rest.body.RequestID = reqID
		aerr.Rest.write(w)
		return
	}
	body, merr := contract.MarshalRESTResponse(result.Snapshot)
	if merr != nil {
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "service unavailable", contract.ActionHintCheckDesktop)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ep.SuccessStatus)
	_, _ = w.Write(body)
}

// ---------------------------------------------------------------------------
// Control Release (endpoint 9: POST /sessions/{id}/control/release)
// ---------------------------------------------------------------------------

func (s *Server) handleV1ControlRelease(w http.ResponseWriter, r *http.Request, ep contract.RestEndpoint, reqID contract.RequestID, corsAllowed bool, principal v1Principal, sessionID contract.SessionID) {
	// Release requires empty body.
	r.Body = http.MaxBytesReader(w, r.Body, 1)
	var peek [1]byte
	if n, _ := r.Body.Read(peek[:]); n > 0 {
		writeV1Error(w, reqID, http.StatusBadRequest, contract.ErrorCodeBadRequest,
			contract.ErrorLayerControl, "request body must be empty", contract.ActionHintRetry)
		return
	}
	result, aerr := s.sessionAdapter.ReleaseControl(r.Context(), reqID, principal, sessionID)
	if aerr != nil {
		aerr.Rest.body.RequestID = reqID
		aerr.Rest.write(w)
		return
	}
	body, merr := contract.MarshalRESTResponse(result.Snapshot)
	if merr != nil {
		writeV1Error(w, reqID, http.StatusServiceUnavailable, contract.ErrorCodeServiceDown,
			contract.ErrorLayerConnection, "service unavailable", contract.ActionHintCheckDesktop)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ep.SuccessStatus)
	_, _ = w.Write(body)
}
