//go:build windows

package wslsetup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
)

// Service installs and inspects the managed CLIs inside a WSL distro.
type Service struct {
	log *logging.Service
}

func NewService(log *logging.Service) *Service {
	return &Service{log: log}
}

// userBinPathPrefix prepends the user npm-global bin (where `npm i -g` shims land
// with our prefix config) to PATH. Ubuntu's ~/.bashrc returns early for
// non-interactive shells (`case $- in *i*) ;; *) return;;`), so a non-interactive
// `wsl.exe -- bash -lc` does NOT pick up the PATH line we append there. Every
// probe/install/verify command therefore sets PATH explicitly rather than
// relying on .bashrc being sourced. /usr/bin covers the NodeSource node/npm.
const userBinPathPrefix = `export PATH="$HOME/.npm-global/bin:/usr/bin:/usr/local/bin:$PATH"; `

// wslExec runs a bash command inside the given distro as the distro's default
// user and returns combined output. user "" = default user. The command is run
// with an explicit PATH prefix (see userBinPathPrefix) so the native toolchain
// and user-global npm shims resolve regardless of .bashrc's interactive guard.
// It is a package var so tests can substitute it.
var wslExec = func(distro, user, script string) (string, error) {
	args := []string{"-d", distro}
	if strings.TrimSpace(user) != "" {
		args = append(args, "-u", user)
	}
	args = append(args, "--", "bash", "-lc", userBinPathPrefix+script)
	out, err := exec.Command("wsl.exe", args...).CombinedOutput()
	return string(out), err
}

// wslExecRoot runs a non-interactive command as root (for apt / NodeSource).
var wslExecRoot = func(distro, script string) (string, error) {
	args := []string{"-d", distro, "-u", "root", "--", "bash", "-lc", script}
	out, err := exec.Command("wsl.exe", args...).CombinedOutput()
	return string(out), err
}

// GetStatus reports whether WSL is usable and which managed CLIs are installed
// natively inside the selected distro.
func (s *Service) GetStatus() Status {
	distro := platform.DefaultWSLDistro(nil)
	if distro == "" {
		return Status{Available: false, Reason: "no usable WSL distro installed"}
	}
	st := Status{Available: true, Distro: distro}
	// Surface the WSL architecture generation (WSL1/WSL2) so the UI can show a
	// "WSL2" badge. wsl.exe is the unified entry for both; the version only
	// affects how the distro runs, not how we launch it. 0 = unknown.
	st.DistroWSLVersion = platform.WSLDistroVersions(nil)[distro]

	// Native node version (empty when only the Windows passthrough exists).
	if p := s.nativeCommandPath(distro, "node"); isNativePath(p) {
		if out, err := wslExec(distro, "", "node -v"); err == nil {
			st.NodeVersion = firstNonEmptyLine(out)
		}
	}

	for _, key := range []string{"claude", "opencode", "codex", "pi"} {
		pkg, _ := packageForTool(key)
		ts := ToolStatus{Tool: key, Package: pkg}
		if p := s.nativeCommandPath(distro, key); isNativePath(p) {
			ts.Installed = true
			ts.ExecutablePath = p
			if out, err := wslExec(distro, "", key+" --version 2>/dev/null | head -1"); err == nil {
				ts.Version = firstNonEmptyLine(out)
			}
		}
		st.Tools = append(st.Tools, ts)
	}
	return st
}

// nativeCommandPath returns the PATH-resolved absolute path of cmd inside the
// distro, or "" when absent. It runs `type -P <cmd>` as the WHOLE script and
// reads its stdout directly: in this non-interactive `wsl.exe -- bash -lc`
// context, wrapping a shell builtin lookup in `$(...)` command substitution
// unreliably returns empty, whereas the builtin's direct stdout is correct.
func (s *Service) nativeCommandPath(distro, cmd string) string {
	out, err := wslExec(distro, "", "type -P "+bashSingleQuote(cmd)+" 2>/dev/null || true")
	if err != nil {
		return ""
	}
	return firstNonEmptyLine(out)
}

// isNativePath reports whether p is a real Linux path (present and not a /mnt
// Windows passthrough).
func isNativePath(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/mnt/")
}

