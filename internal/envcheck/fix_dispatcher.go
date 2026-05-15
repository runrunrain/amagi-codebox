package envcheck

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// Fix action request/result types
// ---------------------------------------------------------------------------

// FixActionRequest is a white-listed, backend-validated fix request from the
// frontend. The frontend never sends arbitrary commands.
type FixActionRequest struct {
	Action    SolutionType `json:"action"`
	Tool      CLITool      `json:"tool,omitempty"`
	ExtraPath string       `json:"extraPath,omitempty"` // optional extra dir for fix_path
	// Method specifies the Claude Code installation method when action is install_claude_method
	Method string `json:"method,omitempty"`
	// Key is the configuration item key when action is fix_claude_config
	Key string `json:"key,omitempty"`
	// Value is the desired value when action is fix_claude_config
	Value string `json:"value,omitempty"`
	// FilePath is the target configuration file path when action is fix_claude_config
	FilePath string `json:"filePath,omitempty"`
}

// FixActionResult is the synchronous result returned by RunFixAction.
type FixActionResult struct {
	Success         bool     `json:"success"`
	Message         string   `json:"message"`
	Error           string   `json:"error,omitempty"`
	ProfilePath     string   `json:"profilePath,omitempty"`
	BackupPath      string   `json:"backupPath,omitempty"`
	AddedPaths      []string `json:"addedPaths,omitempty"`
	Changed         bool     `json:"changed"`
	RequiresRestart bool     `json:"requiresRestart"`
	NextSteps       []string `json:"nextSteps,omitempty"`
}

// ---------------------------------------------------------------------------
// Whitelist: only these actions are accepted
// ---------------------------------------------------------------------------

var allowedFixActions = map[SolutionType]bool{
	SolutionFixPath:             true,
	SolutionInstallTool:         true,
	SolutionInstallNode:         true,
	SolutionRetry:               true,
	SolutionManualCommand:       true, // display-only, never executed
	SolutionFixClaudeConfig:     true,
	SolutionInstallClaudeMethod: true,
	SolutionCleanClaudeInstall:  true,
}

// ---------------------------------------------------------------------------
// RunFixAction is the single entry point for all fix actions.
// It validates the request against the whitelist, then dispatches.
// ---------------------------------------------------------------------------

// RunFixAction executes a white-listed fix action and returns the result.
// It does NOT accept arbitrary commands from the frontend.
func (s *Service) RunFixAction(req FixActionRequest) (*FixActionResult, error) {
	if !allowedFixActions[req.Action] {
		return nil, fmt.Errorf("unsupported fix action: %s", req.Action)
	}

	switch req.Action {
	case SolutionFixPath:
		return s.runFixPath(req)
	case SolutionInstallTool:
		return s.runInstallTool(req)
	case SolutionInstallNode:
		return s.runInstallNode(req)
	case SolutionRetry:
		return s.runRetry()
	case SolutionManualCommand:
		// Never execute; just return a display-only result
		return &FixActionResult{
			Success: true,
			Message: "命令仅供参考显示，未执行任何操作。",
		}, nil
	case SolutionFixClaudeConfig:
		// Defense-in-depth: validate file path before dispatching to config writer
		if req.FilePath != "" {
			expanded := expandTilde(req.FilePath)
			if !isConfigPathAllowed(expanded) {
				return nil, fmt.Errorf("%s", configPathRejectionMessage(expanded))
			}
		}
		result, err := s.fixClaudeConfig(ConfigFixRequest{
			Key:      req.Key,
			Value:    req.Value,
			FilePath: req.FilePath,
		})
		if err != nil {
			return nil, err
		}
		return &FixActionResult{
			Success:    result.Success,
			Message:    result.Message,
			Error:      result.Error,
			BackupPath: result.BackupPath,
			Changed:    result.Changed,
		}, nil
	case SolutionInstallClaudeMethod:
		result, err := s.serializedInstallOrUpdateWithMethod(ClaudeInstallMethod(req.Method))
		if err != nil {
			return nil, err
		}
		return &FixActionResult{
			Success: result.Success,
			Message: result.Message,
		}, nil
	case SolutionCleanClaudeInstall:
		result, err := s.cleanClaudeCode(InstallMethod(req.Method))
		if err != nil {
			return nil, err
		}
		return &FixActionResult{
			Success: result.Success,
			Message: result.Message,
		}, nil
	default:
		return nil, fmt.Errorf("unimplemented fix action: %s", req.Action)
	}
}

