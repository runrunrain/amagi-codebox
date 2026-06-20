package envcheck

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"amagi-codebox/internal/platform"
)

// This file implements the Claude Code npm install "interrupt self-heal"
// capability. Background: Claude Code 2.1.x distributes a ~206MB native
// binary through an npm shell package (@anthropic-ai/claude-code) that
// hardlinks the platform subpackage (@anthropic-ai/claude-code-<os>-<arch>)
// into bin/claude via a postinstall script.
//
// When the npm install is interrupted mid-extract:
//   1. npm leaves behind a staging directory it was renaming into
//      (@anthropic-ai/.claude-code-XXXXXX), which triggers ENOTEMPTY
//      (errno -66) on every subsequent install/uninstall attempt and
//      deadlocks the package ("install fails, uninstall fails").
//   2. A truncated unsigned ~19MB binary gets hardlinked into bin/claude,
//      which macOS AMFI rejects at exec time with SIGKILL (exit code 137).
//
// Self-heal automates the manually-verified recovery documented in
// Claude-Code-安装异常说明.md:
//   - detect & remove staging residue before install/uninstall
//   - detect & remove the orphan npm bin link
//   - identify truncated/unsigned binaries as corrupted (not "not installed")
//
// Strict safety boundaries:
//   - Only @anthropic-ai/.claude-code-* staging directories are removed.
//   - User configuration (~/.claude.json, ~/.claude/, ~/.zshrc, ~/.zprofile,
//     ~/.local/share/claude/versions/) is NEVER touched here.
//   - npm global root is resolved dynamically via `npm prefix -g` /
//     `npm root -g`; no host-specific paths are hardcoded.

// claudeStagingGlobPrefix is the glob prefix npm uses for staging renames
// during install/uninstall of the @anthropic-ai/claude-code package. Only
// directories matching this exact prefix are eligible for self-heal cleanup.
const claudeStagingGlobPrefix = ".claude-code-*"

// claudeNPMPackageDirName is the canonical npm package directory name.
const claudeNPMPackageDirName = "claude-code"

// claudeNPMScopedDirName is the npm scope directory under node_modules.
const claudeNPMScopedDirName = "@anthropic-ai"

// claudeNPMIntegrityMinBytes is the lower-bound size for a healthy Claude
// Code native binary. Official binaries are ~206MB; truncated post-interrupt
// shards observed in the field are ~19MB. We set the bar at 100MB so that
// any binary smaller than half the official size is treated as corrupted
// without being so strict that legitimate minor size variation across
// releases triggers false positives.
//
// Declared as a variable (not a constant) so tests can lower it to keep
// fixture files small.
var claudeNPMIntegrityMinBytes int64 = 100 * 1024 * 1024

// claudeCodesignIdentifier is the expected code-signing identifier on macOS.
const claudeCodesignIdentifier = "com.anthropic.claude-code"

// claudeCodesignTimeout bounds the codesign(1) verification call.
const claudeCodesignTimeout = 10 * time.Second

// claudeNPMResidueCleanupResult describes what self-heal removed, so callers
// can produce actionable user-facing messages.
type claudeNPMResidueCleanupResult struct {
	StagingDirs   []string
	PackageDirs   []string
	OrphanBinLinks []string
}

// Total returns the total number of filesystem entries removed.
func (r claudeNPMResidueCleanupResult) Total() int {
	return len(r.StagingDirs) + len(r.PackageDirs) + len(r.OrphanBinLinks)
}

// Flattened returns all removed entries in a stable order.
func (r claudeNPMResidueCleanupResult) Flattened() []string {
	out := make([]string, 0, r.Total())
	out = append(out, r.StagingDirs...)
	out = append(out, r.PackageDirs...)
	out = append(out, r.OrphanBinLinks...)
	return out
}

// npmGlobalNodeModulesScopedDir resolves the absolute path to the
// `<npm prefix>/node_modules/@anthropic-ai` directory. It uses the same
// npm-prefix resolver as the rest of envcheck to avoid hardcoding any
// host-specific path.
func (s *Service) npmGlobalNodeModulesScopedDir() (string, error) {
	prefix, err := s.npmGlobalPrefix()
	if err != nil {
		return "", err
	}
	scoped := filepath.Join(prefix, "node_modules", claudeNPMScopedDirName)
	return scoped, nil
}

