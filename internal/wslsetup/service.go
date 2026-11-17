// Package wslsetup installs and upgrades the managed AI CLIs (Claude Code,
// OpenCode, Codex, Pi, Oh My Pi) INTO a WSL distribution so they run natively
// in the Linux environment that CodeBox now defaults to on Windows. It is
// orthogonal to internal/envcheck, which manages the Windows-side installs.
//
// The business flow lives in portable files driven through the wslRunner seam
// (runner.go): service_windows.go wires the real wsl.exe implementation on
// Windows, off Windows the runner is nil and every method reports the platform
// limitation (the historical stub contract), while tests on any OS inject a
// fake runner and drive the real InstallTool/UpdateTool flow without WSL.
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
	// omp shares pi's codebase (package name aligned with envcheck's
	// ompNPMPackageName, installer.go). Added 2026-10 per the Windows/WSL
	// unified-management workflow, superseding the old B7 exclusion which
	// predated the five-CLI coverage requirement.
	"omp": "@oh-my-pi/pi-coding-agent",
}

// cliToolOrder is the canonical report order of the five managed CLIs (used by
// GetStatus). Every key must exist in cliPackages — pinned by tests.
var cliToolOrder = []string{"claude", "opencode", "codex", "pi", "omp"}

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
