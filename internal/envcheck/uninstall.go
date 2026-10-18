package envcheck

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"amagi-codebox/internal/platform"
)

// 通用卸载链路（E1 / shared-contract.md）：为 opencode/codex/pi/omp 提供
// ownership-checked 的卸载操作，经现有异步 operation 槽串行管理，终态以
// snapshot.operation（kind=uninstall）为准。
//
// 安全边界（契约）：
//   - 只卸载可确定所有者与安装 prefix 的目标：npm 渠道必须确认可执行文件位
//     于当前解析到的 npm 全局根之下；Homebrew 渠道必须确认位于 `brew
//     --prefix` 之下并从路径推导 formula 名。
//   - 检测到的安装不属于当前 npm/前缀（例如另一个 Node 版本安装）时拒绝卸载，
//     不拿默认 npm 去删别的 Node 环境的包。
//   - native/未知来源一律拒绝并给手动指引，不任意删目录。
//   - 卸载后必须复检；复检仍能检测到时如实报告失败（可能存在另一份安装），
//     不假报全部卸载成功。
//   - Claude Code 走 CleanClaudeCode 按来源卸载，Headroom 走租约保护的
//     CleanHeadroom，均不经本链路。

// brewPrefixProbeTimeout bounds the `brew --prefix` metadata probe.
const brewPrefixProbeTimeout = 15 * time.Second

// canUninstallTool reports whether tool is managed by the generic
// uninstall_tool chain (the four non-Claude CLI agents). Private: the Wails
// binding surface must not grow (integration finding #1).
func canUninstallTool(tool CLITool) bool {
	switch tool {
	case ToolOpenCode, ToolCodex, ToolPi, ToolOmp:
		return true
	default:
		return false
	}
}

// startUninstallTool starts an asynchronous uninstall operation for a
// non-Claude managed tool. Private on purpose: Service is Wails-bound, so an
// exported method would leak a new binding (integration finding #1); the
// frontend reaches this through RunFixAction("uninstall_tool") only. It
// shares the serialized operation slot with install/update (and the
// Claude/Headroom sync uninstalls), so concurrent management operations
// return ErrBusy. Terminal state is published on the operation snapshot
// (kind=uninstall); a returned error only means the request was not accepted.
func (s *Service) startUninstallTool(tool CLITool) (*OperationState, error) {
	if !IsValidCLITool(tool) {
		return nil, fmt.Errorf("unsupported CLI tool: %s", tool)
	}
	if !canUninstallTool(tool) {
		return nil, fmt.Errorf("%s 不走通用卸载：Claude Code 请用按来源卸载（CleanClaudeCode），Headroom 请用租约保护的 CleanHeadroom", tool)
	}
	return s.startOperation(tool, OperationKindUninstall, ClaudeInstallAuto)
}

