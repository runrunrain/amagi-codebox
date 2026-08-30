package platform

// WSL home / UNC 桥接（Bug B：WSL 模式下 pi/omp 配置落盘到 WSL 侧）。
//
// 本文件是 wsl.go 的只读扩展（wsl.go 由另一切片维护，禁止修改）：复用其
// distro 探测与 WSLENV 转发，新增三类能力——
//  1. WSLUserHome：解析 distro 默认用户（wsl.exe -- sh 的运行身份，与 PTY
//     会话 bash -lic 的用户一致）的 $HOME，缓存；
//  2. WSLToUNC：把 WSL 内绝对路径映射为 \\wsl.localhost\<distro>\…（旧别名
//     \\wsl$\ 兜底）UNC 路径，供 Windows 侧直接读写 9P 文件系统；
//  3. WSLSearchToolStatus / WSLChmod：fd/ripgrep 一次探测（缓存）与 Linux
//     权限补偿（Windows os.Chmod 经 9P 只影响只读属性，不改 POSIX mode 位，
//     0600/0700 语义必须经 wsl.exe chmod 达成——2026-08-30 本机 WSL2 Ubuntu
//     实证）。
//
// 实证结论（真实 WSL2 Ubuntu + Windows Go 进程）：
//   - os.MkdirAll / os.WriteFile / os.Rename 在 \\wsl.localhost UNC 上全部成功，
//     但落盘权限为 umask 默认（目录 0755 / 文件 0644），且文件 owner 为 distro
//     默认用户（与 wsl.exe chmod 的运行身份一致，chmod 补偿可行）；
//   - 0600 文件（如 auth.json）可从 Windows 侧经 UNC 读取（merge 读回不受影响）。
//
// 所有探测均走包级 var wslScriptRunner，测试可注入假实现而不触发真实 wsl.exe；
// 非 Windows 平台入口函数直接返回零值（其他平台的 capabilities 目录不含 wsl
// 候选，探测天然不可达）。

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// wslScriptRunner runs `wsl.exe -d <distro> -- sh -lc <script>` and returns raw
// stdout bytes. Package var so tests can inject a fake without invoking real WSL.
// The inner command's stdout is the Linux program's raw bytes (UTF-8), unlike
// `wsl -l` output which is UTF-16 (see wsl.go decodeWSLListOutput).
var wslScriptRunner = func(distro string, script string) ([]byte, error) {
	cmd := exec.Command("wsl.exe", "-d", distro, "--", "sh", "-lc", script)
	// 后台探测 fork 的 wsl.exe 必须抑制控制台窗口（与 wsl.go 的 wslDistroLister
	// 同策略，Bug A：GUI 父进程下 console 子系统 exe 会闪窗）。
	SuppressConsoleWindow(cmd)
	return cmd.Output()
}

