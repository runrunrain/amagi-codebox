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

// wslExecLogin runs a login-shell script inside the given distro as the
// distro's default user WITHOUT the artificial PATH prefix, so `command -v`
// observes the real .profile/.bashrc state — the same resolution a launched
// session's `bash -lic` performs. It is a package var so tests can substitute
// it.
var wslExecLogin = func(distro, script string) (string, error) {
	args := []string{"-d", distro, "--", "bash", "-lc", script}
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
//
// The append guard matches the EXACT standard line via `grep -qF` (fixed
// string). A looser substring guard (`grep -q "npm-global/bin"`) is defeated
// by dirty snapshots: a stale `export PATH=<fully expanded snapshot>` line in
// .bashrc/.profile merely CONTAINS the substring, so the standard line was
// never appended and login shells kept resolving CLIs to /mnt/c. Even when
// such near-miss lines exist, the missing exact line is appended — with
// PATH-prepend semantics the later line takes effect, which also repairs those
// dirty snapshots.
func (s *Service) ensureUserNpmPrefix(distro string) (string, error) {
	script := strings.Join([]string{
		`mkdir -p "$HOME/.npm-global"`,
		`npm config set prefix "$HOME/.npm-global"`,
		`grep -qF 'export PATH="$HOME/.npm-global/bin:$PATH"' "$HOME/.bashrc" 2>/dev/null || printf '%s\n' 'export PATH="$HOME/.npm-global/bin:$PATH"' >> "$HOME/.bashrc"`,
		`grep -qF 'export PATH="$HOME/.npm-global/bin:$PATH"' "$HOME/.profile" 2>/dev/null || printf '%s\n' 'export PATH="$HOME/.npm-global/bin:$PATH"' >> "$HOME/.profile"`,
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
		// Effectiveness guard: nativeVersion resolves through our artificial
		// probe PATH, so a dirty historical PATH snapshot (login shells never got
		// the standard npm-global line) still reports AlreadyOK while real
		// sessions fall through to the /mnt/c Windows shim. ensureUserNpmPrefix
		// matches the exact standard line now, so re-running it appends the
		// missing line and repairs such snapshots (PATH-prepend semantics: the
		// appended line runs last and wins).
		if diag := s.checkLoginResolution(distro, key); diag != "" {
			if prefixLog, e := s.ensureUserNpmPrefix(distro); e != nil {
				logB.WriteString("[npm-prefix-repair]\n" + prefixLog + "\nrepair failed: " + e.Error() + "\n")
			} else {
				logB.WriteString("[npm-prefix-repair]\n" + prefixLog + "\n")
			}
			if diag = s.checkLoginResolution(distro, key); diag != "" {
				res.Success = false
				res.Error = diag
			} else {
				res.Message += "; repaired login PATH (appended missing npm-global line)"
			}
		}
		res.Log = logB.String()
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
		// Final effectiveness check: a plain login shell (no artificial PATH)
		// must resolve the CLI into $HOME/.npm-global/bin, otherwise launched
		// sessions silently run the Windows-side CLI via /mnt/c interop.
		if diag := s.checkLoginResolution(distro, key); diag != "" {
			res.Success = false
			res.Error = diag
		}
	} else {
		res.Success = false
		res.Error = "install completed but CLI did not resolve natively in WSL"
	}
	res.Log = logB.String()
	return res, nil
}

// checkLoginResolution verifies that a plain login shell inside the distro
// resolves the CLI into $HOME/.npm-global/bin rather than a /mnt/<drive>
// Windows passthrough — the same resolution a launched session's `bash -lic`
// builds on (driven by the .profile/.bashrc PATH lines). It must NOT go through
// wslExec, whose artificial PATH prefix would mask a broken login PATH. Returns
// "" when effective; otherwise a diagnostic suitable for InstallResult.Error /
// the InstallResult log. A probe that itself errors is inconclusive and returns
// "" (a diagnostic must not fail an otherwise completed install); the miss is
// logged instead.
func (s *Service) checkLoginResolution(distro, key string) string {
	script := `printf '%s\n' "$HOME/.npm-global/bin"; command -v ` + bashSingleQuote(key) + ` 2>/dev/null || true`
	out, err := wslExecLogin(distro, script)
	if err != nil {
		s.logInfo("wslsetup", "login-resolution probe failed (treated as inconclusive)", "tool="+key+" err="+err.Error())
		return ""
	}
	userBin, resolved := splitLoginResolutionProbe(out)
	if userBin == "" {
		s.logInfo("wslsetup", "login-resolution probe returned no marker line (treated as inconclusive)", "tool="+key)
		return ""
	}
	if strings.HasPrefix(resolved, userBin+"/") {
		return ""
	}
	if resolved == "" {
		return fmt.Sprintf("%s installed but not effective in the WSL login shell: command -v %s found nothing on PATH (expected under %s); check the npm-global PATH line in ~/.profile", key, key, userBin)
	}
	return fmt.Sprintf("%s installed but not effective in the WSL login shell: command -v %s resolves to %s instead of %s (Windows passthrough); WSL sessions would run the Windows-side CLI — check the npm-global PATH line in ~/.profile", key, key, resolved, userBin)
}

// splitLoginResolutionProbe parses checkLoginResolution's probe output: the
// first non-empty line is the echoed "$HOME/.npm-global/bin" marker; the next
// non-empty line (if any) is `command -v`'s direct stdout hit. Direct-stdout
// parsing is used because command substitution is unreliable in this
// non-interactive wsl.exe context (see nativeCommandPath).
func splitLoginResolutionProbe(out string) (userBin, resolved string) {
	seen := 0
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(strings.Trim(l, "\r\x00"))
		if t == "" {
			continue
		}
		if seen == 0 {
			userBin = t
			seen = 1
			continue
		}
		resolved = t
		return
	}
	return
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