// nodeMajorFloor / nodeMinorFloor are the minimum native Node version WSL CLI
// installs accept. pi's undici 8.x dependency uses worker_threads APIs that only
// exist from Node 22.13 on (its engines field says >=22.19.0); anything older
// crashes at module load with "webidl.util.markAsUncloneable is not a function".
const (
	nodeMajorFloor = 22
	nodeMinorFloor = 19
)

// EnsureNode makes sure a native Node.js at or above the version floor is
// available inside the distro. It is idempotent: when a native node at or above
// the floor already exists it is a no-op. Otherwise it installs Node 22 from
// NodeSource (requires network + root), which also upgrades an older native
// Node via apt. Returns whether an install was performed.
func (s *Service) ensureNode(distro string) (installed bool, log string, err error) {
	// Already have a recent enough native node?
	if isNativePath(s.nativeCommandPath(distro, "node")) {
		if out, e := wslExec(distro, "", "node -v"); e == nil {
			ver := strings.TrimSpace(firstNonEmptyLine(out))
			if nodeVersionAtLeast(ver, nodeMajorFloor, nodeMinorFloor) {
				return false, "native node already present: " + ver, nil
			}
			s.logInfo("wslsetup", "native node below floor, upgrading to Node 22 in WSL", "current="+ver)
		}
	}

	s.logInfo("wslsetup", "installing native Node.js 22 in WSL", "distro="+distro)
	script := strings.Join([]string{
		"export DEBIAN_FRONTEND=noninteractive",
		"apt-get update -qq",
		"apt-get install -y ca-certificates curl gnupg",
		"curl -fsSL https://deb.nodesource.com/setup_22.x -o /tmp/nodesource_setup.sh",
		"bash /tmp/nodesource_setup.sh",
		"apt-get install -y nodejs",
		"node -v",
	}, " && ")
	out, e := wslExecRoot(distro, script)
	if e != nil {
		return false, out, fmt.Errorf("install node in WSL: %w", e)
	}
	// Verify a native node now resolves.
	if !isNativePath(s.nativeCommandPath(distro, "node")) {
		return false, out, fmt.Errorf("node still not resolvable inside WSL after install")
	}
	return true, out, nil
}

// ensureUserNpmPrefix configures a user-writable npm global prefix
// (~/.npm-global) and puts it on PATH via ~/.bashrc AND ~/.profile, so
// `npm i -g` works without root and the installed CLI shims resolve ahead of any
// Windows passthrough. The .profile line matters for non-interactive login
// shells (`wsl.exe -- bash -lc`): Ubuntu's .bashrc returns early for those, so a
// .bashrc-only PATH line leaves `bash -lc pi` resolving to the /mnt/c Windows
// shim. Idempotent.
func (s *Service) ensureUserNpmPrefix(distro string) (string, error) {
	script := strings.Join([]string{
		`mkdir -p "$HOME/.npm-global"`,
		`npm config set prefix "$HOME/.npm-global"`,
		`grep -q "npm-global/bin" "$HOME/.bashrc" 2>/dev/null || printf '%s\n' 'export PATH="$HOME/.npm-global/bin:$PATH"' >> "$HOME/.bashrc"`,
		`grep -q "npm-global/bin" "$HOME/.profile" 2>/dev/null || printf '%s\n' 'export PATH="$HOME/.npm-global/bin:$PATH"' >> "$HOME/.profile"`,
		`echo prefix-ok`,
	}, " && ")
	return wslExec(distro, "", script)
}

// ensurePiConfig seeds the distro's ~/.pi/agent (providers/auth/models plus the
// amagi assets) from the Windows-side install, so pi launched inside WSL
// recognizes the same providers (e.g. custom relays) without manual setup.
// It only seeds when the distro has no ~/.pi/agent yet — WSL-local state is
// never overwritten — and is skipped when the Windows side has no .pi. Sessions
// history and settings backups stay machine-local and are dropped. Failures are
// reported to the caller but must not fail the install: pi still runs, just
// without the shared config.
func (s *Service) ensurePiConfig(distro string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "skip: no windows home dir (" + err.Error() + ")", nil
	}
	src := wslPathFromWindowsHome(filepath.Join(home, ".pi", "agent"))
	if src == "" {
		return "skip: could not map windows .pi path", nil
	}
	script := strings.Join([]string{
		`[ -d ` + bashSingleQuote(src) + ` ] || { echo 'no windows .pi to seed'; exit 0; }`,
		`[ -e "$HOME/.pi/agent" ] && { echo 'wsl pi config already present'; exit 0; }`,
		`mkdir -p "$HOME/.pi"`,
		`cp -r ` + bashSingleQuote(src) + ` "$HOME/.pi/agent"`,
		`rm -rf "$HOME/.pi/agent/sessions" "$HOME/.pi/agent"/settings.json.amagi-bak-*`,
		`echo 'seeded pi config from windows'`,
	}, "\n")
	return wslExec(distro, "", script)
}

