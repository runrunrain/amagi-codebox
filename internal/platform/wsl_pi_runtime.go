package platform

import _ "embed"

//go:embed wsl_pi_runtime.sh
var wslPiRuntimePrelude string

// WSLPiRuntimePrelude runs after login-shell initialization and before Pi, so
// only distro-local absolute defaults enter the environment. PI_CODING_AGENT_DIR
// is intentionally untouched: auth, provider configuration, plugin management
// and WebUI discovery continue to use the same Windows canonical root. Pi's
// session transcripts stay there too, preserving native resume and CodeBox's
// usage scanner. Children inherit AMAGI_DATA_DIR through Amagi's existing env
// contract; no credential/config copies or automatic project migration occur.
// Node compile caching is not enabled by default: repeated local measurements
// showed cold cache creation could regress startup. Explicit user values remain.
func WSLPiRuntimePrelude() string { return wslPiRuntimePrelude }
