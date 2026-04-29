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
	SolutionFixPath:       true,
	SolutionInstallTool:   true,
	SolutionInstallNode:   true,
	SolutionRetry:         true,
	SolutionManualCommand: true, // display-only, never executed
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
			Message: "Manual command displayed for user reference. No action was executed.",
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
		return &FixActionResult{
			Success: false,
			Error:   "fix_path is not supported on Windows; use System Properties to edit PATH",
		}, nil
	}

	// Determine which directories to add
	dirs := s.collectPathDirs()
	if len(dirs) == 0 {
		return &FixActionResult{
			Success: false,
			Message: "No additional PATH directories needed",
		}, nil
	}

	// Choose profile file
	profilePath, err := selectShellProfile()
	if err != nil {
		return &FixActionResult{
			Success: false,
			Error:   fmt.Sprintf("cannot determine shell profile: %v", err),
		}, nil
	}

	// Security: reject symlink
	if isSymlink(profilePath) {
		return &FixActionResult{
			Success: false,
			Error:   fmt.Sprintf("shell profile %s is a symlink; refusing to modify for security", profilePath),
		}, nil
	}

	// Security: reject world-writable
	if info, err := os.Stat(profilePath); err == nil {
		if info.Mode().Perm()&0002 != 0 {
			return &FixActionResult{
				Success: false,
				Error:   fmt.Sprintf("shell profile %s is world-writable; refusing to modify for security", profilePath),
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
			Message: "No safe PATH directories to add",
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
				Message:         "PATH is already configured correctly",
				ProfilePath:     profilePath,
				AddedPaths:      safeDirs,
				Changed:         false,
				RequiresRestart: true,
				NextSteps:       []string{"Restart your terminal or run: source " + profilePath},
			}, nil
		}
	}

	// Backup
	backupPath := profilePath + ".amagi-backup-" + time.Now().Format("20060102-150405")
	if existing != "" {
		if err := os.WriteFile(backupPath, []byte(existing), 0o600); err != nil {
			return &FixActionResult{
				Success: false,
				Error:   fmt.Sprintf("failed to backup profile: %v", err),
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
			Error:   fmt.Sprintf("failed to write profile: %v", err),
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
		Message:         fmt.Sprintf("Added %d directories to PATH in %s", len(safeDirs), profilePath),
		ProfilePath:     profilePath,
		BackupPath:      backupPath,
		AddedPaths:      safeDirs,
		Changed:         true,
		RequiresRestart: true,
		NextSteps:       []string{"Restart your terminal or run: source " + profilePath},
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
			Error:   fmt.Sprintf("failed to start install: %v", err),
		}, nil
	}

	return &FixActionResult{
		Success:   true,
		Message:   fmt.Sprintf("Install started for %s (package: %s)", displayToolName(req.Tool), pkgName),
		NextSteps: []string{"Monitor install progress in the UI"},
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
			Message:   "npm/node runtime is now available after PATH refresh",
			Changed:   true,
			NextSteps: []string{"Retry installing your tool"},
		}, nil
	}

	// On macOS, try brew install node
	if runtime.GOOS == "darwin" {
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
					Error:   fmt.Sprintf("brew install node failed: %s", detail),
					NextSteps: []string{
						"Install Node.js manually from https://nodejs.org",
						"Or run in terminal: brew install node",
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
					Message:   "Node.js installed successfully via Homebrew",
					Changed:   true,
					NextSteps: []string{"Restart CodeBox for best results"},
				}, nil
			}

			return &FixActionResult{
				Success: false,
				Error:   "brew install node completed but npm/node is still not functional",
				NextSteps: []string{
					"Restart CodeBox and try again",
					"Or install Node.js manually from https://nodejs.org",
				},
			}, nil
		}

		// brew not found
		return &FixActionResult{
			Success: false,
			Error:   "Homebrew not found; cannot auto-install Node.js",
			NextSteps: []string{
				"Install Homebrew from https://brew.sh, then retry",
				"Or install Node.js manually from https://nodejs.org",
			},
		}, nil
	}

	// Non-macOS: provide manual steps
	return &FixActionResult{
		Success: false,
		Error:   "Automatic Node.js installation is not supported on this platform",
		NextSteps: []string{
			"Install Node.js from https://nodejs.org",
			"Restart CodeBox after installing Node.js",
		},
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
			Error:   fmt.Sprintf("re-check failed: %v", err),
		}, nil
	}

	status := s.GetCachedStatus()
	allOk := status != nil && status.AllOK

	return &FixActionResult{
		Success: true,
		Message: func() string {
			if allOk {
				return "All tools are healthy"
			}
			return fmt.Sprintf("Detection complete; %d issues remaining", len(status.Issues))
		}(),
		Changed:   true,
		NextSteps: []string{"Review updated tool status"},
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

	// Try to find node and npm directories
	for _, cmd := range []string{"node", "npm"} {
		resolved, _, err := resolver.ResolveExecutable(cmd, nil, baseEnv)
		if err == nil && resolved.Path != "" {
			dir := filepath.Dir(resolved.Path)
			if dir != "" && dir != "." {
				pathEntries = append(pathEntries, dir)
			}
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