// uninstallToolWithProgress executes the ownership-checked uninstall and the
// mandatory post-uninstall recheck. It reports progress through the shared
// operation-state reporter.
func (s *Service) uninstallToolWithProgress(tool CLITool, reporter progressReporter) (*InstallResult, error) {
	reporter.report(OperationStepPrecheck, fmt.Sprintf("正在确认 %s 当前安装来源...", displayToolName(tool)), progressPrecheck)

	// 探测失败不能当作“已不存在”（integration finding #3）：nil 状态或显式
	// error 都必须拒绝卸载，只有明确的未安装状态才允许 no-op。
	before, checkErr := s.CheckOne(tool)
	if checkErr != nil || before == nil {
		reason := checkErr
		if reason == nil {
			reason = fmt.Errorf("检测 %s 时返回了空状态", displayToolName(tool))
		}
		return installFailure(tool, "卸载前检测失败，无法确认安装状态，已拒绝卸载", reason), reason
	}
	if !before.Installed {
		return &InstallResult{
			Success: true,
			Message: fmt.Sprintf("未检测到 %s，无需卸载", displayToolName(tool)),
			Tool:    tool,
		}, nil
	}

	method := before.InstallMethod
	if method == InstallMethodUnknown || method == "" {
		method = detectInstallMethodForToolPath(tool, before.ExecutablePath)
	}
	// Homebrew 渠道 rescue：codex/pi 的 checker 不标记 Homebrew，但 Cellar/
	// homebrew 路径特征足以确定归属；交给 brew 渠道做前缀复核。
	if method == InstallMethodNative || method == InstallMethodUnknown {
		if looksLikeHomebrewOwnedPath(resolveRealExecutablePath(before.ExecutablePath)) {
			method = InstallMethodHomebrew
		}
	}

	var channelDetail string
	var channelCmdErr string
	switch method {
	case InstallMethodNPM:
		result, cmdErr := s.uninstallViaNPM(tool, before, reporter)
		if result != nil {
			return result, nil
		}
		channelCmdErr = cmdErr
		channelDetail = "npm"
	case InstallMethodHomebrew:
		result, cmdErr := s.uninstallViaHomebrew(tool, before, reporter)
		if result != nil {
			return result, nil
		}
		channelCmdErr = cmdErr
		channelDetail = "Homebrew"
	default:
		return uninstallRefusedResult(tool, before), nil
	}

	// 终态复检：以实际检测为准，不按退出码报成功。探测失败同样不能当
	// 作已不存在（integration finding #3）：nil/error 复检 → 明确失败。
	reporter.report(OperationStepVerify, fmt.Sprintf("正在复检 %s 卸载结果...", displayToolName(tool)), progressVerify)
	after, afterErr := s.CheckOne(tool)
	if afterErr != nil || after == nil {
		recheckReason := afterErr
		if recheckReason == nil {
			recheckReason = fmt.Errorf("复检 %s 时返回了空状态", displayToolName(tool))
		}
		commandNote := ""
		if channelCmdErr != "" {
			commandNote = fmt.Sprintf("（卸载命令异常：%s）", channelCmdErr)
		}
		return installFailure(tool, fmt.Sprintf("卸载后复检失败，无法确认卸载结果%s，请手动重新检测", commandNote), recheckReason), recheckReason
	}
	if after.Installed {
		remaining := strings.TrimSpace(after.ExecutablePath)
		// 保留脱敏后的命令异常作为失败诊断，不吞错：需要区分“卸载命令报错”
		// 与“存在另一份安装”。
		commandNote := ""
		if channelCmdErr != "" {
			commandNote = fmt.Sprintf("；卸载命令异常：%s", channelCmdErr)
		}
		message := fmt.Sprintf(
			"卸载命令已执行（%s），但复检仍检测到 %s（路径 %s，来源 %s）%s。可能存在另一份安装或卸载未完全生效；请重新检测后按当前来源再次卸载或手动处理。",
			channelDetail, displayToolName(tool), remaining, after.InstallMethod, commandNote,
		)
		return &InstallResult{
			Success: false,
			Message: message,
			Tool:    tool,
			Version: after.Version,
		}, nil
	}
	// The resolver may hide an inaccessible or broken entry. Successful
	// removal also requires the original executable to be positively absent;
	// permission/IO errors are not evidence of deletion.
	if err := verifyUninstalledExecutable(before.ExecutablePath); err != nil {
		if channelCmdErr != "" {
			err = fmt.Errorf("%w；卸载命令异常: %s", err, channelCmdErr)
		}
		return installFailure(tool, "卸载后原入口仍存在或无法确认已删除，请手动重新检测", err), err
	}
	if channelCmdErr != "" {
		// 命令报错但复检与原入口检查确认已不可见：如实说明命令异常。
		return &InstallResult{
			Success: true,
			Message: fmt.Sprintf("%s 复检已不可见，视为卸载成功（卸载命令曾报错：%s）", displayToolName(tool), channelCmdErr),
			Tool:    tool,
		}, nil
	}
	return &InstallResult{
		Success: true,
		Message: fmt.Sprintf("%s 已通过 %s 渠道成功卸载（复检确认）", displayToolName(tool), channelDetail),
		Tool:    tool,
	}, nil
}

func verifyUninstalledExecutable(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("缺少卸载前入口路径，无法确认删除")
	}
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("无法检查原入口 %s: %w", path, err)
	}
	return fmt.Errorf("原入口仍存在: %s", path)
}

