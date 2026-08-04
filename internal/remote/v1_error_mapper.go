package remote

// v1_error_mapper.go — the SINGLE error mapper for REST handlers, WS actors,
// resolver and raw port (design §8.1).
//
// It maps internal typed causes (ControlGateError, LaunchResolveFailure,
// session lookup results) to wire ErrorCode/HTTP status/layer/message/action.
// Wire codes MUST come from KnownErrorCodes; raw err.Error(), CLI stderr,
// command, workdir, provider/model, device/holder ID are NEVER leaked.
//
// Design authority: §8.1 (error table), §8.2 (deny/raw cause mapping),
// §8.3 (launch failure classification).

import (
	"errors"
	"net/http"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// REST error result (status + contract APIError body)
// ---------------------------------------------------------------------------

// restError is the REST error outcome: HTTP status + the wire APIError body.
type restError struct {
	status int
	body   contract.APIError
}

// writeRest writes the restError as a JSON APIError response.
func (e restError) write(w http.ResponseWriter) {
	writeV1Error(w, e.body.RequestID, e.status, e.body.Code, e.body.Layer, e.body.Message, e.body.ActionHint)
}

// ---------------------------------------------------------------------------
// v1ErrorMapper
// ---------------------------------------------------------------------------

// v1ErrorMapper maps internal typed causes to wire error results. It is the
// sole mapper for the REST/WS layer (design §8.1: "一个 v1ErrorMapper 负责
// status/code/layer/message/action/details").
type v1ErrorMapper struct{}

// newV1ErrorMapper returns the singleton mapper (stateless).
func newV1ErrorMapper() v1ErrorMapper { return v1ErrorMapper{} }

// mapGateError maps a ControlGateError to a restError (design §8.2).
func (m v1ErrorMapper) mapGateError(reqID contract.RequestID, err error) (restError, bool) {
	kind, ok := extractControlDenyKind(err)
	if !ok {
		return restError{}, false
	}
	switch kind {
	case DenyBusy:
		return restError{
			status: http.StatusConflict,
			body:   newAPIError(reqID, contract.ErrorCodeControlBusy, contract.ErrorLayerControl, "control already held", contract.ActionHintRequestControl),
		}, true
	case DenyNotController, DenyNoAuthoritativeAttachment:
		return restError{
			status: http.StatusForbidden,
			body:   newAPIError(reqID, contract.ErrorCodeControlForbidden, contract.ErrorLayerControl, "control access denied", contract.ActionHintRequestControl),
		}, true
	case DenySessionNotFound:
		return restError{
			status: http.StatusNotFound,
			body:   newAPIError(reqID, contract.ErrorCodeSessionNotFound, contract.ErrorLayerSession, "session not found", contract.ActionHintRetry),
		}, true
	case DenySessionNotWritable:
		return restError{
			status: http.StatusConflict,
			body:   newAPIError(reqID, contract.ErrorCodeBadRequest, contract.ErrorLayerSession, "session is not writable", contract.ActionHintRetry),
		}, true
	case DenyDeviceRevoked:
		return restError{
			status: http.StatusUnauthorized,
			body:   newAPIError(reqID, contract.ErrorCodeAuthRevoked, contract.ErrorLayerAuth, "device revoked", contract.ActionHintRePair),
		}, true
	case DenyLifecycleInProgress:
		return restError{
			status: http.StatusConflict,
			body:   newAPIError(reqID, contract.ErrorCodeControlBusy, contract.ErrorLayerControl, "lifecycle operation in progress", contract.ActionHintRetry),
		}, true
	case DenyControlUnavailable, DenyNotAccepting, DenyShutdown, DenyStalePermit:
		return restError{
			status: http.StatusServiceUnavailable,
			body:   newAPIError(reqID, contract.ErrorCodeServiceDown, contract.ErrorLayerConnection, "control service unavailable", contract.ActionHintCheckDesktop),
		}, true
	default:
		return restError{
			status: http.StatusServiceUnavailable,
			body:   newAPIError(reqID, contract.ErrorCodeServiceDown, contract.ErrorLayerConnection, "control service unavailable", contract.ActionHintCheckDesktop),
		}, true
	}
}

// extractControlDenyKind extracts the ControlDenyKind from an error that may
// be either ControlGateError (value) or *ControlGateError (pointer), as
// different code paths return different representations.
func extractControlDenyKind(err error) (ControlDenyKind, bool) {
	if err == nil {
		return 0, false
	}
	var gErrVal ControlGateError
	if errors.As(err, &gErrVal) {
		return gErrVal.Kind, true
	}
	var gErrPtr *ControlGateError
	if errors.As(err, &gErrPtr) {
		return gErrPtr.Kind, true
	}
	return 0, false
}

// mapLaunchFailure maps a LaunchResolveFailure to a restError (design §8.3,
// AC-25).
func (m v1ErrorMapper) mapLaunchFailure(reqID contract.RequestID, lf *LaunchResolveFailure) restError {
	switch lf.Kind {
	case LaunchResolveFailureWorkdir:
		return restError{
			status: http.StatusBadRequest,
			body:   newAPIError(reqID, contract.ErrorCodeBadRequest, contract.ErrorLayerSession, "Working directory is invalid", contract.ActionHintRetry),
		}
	case LaunchResolveFailureContext:
		return restError{
			status: http.StatusUnprocessableEntity,
			body:   newLaunchFailedError(reqID, lf.CLIType, "Host launch configuration is unavailable"),
		}
	case LaunchResolveFailureCapability:
		return restError{
			status: http.StatusUnprocessableEntity,
			body:   newLaunchFailedError(reqID, lf.CLIType, "CLI is unavailable on this host"),
		}
	case LaunchResolveFailureEffect:
		return restError{
			status: http.StatusUnprocessableEntity,
			body:   newLaunchFailedError(reqID, lf.CLIType, "CLI session could not be started"),
		}
	default:
		return restError{
			status: http.StatusUnprocessableEntity,
			body:   newLaunchFailedError(reqID, lf.CLIType, "CLI session could not be started"),
		}
	}
}

// mapGenericError maps a non-typed error to service.down (design §8.1: raw
// errors never reach the wire; the safe fallback is service.down).
func (m v1ErrorMapper) mapGenericError(reqID contract.RequestID) restError {
	return restError{
		status: http.StatusServiceUnavailable,
		body:   newAPIError(reqID, contract.ErrorCodeServiceDown, contract.ErrorLayerConnection, "service unavailable", contract.ActionHintCheckDesktop),
	}
}

// mapJournalError maps a journal error to service.down (design §8.5.2: journal
// failure blocks dangerous operations with service.down + zero lifecycle effect).
func (m v1ErrorMapper) mapJournalError(reqID contract.RequestID) restError {
	return restError{
		status: http.StatusServiceUnavailable,
		body:   newAPIError(reqID, contract.ErrorCodeServiceDown, contract.ErrorLayerConnection, "operation journal unavailable", contract.ActionHintCheckDesktop),
	}
}

// ---------------------------------------------------------------------------
// REST error helpers
// ---------------------------------------------------------------------------

func newAPIError(reqID contract.RequestID, code contract.ErrorCode, layer contract.ErrorLayer, msg string, hint contract.ActionHint) contract.APIError {
	return contract.APIError{
		RequestID:  reqID,
		Code:       code,
		Layer:      layer,
		Message:    msg,
		ActionHint: hint,
	}
}

// newLaunchFailedError creates a session.launch_failed error with the frozen
// cliType detail (design §8.3: "details: {cliType}").
func newLaunchFailedError(reqID contract.RequestID, cliType contract.CLIType, msg string) contract.APIError {
	return contract.APIError{
		RequestID:  reqID,
		Code:       contract.ErrorCodeSessionLaunchFailed,
		Layer:      contract.ErrorLayerSession,
		Message:    msg,
		ActionHint: contract.ActionHintCheckDesktop,
		Details:    contract.Details{contract.DetailKeyCLIType: string(cliType)},
	}
}

// ---------------------------------------------------------------------------
// LaunchResolveFailure (design §5.4 CLIResolver seam)
// ---------------------------------------------------------------------------

// LaunchResolveFailureKind classifies why a launch resolution failed (design
// §5.4). Internal closed enum; never wire-visible.
type LaunchResolveFailureKind uint8

const (
	// LaunchResolveFailureWorkdir: path decode/不存在/非目录/不可进入.
	LaunchResolveFailureWorkdir LaunchResolveFailureKind = iota + 1
	// LaunchResolveFailureContext: default缺失/歧义、profile/secret ref失效.
	LaunchResolveFailureContext
	// LaunchResolveFailureCapability: CLIResolver.Resolve error、PTY unsupported.
	LaunchResolveFailureCapability
	// LaunchResolveFailureEffect: proxy/headroom/PTY/process/bootstrap fail
	// (after spec resolution; not a resolver error).
	LaunchResolveFailureEffect
)

func (k LaunchResolveFailureKind) String() string {
	switch k {
	case LaunchResolveFailureWorkdir:
		return "workdir"
	case LaunchResolveFailureContext:
		return "context"
	case LaunchResolveFailureCapability:
		return "capability"
	case LaunchResolveFailureEffect:
		return "effect"
	default:
		return "unknown"
	}
}

// LaunchResolveFailure carries a typed launch resolution failure.
type LaunchResolveFailure struct {
	Kind    LaunchResolveFailureKind
	CLIType contract.CLIType
}

// newLaunchResolveFailure constructs a typed failure.
func newLaunchResolveFailure(kind LaunchResolveFailureKind, cliType contract.CLIType) *LaunchResolveFailure {
	return &LaunchResolveFailure{Kind: kind, CLIType: cliType}
}