// ---------------------------------------------------------------------------
// fix_path: write Amagi marker block into shell profile
// ---------------------------------------------------------------------------

const (
	amagiMarkerBegin = "# >>> amagi-codebox PATH >>>"
	amagiMarkerEnd   = "# <<< amagi-codebox PATH <<<"
)

func (s *Service) runFixPath(req FixActionRequest) (*FixActionResult, error) {
	if runtime.GOOS == "windows" {
		return s.runFixPathWindows(req)
	}

	// Determine which directories to add
	dirs := s.collectPathDirs()
	if len(dirs) == 0 {
		return &FixActionResult{
			Success: false,
			Message: "不需要添加额外的 PATH 目录",
		}, nil
	}

	// Choose profile file
	profilePath, err := selectShellProfile()
	if err != nil {
		return &FixActionResult{
			Success: false,
			Error:   fmt.Sprintf("无法确定 shell 配置文件: %v", err),
		}, nil
	}

	// Security: reject symlink
	if isSymlink(profilePath) {
		return &FixActionResult{
			Success: false,
			Error:   fmt.Sprintf("shell 配置文件 %s 是符号链接；出于安全考虑拒绝修改", profilePath),
		}, nil
	}

	// Security: reject world-writable
	if info, err := os.Stat(profilePath); err == nil {
		if info.Mode().Perm()&0002 != 0 {
			return &FixActionResult{
				Success: false,
				Error:   fmt.Sprintf("shell 配置文件 %s 权限过于开放（world-writable）；出于安全考虑拒绝修改", profilePath),
			}, nil
		}
	}

	// Validate all directories
	safeDirs := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if err := validatePathDir(dir); err != nil {
			continue // skip invalid dirs
		}
		safeDirs = append(safeDirs, dir)
	}
	if len(safeDirs) == 0 {
		return &FixActionResult{
			Success: false,
			Message: "没有可安全添加的 PATH 目录",
		}, nil
	}

	// Read existing profile
	existing := ""
	if data, err := os.ReadFile(profilePath); err == nil {
		existing = string(data)
	}

	// Check if marker block already exists and is up-to-date
	newBlock := buildMarkerBlock(safeDirs)
	if strings.Contains(existing, amagiMarkerBegin) {
		// Extract existing block and compare
		if strings.Contains(existing, newBlock) {
			return &FixActionResult{
				Success:         true,
				Message:         "PATH 已正确配置",
				ProfilePath:     profilePath,
				AddedPaths:      safeDirs,
				Changed:         false,
				RequiresRestart: true,
				NextSteps:       []string{"请重启终端或运行: source " + profilePath},
			}, nil
		}
	}

	// Backup
	backupPath := profilePath + ".amagi-backup-" + time.Now().Format("20060102-150405")
	if existing != "" {
		if err := os.WriteFile(backupPath, []byte(existing), 0o600); err != nil {
			return &FixActionResult{
				Success: false,
				Error:   fmt.Sprintf("备份配置文件失败: %v", err),
			}, nil
		}
	}

	// Build new content
	var newContent string
	if strings.Contains(existing, amagiMarkerBegin) {
		// Replace existing marker block
		newContent = replaceMarkerBlock(existing, newBlock)
	} else {
		// Append marker block
		if existing != "" && !strings.HasSuffix(existing, "\n") {
			existing += "\n"
		}
		newContent = existing + "\n" + newBlock + "\n"
	}

	// Determine desired permissions:
	//   - If the profile already exists, preserve its current permissions.
	//   - If we are creating a new file, use 0600 (owner-only read/write).
	writePerm := os.FileMode(0o600)
	if info, statErr := os.Stat(profilePath); statErr == nil {
		writePerm = info.Mode().Perm()
	}

	if err := atomicWriteFileWithPerm(profilePath, []byte(newContent), writePerm); err != nil {
		return &FixActionResult{
			Success: false,
			Error:   fmt.Sprintf("写入配置文件失败: %v", err),
		}, nil
	}

	// Reset npm availability cache so the next check picks up the new PATH.
	// This is critical: the npm probe result may have been "node not found"
	// because the GUI process PATH lacked /opt/homebrew/bin. After writing
	// the profile, a re-check through the resolver (which uses shell/login
	// fallback) may find node, flipping npmAvailable to true.
	s.resetNPMCache()

	// Synchronously re-check all tools so the cached status is fresh when
	// the frontend polls immediately after this call returns.
	_, _ = s.CheckAll()

	return &FixActionResult{
		Success:         true,
		Message:         fmt.Sprintf("已将 %d 个目录添加到 PATH（配置文件: %s）", len(safeDirs), profilePath),
		ProfilePath:     profilePath,
		BackupPath:      backupPath,
		AddedPaths:      safeDirs,
		Changed:         true,
		RequiresRestart: true,
		NextSteps:       []string{"请重启终端或运行: source " + profilePath},
	}, nil
}