// uninstallViaNPM runs `npm uninstall -g <pkg>` after verifying the detected
// executable actually belongs to the currently resolved npm global prefix.
// Returns a non-nil InstallResult for refusals/precondition failures (no
// uninstall command executed); otherwise (nil result) it returns the
// sanitized command error text (empty on a clean run) and lets the caller's
// terminal recheck decide — npm's uninstall of an already-removed package
// can legitimately error while the resulting state is fine.
func (s *Service) uninstallViaNPM(tool CLITool, before *CheckStatus, reporter progressReporter) (*InstallResult, string) {
	if err := s.ensureNPMAvailable(); err != nil {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("卸载 %s 需要 npm，但 npm 当前不可用：%v", displayToolName(tool), err),
			Tool:    tool,
		}, ""
	}

	prefix, err := s.npmGlobalPrefix()
	if err != nil {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("无法确定 npm 全局安装前缀，已拒绝卸载：%v", err),
			Tool:    tool,
		}, ""
	}
	root, _ := s.npmGlobalRootWithPrefixFallback()

	pkg := npmPackageName(tool)
	// 归属校验（integration finding #2）：仅“可执行文件在当前 npm 根下”不够，
	// 同一根下的另一个包同样满足；必须命中本工具预期包目录，或验证 bin shim
	// 文本绑定到预期包，证据不足一律拒绝（不退回纯 prefix 判断）。
	executable := resolveRealExecutablePath(strings.TrimSpace(before.ExecutablePath))
	canonicalRoot := canonicalDirPath(root)
	canonicalPrefix := canonicalDirPath(prefix)
	expectedPkgDir := ""
	if canonicalRoot != "" {
		expectedPkgDir = canonicalDirPath(filepath.Join(canonicalRoot, filepath.FromSlash(pkg)))
	}
	ownedByExpectedPackage := expectedPkgDir != "" && uninstallPathHasPrefix(executable, expectedPkgDir)
	if !ownedByExpectedPackage {
		// 平台 shim（Windows 的 .cmd/.ps1/无后缀脚本不是符号链接，解析后不会
		// 落在包目录内）：仅当未解析路径直属于 <prefix>/bin 或 <prefix> 本身时，
		// 读取 shim 文本验证它绑定到预期包；不执行、不信任 manifest 命令。
		invocation := filepath.Clean(strings.TrimSpace(before.ExecutablePath))
		invocationDir := canonicalDirPath(filepath.Dir(invocation))
		isPrefixBinShim := invocationDir != "" &&
			((canonicalPrefix != "" && (invocationDir == canonicalPrefix || invocationDir == canonicalDirPath(filepath.Join(canonicalPrefix, "bin")))) ||
				(canonicalRoot != "" && invocationDir == canonicalDirPath(filepath.Join(canonicalRoot, ".bin"))))
		if !isPrefixBinShim || !npmShimBindsToPackage(invocation, root, pkg) {
			return &InstallResult{
				Success: false,
				Message: fmt.Sprintf(
					"检测到的 %s 位于 %s，未验证到它与当前 npm 全局根下的预期包 %s 的绑定（prefix %s / root %s，可能属于另一个包或另一个 Node 版本）。为避免删错目标已拒绝卸载；请确认该安装来源后手动处理。",
					displayToolName(tool), executable, pkg, prefix, root,
				),
				Tool: tool,
			}, ""
		}
	}

	command := installCommand{
		description: fmt.Sprintf("npm uninstall -g --prefix %s %s", prefix, pkg),
		path:        s.resolveNPMPath(),
		// 固定传入已验证 prefix：避免 npm 进程环境在探测与卸载之间变化后
		// 删到另一个全局根（integration finding #2）。
		args:    []string{"uninstall", "-g", "--prefix", prefix, pkg},
		timeout: npmCLIToolInstallTimeout,
	}
	reporter.report(OperationStepRunCommand, fmt.Sprintf("正在执行 %s...", command.description), progressRunStart)
	// npm 对未安装包的 uninstall 也可能报错：不在这里裁决成败，返回脱敏的
	// 命令错误文本，交给终态复检决定结果。
	cmdErrText := ""
	if _, runErr := s.runInstallCommandResult(command); runErr != nil {
		cmdErrText = sanitizeInstallerOutput(runErr.Error())
	}
	return nil, cmdErrText
}