// detectClaudeNPMStagingResidue globs the @anthropic-ai scoped directory
// for staging directories matching `.claude-code-*`. It does NOT inspect
// or modify any other directory. Returns the absolute paths of any staging
// directories found.
func (s *Service) detectClaudeNPMStagingResidue() ([]string, error) {
	scopedDir, err := s.npmGlobalNodeModulesScopedDir()
	if err != nil {
		return nil, err
	}
	return globClaudeStagingDirs(scopedDir), nil
}

// globClaudeStagingDirs is the pure filesystem helper extracted for
// testability. It only matches directories directly under scopedDir whose
// base name matches `.claude-code-*`. The main package directory
// (`claude-code`) is intentionally NOT matched here.
func globClaudeStagingDirs(scopedDir string) []string {
	scopedDir = filepath.Clean(strings.TrimSpace(scopedDir))
	if scopedDir == "" || scopedDir == "." {
		return nil
	}
	pattern := filepath.Join(scopedDir, claudeStagingGlobPrefix)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		info, statErr := os.Stat(m)
		if statErr != nil {
			continue
		}
		if !info.IsDir() {
			continue
		}
		// Defense-in-depth: ensure the match is a DIRECT child of the scoped
		// directory (filepath.Glob does not recurse, but be explicit).
		if filepath.Dir(filepath.Clean(m)) != scopedDir {
			continue
		}
		out = append(out, filepath.Clean(m))
	}
	return out
}

// cleanClaudeNPMResidueIfPresent is the install pre-check entry point. If
// staging residue is detected, it removes it and returns a non-nil result
// describing what was cleaned. If no residue is present it returns nil with
// no error. It is safe to call before any install attempt.
func (s *Service) cleanClaudeNPMResidueIfPresent() (*claudeNPMResidueCleanupResult, error) {
	staging, err := s.detectClaudeNPMStagingResidue()
	if err != nil {
		return nil, err
	}
	if len(staging) == 0 {
		return nil, nil
	}
	return s.cleanClaudeNPMResidue()
}

// cleanClaudeNPMResidue performs the three-step recovery documented in
// Claude-Code-安装异常说明.md section 四.2:
//   1. Remove every @anthropic-ai/.claude-code-* staging directory.
//   2. Remove the @anthropic-ai/claude-code main package directory.
//   3. Remove orphan npm bin links (claude / claude.cmd / claude.exe) that
//      point at the now-deleted package.
//
// It does NOT touch any user configuration, any other package, or any
// native-channel binary under ~/.local.
func (s *Service) cleanClaudeNPMResidue() (*claudeNPMResidueCleanupResult, error) {
	scopedDir, err := s.npmGlobalNodeModulesScopedDir()
	if err != nil {
		return nil, err
	}

	result := &claudeNPMResidueCleanupResult{}

	// Step 1: staging directories.
	for _, staging := range globClaudeStagingDirs(scopedDir) {
		if rmErr := os.RemoveAll(staging); rmErr != nil {
			// Surface removal failures to the caller; partial cleanup is
			// still returned so the user can see what succeeded.
			return result, fmt.Errorf("remove staging directory %q: %w", staging, rmErr)
		}
		result.StagingDirs = append(result.StagingDirs, staging)
	}

	// Step 2: main package directory. Only remove the canonical name; never
	// wildcard-remove siblings.
	packageDir := filepath.Join(scopedDir, claudeNPMPackageDirName)
	if info, statErr := os.Stat(packageDir); statErr == nil && info.IsDir() {
		if rmErr := os.RemoveAll(packageDir); rmErr != nil {
			return result, fmt.Errorf("remove package directory %q: %w", packageDir, rmErr)
		}
		result.PackageDirs = append(result.PackageDirs, packageDir)
	}

	// Step 3: orphan npm bin links. Resolve npm prefix (the dir containing
	// `bin/claude`), and only remove entries that resolve (or symlink) into
	// the scoped/package directory we just cleared. This prevents removing
	// user-installed claude binaries from unrelated locations.
	prefix, prefixErr := s.npmGlobalPrefix()
	if prefixErr == nil {
		result.OrphanBinLinks = removeOrphanClaudeBinLinks(prefix, packageDir)
	}

	return result, nil
}