// wslUNCRootProbe resolves the Windows UNC share root for a distro:
// \\wsl.localhost\<distro> (Win10 2004+) with the legacy \\wsl$\<distro> alias as
// fallback. Package var so tests can redirect the "UNC" root to a local temp dir.
var wslUNCRootProbe = func(distro string) string {
	for _, prefix := range []string{`\\wsl.localhost\`, `\\wsl$\`} {
		root := prefix + distro
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root
		}
	}
	return ""
}

var (
	wslHomeMu    sync.Mutex
	wslHomeCache = map[string]string{}

	wslUNCRootMu    sync.Mutex
	wslUNCRootCache = map[string]string{}

	wslToolsMu    sync.Mutex
	wslToolsCache = map[string]WSLSearchTools{}
)

// WSLSearchTools reports which of the search tools pi/omp shell out to exist in
// a distro. FD covers both `fd` and the Debian/Ubuntu `fdfind` binary name.
type WSLSearchTools struct {
	FD      bool
	Ripgrep bool
}

// WSLUserHome returns the $HOME of the distro's default user (the same identity
// `wsl.exe -d <distro> --` runs, and therefore the identity the PTY session's
// `bash -lic` runs as). Cached per distro, including negative results ("" —
// avoid re-forking wsl.exe on every session launch). Empty when not on Windows,
// the distro is unusable, or the probe fails / returns a non-absolute path.
func WSLUserHome(distro string) string {
	distro = strings.TrimSpace(distro)
	if distro == "" || runtime.GOOS != "windows" {
		return ""
	}
	wslHomeMu.Lock()
	defer wslHomeMu.Unlock()
	if home, ok := wslHomeCache[distro]; ok {
		return home
	}
	home := ""
	if out, err := wslScriptRunner(distro, `printf %s "$HOME"`); err == nil {
		// Linux 侧 stdout 为原始 UTF-8；仅去 CR/空白。wsl.exe 自身错误走
		// stderr 且 err != nil，不会污染 stdout。
		home = strings.TrimSpace(decodeWSLListOutput(out))
	}
	if !strings.HasPrefix(home, "/") {
		home = "" // 必须是绝对 Linux 路径，否则视为探测失败
	}
	wslHomeCache[distro] = home
	return home
}

// WSLToUNC maps an absolute Linux path inside the distro to its Windows UNC
// form for direct os.* I/O from the Windows host:
//
//	/home/u/.pi/agent + Ubuntu -> \\wsl.localhost\Ubuntu\home\u\.pi\agent
//
// Returns "" when the distro share is unreachable (both UNC aliases fail).
func WSLToUNC(distro string, linuxPath string) string {
	distro = strings.TrimSpace(distro)
	trimmed := strings.TrimSpace(linuxPath)
	if distro == "" || !strings.HasPrefix(trimmed, "/") {
		return ""
	}
	root := wslUNCRootCached(distro)
	if root == "" {
		return ""
	}
	return root + `\` + strings.ReplaceAll(strings.Trim(trimmed, "/"), "/", `\`)
}

func wslUNCRootCached(distro string) string {
	wslUNCRootMu.Lock()
	defer wslUNCRootMu.Unlock()
	if root, ok := wslUNCRootCache[distro]; ok {
		return root
	}
	root := wslUNCRootProbe(distro)
	wslUNCRootCache[distro] = root
	return root
}

// WSLSearchToolStatus probes (once per distro, cached) whether fd/fdfind and
// ripgrep are on the distro's login-shell PATH — the same PATH context the PTY
// session's `bash -lic` sees. Missing tools degrade pi/omp file-search features
// (pi prints "fd not found" warnings under PI_OFFLINE=1 and silently skips the
// download); callers surface a WARN with install guidance.
func WSLSearchToolStatus(distro string) WSLSearchTools {
	distro = strings.TrimSpace(distro)
	if distro == "" || runtime.GOOS != "windows" {
		return WSLSearchTools{}
	}
	wslToolsMu.Lock()
	defer wslToolsMu.Unlock()
	if tools, ok := wslToolsCache[distro]; ok {
		return tools
	}
	tools := WSLSearchTools{}
	// 标记行格式固定为 `fd:<path|missing>` / `rg:<path|missing>`，整体 exit 0，
	// 输出可无歧义按行解析（fd 缺失时不会与 rg 行混淆）。
	script := `echo fd:$(command -v fd || command -v fdfind || echo missing); echo rg:$(command -v rg || echo missing)`
	if out, err := wslScriptRunner(distro, script); err == nil {
		for _, line := range strings.Split(decodeWSLListOutput(out), "\n") {
			line = strings.TrimSpace(strings.Trim(line, "\r"))
			switch {
			case strings.HasPrefix(line, "fd:") && strings.TrimSpace(line[3:]) != "missing":
				tools.FD = true
			case strings.HasPrefix(line, "rg:") && strings.TrimSpace(line[3:]) != "missing":
				tools.Ripgrep = true
			}
		}
	}
	wslToolsCache[distro] = tools
	return tools
}

// WSLChmod applies a Linux chmod inside the distro. Windows os.Chmod through
// the 9P UNC share only toggles the read-only attribute and never sets POSIX
// mode bits, so 0600/0700 contracts on WSL-side files must be enforced by
// running chmod in the distro itself. mode is embedded verbatim by this
// package's own callers (0700/0600); paths are single-quote escaped.
func WSLChmod(distro string, mode string, linuxPaths ...string) error {
	distro = strings.TrimSpace(distro)
	if distro == "" || len(linuxPaths) == 0 {
		return nil
	}
	if mode == "" || strings.ContainsAny(mode, " \t'\";&|`$") {
		// 防御：mode 由本包调用方内联常量传入，此处只做硬校验。
		return errWSLChmodInvalidMode(mode)
	}
	quoted := make([]string, 0, len(linuxPaths))
	for _, p := range linuxPaths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		quoted = append(quoted, wslShSingleQuote(p))
	}
	if len(quoted) == 0 {
		return nil
	}
	script := "chmod " + mode + " " + strings.Join(quoted, " ")
	if _, err := wslScriptRunner(distro, script); err != nil {
		return err
	}
	return nil
}

type wslChmodError struct{ mode string }

func (e *wslChmodError) Error() string { return "invalid wsl chmod mode: " + e.mode }

func errWSLChmodInvalidMode(mode string) error { return &wslChmodError{mode: mode} }

// wslShSingleQuote wraps a token in POSIX single quotes with the standard
// '\” escaping (same idiom as pty.buildWSLInnerCommand's bashSingleQuote).
func wslShSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// EmbeddedLaunchTargetsWSL reports whether an embedded (or default-mode)
// launch with the given requested shell would run inside WSL on this host.
// It mirrors the resolver's WSL branch (resolver.go): Windows host + embedded
// launch mode + effective shell (requested or capability default) resolved to
// key "wsl" — which itself requires a usable distro. Callers use this BEFORE
// resolving the launch spec to redirect agent-config writes to the WSL side.
// Returns false on non-Windows hosts unconditionally.
func EmbeddedLaunchTargetsWSL(launchMode string, requestedShellPath string, env []string) bool {
	return embeddedLaunchTargetsWSLForOS(runtime.GOOS, launchMode, requestedShellPath, env)
}

// embeddedLaunchTargetsWSLForOS is the testable core of
// EmbeddedLaunchTargetsWSL: osName gates the Windows-only branch while the
// shell decision reuses the resolver's own resolveRequestedShell against
// windows-shaped capabilities.
func embeddedLaunchTargetsWSLForOS(osName string, launchMode string, requestedShellPath string, env []string) bool {
	if osName != "windows" {
		return false
	}
	if mode := strings.TrimSpace(strings.ToLower(launchMode)); mode != "" && mode != "embedded" {
		return false
	}
	shell, _, _ := resolveRequestedShell(strings.TrimSpace(requestedShellPath), env, capabilitiesForTarget("windows", runtime.GOARCH))
	return strings.EqualFold(shell.Key, "wsl")
}

// resetWSLHomeCachesForTest clears the home/UNC/tools probe caches.
func resetWSLHomeCachesForTest() {
	wslHomeMu.Lock()
	wslHomeCache = map[string]string{}
	wslHomeMu.Unlock()
	wslUNCRootMu.Lock()
	wslUNCRootCache = map[string]string{}
	wslUNCRootMu.Unlock()
	wslToolsMu.Lock()
	wslToolsCache = map[string]WSLSearchTools{}
	wslToolsMu.Unlock()
}
