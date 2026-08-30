// Package wslsetup installs the managed AI CLIs (Claude Code, OpenCode, Codex,
// Pi) INTO a WSL distribution so they run natively in the Linux environment that
// CodeBox now defaults to on Windows. It is orthogonal to internal/envcheck,
// which manages the Windows-side installs.
//
// The whole package is only meaningful on Windows with a usable WSL distro; the
// heavy lifting lives in service_windows.go, with a stub in service_other.go so
// the module still builds on macOS/Linux (per the repo's build-tag convention).
package wslsetup

// cliPackages maps a canonical CLI tool key (as returned by normalizeToolKey) to
// the npm package installed inside WSL. Package names verified against the live
// npm registry (2026-08). Keys are already-normalized, so only the canonical
// "claude" form is present (callers pass through normalizeToolKey first).
var cliPackages = map[string]string{
	"claude":   "@anthropic-ai/claude-code",
	"opencode": "opencode-ai",
	"codex":    "@openai/codex",
	"pi":       "@earendil-works/pi-coding-agent",
}

// InstallResult reports the outcome of an install-into-WSL operation. It mirrors
// the shape of envcheck.InstallResult so the frontend can treat both uniformly.
type InstallResult struct {
	Tool          string `json:"tool"`
	Package       string `json:"package"`
	Distro        string `json:"distro"`
	NodeInstalled bool   `json:"nodeInstalled"`
	AlreadyOK     bool   `json:"alreadyOK"`
	Success       bool   `json:"success"`
	Version       string `json:"version"`
	Message       string `json:"message"`
	Error         string `json:"error"`
	Log           string `json:"log"`
}

// ToolStatus is a lightweight per-tool snapshot of whether the CLI is installed
// natively inside WSL.
type ToolStatus struct {
	Tool           string `json:"tool"`
	Package        string `json:"package"`
	Installed      bool   `json:"installed"`
	Version        string `json:"version"`
	ExecutablePath string `json:"executablePath"`
}

// SearchToolsKey is the synthetic Tool key InstallSearchTools reports in
// InstallResult.Tool. It is not a CLI in cliPackages; it names the fd-find +
// ripgrep pair pi/omp shell out to for file search.
const SearchToolsKey = "search-tools"

// Status is the frontend-facing snapshot for the WSL CLI environment.
type Status struct {
	Available        bool         `json:"available"`        // a usable WSL distro exists
	Distro           string       `json:"distro"`           // selected distro name ("" when none)
	DistroWSLVersion int          `json:"distroWSLVersion"` // WSL architecture generation of Distro (1/2); 0 = unknown (probe failed / older wsl.exe)
	NodeVersion      string       `json:"nodeVersion"`      // native node -v inside WSL ("" when absent)
	Tools            []ToolStatus `json:"tools"`
	Reason           string       `json:"reason"` // why Available is false
}

// packageForTool resolves the npm package for a CLI tool key.
func packageForTool(tool string) (string, bool) {
	pkg, ok := cliPackages[normalizeToolKey(tool)]
	return pkg, ok
}