// removeOrphanClaudeBinLinks removes npm-managed `claude` bin entries under
// the npm prefix that point at the (now-removed) @anthropic-ai/claude-code
// package directory. It only considers candidates inside the npm prefix's
// bin/ directory and the prefix root, never user PATH directories.
func removeOrphanClaudeBinLinks(npmPrefix string, packageDir string) []string {
	npmPrefix = filepath.Clean(strings.TrimSpace(npmPrefix))
	if npmPrefix == "" || npmPrefix == "." {
		return nil
	}
	candidates := []string{
		filepath.Join(npmPrefix, "bin", "claude"),
		filepath.Join(npmPrefix, "bin", "claude.cmd"),
		filepath.Join(npmPrefix, "bin", "claude.exe"),
		filepath.Join(npmPrefix, "claude"),
		filepath.Join(npmPrefix, "claude.cmd"),
		filepath.Join(npmPrefix, "claude.exe"),
	}
	if isWindows() {
		// On Windows the .cmd/.exe variants are the primary entry points.
		candidates = append(candidates,
			filepath.Join(npmPrefix, "claude.cmd"),
			filepath.Join(npmPrefix, "claude.exe"),
		)
	}

	removed := []string{}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		info, err := os.Lstat(candidate)
		if err != nil {
			continue
		}
		// Only remove if it is a symlink (typical npm global install) or a
		// regular file that looks like a hardlink/shim into the removed
		// package directory. We do NOT remove directories here.
		if info.IsDir() {
			continue
		}
		if !looksLikeOrphanClaudeBinLink(candidate, info, packageDir) {
			continue
		}
		if rmErr := os.Remove(candidate); rmErr != nil {
			continue
		}
		removed = append(removed, candidate)
	}
	return removed
}

