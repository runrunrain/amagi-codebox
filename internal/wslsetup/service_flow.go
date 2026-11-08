package wslsetup

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/logging"
)

// Unsupported-platform messages. They mirror the historical non-Windows stub
// contract: the methods exist on every platform (App wiring stays uniform),
// but off Windows they report a structured failure instead of doing work.
const (
	errInstallUnsupported = "WSL CLI install is only supported on Windows"
	errUpdateUnsupported  = "WSL CLI update is only supported on Windows"
)

// Service installs, upgrades, and inspects the managed CLIs inside a WSL
// distro. All real I/O flows through the wslRunner seam; see runner.go.
type Service struct {
	log *logging.Service
}

func NewService(log *logging.Service) *Service {
	return &Service{log: log}
}

// Operation timeouts. The Windows runner bounds probes and small config writes
// with its default probe budget; the long, network-bound steps are bounded so a stalled
// wsl.exe/npm/apt cannot hang an operation — and, with the per-distro lock
// below, block every later operation on that distro — forever. envcheck's
// native-Windows install timeout is 120s (installer.go installCommandTimeout);
// the WSL chains additionally run apt/NodeSource plus npm inside the distro,
// so they get the more generous budgets below.
const (
	// npmOpTimeout bounds `npm i -g` (fresh install or @latest upgrade) of one CLI.
	npmOpTimeout = 10 * time.Minute
	// nodeInstallTimeout bounds the apt + NodeSource chain that installs or
	// upgrades native Node.js inside the distro.
	nodeInstallTimeout = 15 * time.Minute
	// aptOpTimeout bounds the root apt install of the search-tools pair.
	aptOpTimeout = 10 * time.Minute
)

// distroLocks serializes CLI install/upgrade operations per distro: two
// concurrent `npm i -g` runs into the same ~/.npm-global prefix can corrupt
// it, so InstallTool/UpdateTool targeting the SAME distro queue behind each
// other. Operations on different distros never block each other, and nothing
// else in the process shares these locks — deliberately not one global mutex.
var distroLocks sync.Map // distro name -> *sync.Mutex