// collectPathDirs gathers directories that should be in PATH.
func (s *Service) collectPathDirs() []string {
	var dirs []string
	seen := map[string]bool{}

	addIfNew := func(dir string) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		dirs = append(dirs, abs)
	}

	// Check what resolver's augmented PATH provides vs system PATH
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	for _, tool := range SupportedTools() {
		resolved, diags, err := resolver.ResolveExecutable(string(tool), nil, os.Environ())
		if err != nil || resolved.Path == "" {
			continue
		}
		// If the tool is in the resolver's PATH but not in system PATH,
		// add the tool's directory
		toolDir := filepath.Dir(resolved.Path)
		if toolDir != "" && toolDir != "." {
			addIfNew(toolDir)
		}
		// Also add directories from resolver's PATH additions
		for _, entry := range diags.PATHSources {
			addIfNew(entry)
		}
	}

	// Check npm location
	npmResolved, _, npmErr := resolver.ResolveExecutable("npm", nil, os.Environ())
	if npmErr == nil && npmResolved.Path != "" {
		npmDir := filepath.Dir(npmResolved.Path)
		if npmDir != "" && npmDir != "." {
			addIfNew(npmDir)
		}
	}

	// Check node location
	nodeResolved, _, nodeErr := resolver.ResolveExecutable("node", nil, os.Environ())
	if nodeErr == nil && nodeResolved.Path != "" {
		nodeDir := filepath.Dir(nodeResolved.Path)
		if nodeDir != "" && nodeDir != "." {
			addIfNew(nodeDir)
		}
	}

	return dirs
}

// selectShellProfile returns the preferred shell profile path.
func selectShellProfile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot get home directory: %w", err)
	}

	// Prefer .zprofile on macOS (zsh is default shell)
	if runtime.GOOS == "darwin" {
		zprofile := filepath.Join(home, ".zprofile")
		if fileExists(zprofile) {
			return zprofile, nil
		}
		// Fall back to .zshrc
		zshrc := filepath.Join(home, ".zshrc")
		if fileExists(zshrc) {
			return zshrc, nil
		}
		// Create .zprofile if neither exists
		return zprofile, nil
	}

	// Linux: prefer .bash_profile, then .profile
	for _, name := range []string{".bash_profile", ".profile"} {
		candidate := filepath.Join(home, name)
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	// Create .profile as fallback
	return filepath.Join(home, ".profile"), nil
}

