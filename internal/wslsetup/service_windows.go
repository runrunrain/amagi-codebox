//go:build windows

package wslsetup

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"amagi-codebox/internal/platform"
)

// userBinPathPrefix prepends the user npm-global bin (where `npm i -g` shims land
// with our prefix config) to PATH. Ubuntu's ~/.bashrc returns early for
// non-interactive shells (`case $- in *i*) ;; *) return;;`), so a non-interactive
// `wsl.exe -- bash -lc` does NOT pick up the PATH line we append there. Every
// probe/install/verify command therefore sets PATH explicitly rather than
// relying on .bashrc being sourced. /usr/bin covers the NodeSource node/npm.
const userBinPathPrefix = `export PATH="$HOME/.npm-global/bin:/usr/bin:/usr/local/bin:$PATH"; `

// realRunnerOnInit snapshots the runner wired by init() so the Windows
// wiring test can assert it even if other tests injected fakes meanwhile
// (package tests run sequentially and restore via t.Cleanup; this makes the
// pin independent of ordering).
var realRunnerOnInit *wslRunner

// init wires the real wsl.exe runner into the portable business flow. Off
// Windows no wiring happens and the package methods report the platform
// limitation (see runner.go).
func init() {
	wslRunnerImpl = &wslRunner{
		defaultDistro:    func() string { return platform.DefaultWSLDistro(nil) },
		distroVersion:    func(distro string) int { return platform.WSLDistroVersions(nil)[distro] },
		execUser:         runWslUser,
		execRoot:         runWslRoot,
		execLogin:        runWslLogin,
		searchStatus:     platform.WSLSearchToolStatus,
		resetSearchCache: platform.ResetWSLSearchToolCache,
	}
	realRunnerOnInit = wslRunnerImpl
}

// runWslUser runs a bash command inside the given distro as the distro's
// default user (user "" = default) and returns combined output, with the
// explicit PATH prefix applied (see userBinPathPrefix). timeout <= 0 uses the
// probe budget; positive values bound long installation steps.
func runWslUser(distro, user, script string, timeout time.Duration) (string, error) {
	args := []string{"-d", distro}
	if strings.TrimSpace(user) != "" {
		args = append(args, "-u", user)
	}
	args = append(args, "--", "bash", "-lc", userBinPathPrefix+script)
	return runWsl(args, timeout)
}

// runWslRoot runs a non-interactive command as root (for apt / NodeSource).
func runWslRoot(distro, script string, timeout time.Duration) (string, error) {
	return runWsl([]string{"-d", distro, "-u", "root", "--", "bash", "-lc", script}, timeout)
}

// runWslLogin runs a login-shell script inside the given distro as the
// distro's default user WITHOUT the artificial PATH prefix, so `command -v`
// observes the real .profile/.bashrc state — the same resolution a launched
// session's `bash -lic` performs.
func runWslLogin(distro, script string) (string, error) {
	return runWsl([]string{"-d", distro, "--", "bash", "-lc", script}, 0)
}

// runWsl executes wsl.exe with the given args and returns combined output.
// Even probes need a budget: a stopped/unresponsive distro must not hold the
// combined install operation forever. On expiry wsl.exe is killed (which
// disconnects the distro-side session) and a timeout error is returned so
// callers surface a real failure instead of hanging forever — and, with the
// per-distro operation lock, blocking every later operation on that distro.
func runWsl(args []string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	platform.SuppressConsoleWindow(cmd)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("wsl command timed out after %s", timeout)
	}
	return string(out), err
}