// uninstallViaHomebrew runs `brew uninstall <formula>` after verifying the
// detected executable is under the authoritative `brew --prefix` and deriving
// the formula name from the Cellar/opt path segments. Return semantics match
// uninstallViaNPM: non-nil result = refusal; string = sanitized command error.
func (s *Service) uninstallViaHomebrew(tool CLITool, before *CheckStatus, reporter progressReporter) (*InstallResult, string) {
	brewPath, err := s.resolveBrewPath()
	if err != nil {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("卸载 %s 需要 Homebrew，但未找到可用的 brew：%v", displayToolName(tool), err),
			Tool:    tool,
		}, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), brewPrefixProbeTimeout)
	defer cancel()
	result, runErr := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   brewPath,
		Args:   []string{"--prefix"},
		Env:    s.buildEnhancedEnv(),
		Policy: platform.DefaultProcessPolicy(),
	})
	if runErr != nil {
		detail := strings.TrimSpace(resultText(result))
		if detail == "" {
			detail = runErr.Error()
		}
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("无法确定 Homebrew 前缀，已拒绝卸载：brew --prefix 失败（%s）", sanitizeInstallerOutput(detail)),
			Tool:    tool,
		}, ""
	}
	brewPrefixRaw := strings.TrimSpace(firstNonEmptyLine(resultText(result)))
	brewPrefix := canonicalDirPath(brewPrefixRaw)
	if brewPrefixRaw == "" || brewPrefix == "" || !filepath.IsAbs(brewPrefix) {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("brew --prefix 返回了无效前缀 %q，已拒绝卸载", brewPrefix),
			Tool:    tool,
		}, ""
	}

	executable := resolveRealExecutablePath(strings.TrimSpace(before.ExecutablePath))
	if !uninstallPathHasPrefix(executable, brewPrefix) {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf(
				"检测到的 %s 位于 %s，不在 Homebrew 前缀 %s 之下。为避免删错环境已拒绝卸载；请按该路径的来源手动处理。",
				displayToolName(tool), executable, brewPrefix,
			),
			Tool: tool,
		}, ""
	}
	formula := homebrewFormulaFromPath(executable, brewPrefix)
	// 归属校验（integration finding #2）：Cellar 路径只能证明 keg 存在，不能
	// 证明它属于本工具；未映射到工具的 formula（如 node/python 等共享依赖）
	// 一律拒绝，不从任意路径段构造卸载目标。
	if !homebrewFormulaAllowedForTool(tool, formula) {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf(
				"检测到的 %s 位于 Homebrew keg %q，但该 formula 不是 %s 的已知 Homebrew 安装来源（可能是 node/python 等共享依赖）。为避免误删共享依赖已拒绝卸载；请确认来源后手动处理。",
				displayToolName(tool), formula, displayToolName(tool),
			),
			Tool: tool,
		}, ""
	}

	command := installCommand{
		description: fmt.Sprintf("brew uninstall %s", formula),
		path:        brewPath,
		args:        []string{"uninstall", formula},
		timeout:     installCommandTimeout,
	}
	reporter.report(OperationStepRunCommand, fmt.Sprintf("正在执行 %s...", command.description), progressRunStart)
	// 与 npm 渠道一致：不在这里裁决成败，返回脱敏命令错误文本交给终态复检。
	cmdErrText := ""
	if _, runErr := s.runInstallCommandResult(command); runErr != nil {
		cmdErrText = sanitizeInstallerOutput(runErr.Error())
	}
	return nil, cmdErrText
}

// uninstallRefusedResult builds the structured refusal for native/unknown
// ownership: no command runs, the user gets manual guidance.
func uninstallRefusedResult(tool CLITool, before *CheckStatus) *InstallResult {
	path := strings.TrimSpace(before.ExecutablePath)
	message := fmt.Sprintf(
		"无法确定 %s 的安装来源（路径 %s，来源 %s）。为避免误删用户文件已拒绝自动卸载。如确认要移除：npm 安装可在终端执行 `npm uninstall -g %s`，Homebrew 安装可执行 `brew uninstall <formula>`，其余自定义安装请自行确认该路径仅属于该工具后手动删除。",
		displayToolName(tool), path, before.InstallMethod, npmPackageName(tool),
	)
	return &InstallResult{Success: false, Message: message, Tool: tool}
}

// detectInstallMethodForToolPath dispatches to the per-tool path classifier.
func detectInstallMethodForToolPath(tool CLITool, executablePath string) InstallMethod {
	switch tool {
	case ToolOpenCode:
		return detectOpenCodeInstallMethod(executablePath)
	case ToolCodex:
		return detectCodexInstallMethod(executablePath)
	case ToolPi:
		return detectPiInstallMethod(executablePath)
	case ToolOmp:
		return detectOmpInstallMethod(executablePath)
	default:
		return InstallMethodUnknown
	}
}

// canonicalDirPath resolves symlinks when possible so ownership comparisons
// (/var/folders vs /private/var/folders on macOS, alias vs physical install
// dirs for fnm) compare equivalent directories.
func canonicalDirPath(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	cleaned := filepath.Clean(strings.TrimSpace(dir))
	if cleaned == "" || cleaned == "." {
		return cleaned
	}
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil && strings.TrimSpace(resolved) != "" {
		return resolved
	}
	return cleaned
}