// buildMarkerBlock creates the Amagi PATH marker block.
// Each directory is shell-single-quoted to prevent injection.
// Paths containing single quotes, dollar signs, backticks, backslashes,
// newlines, or other dangerous characters are skipped by validatePathDir
// before this function runs; quoting here is defense-in-depth.
func buildMarkerBlock(dirs []string) string {
	var sb strings.Builder
	sb.WriteString(amagiMarkerBegin + "\n")
	for _, dir := range dirs {
		// Use single-quote style: the directory is inside '...', $PATH is
		// outside quotes so it expands normally. This is safe against
		// injection because single quotes in POSIX shell have no
		// interpolation whatsoever. Any embedded single quote in dir is
		// escaped by ending the quote, inserting a backslash-escaped
		// literal single quote, and re-opening the quote (the standard
		// ''\'' idiom).
		sb.WriteString("export PATH='" + shellSingleQuote(dir) + "':\"$PATH\"\n")
	}
	sb.WriteString(amagiMarkerEnd)
	return sb.String()
}

// shellSingleQuote wraps s in POSIX single-quote escaping:
// every ' is replaced with '\” (end quote, escaped quote, reopen quote).
// The result is meant to be placed inside a surrounding pair of single
// quotes by the caller.
func shellSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// isPathShellSafe reports whether dir is safe to embed in a shell PATH
// export line. Paths containing characters that could break out of quoting
// or enable injection are rejected.
func isPathShellSafe(dir string) bool {
	// Reject newlines and control characters.
	if strings.ContainsAny(dir, "\n\r\t\x00") {
		return false
	}
	// Reject characters that are dangerous in any quoting context:
	// backtick, $(), ${}, ;, |, &, <, >.
	if strings.ContainsAny(dir, "`$;|&<>") {
		return false
	}
	return true
}

// replaceMarkerBlock replaces the existing marker block in the content.
func replaceMarkerBlock(content string, newBlock string) string {
	beginIdx := strings.Index(content, amagiMarkerBegin)
	if beginIdx < 0 {
		return content
	}
	endIdx := strings.Index(content, amagiMarkerEnd)
	if endIdx < 0 || endIdx <= beginIdx {
		return content
	}
	// Find end of the marker end line
	endOfBlock := endIdx + len(amagiMarkerEnd)
	if endOfBlock < len(content) && content[endOfBlock] == '\n' {
		endOfBlock++
	}
	return content[:beginIdx] + newBlock + content[endOfBlock:]
}

// validatePathDir checks that a directory is safe to add to PATH.
func validatePathDir(dir string) error {
	// Must be absolute
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("relative path rejected: %s", dir)
	}
	// Must be shell-safe (no injection characters)
	if !isPathShellSafe(dir) {
		return fmt.Errorf("path contains unsafe shell characters: %s", dir)
	}
	// Must exist
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %s", dir)
		}
		return fmt.Errorf("cannot stat directory: %s: %v", dir, err)
	}
	// Must be a directory
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", dir)
	}
	// Must not be world-writable
	if info.Mode().Perm()&0002 != 0 {
		return fmt.Errorf("world-writable directory rejected: %s", dir)
	}
	return nil
}

// isSymlink checks if the path is a symlink.
func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode()&fs.ModeSymlink != 0
}

// ---------------------------------------------------------------------------
// install_tool: whitelist-forwarded install
// ---------------------------------------------------------------------------

func (s *Service) runInstallTool(req FixActionRequest) (*FixActionResult, error) {
	// Validate tool is whitelisted
	if !IsValidCLITool(req.Tool) {
		return nil, fmt.Errorf("unsupported tool for install: %s", req.Tool)
	}

	// Use the backend's own package name, ignoring any frontend-provided name
	pkgName := npmPackageName(req.Tool)
	if pkgName == "" {
		return nil, fmt.Errorf("no npm package mapping for tool: %s", req.Tool)
	}

	// Use the existing async install flow
	_, err := s.StartInstallTool(req.Tool)
	if err != nil {
		return &FixActionResult{
			Success: false,
			Error:   fmt.Sprintf("启动安装失败: %v", err),
		}, nil
	}

	return &FixActionResult{
		Success:   true,
		Message:   fmt.Sprintf("已开始安装 %s（包: %s）", displayToolName(req.Tool), pkgName),
		NextSteps: []string{"请在界面中监控安装进度"},
		Error:     "",
	}, nil
}

