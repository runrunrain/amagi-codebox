package contract

// APIVersion is the remote API version string. It is case-sensitive; the only
// value in v1 is APIVersionV1 ("v1").
type APIVersion string

const (
	// APIVersionV1 is the sole v1 API version literal.
	APIVersionV1 APIVersion = "v1"
)

// Request-correlation and route constants. The REST request ID travels in the
// X-Request-ID header; the WS request ID travels as the top-level requestId
// field. They are the SAME concept — do not introduce a second alias.
const (
	RequestIDHeader = "X-Request-ID"
	// RESTBasePath is the base path for all v1 REST endpoints.
	RESTBasePath = "/api/remote/v1"
	// WebSocketV1Path is the SOLE v1 WebSocket upgrade path. There is no
	// second "/events" entry point (design I-01).
	WebSocketV1Path = "/ws/v1"
)

// RestEndpoint describes one v1 REST endpoint relative to RESTBasePath.
type RestEndpoint struct {
	Method        string `json:"method"`
	Path          string `json:"path"`
	SuccessStatus int    `json:"successStatus"`
}

// V1RestEndpoints is the complete, ordered set of 10 v1 REST endpoints. It is
// the canonical enumeration of method/path/status; consumers MUST use these
// rather than redeclaring strings. {id} is a single URL path segment the
// client percent-encodes; the server decodes it to an opaque SessionID.
var V1RestEndpoints = []RestEndpoint{
	{Method: "POST", Path: "/pairing/complete", SuccessStatus: 201},
	{Method: "GET", Path: "/host/summary", SuccessStatus: 200},
	{Method: "GET", Path: "/sessions", SuccessStatus: 200},
	{Method: "GET", Path: "/sessions/{id}", SuccessStatus: 200},
	{Method: "POST", Path: "/sessions", SuccessStatus: 201},
	{Method: "POST", Path: "/sessions/{id}/stop", SuccessStatus: 200},
	{Method: "POST", Path: "/sessions/{id}/restart", SuccessStatus: 200},
	{Method: "DELETE", Path: "/sessions/{id}", SuccessStatus: 204},
	{Method: "POST", Path: "/sessions/{id}/control/acquire", SuccessStatus: 200},
	{Method: "POST", Path: "/sessions/{id}/control/release", SuccessStatus: 200},
}
