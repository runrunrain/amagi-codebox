package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// PairingCompleteRequest is the body of POST /pairing/complete. It is the only
// unauthenticated endpoint. `code` is one-time pairing material and MUST NOT
// be logged (design §7.2, §13).
type PairingCompleteRequest struct {
	Code       string `json:"code"`
	DeviceName string `json:"deviceName"`
}

// DeviceSummary is the device projection returned by pairing. The Cookie is
// the ONLY device credential carrier; no credential/token/apiKey field appears
// in this or any response body.
type DeviceSummary struct {
	ID       DeviceID `json:"id"`
	Name     string   `json:"name"`
	PairedAt string   `json:"pairedAt"` // RFC3339 UTC 'Z'
}

// CLIAvailability reports whether one frozen CLI type is launchable.
type CLIAvailability struct {
	CLIType   CLIType `json:"cliType"`
	Available bool    `json:"available"` // required, no omitempty (false is meaningful)
}

// LaunchPathOption is a non-secret, host-owned path choice exposed to an
// authorized remote client. Path values are launch inputs, never credentials.
type LaunchPathOption struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

// LaunchProviderOption exposes only the stable provider reference and display
// metadata needed by the remote launcher. URLs, environment and API keys are
// deliberately absent.
type LaunchProviderOption struct {
	Ref          string `json:"ref"`
	Label        string `json:"label"`
	Kind         string `json:"kind,omitempty"`
	DefaultModel string `json:"defaultModel,omitempty"`
}

// LaunchPresetOption is the safe projection of a host terminal preset. Ref is
// the stable key accepted by CreateSessionRequest; it never contains secrets.
type LaunchPresetOption struct {
	Ref         string `json:"ref"`
	Label       string `json:"label"`
	ProviderRef string `json:"providerRef,omitempty"`
	ModelRef    string `json:"modelRef,omitempty"`
}

// LaunchDefaults is the last successfully activated desktop recipe for one
// CLI. It contains stable references and booleans only.
type LaunchDefaults struct {
	ProviderRef string `json:"providerRef,omitempty"`
	PresetRef   string `json:"presetRef,omitempty"`
	ModelRef    string `json:"modelRef,omitempty"`
	ShellRef    string `json:"shellRef,omitempty"`
	UseHeadroom bool   `json:"useHeadroom"`
}

// CLILaunchSettings groups the safe choices for one frozen CLI type.
type CLILaunchSettings struct {
	CLIType   CLIType                `json:"cliType"`
	Providers []LaunchProviderOption `json:"providers"`
	Presets   []LaunchPresetOption   `json:"presets"`
	Defaults  *LaunchDefaults        `json:"defaults,omitempty"`
}

// LaunchSettings is the complete non-secret settings surface used to create a
// remote terminal session. It is optional on HostSummary for additive v1
// compatibility with older hosts/fixtures.
type LaunchSettings struct {
	Workdirs []LaunchPathOption  `json:"workdirs"`
	Shells   []LaunchPathOption  `json:"shells"`
	CLIs     []CLILaunchSettings `json:"clis"`
}

// HostSummary is the body of GET /host/summary. It exposes the API version and
// CLI availability, but never Provider API Key, RemoteToken, exported JSON or
// environment variable values.
type HostSummary struct {
	APIVersion      APIVersion        `json:"apiVersion"`
	ServerVersion   string            `json:"serverVersion"`
	CLIAvailability []CLIAvailability `json:"cliAvailability"`
	LaunchSettings  *LaunchSettings   `json:"launchSettings,omitempty"`
}

// PairingCompleteResponse is the 201 body of POST /pairing/complete. The device
// Cookie is set via Set-Cookie; the body carries no credential.
type PairingCompleteResponse struct {
	Device DeviceSummary `json:"device"`
	Host   HostSummary   `json:"host"`
}

// ControlSnapshot is the control projection relative to the current device.
// It is a discriminated union on State: deviceName is REQUIRED (and present)
// only when State == ControlStateOther and MUST be omitted otherwise. It is
// NOT a mega-struct: one required discriminant + one conditional pointer.
type ControlSnapshot struct {
	State      ControlState `json:"state"`
	DeviceName *string      `json:"deviceName,omitempty"`
}

// SessionSummary is the list/detail common projection. GET never returns
// already-deleted resources; `removed` is primarily an event state.
type SessionSummary struct {
	ID             SessionID       `json:"id"`
	Title          string          `json:"title"`
	CLIType        CLIType         `json:"cliType"`
	State          SessionState    `json:"state"`
	Control        ControlSnapshot `json:"control"`
	LastActivityAt string          `json:"lastActivityAt"` // RFC3339 UTC 'Z'
}