// npmShimBindsToPackage reports whether a bin shim created by npm (a text
// script under <prefix>/bin — Windows .cmd/.ps1 or the extensionless POSIX
// shim — or <root>/.bin) references the expected package's install path.
// npm shims embed the "node_modules/<pkg>/…" path as plain text, so checking
// the substring verifies the binding WITHOUT executing the file or trusting
// manifest commands. Oversized/unreadable files fail closed (integration
// finding #2).
func npmShimBindsToPackage(shimPath string, root string, pkg string) bool {
	root = strings.TrimSpace(root)
	pkg = strings.TrimSpace(pkg)
	if root == "" || pkg == "" || strings.TrimSpace(shimPath) == "" {
		return false
	}
	info, err := os.Lstat(shimPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return false
	}
	f, err := os.Open(shimPath)
	if err != nil {
		return false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return false
	}
	want := strings.ToLower(strings.ReplaceAll(filepath.ToSlash(filepath.Join("node_modules", filepath.FromSlash(pkg))), `\`, "/")) + "/"
	content := strings.ToLower(strings.ReplaceAll(string(data), `\`, "/"))
	// Only npm-style executable lines count. Comments or a sibling package
	// whose name merely starts with pkg are not ownership evidence.
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "exec ") && !strings.HasPrefix(line, "& ") && !strings.HasPrefix(line, `"%_prog%" `) {
			continue
		}
		if strings.Contains(line, `"$basedir/`+want) || strings.Contains(line, `"%dp0%/`+want) {
			return true
		}
	}
	return false
}

// homebrewUninstallFormulaAllowlist is the explicit tool→formula relationship
// for the generic uninstall chain (integration finding #2). A Cellar path
// alone proves nothing: the keg could be a shared dependency (node, python,
// …). Only a formula explicitly mapped to the tool may be uninstalled, and
// the mapping stays verifiable in this repo.
//
//   - opencode: homebrew-core formula "opencode"
//   - codex:    homebrew-core formula "codex"
//   - omp:      the can1357/tap/omp keg installs under Cellar/omp (see
//     ompSelfUpdateCommand, which drives the same tap)
//   - pi:       no known Homebrew formula — Cellar-owned pi installs are
//     rejected with manual guidance.
var homebrewUninstallFormulaAllowlist = map[CLITool][]string{
	ToolOpenCode: {"opencode"},
	ToolCodex:    {"codex"},
	ToolOmp:      {"omp"},
}

// homebrewFormulaAllowedForTool reports whether formula is an explicitly
// mapped Homebrew formula for tool.
func homebrewFormulaAllowedForTool(tool CLITool, formula string) bool {
	for _, allowed := range homebrewUninstallFormulaAllowlist[tool] {
		if allowed == formula {
			return true
		}
	}
	return false
}

// uninstallPathHasPrefix reports whether path is equal to or inside prefix,
// comparing normalized (slash, lowercased-on-windows) variants. The caller
// passes a symlink-resolved path (resolveRealExecutablePath).
func uninstallPathHasPrefix(path string, prefix string) bool {
	for _, pathVariant := range normalizedPathVariants(path) {
		for _, prefixVariant := range normalizedPathVariants(prefix) {
			prefixVariant = strings.TrimRight(prefixVariant, "/")
			if prefixVariant == "" || pathVariant == "" {
				continue
			}
			if pathVariant == prefixVariant || strings.HasPrefix(pathVariant, prefixVariant+"/") {
				return true
			}
		}
	}
	return false
}

// looksLikeHomebrewOwnedPath matches the canonical Homebrew installation
// layout markers (Cellar kegs and opt links under a brew prefix).
func looksLikeHomebrewOwnedPath(realPath string) bool {
	normalized := strings.ToLower(filepath.ToSlash(realPath))
	return strings.Contains(normalized, "/cellar/") || strings.Contains(normalized, "/homebrew/")
}

// homebrewFormulaFromPath extracts the formula name from a Cellar or opt
// layout: <prefix>/Cellar/<formula>/<version>/bin/<cmd> or
// <prefix>/opt/<formula>/bin/<cmd>.
func homebrewFormulaFromPath(realPath, brewPrefix string) string {
	rel, err := filepath.Rel(brewPrefix, realPath)
	if err != nil {
		return ""
	}
	segments := strings.Split(filepath.ToSlash(rel), "/")
	// Parse relative to the verified prefix: /opt/homebrew must not be
	// mistaken for the <prefix>/opt/<formula> layout.
	if len(segments) >= 3 && (segments[0] == "Cellar" || segments[0] == "opt") && segments[1] != "" && segments[1] != ".." {
		return segments[1]
	}
	return ""
}
