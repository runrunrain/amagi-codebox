package envcheck

import (
	"context"
	"fmt"
	"strings"
	"time"

	"amagi-codebox/internal/platform"
)

// 混合架构预警（C7）：Windows 上 CodeBox 的内嵌会话默认落在 WSL（resolver 的
// BootstrapWSL 分支），CLI 由 Linux 侧 PATH 解析。当发行版里没有原生安装时，
// `bash -lic '<cli>'` 会沿 Windows 互操作 PATH 命中 /mnt/c/... 的 Windows shim ——
// 会话名义上在 Linux shell 里，实际跑的是 Windows 侧 CLI，路径/换行/stdin 语义
// 被 interop 混合。本文件在 envcheck 收尾时探测登录 shell 的解析结果，命中
// /mnt/* 即向 CheckStatus.Issues 追加一条结构化 warning（后端字段为主，前端按
// 通用 issue 行一句话 + 徽标展示）。

const (
	// wslHybridProbeTimeout bounds one batched `wsl.exe -- bash -lc` probe.
	wslHybridProbeTimeout = 10 * time.Second
	// wslHybridProbeTTL caches the batched probe so one CheckAll (six CheckOne
	// calls) spawns at most one wsl.exe; also bounds staleness after the user
	// installs a CLI into WSL via the WSL CLI settings card.
	wslHybridProbeTTL = 30 * time.Second
)

// wslSessionCommands maps the CLITools that can launch inside WSL to the bare
// command names a WSL session resolves (resolver cliCandidates[0] per app
// type). Order fixes the probe script order (map iteration is random).
// Headroom is absent on purpose: it is a CodeBox-managed Windows-side proxy
// and is never launched inside WSL.
var wslSessionCommands = []struct {
	tool CLITool
	cmd  string
}{
	{ToolClaudeCode, "claude"},
	{ToolOpenCode, "opencode"},
	{ToolCodex, "codex"},
	{ToolPi, "pi"},
	{ToolOmp, "omp"},
}

// wslSessionCommandFor returns the bare command a WSL session resolves for
// tool, or "" when the tool never runs inside WSL.
func wslSessionCommandFor(tool CLITool) string {
	for _, e := range wslSessionCommands {
		if e.tool == tool {
			return e.cmd
		}
	}
	return ""
}

// wslHybridDistro resolves the WSL distro the hybrid probe should target.
// Package var so tests on any OS can inject a distro (mirrors runtimeGOOS).
var wslHybridDistro = func() string {
	return platform.DefaultWSLDistro(nil)
}

// appendWSLHybridArchWarning probes how the distro's login shell resolves the
// tool's session command and appends a warning issue when the hit path is a
// Windows interop passthrough (/mnt/<drive>/...). No-op when WSL is not the
// launch path (non-Windows host / no usable distro), when the tool never runs
// inside WSL, or when the probe is inconclusive. The warning is severity-only:
// it must not flip OverallStatus.AllOK.
func (s *Service) appendWSLHybridArchWarning(status *CheckStatus) {
	if status == nil {
		return
	}
	cmd := wslSessionCommandFor(status.Tool)
	if cmd == "" {
		return
	}
	if runtimeGOOS != "windows" {
		return
	}
	distro := wslHybridDistro()
	if distro == "" {
		return
	}
	resolved := s.probeWSLLoginResolutions(distro)[cmd]
	if resolved == "" || !strings.HasPrefix(resolved, "/mnt/") {
		return
	}
	name := displayToolName(status.Tool)
	status.Issues = append(status.Issues, CheckIssue{
		Severity: SeverityWarning,
		Code:     "wsl_windows_passthrough",
		Message:  fmt.Sprintf("WSL 会话将运行 Windows 侧的 %s", name),
		Detail:   fmt.Sprintf("WSL 内 bash -lc 'command -v %s' 命中 %s（/mnt/* 为 Windows 互操作路径）。建议在「环境检测」页的「WSL 内的 CLI」卡片把 %s 安装进 WSL 原生运行。", cmd, resolved, name),
	})
}

// probeWSLLoginResolutions batch-resolves every session command inside the
// distro's login shell WITHOUT any artificial PATH prefix, mirroring what a
// launched WSL session resolves. Results are TTL-cached under wslHybridMu so
// concurrent/repeated CheckOne calls spawn at most one wsl.exe per TTL window.
func (s *Service) probeWSLLoginResolutions(distro string) map[string]string {
	s.wslHybridMu.Lock()
	defer s.wslHybridMu.Unlock()
	if !s.wslHybridFetchedAt.IsZero() && time.Since(s.wslHybridFetchedAt) < wslHybridProbeTTL {
		return s.wslHybridPaths
	}
	paths := map[string]string{}
	if s.processRunner != nil {
		var script strings.Builder
		script.WriteString("for c in")
		for _, e := range wslSessionCommands {
			script.WriteString(" " + bashQuoteSingle(e.cmd))
		}
		script.WriteString(`; do printf '%s\t' "$c"; command -v "$c" 2>/dev/null || true; printf '\n'; done`)

		ctx, cancel := context.WithTimeout(context.Background(), wslHybridProbeTimeout)
		defer cancel()
		result, err := s.processRunner.Run(ctx, platform.CommandSpec{
			Path:   "wsl.exe",
			Args:   []string{"-d", distro, "--", "bash", "-lc", script.String()},
			Policy: platform.DefaultProcessPolicy(),
		})
		if err == nil && result != nil {
			paths = parseWSLLoginProbeOutput(result.Stdout)
		}
	}
	s.wslHybridPaths = paths
	s.wslHybridFetchedAt = time.Now()
	return paths
}

// parseWSLLoginProbeOutput parses the batched `printf '%s\t'; command -v`
// probe output: one `command\tpath` line per probed command, `command\t` when
// unresolved. Returns command → resolved absolute path; unresolved commands
// and non-path hits (builtins/aliases print a bare name) are dropped.
func parseWSLLoginProbeOutput(out string) map[string]string {
	paths := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.Trim(line, "\r\x00"))
		if line == "" {
			continue
		}
		cmd, path, ok := strings.Cut(line, "\t")
		cmd = strings.TrimSpace(cmd)
		path = strings.TrimSpace(strings.Trim(path, "\r\x00"))
		if !ok || cmd == "" || path == "" || !strings.HasPrefix(path, "/") {
			continue
		}
		paths[cmd] = path
	}
	return paths
}

// bashQuoteSingle wraps s in POSIX single quotes, escaping internal single
// quotes with the classic backslash-quote sequence, for safe embedding in a
// bash -lc script.
func bashQuoteSingle(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