// SessionDetail extends SessionSummary with the authorized-device detail
// fields. Embedded fields are promoted to the top level of the JSON object.
// earliestSeq/latestSeq are required even when 0 (empty history sentinel);
// they MUST NOT use omitempty.
type SessionDetail struct {
	SessionSummary
	Workdir     string `json:"workdir"`
	StartedAt   string `json:"startedAt"`
	EarliestSeq Seq    `json:"earliestSeq"`
	LatestSeq   Seq    `json:"latestSeq"`
}

// CreateSessionRequest is the body of POST /sessions. cliType is required;
// all other fields are optional safe launch selections. API keys, provider
// URLs and environment values never cross this boundary: the host resolves
// stable references against its own configuration and secret store.
type CreateSessionRequest struct {
	CLIType     CLIType `json:"cliType"`
	Workdir     *string `json:"workdir,omitempty"`
	ProviderRef *string `json:"providerRef,omitempty"`
	PresetRef   *string `json:"presetRef,omitempty"`
	ModelRef    *string `json:"modelRef,omitempty"`
	ShellRef    *string `json:"shellRef,omitempty"`
	UseHeadroom *bool   `json:"useHeadroom,omitempty"`
}

// ConfirmActionRequest is the body of stop/restart/delete. confirm MUST be
// literal true; false/omitted/null is bad_request. This is a protocol-level
// intent assertion and does NOT replace the frontend PG-06 confirmation or the
// server-side local record.
type ConfirmActionRequest struct {
	Confirm bool `json:"confirm"`
}

// SessionList is the GET /sessions top-level array. nil is rejected; a non-nil
// empty slice marshals as [] (the wire form for "no sessions").
type SessionList []SessionSummary

// RESTResponse is the closed set of v1 REST success-response bodies. DELETE
// returns 204 (no body) and is not a member; APIError goes through errors.go.
type RESTResponse interface {
	isRESTResponse()
}

func (PairingCompleteResponse) isRESTResponse() {}
func (HostSummary) isRESTResponse()             {}
func (SessionList) isRESTResponse()             {}
func (SessionDetail) isRESTResponse()           {}
func (ControlSnapshot) isRESTResponse()         {}

// ---------------------------------------------------------------------------
// Closed-enum helpers
// ---------------------------------------------------------------------------

func isKnownCLIType(c CLIType) bool {
	for _, k := range KnownCLITypes {
		if k == c {
			return true
		}
	}
	return false
}