// ---------------------------------------------------------------------------
// install_node: attempt to fix npm/node runtime
// ---------------------------------------------------------------------------

func (s *Service) runInstallNode(req FixActionRequest) (*FixActionResult, error) {
	// First, try npm runtime repair (enhanced PATH)
	s.resetNPMCache()
	s.npmOnce.Do(func() {
		s.probeNPMAvailability()
	})

	if s.npmAvailable {
		return &FixActionResult{
			Success:   true,
			Message:   "npm/node 运行时在 PATH 刷新后已可用",
			Changed:   true,
			NextSteps: []string{"请重试安装工具"},
		}, nil
	}

	// Dispatch to platform-specific installer
	switch runtime.GOOS {
	case "darwin":
		return s.runInstallNodeDarwin()
	case "windows":
		return s.runInstallNodeWindows()
	case "linux":
		return s.runInstallNodeLinux()
	default:
		return &FixActionResult{
			Success: false,
			Error:   fmt.Sprintf("不支持的操作系统: %s", runtime.GOOS),
		}, nil
	}
}

// runInstallNodeDarwin installs Node.js on macOS using Homebrew.
func (s *Service) runInstallNodeDarwin() (*FixActionResult, error) {
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	resolved, _, err := resolver.ResolveExecutable("brew", nil, os.Environ())
	if err == nil && resolved.Path != "" {
		// Attempt brew install node with enhanced env
		env := s.buildEnhancedEnv()
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		result, runErr := s.processRunner.Run(ctx, platform.CommandSpec{
			Path:   resolved.Path,
			Args:   []string{"install", "node"},
			Env:    env,
			Policy: platform.DefaultProcessPolicy(),
		})
		if runErr != nil {
			detail := strings.TrimSpace(resultText(result))
			if detail == "" {
				detail = runErr.Error()
			}
			return &FixActionResult{
				Success: false,
				Error:   fmt.Sprintf("brew install node 失败: %s", detail),
				NextSteps: []string{
					"请从 https://nodejs.org 手动安装 Node.js",
					"或在终端运行: brew install node",
				},
			}, nil
		}

		// Verify node is now available
		s.resetNPMCache()
		s.npmOnce.Do(func() {
			s.probeNPMAvailability()
		})
		if s.npmAvailable {
			return &FixActionResult{
				Success:   true,
				Message:   "已通过 Homebrew 成功安装 Node.js",
				Changed:   true,
				NextSteps: []string{"建议重启 CodeBox 以获得最佳效果"},
			}, nil
		}

		return &FixActionResult{
			Success: false,
			Error:   "brew install node 已完成但 npm/node 仍不可用",
			NextSteps: []string{
				"请重启 CodeBox 后重试",
				"或从 https://nodejs.org 手动安装 Node.js",
			},
		}, nil
	}

	// brew not found
	return &FixActionResult{
		Success: false,
		Error:   "未找到 Homebrew，无法自动安装 Node.js",
		NextSteps: []string{
			"请从 https://brew.sh 安装 Homebrew 后重试",
			"或从 https://nodejs.org 手动安装 Node.js",
		},
	}, nil
}

// runInstallNodeWindows installs Node.js on Windows using winget.
func (s *Service) runInstallNodeWindows() (*FixActionResult, error) {
	// 优先尝试 winget
	ctx, cancel := context.WithTimeout(context.Background(), installCommandTimeout)
	defer cancel()

	_, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   "winget",
		Args:   []string{"install", "OpenJS.NodeJS.LTS", "--accept-source-agreements", "--accept-package-agreements", "--silent"},
		Policy: platform.DefaultProcessPolicy(),
	})
	if err == nil {
		return &FixActionResult{
			Success:         true,
			Message:         "正在通过 winget 安装 Node.js LTS，安装完成后请重启终端",
			RequiresRestart: true,
			NextSteps:       []string{"安装完成后请重启终端，然后重新检测环境"},
		}, nil
	}

	// winget 失败，降级为手动指引
	return &FixActionResult{
		Success: true,
		Message: "请手动安装 Node.js",
		NextSteps: []string{
			"1. 访问 https://nodejs.org/ 下载 LTS 版本安装包",
			"2. 运行安装程序，确保勾选「Add to PATH」",
			"3. 安装完成后重启终端",
			"4. 回到环境检测页面点击「重新检测」",
		},
		Changed: false,
	}, nil
}