// looksLikeOrphanClaudeBinLink decides whether a candidate bin entry is safe
// to remove as part of npm-package cleanup. The rule is conservative: only
// remove symlinks whose target lives under the @anthropic-ai package
// directory we just removed, or regular files that resolve into that
// directory via symlink/hardlink (npm uses both depending on platform).
func looksLikeOrphanClaudeBinLink(candidate string, info os.FileInfo, packageDir string) bool {
	packageDir = filepath.Clean(packageDir)
	if packageDir == "" || packageDir == "." {
		return false
	}
	// Follow symlinks. If the resolved target lives inside the package
	// directory we just removed, this is unambiguously an orphan.
	if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
		resolved = filepath.Clean(resolved)
		if strings.HasPrefix(resolved+string(filepath.Separator), packageDir+string(filepath.Separator)) {
			return true
		}
	}
	// Fallback: if the link target string (read without resolution) mentions
	// the scoped package path, treat it as an orphan. This covers broken
	// symlinks where the target no longer exists (the common post-cleanup
	// state).
	if info.Mode()&os.ModeSymlink != 0 {
		if dest, err := os.Readlink(candidate); err == nil {
			normalizedDest := normalizeClaudePath(dest)
			normalizedPkg := normalizeClaudePath(packageDir)
			if normalizedDest != "" && normalizedPkg != "" && strings.Contains(normalizedDest, normalizedPkg) {
				return true
			}
			// npm bin links commonly point at "../lib/node_modules/@anthropic-ai/..."
			// even when the resolved absolute target is gone. Match on the
			// distinctive path marker.
			if strings.Contains(normalizedDest, "/node_modules/@anthropic-ai/claude-code") {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Binary integrity inspection (P0-3)
// ---------------------------------------------------------------------------

// ClaudeBinaryIntegrity classifies the result of inspecting a Claude Code
// binary on disk.
type ClaudeBinaryIntegrity struct {
	// Path is the absolute path that was inspected.
	Path string
	// Exists reports whether the file exists at all.
	Exists bool
	// SizeBytes is the file size (0 if not exists).
	SizeBytes int64
	// Signed is true on macOS when `codesign -dv` confirms the binary carries
	// a valid signature. Always true on non-macOS platforms (signing is not
	// enforced there).
	Signed bool
	// CodesignOutput is the raw codesign output (macOS only), retained for
	// diagnostics.
	CodesignOutput string
	// Corrupted is true when the binary should be treated as a truncated /
	// unsigned shard rather than a usable installation.
	Corrupted bool
	// Reason explains why Corrupted was set.
	Reason string
}

// InspectClaudeBinaryIntegrity performs size + (macOS) signature checks on
// the given path. It is safe to call from any goroutine and never exec's the
// binary itself -- only `codesign -dv` on macOS. The codesign invocation is
// bounded by claudeCodesignTimeout.
//
// On non-macOS platforms the signature check is skipped (returns Signed=true
// unconditionally) but the size check still applies, so truncated Linux /
// Windows binaries are still detected.
func InspectClaudeBinaryIntegrity(path string) ClaudeBinaryIntegrity {
	report := ClaudeBinaryIntegrity{Path: path}
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		report.Reason = "empty path"
		return report
	}
	info, err := os.Stat(cleaned)
	if err != nil {
		// Missing file is not "corrupted" -- it is simply absent. Callers
		// distinguish via Exists.
		return report
	}
	if info.IsDir() {
		report.Exists = true
		report.Corrupted = true
		report.Reason = "path is a directory, not a binary"
		return report
	}
	report.Exists = true
	report.SizeBytes = info.Size()

	if report.SizeBytes < claudeNPMIntegrityMinBytes {
		report.Corrupted = true
		report.Reason = fmt.Sprintf("binary size %d bytes is below healthy minimum %d bytes (likely a truncated post-interrupt shard)", report.SizeBytes, claudeNPMIntegrityMinBytes)
		// Still attempt the signature check on macOS for diagnostics, but the
		// size failure is already dispositive.
	}

	if runtimeGOOS != "darwin" {
		// Signature enforcement is macOS-only. Mark Signed=true so callers do
		// not interpret "skipped" as "unsigned". Use the package-level
		// runtimeGOOS variable (not runtime.GOOS) so tests can simulate
		// non-darwin platforms.
		report.Signed = true
		return report
	}

	output, signed := verifyClaudeDarwinSignature(cleaned)
	report.CodesignOutput = output
	report.Signed = signed
	if !signed {
		wasCorrupted := report.Corrupted
		report.Corrupted = true
		if report.Reason == "" {
			report.Reason = "codesign verification failed; binary is not signed (macOS AMFI will SIGKILL on exec)"
		} else if !wasCorrupted {
			report.Reason += "; codesign verification also failed"
		}
	}
	return report
}

// verifyClaudeDarwinSignature runs `codesign -dv` against the binary and
// reports whether it carries a valid signature. It does NOT require the
// signature to match claudeCodesignIdentifier -- any valid signature is
// accepted. The identifier check is informational only.
func verifyClaudeDarwinSignature(path string) (string, bool) {
	if runtimeGOOS != "darwin" {
		return "", true
	}
	codesignPath, lookErr := exec.LookPath("codesign")
	if lookErr != nil {
		// If codesign is unavailable we cannot make a determination; treat
		// as signed to avoid false positives in stripped environments.
		return "", true
	}
	ctx, cancel := context.WithTimeout(context.Background(), claudeCodesignTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, codesignPath, "-dv", path)
	out, err := cmd.CombinedOutput()
	combined := strings.TrimSpace(string(out))
	if err != nil {
		return combined, false
	}
	// codesign -dv prints to stderr on success; CombinedOutput merges both.
	// Treat "code object is not signed at all" and any non-zero exit as
	// unsigned.
	if strings.Contains(strings.ToLower(combined), "not signed") {
		return combined, false
	}
	return combined, true
}

// classifyClaudeVersionError inspects the error and process result from a
// failed `claude --version` invocation and returns a structured error that
// distinguishes three states:
//   - exit code 137 / SIGKILL (macOS AMFI rejecting a corrupted binary)
//   - binary-on-disk corruption detected via integrity inspection
//   - generic "run claude --version" failure
//
// When the invocation path is provided and integrity inspection confirms
// corruption, the returned error carries the corruption reason so the
// frontend can surface a targeted fix action.
func classifyClaudeVersionError(executablePath string, result *platform.ProcessResult, err error) error {
	if err == nil {
		return nil
	}
	rawMessage := strings.TrimSpace(resultText(result))
	if rawMessage == "" {
		rawMessage = strings.TrimSpace(err.Error())
	}
	lower := strings.ToLower(rawMessage)

	// macOS AMFI SIGKILL: the kernel refuses to exec an unsigned / malformed
	// Mach-O. Go's exec package surfaces this as "signal: killed" and the
	// process exit code is 137 (=128+SIGKILL=9).
	if isClaudeSIGKILLSignal(lower, err) {
		return fmt.Errorf("claude binary likely corrupted (AMFI SIGKILL / exit code 137): %s", rawMessage)
	}

	// Integrity check on disk -- authoritative for "truncated shard" cases
	// where the binary is too small or unsigned even if the exec failure
	// surfaced as something else (e.g. exec format error).
	if executablePath != "" {
		if report := InspectClaudeBinaryIntegrity(executablePath); report.Exists && report.Corrupted {
			return fmt.Errorf("claude binary corrupted at %s: %s", executablePath, report.Reason)
		}
	}

	return fmt.Errorf("run claude --version: %s", rawMessage)
}

// isClaudeSIGKILLSignal matches the various ways Go's os/exec surfaces a
// SIGKILL to callers. err.Error() typically contains "signal: killed" on
// Unix; on macOS AMFI rejection the message may also include
// "unrecoverable" / "unknown system error -88". Exit code 137 may appear
// in stderr/output captured from wrappers.
func isClaudeSIGKILLSignal(lowerMessage string, err error) bool {
	if strings.Contains(lowerMessage, "signal: killed") {
		return true
	}
	if strings.Contains(lowerMessage, "exit code 137") || strings.Contains(lowerMessage, "exit status 137") {
		return true
	}
	// macOS-specific unrecoverable exec errors reported by the cli-wrapper.
	if strings.Contains(lowerMessage, "unknown system error -88") || strings.Contains(lowerMessage, "enotrecoverable") {
		return true
	}
	if err != nil {
		errLower := strings.ToLower(err.Error())
		if strings.Contains(errLower, "signal: killed") {
			return true
		}
		if strings.Contains(errLower, "exit status 137") || strings.Contains(errLower, "exit code 137") {
			return true
		}
	}
	return false
}

// ensureClaudeBinaryHealthy returns nil when the binary at path passes the
// integrity inspection. It returns a descriptive error otherwise. Used by
// the install pre-check to short-circuit attempts to "fall back" to a
// truncated shard.
func ensureClaudeBinaryHealthy(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("empty claude binary path")
	}
	report := InspectClaudeBinaryIntegrity(path)
	if !report.Exists {
		return nil // absent binary is handled elsewhere; not a corruption
	}
	if report.Corrupted {
		return fmt.Errorf("claude binary at %s is corrupted: %s", path, report.Reason)
	}
	return nil
}

// healthyClaudeBinaryCandidates filters a list of candidate paths down to
// those that pass the integrity inspection. It is used to gate the npm
// package-binary fallback (installer.go claudeNPMPackageBinaryFallbackCandidates)
// so a truncated shard left behind by an interrupted install is NOT
// reported as a usable Claude Code binary.
func healthyClaudeBinaryCandidates(candidates []string) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		report := InspectClaudeBinaryIntegrity(candidate)
		if report.Exists && !report.Corrupted {
			out = append(out, candidate)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// M-1: Detect-path auto self-heal
// ---------------------------------------------------------------------------
//
// When checkClaudeCode invokes `claude --version` and the binary is a
// truncated / unsigned shard (left behind by an interrupted npm install),
// classifyClaudeVersionError returns one of:
//
//   - "claude binary likely corrupted (AMFI SIGKILL / exit code 137): ..."
//   - "claude binary corrupted at <path>: ..."
//
// Previously the check path only surfaced this error to the frontend without
// attempting any recovery, so a user whose previous install was interrupted
// would see a broken Claude Code every launch until they manually found the
// clean-install entry point.
//
// The detect-path self-heal closes this gap: on detecting corruption, we
// automatically invoke cleanClaudeNPMResidue (the same three-step recovery
// used by the install/uninstall paths) so the next install attempt does not
// deadlock on ENOTEMPTY staging residue or pick up the same shard via the
// npm package-binary fallback.

// corruptionSentinels are the substring markers classifyClaudeVersionError
// emits when it identifies a corrupted Claude Code binary. Detection-path
// self-heal keys off these markers so it triggers only for genuine corruption
// -- not for "not installed", "PATH missing", or transient network errors.
var corruptionSentinels = []string{
	"claude binary likely corrupted",
	"claude binary corrupted",
	"amfi sigkill",
	"exit code 137",
}

// looksLikeClaudeCorruptionError reports whether err carries a corruption
// signal produced by classifyClaudeVersionError. It is the gate for
// detect-path self-heal: only these errors justify automatic staging +
// shard cleanup.
func looksLikeClaudeCorruptionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	for _, sentinel := range corruptionSentinels {
		if strings.Contains(msg, sentinel) {
			return true
		}
	}
	return false
}

// claudeNPMResidueSelfHealOutcome bundles the inputs and outputs of a
// detect-path self-heal attempt so checkClaudeCode can render a precise
// user-facing issue regardless of whether cleanup succeeded.
type claudeNPMResidueSelfHealOutcome struct {
	// Triggered is true when self-heal actually ran (binary was corrupted
	// and a cleanup attempt was made). When false, no residue was found.
	Triggered bool
	// Integrity captures the on-disk corruption finding that justified the
	// cleanup. Frontend can use Reason to explain the diagnosis.
	Integrity ClaudeBinaryIntegrity
	// Cleanup is the result of cleanClaudeNPMResidue (nil when the cleanup
	// itself failed before returning a partial result).
	Cleanup *claudeNPMResidueCleanupResult
	// CleanupErr is the error returned by cleanClaudeNPMResidue (nil on
	// success). Frontend treats cleanup failure as "manual recovery needed".
	CleanupErr error
}

// selfHealClaudeNPMResidueForDetection is the detect-path self-heal entry
// point. It is called by checkClaudeCode only after classifyClaudeVersionError
// has classified the failure as corruption. It performs the three-step
// recovery (staging + package dir + orphan bin link) and returns an outcome
// describing what happened. The function never returns an error itself --
// failure is communicated via outcome.CleanupErr so the caller can keep
// constructing a useful CheckStatus rather than short-circuiting detection.
//
// executablePath is the corrupted binary path (used only for diagnostics in
// the outcome; the cleanup itself targets the npm global prefix where the
// interrupted install left its residue).
func (s *Service) selfHealClaudeNPMResidueForDetection(executablePath string) claudeNPMResidueSelfHealOutcome {
	outcome := claudeNPMResidueSelfHealOutcome{}

	// Re-inspect the binary so the issue text can carry the same corruption
	// reason classifyClaudeVersionError used, even when classification was
	// triggered via SIGKILL alone (which does not run an integrity scan).
	if path := strings.TrimSpace(executablePath); path != "" {
		outcome.Integrity = InspectClaudeBinaryIntegrity(path)
	}

	// Always attempt cleanup once -- staging residue is the proximate cause
	// of interrupted installs and must be cleared even when the binary itself
	// is a hardlink we cannot safely unlink in isolation.
	result, err := s.cleanClaudeNPMResidue()
	outcome.Triggered = true
	outcome.Cleanup = result
	outcome.CleanupErr = err

	if err != nil {
		log.Printf("envcheck: claude detect-path self-heal completed with errors: %v", err)
	} else if result != nil && result.Total() > 0 {
		log.Printf("envcheck: claude detect-path self-heal removed %d entries (staging=%d package=%d bin=%d)",
			result.Total(), len(result.StagingDirs), len(result.PackageDirs), len(result.OrphanBinLinks))
	}
	return outcome
}