func isKnownSessionState(s SessionState) bool {
	for _, k := range KnownSessionStates {
		if k == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Production Decode (ingress) — addendum §5.1/§5.2.
// ---------------------------------------------------------------------------

// DecodePairingCompleteRequest strictly decodes a pairing-complete request
// body: exactly {code, deviceName}, both required non-empty.
func DecodePairingCompleteRequest(raw []byte) (PairingCompleteRequest, error) {
	f, err := strictFields(raw)
	if err != nil {
		return PairingCompleteRequest{}, err
	}
	if err := rejectUnknown(f, "code", "deviceName"); err != nil {
		return PairingCompleteRequest{}, err
	}
	code, err := reqNonEmptyString(f, "code")
	if err != nil {
		return PairingCompleteRequest{}, err
	}
	name, err := reqNonEmptyString(f, "deviceName")
	if err != nil {
		return PairingCompleteRequest{}, err
	}
	return PairingCompleteRequest{Code: code, DeviceName: name}, nil
}

// DecodeCreateSessionRequest strictly decodes a create-session request:
// cliType required known CLI; optional string selections must be non-empty.
func DecodeCreateSessionRequest(raw []byte) (CreateSessionRequest, error) {
	f, err := strictFields(raw)
	if err != nil {
		return CreateSessionRequest{}, err
	}
	if err := rejectUnknown(f, "cliType", "workdir", "providerRef", "presetRef", "modelRef", "shellRef", "useHeadroom"); err != nil {
		return CreateSessionRequest{}, err
	}
	cli, err := reqField[CLIType](f, "cliType")
	if err != nil {
		return CreateSessionRequest{}, err
	}
	if !isKnownCLIType(cli) {
		return CreateSessionRequest{}, fmt.Errorf("contract: cliType %q is not a known CLI type", cli)
	}
	req := CreateSessionRequest{CLIType: cli}
	if wd, ok, err := optFieldT[string](f, "workdir"); err != nil {
		return req, err
	} else if ok {
		if wd == "" {
			return req, errors.New("contract: workdir must be non-empty when present")
		}
		req.Workdir = &wd
	}
	for key, target := range map[string]**string{
		"providerRef": &req.ProviderRef,
		"presetRef":   &req.PresetRef,
		"modelRef":    &req.ModelRef,
		"shellRef":    &req.ShellRef,
	} {
		value, ok, err := optFieldT[string](f, key)
		if err != nil {
			return req, err
		}
		if ok {
			if strings.TrimSpace(value) == "" {
				return req, fmt.Errorf("contract: %s must be non-empty when present", key)
			}
			value = strings.TrimSpace(value)
			*target = &value
		}
	}
	if value, ok, err := optFieldT[bool](f, "useHeadroom"); err != nil {
		return req, err
	} else if ok {
		req.UseHeadroom = &value
	}
	return req, nil
}

// DecodeConfirmActionRequest strictly decodes a confirm-action request:
// exactly {confirm}, confirm must be literal true.
func DecodeConfirmActionRequest(raw []byte) (ConfirmActionRequest, error) {
	f, err := strictFields(raw)
	if err != nil {
		return ConfirmActionRequest{}, err
	}
	if err := rejectUnknown(f, "confirm"); err != nil {
		return ConfirmActionRequest{}, err
	}
	confirm, err := reqField[bool](f, "confirm")
	if err != nil {
		return ConfirmActionRequest{}, err
	}
	if !confirm {
		return ConfirmActionRequest{}, errors.New("contract: confirm must be true")
	}
	return ConfirmActionRequest{Confirm: true}, nil
}

// ---------------------------------------------------------------------------
// Production Validate / Marshal (egress) — addendum §5.3.
// ---------------------------------------------------------------------------

func validateCLIAvailability(c CLIAvailability) error {
	if !isKnownCLIType(c.CLIType) {
		return fmt.Errorf("contract: cliType %q is not a known CLI type", c.CLIType)
	}
	return nil
}

func validateLaunchSettings(s *LaunchSettings) error {
	if s == nil {
		return nil
	}
	if s.Workdirs == nil || s.Shells == nil || s.CLIs == nil {
		return errors.New("contract: LaunchSettings slices must not be nil")
	}
	seenCLI := make(map[CLIType]bool, len(s.CLIs))
	for _, item := range s.CLIs {
		if !isKnownCLIType(item.CLIType) || seenCLI[item.CLIType] {
			return fmt.Errorf("contract: invalid or duplicate launch settings CLI %q", item.CLIType)
		}
		seenCLI[item.CLIType] = true
		if item.Providers == nil || item.Presets == nil {
			return fmt.Errorf("contract: launch settings for %q must use non-nil option slices", item.CLIType)
		}
		for _, option := range item.Providers {
			if strings.TrimSpace(option.Ref) == "" || strings.TrimSpace(option.Label) == "" {
				return errors.New("contract: launch provider ref and label must be non-empty")
			}
		}
		for _, option := range item.Presets {
			if strings.TrimSpace(option.Ref) == "" || strings.TrimSpace(option.Label) == "" {
				return errors.New("contract: launch preset ref and label must be non-empty")
			}
		}
	}
	for _, option := range append(append([]LaunchPathOption{}, s.Workdirs...), s.Shells...) {
		if strings.TrimSpace(option.Path) == "" || strings.TrimSpace(option.Label) == "" {
			return errors.New("contract: launch path and label must be non-empty")
		}
	}
	return nil
}

func validateDeviceSummary(d DeviceSummary) error {
	if d.ID == "" {
		return errors.New("contract: DeviceSummary.ID must be non-empty")
	}
	if d.Name == "" {
		return errors.New("contract: DeviceSummary.Name must be non-empty")
	}
	if d.PairedAt == "" {
		return errors.New("contract: DeviceSummary.PairedAt must be non-empty")
	}
	return nil
}

func validateHostSummary(h HostSummary) error {
	if h.APIVersion != APIVersionV1 {
		return fmt.Errorf("contract: HostSummary.APIVersion must be %q", APIVersionV1)
	}
	if h.ServerVersion == "" {
		return errors.New("contract: HostSummary.ServerVersion must be non-empty")
	}
	if h.CLIAvailability == nil {
		return errors.New("contract: HostSummary.CLIAvailability must not be nil")
	}
	if len(h.CLIAvailability) != len(KnownCLITypes) {
		return fmt.Errorf("contract: HostSummary.CLIAvailability must have exactly %d entries, got %d", len(KnownCLITypes), len(h.CLIAvailability))
	}
	seen := make(map[CLIType]bool, len(KnownCLITypes))
	for _, c := range h.CLIAvailability {
		if err := validateCLIAvailability(c); err != nil {
			return err
		}
		if seen[c.CLIType] {
			return fmt.Errorf("contract: duplicate CLIType %q", c.CLIType)
		}
		seen[c.CLIType] = true
	}
	for _, k := range KnownCLITypes {
		if !seen[k] {
			return fmt.Errorf("contract: HostSummary.CLIAvailability missing CLIType %q", k)
		}
	}
	return validateLaunchSettings(h.LaunchSettings)
}

// ValidateControlSnapshot enforces the conditional union on State.
func ValidateControlSnapshot(c ControlSnapshot) error {
	return validateControlSnapshot(c)
}

func validateControlSnapshot(c ControlSnapshot) error {
	switch c.State {
	case ControlStateNone, ControlStateYou, ControlStateDesktop:
		if c.DeviceName != nil {
			return fmt.Errorf("contract: ControlSnapshot.DeviceName must be omitted for state %q", c.State)
		}
	case ControlStateOther:
		if c.DeviceName == nil || *c.DeviceName == "" {
			return errors.New("contract: ControlSnapshot.DeviceName required and non-empty for state other")
		}
	default:
		return fmt.Errorf("contract: ControlSnapshot.State %q is not a known control state", c.State)
	}
	return nil
}

func validateSessionSummary(s SessionSummary) error {
	if s.ID == "" {
		return errors.New("contract: SessionSummary.ID must be non-empty")
	}
	if s.Title == "" {
		return errors.New("contract: SessionSummary.Title must be non-empty")
	}
	if !isKnownCLIType(s.CLIType) {
		return fmt.Errorf("contract: SessionSummary.CLIType %q is not known", s.CLIType)
	}
	if !isKnownSessionState(s.State) {
		return fmt.Errorf("contract: SessionSummary.State %q is not a known session state", s.State)
	}
	if err := validateControlSnapshot(s.Control); err != nil {
		return err
	}
	if s.LastActivityAt == "" {
		return errors.New("contract: SessionSummary.LastActivityAt must be non-empty")
	}
	return nil
}

func validateSessionDetail(d SessionDetail) error {
	if err := validateSessionSummary(d.SessionSummary); err != nil {
		return err
	}
	if d.Workdir == "" {
		return errors.New("contract: SessionDetail.Workdir must be non-empty")
	}
	if d.StartedAt == "" {
		return errors.New("contract: SessionDetail.StartedAt must be non-empty")
	}
	if err := validateSeqRange(d.EarliestSeq); err != nil {
		return err
	}
	if err := validateSeqRange(d.LatestSeq); err != nil {
		return err
	}
	if d.EarliestSeq > d.LatestSeq {
		return errors.New("contract: SessionDetail.EarliestSeq must be <= LatestSeq")
	}
	return nil
}

func validateSessionList(s SessionList) error {
	if s == nil {
		return errors.New("contract: SessionList must not be nil (use non-nil empty for no sessions)")
	}
	for i, item := range s {
		if err := validateSessionSummary(item); err != nil {
			return fmt.Errorf("contract: SessionList[%d]: %w", i, err)
		}
	}
	return nil
}

func validatePairingCompleteResponse(p PairingCompleteResponse) error {
	if err := validateDeviceSummary(p.Device); err != nil {
		return err
	}
	return validateHostSummary(p.Host)
}

// ValidateRESTResponse validates a v1 REST success-response body by its closed
// type. It enforces required fields, closed enums, conditional control and
// required-slice nil rejection (HostSummary.CLIAvailability, SessionList).
func ValidateRESTResponse(r RESTResponse) error {
	switch v := r.(type) {
	case PairingCompleteResponse:
		return validatePairingCompleteResponse(v)
	case HostSummary:
		return validateHostSummary(v)
	case SessionList:
		return validateSessionList(v)
	case SessionDetail:
		return validateSessionDetail(v)
	case ControlSnapshot:
		return validateControlSnapshot(v)
	default:
		return fmt.Errorf("contract: unknown RESTResponse type %T", r)
	}
}

// MarshalRESTResponse validates then marshals a REST success-response body. It
// produces no bytes on validation failure. Future v1 handlers MUST use this
// instead of json.Marshal on a raw DTO.
func MarshalRESTResponse(r RESTResponse) ([]byte, error) {
	if err := ValidateRESTResponse(r); err != nil {
		return nil, err
	}
	return json.Marshal(r)
}