// runInstallNodeLinux provides manual installation guidance for Linux.
func (s *Service) runInstallNodeLinux() (*FixActionResult, error) {
	return &FixActionResult{
		Success: true,
		Message: "请手动安装 Node.js",
		NextSteps: []string{
			"1. 使用包管理器安装: sudo apt-get install nodejs npm (Debian/Ubuntu)",
			"2. 或: sudo dnf install nodejs (Fedora)",
			"3. 安装完成后重启终端",
		},
		Changed: false,
	}, nil
}

// ---------------------------------------------------------------------------
// retry: just re-run detection
// ---------------------------------------------------------------------------

func (s *Service) runRetry() (*FixActionResult, error) {
	// Reset npm cache so we re-probe
	s.resetNPMCache()

	_, err := s.CheckAll()
	if err != nil {
		return &FixActionResult{
			Success: false,
			Error:   fmt.Sprintf("重新检测失败: %v", err),
		}, nil
	}

	status := s.GetCachedStatus()
	allOk := status != nil && status.AllOK

	return &FixActionResult{
		Success: true,
		Message: func() string {
			if allOk {
				return "所有工具运行正常"
			}
			return fmt.Sprintf("检测完成；仍有 %d 个问题", len(status.Issues))
		}(),
		Changed:   true,
		NextSteps: []string{"查看更新后的工具状态"},
	}, nil
}

// ---------------------------------------------------------------------------
// npm/node runtime enhancement
// ---------------------------------------------------------------------------

// buildEnhancedEnv returns an env slice with an augmented PATH that includes
// directories where node/npm were found by the resolver.
func (s *Service) buildEnhancedEnv() []string {
	baseEnv := os.Environ()
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())

	pathEntries := []string{}

	// Try to find node and npm directories. Keep these ahead of the native
	// Claude directory so the npm bootstrap command `claude install` still uses
	// the freshly installed npm shim when both channels are present.
	for _, cmd := range []string{"node", "npm"} {
		resolved, _, err := resolver.ResolveExecutable(cmd, nil, baseEnv)
		if err == nil && resolved.Path != "" {
			dir := filepath.Dir(resolved.Path)
			if dir != "" && dir != "." {
				pathEntries = append(pathEntries, dir)
			}
		}
	}

	for _, candidate := range claudeNativeDefaultExecutableCandidates() {
		if !fileExists(candidate) {
			continue
		}
		dir := filepath.Dir(candidate)
		if dir != "" && dir != "." {
			pathEntries = append(pathEntries, dir)
		}
	}

	if len(pathEntries) == 0 {
		return baseEnv
	}

	// Find and augment PATH in env
	existingPATH := ""
	for _, entry := range baseEnv {
		if strings.HasPrefix(entry, "PATH=") {
			existingPATH = strings.TrimPrefix(entry, "PATH=")
			break
		}
	}

	// Prepend new entries
	newPATH := strings.Join(pathEntries, string(os.PathListSeparator))
	if existingPATH != "" {
		newPATH += string(os.PathListSeparator) + existingPATH
	}

	// Replace PATH in env
	env := make([]string, 0, len(baseEnv))
	for _, entry := range baseEnv {
		if strings.HasPrefix(entry, "PATH=") {
			env = append(env, "PATH="+newPATH)
		} else {
			env = append(env, entry)
		}
	}

	return env
}

// resetNPMCache allows re-probing npm availability.
func (s *Service) resetNPMCache() {
	s.npmOnce = sync.Once{}
	s.npmAvailable = false
	s.npmResolvedErr = nil
}