// lockDistro acquires the per-distro operation lock and returns the unlock
// func. Callers must have resolved the distro name once (same-distro identity)
// so the lock key and the operation target cannot diverge mid-flight.
func lockDistro(distro string) func() {
	v, _ := distroLocks.LoadOrStore(distro, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// GetStatus reports whether WSL is usable and which managed CLIs are installed
// natively inside the selected distro.
func (s *Service) GetStatus() Status {
	r := wslRunnerImpl
	if r == nil {
		return Status{Available: false, Reason: errInstallUnsupported}
	}
	distro := r.defaultDistro()
	if distro == "" {
		return Status{Available: false, Reason: "no usable WSL distro installed"}
	}
	st := Status{Available: true, Distro: distro}
	// Surface the WSL architecture generation (WSL1/WSL2) so the UI can show a
	// "WSL2" badge. wsl.exe is the unified entry for both; the version only
	// affects how the distro runs, not how we launch it. 0 = unknown.
	st.DistroWSLVersion = r.distroVersion(distro)

	// Native node version (empty when only the Windows passthrough exists).
	if p := nativeCommandPath(r, distro, "node"); isNativePath(p) {
		if out, err := r.execUser(distro, "", "node -v", 0); err == nil {
			st.NodeVersion = firstNonEmptyLine(out)
		}
	}

	for _, key := range cliToolOrder {
		pkg, _ := packageForTool(key)
		ts := ToolStatus{Tool: key, Package: pkg}
		if p := nativeCommandPath(r, distro, key); isNativePath(p) {
			ts.Installed = true
			ts.ExecutablePath = p
			if out, err := r.execUser(distro, "", key+" --version 2>/dev/null | head -1", 0); err == nil {
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
func nativeCommandPath(r *wslRunner, distro, cmd string) string {
	out, err := r.execUser(distro, "", "type -P "+bashSingleQuote(cmd)+" 2>/dev/null || true", 0)
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
func (s *Service) ensureNode(r *wslRunner, distro string) (installed bool, log string, err error) {
	// Already have a recent enough native node?
	if isNativePath(nativeCommandPath(r, distro, "node")) {
		if out, e := r.execUser(distro, "", "node -v", 0); e == nil {
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
	out, e := r.execRoot(distro, script, nodeInstallTimeout)
	if e != nil {
		return false, out, fmt.Errorf("install node in WSL: %w", e)
	}
	// Verify a native node now resolves.
	if !isNativePath(nativeCommandPath(r, distro, "node")) {
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
func (s *Service) ensureUserNpmPrefix(r *wslRunner, distro string) (string, error) {
	script := strings.Join([]string{
		`mkdir -p "$HOME/.npm-global"`,
		`npm config set prefix "$HOME/.npm-global"`,
		`grep -qF 'export PATH="$HOME/.npm-global/bin:$PATH"' "$HOME/.bashrc" 2>/dev/null || printf '%s\n' 'export PATH="$HOME/.npm-global/bin:$PATH"' >> "$HOME/.bashrc"`,
		`grep -qF 'export PATH="$HOME/.npm-global/bin:$PATH"' "$HOME/.profile" 2>/dev/null || printf '%s\n' 'export PATH="$HOME/.npm-global/bin:$PATH"' >> "$HOME/.profile"`,
		`echo prefix-ok`,
	}, " && ")
	return r.execUser(distro, "", script, 0)
}

// InstallTool installs one CLI into WSL: ensure Node, ensure user npm prefix,
// then npm i -g the package, and verify. Idempotent: a present native install
// short-circuits with AlreadyOK. The signature and AlreadyOK semantics are
// pinned by App bindings and tests; upgrades go through UpdateTool.
func (s *Service) InstallTool(tool string) (*InstallResult, error) {
	return s.installOrUpdate(tool, opInstall)
}

// UpdateTool upgrades one CLI inside WSL to the latest npm release. Unlike
// InstallTool it does NOT short-circuit AlreadyOK: a present native install is
// upgraded via `npm i -g <pkg>@latest`, and a missing one is installed by the
// very same command, so callers never need to pre-check which case applies.
// The ensure/verify chain (node floor, npm prefix, login-shell effectiveness)
// is shared with InstallTool, and operations serialize per distro, so an
// upgrade never races an install into the same ~/.npm-global prefix.
func (s *Service) UpdateTool(tool string) (*InstallResult, error) {
	return s.installOrUpdate(tool, opUpdate)
}

type opMode int

const (
	opInstall opMode = iota
	opUpdate
)

func (m opMode) String() string {
	if m == opUpdate {
		return "update"
	}
	return "install"
}

// installOrUpdate is the single install/upgrade spine shared by InstallTool
// and UpdateTool: validate tool → resolve distro once → take the per-distro
// lock → (install mode only: AlreadyOK short-circuit with repair) → ensure
// Node → ensure npm prefix → npm i -g (plain / @latest) → verify native
// resolution and login-shell effectiveness.
func (s *Service) installOrUpdate(tool string, mode opMode) (*InstallResult, error) {
	key := normalizeToolKey(tool)
	pkg, ok := packageForTool(key)
	if !ok {
		return nil, fmt.Errorf("unsupported CLI tool for WSL install: %q", tool)
	}
	r := wslRunnerImpl
	if r == nil {
		msg := errInstallUnsupported
		if mode == opUpdate {
			msg = errUpdateUnsupported
		}
		return &InstallResult{Tool: key, Package: pkg, Success: false, Error: msg}, nil
	}
	// Resolve the distro ONCE: every later call in this operation — and the
	// per-distro lock below — targets this single identity even if the
	// platform probe cache would answer differently mid-operation.
	distro := r.defaultDistro()
	if distro == "" {
		return &InstallResult{Tool: key, Package: pkg, Success: false,
			Error: "no usable WSL distro installed"}, nil
	}
	unlock := lockDistro(distro)
	defer unlock()

	res := &InstallResult{Tool: key, Package: pkg, Distro: distro}
	var logB strings.Builder

	// nativeVersion probes for a native (non-/mnt) install and returns its
	// version line, or "" when absent/Windows-bridged.
	nativeVersion := func() string {
		if !isNativePath(nativeCommandPath(r, distro, key)) {
			return ""
		}
		out, e := r.execUser(distro, "", key+" --version 2>/dev/null | head -1", 0)
		if e != nil {
			return ""
		}
		return firstNonEmptyLine(out)
	}

	// Install mode short-circuits on a present native install; update mode
	// deliberately does not — it must always drive npm to @latest.
	if mode == opInstall {
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
			if diag := s.checkLoginResolution(r, distro, key); diag != "" {
				if prefixLog, e := s.ensureUserNpmPrefix(r, distro); e != nil {
					logB.WriteString("[npm-prefix-repair]\n" + prefixLog + "\nrepair failed: " + e.Error() + "\n")
				} else {
					logB.WriteString("[npm-prefix-repair]\n" + prefixLog + "\n")
				}
				if diag = s.checkLoginResolution(r, distro, key); diag != "" {
					res.Success = false
					res.Error = diag
				} else {
					res.Message += "; repaired login PATH (appended missing npm-global line)"
				}
			}
			res.Log = logB.String()
			return res, nil
		}
	}

	nodeInstalled, nodeLog, err := s.ensureNode(r, distro)
	logB.WriteString("[node]\n" + nodeLog + "\n")
	if err != nil {
		res.Success = false
		res.Error = err.Error()
		res.Log = logB.String()
		return res, nil
	}
	res.NodeInstalled = nodeInstalled

	if prefixLog, e := s.ensureUserNpmPrefix(r, distro); e != nil {
		logB.WriteString("[npm-prefix]\n" + prefixLog + "\n")
		res.Success = false
		res.Error = "configure npm prefix: " + e.Error()
		res.Log = logB.String()
		return res, nil
	}

	// The npm step: plain package for install mode, @latest for update mode
	// (which also covers the not-yet-installed case). Bounded by npmOpTimeout
	// so a stalled npm cannot hold the distro lock indefinitely.
	spec, opTag, failPrefix := pkg, "[install]", "npm install failed: "
	if mode == opUpdate {
		spec, opTag, failPrefix = pkg+"@latest", "[update]", "npm upgrade failed: "
	}
	s.logInfo("wslsetup", "npm i -g in WSL", fmt.Sprintf("mode=%s distro=%s pkg=%s", mode, distro, spec))
	installScript := fmt.Sprintf(`export PATH="$HOME/.npm-global/bin:$PATH"; npm i -g %s`, bashSingleQuote(spec))
	out, e := r.execUser(distro, "", installScript, npmOpTimeout)
	logB.WriteString(opTag + "\n" + out + "\n")
	if e != nil {
		res.Success = false
		res.Error = failPrefix + e.Error()
		res.Log = logB.String()
		return res, nil
	}

	// Verify.
	if v := nativeVersion(); v != "" {
		res.Success = true
		res.Version = v
		if mode == opUpdate {
			res.Message = fmt.Sprintf("%s updated in WSL (%s)", key, v)
		} else {
			res.Message = fmt.Sprintf("%s installed in WSL (%s)", key, v)
		}
		// P3-S0：不再 cp -r 整树播种 ~/.pi/agent（证据 T1/T3）；Pi 在 WSL 内
		// 共享 Windows agent 根（1.3.126），登录凭据无需安装期处理。
		// Final effectiveness check: a plain login shell (no artificial PATH)
		// must resolve the CLI into $HOME/.npm-global/bin, otherwise launched
		// sessions silently run the Windows-side CLI via /mnt/c interop.
		if diag := s.checkLoginResolution(r, distro, key); diag != "" {
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

// InstallSearchTools installs the fd-find + ripgrep pair pi/omp shell out to
// for file search into the default WSL distro, as root via apt (Debian/Ubuntu
// family). CodeBox injects PI_OFFLINE=1 into pi/omp sessions, so pi never
// auto-downloads these tools; a distro without them degrades file search.
// Idempotent: when both tools already probe present (after refreshing the
// platform cache) it short-circuits with AlreadyOK. On success it invalidates
// the platform-side probe cache so env checks / launch warnings observe the
// new state immediately. Non-apt distros surface the raw apt failure plus
// manual guidance in Error.
func (s *Service) InstallSearchTools() (*InstallResult, error) {
	r := wslRunnerImpl
	if r == nil {
		return &InstallResult{
			Tool:    SearchToolsKey,
			Package: searchToolsAptPackages,
			Success: false,
			Error:   errInstallUnsupported,
		}, nil
	}
	distro := r.defaultDistro()
	if distro == "" {
		return &InstallResult{
			Tool:    SearchToolsKey,
			Package: searchToolsAptPackages,
			Success: false,
			Error:   "no usable WSL distro installed",
		}, nil
	}
	res := &InstallResult{Tool: SearchToolsKey, Package: searchToolsAptPackages, Distro: distro}

	// 先失效 platform 侧探测缓存：更早的启动探测/环境检测可能已把“缺失”
	// 缓存住，AlreadyOK 判定与安装后验证都必须看到当前文件系统真值。
	r.resetSearchCache(distro)
	if tools := r.searchStatus(distro); tools.FD && tools.Ripgrep {
		res.AlreadyOK = true
		res.Success = true
		res.Message = "WSL 内 fd-find 与 ripgrep 均已存在，无需安装"
		return res, nil
	}

	s.logInfo("wslsetup", "installing fd-find + ripgrep in WSL", "distro="+distro)
	// 与 ensureNode 同构：DEBIAN_FRONTEND 抑制交互式前端；update -qq 先刷新
	// 索引，避免全新 distro 上 install 因空索引报 Unable to locate package。
	script := strings.Join([]string{
		"export DEBIAN_FRONTEND=noninteractive",
		"apt-get update -qq",
		"apt-get install -y " + searchToolsAptPackages,
	}, " && ")
	out, e := r.execRoot(distro, script, aptOpTimeout)
	res.Log = out
	if e != nil {
		res.Success = false
		res.Error = "apt 安装 fd-find/ripgrep 失败: " + e.Error()
		// 非 apt 系发行版（Alpine/Fedora 等）在 apt-get 不存在时在这里暴露；
		// 失败信息附上对应包管理器的手动指引。
		if strings.Contains(out, "command not found") || strings.Contains(out, "not found") {
			res.Error += "；该发行版可能非 Debian/Ubuntu（无 apt），请手动安装，如 Alpine: apk add fd ripgrep，Fedora: sudo dnf install fd-find ripgrep"
		}
		return res, nil
	}

	// 安装后失效缓存并重新探测验证（fdfind 与 rg 均须在登录 shell PATH 上）。
	r.resetSearchCache(distro)
	tools := r.searchStatus(distro)
	if !tools.FD || !tools.Ripgrep {
		res.Success = false
		res.Error = "apt 安装已完成，但 fd/ripgrep 仍未在 WSL 登录 shell PATH 中解析到；请在 WSL 终端内手动执行: sudo apt-get install -y fd-find ripgrep"
		return res, nil
	}
	res.Success = true
	res.Message = fmt.Sprintf("已在 WSL（%s）内安装 fd-find 与 ripgrep", distro) +
		"（CodeBox 向 pi/omp 会话注入 PI_OFFLINE=1，pi 不会自动下载搜索工具；安装后文件搜索能力恢复）"
	return res, nil
}

// searchToolsAptPackages is the apt package pair installed by
// InstallSearchTools (Debian/Ubuntu name the fd binary fdfind; ripgrep ships rg).
const searchToolsAptPackages = "fd-find ripgrep"

// checkLoginResolution verifies that a plain login shell inside the distro
// resolves the CLI into $HOME/.npm-global/bin rather than a /mnt/<drive>
// Windows passthrough — the same resolution a launched session's `bash -lic`
// builds on (driven by the .profile/.bashrc PATH lines). It must NOT go through
// the probe-PATH exec, whose artificial prefix would mask a broken login PATH.
// Returns "" when effective; otherwise a diagnostic suitable for
// InstallResult.Error / the InstallResult log. A probe that itself errors is
// inconclusive and returns "" (a diagnostic must not fail an otherwise
// completed install); the miss is logged instead.
func (s *Service) checkLoginResolution(r *wslRunner, distro, key string) string {
	script := `printf '%s\n' "$HOME/.npm-global/bin"; command -v ` + bashSingleQuote(key) + ` 2>/dev/null || true`
	out, err := r.execLogin(distro, script)
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
