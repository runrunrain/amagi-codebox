package contract

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrorLayer enumerates the five error layers. bad_request chooses the layer
// of the affected concern.
type ErrorLayer string

const (
	ErrorLayerConnection ErrorLayer = "connection"
	ErrorLayerAuth       ErrorLayer = "auth"
	ErrorLayerSession    ErrorLayer = "session"
	ErrorLayerControl    ErrorLayer = "control"
	ErrorLayerHistory    ErrorLayer = "history"
)

// KnownErrorLayers is the complete set of five error layers.
var KnownErrorLayers = []ErrorLayer{
	ErrorLayerConnection,
	ErrorLayerAuth,
	ErrorLayerSession,
	ErrorLayerControl,
	ErrorLayerHistory,
}

// ErrorCode enumerates the 12 stable v1 error codes. session.launch_failed is
// the stable base code; the dynamic {cliType} parameter travels in Details
// (design I-10), NOT as a separate code.
type ErrorCode string

const (
	ErrorCodeNetUnreachable      ErrorCode = "net.unreachable"
	ErrorCodeServiceDown         ErrorCode = "service.down"
	ErrorCodeAuthUnpaired        ErrorCode = "auth.unpaired"
	ErrorCodeAuthWindowExpired   ErrorCode = "auth.window_expired"
	ErrorCodeAuthRevoked         ErrorCode = "auth.revoked"
	ErrorCodeSessionNotFound     ErrorCode = "session.not_found"
	ErrorCodeSessionLaunchFailed ErrorCode = "session.launch_failed"
	ErrorCodeControlBusy         ErrorCode = "control.busy"
	ErrorCodeControlForbidden    ErrorCode = "control.forbidden"
	ErrorCodeHistoryGap          ErrorCode = "history.gap"
	ErrorCodeRateLimited         ErrorCode = "rate.limited"
	ErrorCodeBadRequest          ErrorCode = "bad_request"
)

// KnownErrorCodes is the complete set of 12 stable v1 error codes.
var KnownErrorCodes = []ErrorCode{
	ErrorCodeNetUnreachable,
	ErrorCodeServiceDown,
	ErrorCodeAuthUnpaired,
	ErrorCodeAuthWindowExpired,
	ErrorCodeAuthRevoked,
	ErrorCodeSessionNotFound,
	ErrorCodeSessionLaunchFailed,
	ErrorCodeControlBusy,
	ErrorCodeControlForbidden,
	ErrorCodeHistoryGap,
	ErrorCodeRateLimited,
	ErrorCodeBadRequest,
}

// ActionHint is the opaque client-recovery action token. Clients show a generic
// recovery action for unknown values.
type ActionHint string

const (
	ActionHintRetry              ActionHint = "retry"
	ActionHintCheckDesktop       ActionHint = "check-desktop"
	ActionHintRePair             ActionHint = "re-pair"
	ActionHintRequestControl     ActionHint = "request-control"
	ActionHintContinueFromLatest ActionHint = "continue-from-latest"
	ActionHintUpgradeClient      ActionHint = "upgrade-client"
)

// KnownActionHints is the complete set of six known action hints. ActionHint is
// an open string, so unknown values are still valid wire; this slice is for
// manifest parity only.
var KnownActionHints = []ActionHint{
	ActionHintRetry,
	ActionHintCheckDesktop,
	ActionHintRePair,
	ActionHintRequestControl,
	ActionHintContinueFromLatest,
	ActionHintUpgradeClient,
}

// Details is the error extension object. Values MUST be safe structured data
// only — never credentials, tokens, raw terminal content or internal stacks.
type Details map[string]any

// Structured detail keys used by v1.
const (
	// DetailKeyReason is the structured reason inside bad_request details.
	DetailKeyReason = "reason"
	// DetailReasonUnsupportedAPIVersion marks a version-mismatch bad_request.
	DetailReasonUnsupportedAPIVersion = "unsupported_api_version"
	// DetailKeyCLIType carries the failing CLI type for session.launch_failed.
	DetailKeyCLIType = "cliType"
)

// APIError is the unified REST error body. REST requestId is REQUIRED and
// equals the X-Request-ID response header. There is no {error:{...}} envelope:
// the object IS the top-level body. The WS error event is a separate type
// (ErrorEvent) whose requestId is conditional, not this struct.
type APIError struct {
	RequestID  RequestID  `json:"requestId"`
	Code       ErrorCode  `json:"code"`
	Layer      ErrorLayer `json:"layer"`
	Message    string     `json:"message"`
	ActionHint ActionHint `json:"actionHint"`
	Details    Details    `json:"details,omitempty"`
}

// Version-mismatch detail payload (design I-11): expressed as bad_request +
// details rather than a new top-level error code.
type UnsupportedAPIVersionDetails struct {
	Reason              string   `json:"reason"` // DetailReasonUnsupportedAPIVersion
	ReceivedAPIVersion  string   `json:"receivedApiVersion,omitempty"`
	SupportedAPIVersion []string `json:"supportedApiVersions"`
}

// isKnownErrorLayer reports whether layer is one of the five closed layers.
func isKnownErrorLayer(layer ErrorLayer) bool {
	for _, k := range KnownErrorLayers {
		if k == layer {
			return true
		}
	}
	return false
}

// ValidateAPIError enforces the REST error-body contract: required non-empty
// requestId, non-empty code (open string), closed layer, non-empty message,
// non-empty actionHint (open string). Details is optional; if present it must
// be a non-nil map. It never concatenates raw payload or credential values.
func ValidateAPIError(value APIError) error {
	if value.RequestID == "" {
		return errors.New("contract: APIError.RequestID must be non-empty")
	}
	if value.Code == "" {
		return errors.New("contract: APIError.Code must be non-empty")
	}
	if !isKnownErrorLayer(value.Layer) {
		return fmt.Errorf("contract: APIError.Layer %q is not a known error layer", value.Layer)
	}
	if value.Message == "" {
		return errors.New("contract: APIError.Message must be non-empty")
	}
	if value.ActionHint == "" {
		return errors.New("contract: APIError.ActionHint must be non-empty")
	}
	return nil
}

// MarshalAPIError validates then marshals an APIError. It produces no bytes on
// validation failure.
func MarshalAPIError(value APIError) ([]byte, error) {
	if err := ValidateAPIError(value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
