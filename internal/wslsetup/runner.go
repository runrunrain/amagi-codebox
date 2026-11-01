package wslsetup

import (
	"time"

	"amagi-codebox/internal/platform"
)

// wslRunner is the package's entire OS I/O surface: resolving the target
// distro, running scripts inside it, and the platform-side search-tool cache.
// The install/upgrade business flow (service_flow.go) is plain logic layered
// on this seam, which is what makes it testable on any OS: tests inject a fake
// runner and drive InstallTool/UpdateTool end to end — no real WSL, no wsl.exe
// process, no user directories touched.
//
// A nil wslRunnerImpl means "not on Windows": every method then reports the
// platform limitation (mirroring the historical non-Windows stub contract).
// service_windows.go's init() installs the real implementation on Windows.
type wslRunner struct {
	// defaultDistro resolves the operation target ("" = no usable distro).
	// Callers resolve it ONCE per operation and use that name for the whole
	// run — same-distro identity, including the per-distro operation lock.
	defaultDistro func() string
	// distroVersion reports the WSL architecture generation of a distro
	// (1/2; 0 = unknown probe failure / older wsl.exe) for the Status badge.
	distroVersion func(distro string) int
	// execUser runs a script as the distro's default user (user "" = default)
	// with the probe PATH prefix applied by the implementation. timeout <= 0
	// means unbounded; positive values bound the long, network-bound steps so
	// a stalled command cannot hold the per-distro operation lock forever.
	execUser func(distro, user, script string, timeout time.Duration) (string, error)
	// execRoot runs a script as root (apt / NodeSource), same timeout rules.
	execRoot func(distro, script string, timeout time.Duration) (string, error)
	// execLogin runs a login-shell script WITHOUT the artificial PATH prefix
	// so `command -v` observes the real .profile/.bashrc state — the same
	// resolution a launched session's `bash -lic` performs.
	execLogin func(distro, script string) (string, error)
	// searchStatus / resetSearchCache wrap the platform-side cached probe of
	// fd/fdfind + ripgrep (see InstallSearchTools).
	searchStatus     func(distro string) platform.WSLSearchTools
	resetSearchCache func(distro string)
}

// wslRunnerImpl is nil until service_windows.go wires it on Windows.
var wslRunnerImpl *wslRunner