// InstallTool installs one CLI into WSL: ensure Node, ensure user npm prefix,
// then npm i -g the package, and verify. Idempotent: a present native install
// short-circuits with AlreadyOK.
func (s *Service) InstallTool(tool string) (*InstallResult, error) {
	key := normalizeToolKey(tool)
	pkg, ok := packageForTool(key)
	if !ok {
		return nil, fmt.Errorf("unsupported CLI tool for WSL install: %q", tool)
	}
	distro := platform.DefaultWSLDistro(nil)
	if distro == "" {
		return &InstallResult{Tool: key, Package: pkg, Success: false,
			Error: "no usable WSL distro installed"}, nil
	}

	res := &InstallResult{Tool: key, Package: pkg, Distro: distro}
	var logB strings.Builder

	// nativeVersion probes for a native (non-/mnt) install and returns its
	// version line, or "" when absent/Windows-bridged.
	nativeVersion := func() string {
		if !isNativePath(s.nativeCommandPath(distro, key)) {
			return ""
		}
		out, e := wslExec(distro, "", key+" --version 2>/dev/null | head -1")
		if e != nil {
			return ""
		}
		return firstNonEmptyLine(out)
	}

	// Short-circuit if already installed natively.
	if v := nativeVersion(); v != "" {
		res.AlreadyOK = true
		res.Success = true
		res.Version = v
		res.Message = fmt.Sprintf("%s already installed in WSL (%s)", key, v)
		return res, nil
	}

	nodeInstalled, nodeLog, err := s.ensureNode(distro)
	logB.WriteString("[node]\n" + nodeLog + "\n")
	if err != nil {
		res.Success = false
		res.Error = err.Error()
		res.Log = logB.String()
		return res, nil
	}
	res.NodeInstalled = nodeInstalled

	if prefixLog, e := s.ensureUserNpmPrefix(distro); e != nil {
		logB.WriteString("[npm-prefix]\n" + prefixLog + "\n")
		res.Success = false
		res.Error = "configure npm prefix: " + e.Error()
		res.Log = logB.String()
		return res, nil
	}

	s.logInfo("wslsetup", "npm i -g in WSL", fmt.Sprintf("distro=%s pkg=%s", distro, pkg))
	installScript := fmt.Sprintf(`export PATH="$HOME/.npm-global/bin:$PATH"; npm i -g %s`, bashSingleQuote(pkg))
	out, e := wslExec(distro, "", installScript)
	logB.WriteString("[install]\n" + out + "\n")
	if e != nil {
		res.Success = false
		res.Error = "npm install failed: " + e.Error()
		res.Log = logB.String()
		return res, nil
	}

	// Verify.
	if v := nativeVersion(); v != "" {
		res.Success = true
		res.Version = v
		res.Message = fmt.Sprintf("%s installed in WSL (%s)", key, v)
		// pi keeps its providers/auth in ~/.pi/agent on the Windows side; seed
		// the distro copy on first WSL use so the same providers resolve.
		if key == "pi" {
			if cfgLog, e := s.ensurePiConfig(distro); e != nil {
				logB.WriteString("[pi-config]\n" + cfgLog + "\nseed failed (non-fatal): " + e.Error() + "\n")
			} else {
				logB.WriteString("[pi-config]\n" + cfgLog + "\n")
			}
		}
	} else {
		res.Success = false
		res.Error = "install completed but CLI did not resolve natively in WSL"
	}
	res.Log = logB.String()
	return res, nil
}

func (s *Service) logInfo(scope, msg, detail string) {
	if s.log != nil {
		s.log.Info(scope, msg, detail)
	}
}

func firstNonEmptyLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(strings.Trim(l, "\r\x00"))
		if t != "" {
			return t
		}
	}
	return ""
}