// atomicWriteFileWithPerm writes data to path atomically using a temp file
// in the same directory followed by rename. The temp file is created with the
// given perm. The rename preserves the target's existing permissions because
// rename(2) does not change the inode permissions of the source; however the
// new file will carry perm because it was created with that mode.
func atomicWriteFileWithPerm(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Close and remove on any early return.
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if _, writeErr := tmp.Write(data); writeErr != nil {
		cleanup()
		return fmt.Errorf("write temp: %w", writeErr)
	}
	// Set permissions before rename.
	if chmodErr := tmp.Chmod(perm); chmodErr != nil {
		cleanup()
		return fmt.Errorf("chmod temp: %w", chmodErr)
	}
	if syncErr := tmp.Sync(); syncErr != nil {
		cleanup()
		return fmt.Errorf("sync temp: %w", syncErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return fmt.Errorf("close temp: %w", closeErr)
	}
	if renameErr := os.Rename(tmpName, path); renameErr != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename temp to target: %w", renameErr)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Windows PATH fix: registry-based user PATH modification
// ---------------------------------------------------------------------------

// runFixPathWindows adds directories to the user-level PATH on Windows
// using registry operations (reg add HKCU\Environment).
func (s *Service) runFixPathWindows(req FixActionRequest) (*FixActionResult, error) {
	// 1. 收集需要加入 PATH 的目录
	dirs := s.collectPathDirs()
	if len(dirs) == 0 {
		return &FixActionResult{
			Success: false,
			Message: "不需要添加额外的 PATH 目录",
		}, nil
	}

	// 2. 安全校验收集的目录
	safeDirs := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		// 检查是否为绝对路径
		if !filepath.IsAbs(dir) {
			continue
		}
		// 检查目录是否存在
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		// 检查 PATH 值中无危险字符（分号、空字符等）
		if strings.Contains(dir, ";") || strings.Contains(dir, "\x00") {
			continue
		}
		safeDirs = append(safeDirs, dir)
	}
	if len(safeDirs) == 0 {
		return &FixActionResult{
			Success: false,
			Message: "没有可安全添加的 PATH 目录",
		}, nil
	}

	// 3. 读取当前用户 PATH（从注册表 HKCU\Environment\PATH）
	currentPath, err := s.readWindowsUserPath()
	if err != nil {
		return &FixActionResult{
			Success: false,
			Error:   fmt.Sprintf("读取当前 PATH 失败: %v", err),
		}, nil
	}

	// 4. 去重合并
	newDirs := []string{}
	pathSet := make(map[string]bool)
	for _, entry := range strings.Split(currentPath, ";") {
		entry = strings.TrimSpace(entry)
		if entry != "" {
			pathSet[strings.ToLower(entry)] = true
		}
	}
	for _, dir := range safeDirs {
		if !pathSet[strings.ToLower(dir)] {
			newDirs = append(newDirs, dir)
			pathSet[strings.ToLower(dir)] = true
		}
	}

	if len(newDirs) == 0 {
		return &FixActionResult{
			Success: true,
			Message: "PATH 已包含所有必要的目录，无需修改",
			Changed: false,
		}, nil
	}

	// 5. 构建新 PATH 值
	sep := ";"
	newPath := currentPath
	for _, dir := range newDirs {
		if !strings.HasSuffix(newPath, sep) && newPath != "" {
			newPath += sep
		}
		newPath += dir
	}

	// 6. 检查新 PATH 长度是否超过系统限制
	const maxPathLength = 2048
	if len(newPath) > maxPathLength {
		return &FixActionResult{
			Success:   false,
			Message:   fmt.Sprintf("PATH 总长度超过系统限制 (%d > %d)，请手动精简后重试", len(newPath), maxPathLength),
			NextSteps: []string{"请手动编辑系统环境变量 PATH，删除不需要的条目"},
		}, nil
	}

	// 7. 备份当前 PATH（写入注册表旁路值）
	backupName := "PATH_amagi_backup_" + time.Now().Format("20060102_150405")
	backupCmd := platform.CommandSpec{
		Path:   "reg",
		Args:   []string{"add", "HKCU\\Environment", "/v", backupName, "/t", "REG_EXPAND_SZ", "/d", currentPath, "/f"},
		Policy: platform.DefaultProcessPolicy(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := s.processRunner.Run(ctx, backupCmd); err != nil {
		return &FixActionResult{
			Success: false,
			Error:   fmt.Sprintf("备份 PATH 失败: %v", err),
		}, nil
	}

	// 8. 写入新 PATH
	setCmd := platform.CommandSpec{
		Path:   "reg",
		Args:   []string{"add", "HKCU\\Environment", "/v", "PATH", "/t", "REG_EXPAND_SZ", "/d", newPath, "/f"},
		Policy: platform.DefaultProcessPolicy(),
	}
	{
		ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel2()
		if _, err := s.processRunner.Run(ctx2, setCmd); err != nil {
			return &FixActionResult{
				Success: false,
				Error:   fmt.Sprintf("写入 PATH 失败: %v。已备份原 PATH 到注册表值 %s，可手动恢复", err, backupName),
			}, nil
		}
	}

	// 9. 广播环境变更（通知所有窗口）
	// 使用 SendMessageTimeout 广播 WM_SETTINGCHANGE 以通知所有窗口环境变量已变更
	broadcastCmd := platform.CommandSpec{
		Path: "powershell.exe",
		Args: []string{"-NoProfile", "-NonInteractive", "-Command",
			"Add-Type -Name Win32 -Namespace System -MemberDefinition '[DllImport(\"user32.dll\")]public static extern IntPtr SendMessageTimeout(IntPtr hWnd,uint Msg,UIntPtr wParam,string lParam,uint fuFlags,uint uTimeout,out UIntPtr lpdwResult);'; $HWND_BROADCAST=0xffff; $WM_SETTINGCHANGE=0x001a; $result=0; [System.Win32]::SendMessageTimeout($HWND_BROADCAST,$WM_SETTINGCHANGE,0,'Environment',2,5000,[ref]$result)"},
		Policy: platform.DefaultProcessPolicy(),
	}
	s.processRunner.Run(context.Background(), broadcastCmd) // 忽略广播错误

	return &FixActionResult{
		Success:         true,
		Message:         fmt.Sprintf("已将 %d 个目录添加到用户 PATH", len(newDirs)),
		Changed:         true,
		RequiresRestart: true,
		NextSteps:       []string{"请重启终端或重新登录以使 PATH 修改生效"},
		BackupPath:      fmt.Sprintf("注册表值 HKCU\\Environment\\%s", backupName),
	}, nil
}

// readWindowsUserPath reads the current user-level PATH from the registry.
func (s *Service) readWindowsUserPath() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   "reg",
		Args:   []string{"query", "HKCU\\Environment", "/v", "PATH"},
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		// Check if the error is "value not found" (PATH doesn't exist for this user).
		// This is a normal condition for new users and should not block the operation.
		output := strings.TrimSpace(resultText(result))
		// 区分"值不存在"和"其他错误"
		if strings.Contains(output, "unable to find") || strings.Contains(output, "找不到") {
			return "", nil // PATH 值不存在是正常情况（新用户）
		}
		return "", fmt.Errorf("读取注册表 PATH 失败: %v", err)
	}

	// 解析输出
	output := strings.TrimSpace(resultText(result))
	// reg query 输出格式:
	// HKEY_CURRENT_USER\Environment
	//     PATH    REG_EXPAND_SZ    C:\Windows;...
	// 需要提取最后一部分
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		// 找 "REG_EXPAND_SZ" 或 "REG_SZ" 之后的空格
		for _, typePrefix := range []string{"REG_EXPAND_SZ    ", "REG_SZ    ", "REG_EXPAND_SZ   ", "REG_SZ   "} {
			if idx := strings.Index(line, typePrefix); idx >= 0 {
				return strings.TrimSpace(line[idx+len(typePrefix):]), nil
			}
		}
	}
	// 输出格式不符合预期，返回错误而非静默继续
	return "", fmt.Errorf("无法解析注册表 PATH 输出格式: %s", output)
}
