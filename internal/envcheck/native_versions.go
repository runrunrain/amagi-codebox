package envcheck

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// This file implements the "versions/ as native truth source" model.
//
// Background (see Claude-Code-安装异常说明.md section 二 and baize exploration
// report 20260621-npm-native-detection-exploration):
//   - The native Claude Code binary is stored under
//     ~/.local/share/claude/versions/<version>/claude (the native version
//     manager directory).
//   - ~/.local/bin/claude is only a PATH-entry shim created by
//     `claude install`. It is NOT the binary itself.
//   - Previous CodeBox treated the shim as the sole detection signal, so any
//     scenario where the shim was temporarily missing (mid upgrade), replaced
//     by an npm stub, or removed by cleanClaudeCodeNative would hide the
//     native installation entirely -- even though versions/ still held a
//     perfectly usable signed binary.
//
// This module scans versions/ to enumerate healthy native binaries. The
// results feed a single "candidate priority" function used by detection
// (checker_claude.go) and install verification (installer.go
// verifyClaudeNativeAvailableWithHint) so that versions/ is the authoritative
// native source of truth and the shim is demoted to a PATH-entry hint.
//
// Safety boundaries (must hold across every function here):
//   - This module only READS the versions/ tree. It never writes, deletes,
//     or renames anything inside it.
//   - Paths are derived from os.UserHomeDir + the per-OS relative location.
//     No host-specific paths are hardcoded.
//   - Cross-platform layout:
//       darwin/linux/freebsd: <home>/.local/share/claude/versions/
//       windows:              <home>\AppData\Local\claude\versions\
//     (Windows layout matches Anthropic's installer behaviour: the native
//     version manager uses %LOCALAPPDATA% on Windows.)
//   - Each candidate must pass InspectClaudeBinaryIntegrity. Truncated shards
//     and unsigned macOS binaries are rejected so that detection never picks
//     up a partial download.

// claudeNativeVersionsRelDir is the versions/ subdirectory relative to the
// per-OS local data dir. It is the Anthropic native version manager layout.
var claudeNativeVersionsRelDir = []string{"claude", "versions"}

// claudeNativeLocalDataDir returns the absolute path of the per-OS "local
// data" directory in which the native version manager lives. The versions/
// directory is one level below this. It never returns an error -- on failure
// it returns "" and the caller treats that as "no versions/ available".
func claudeNativeLocalDataDir() string {
	if runtimeGOOS == "windows" {
		base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if base == "" {
			if home, err := claudeUserHomeDir(); err == nil {
				base = filepath.Join(home, "AppData", "Local")
			}
		}
		if base == "" {
			return ""
		}
		return filepath.Clean(base)
	}
	// POSIX: $XDG_DATA_HOME overrides $HOME/.local/share per the XDG Base
	// Directory spec, which the Anthropic installer also respects.
	if xdgData := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdgData != "" {
		if cleaned := filepath.Clean(xdgData); cleaned != "." && cleaned != "" {
			return cleaned
		}
	}
	home, err := claudeUserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share")
}

// claudeNativeVersionsDir returns the absolute path to the versions/
// directory, or "" when the local data dir cannot be resolved. It does not
// require the directory to exist on disk.
func claudeNativeVersionsDir() string {
	base := claudeNativeLocalDataDir()
	if base == "" {
		return ""
	}
	parts := append([]string{base}, claudeNativeVersionsRelDir...)
	return filepath.Join(parts...)
}

// claudeNativeVersionEntry describes one healthy binary discovered under
// versions/. Version is the parsed semantic version (e.g. "2.1.181"); Path
// is the absolute executable path; Integrity carries the on-disk report so
// callers do not need to re-inspect.
type claudeNativeVersionEntry struct {
	Version   string
	Path      string
	Integrity ClaudeBinaryIntegrity
}

// scanClaudeNativeVersionsDir enumerates healthy Claude Code binaries stored
// under versions/. It returns entries sorted by version descending (newest
// first), so the first element is the preferred candidate. The directory is
// never modified -- this is a read-only scan.
//
// Truncated shards / unsigned macOS binaries are skipped (they fail
// InspectClaudeBinaryIntegrity). This guarantees that detection never picks
// up a partial download and that the frontend is never told "Native is
// installed" based on a corrupt file.
//
// The function tolerates a missing versions/ directory (returns nil + nil).
// A real I/O error (e.g. unreadable directory) is surfaced so callers can
// distinguish "no versions/ yet" from "scan failed".
func scanClaudeNativeVersionsDir() ([]claudeNativeVersionEntry, error) {
	dir := claudeNativeVersionsDir()
	if dir == "" {
		return nil, nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	// The native version manager names child directories by version
	// (e.g. "2.1.181"). Inside each version directory the binary is named
	// "claude" on POSIX and "claude.exe" on Windows.
	executableName := "claude"
	if runtimeGOOS == "windows" {
		executableName = "claude.exe"
	}

	out := make([]claudeNativeVersionEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		version := parseClaudeNativeVersionName(entry.Name())
		if version == "" {
			continue
		}
		binaryPath := filepath.Join(dir, entry.Name(), executableName)
		report := InspectClaudeBinaryIntegrity(binaryPath)
		if !report.Exists {
			continue
		}
		if report.Corrupted {
			// Truncated / unsigned shard inside versions/ -- skip but log so
			// we have a forensic trail when users report flaky detection.
			log.Printf("envcheck: native versions/ entry %s skipped (corrupted: %s)", binaryPath, report.Reason)
			continue
		}
		out = append(out, claudeNativeVersionEntry{
			Version:   version,
			Path:      binaryPath,
			Integrity: report,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return compareClaudeNativeVersions(out[i].Version, out[j].Version) > 0
	})
	return out, nil
}

// parseClaudeNativeVersionName returns the semantic version encoded in a
// versions/ child directory name. The Anthropic installer uses bare
// versions like "2.1.181"; we accept optional "v" prefix and ignore
// non-matching names (e.g. "latest", "tmp").
func parseClaudeNativeVersionName(name string) string {
	trimmed := strings.TrimSpace(name)
	trimmed = strings.TrimPrefix(trimmed, "v")
	if trimmed == "" {
		return ""
	}
	if !claudeNativeVersionPattern.MatchString(trimmed) {
		return ""
	}
	return trimmed
}

// claudeNativeVersionPattern matches a strict major.minor[.patch[.build]]
// version. It rejects anything that contains non-numeric segments so a
// stray "downloads" or ".tmp" directory is never mistaken for a version.
var claudeNativeVersionPattern = regexp.MustCompile(`^\d+(?:\.\d+){1,3}$`)

// compareClaudeNativeVersions compares two semantic versions. The result is
// 0 when equal, >0 when a is newer than b, <0 when a is older than b. Each
// segment is compared numerically so "2.1.9" < "2.1.10" (string comparison
// would get this wrong).
func compareClaudeNativeVersions(a, b string) int {
	split := func(v string) []int {
		parts := strings.Split(v, ".")
		out := make([]int, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			n := 0
			for _, ch := range p {
				if ch < '0' || ch > '9' {
					n = 0
					break
				}
				n = n*10 + int(ch-'0')
			}
			out = append(out, n)
		}
		return out
	}
	av := split(a)
	bv := split(b)
	n := len(av)
	if len(bv) > n {
		n = len(bv)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(av) {
			ai = av[i]
		}
		if i < len(bv) {
			bi = bv[i]
		}
		if ai != bi {
			return ai - bi
		}
	}
	return len(av) - len(bv)
}

// firstHealthyClaudeNativeVersion returns the newest healthy binary under
// versions/, or "" when none exists. It is the primary entry point used by
// the candidate-priority logic in checker_claude.go.
func firstHealthyClaudeNativeVersion() string {
	entries, err := scanClaudeNativeVersionsDir()
	if err != nil {
		// Scan failure is non-fatal: fall back to the existing shim-only
		// detection path. Log so we have visibility in production traces.
		log.Printf("envcheck: native versions/ scan failed (falling back to shim): %v", err)
		return ""
	}
	if len(entries) == 0 {
		return ""
	}
	return entries[0].Path
}

// healthyClaudeNativeVersionCandidates returns up to maxCandidates healthy
// version/ binaries, newest first. It is used by install verification to
// confirm "claude install wrote a binary into versions/" even when the shim
// has not yet been (re)created. maxCandidates <= 0 returns all entries.
func healthyClaudeNativeVersionCandidates(maxCandidates int) []claudeNativeVersionEntry {
	entries, err := scanClaudeNativeVersionsDir()
	if err != nil {
		return nil
	}
	if maxCandidates > 0 && len(entries) > maxCandidates {
		entries = entries[:maxCandidates]
	}
	return entries
}

// isClaudeNativeVersionsBinaryPath reports whether path is located inside
// the versions/ directory. It is the foundation for the versions/-aware
// InstallMethod detection: when CodeBox resolves a path here, it is
// unambiguously a native binary regardless of PATH ordering.
//
// The comparison is robust to platform-level symlink indirection: on macOS
// $TMPDIR lives under /var which is itself a symlink to /private/var, and
// EvalSymlinks returns the resolved form. We resolve both sides so the
// detection does not falsely conclude "this binary is NOT under versions/"
// just because one path went through EvalSymlinks and the other did not.
func isClaudeNativeVersionsBinaryPath(path string) bool {
	normalized := normalizeClaudePath(resolveRealExecutablePath(path))
	if normalized == "" {
		return false
	}
	versionsDir := normalizeClaudePath(resolveRealExecutablePath(claudeNativeVersionsDir()))
	if versionsDir == "" {
		return false
	}
	return pathHasPrefix(normalized, versionsDir)
}

// ErrNoClaudeNativeVersion is returned by helpers that explicitly require a
// versions/ binary and find none. It is never a fatal detection failure --
// callers use it to fall back to the legacy shim-only path.
var ErrNoClaudeNativeVersion = errors.New("no healthy Claude Code native binary found under versions/")

// resolveClaudeNativeVersionsBinary is the install-verification entry point.
// It returns the newest healthy versions/ binary, or ErrNoClaudeNativeVersion
// when none exists. Install verification uses this to fall back to versions/
// truth when the shim has not been (re)created yet (F5-2 in baize report).
func resolveClaudeNativeVersionsBinary() (string, error) {
	if p := firstHealthyClaudeNativeVersion(); p != "" {
		return p, nil
	}
	return "", ErrNoClaudeNativeVersion
}

// listClaudeNativeVersionsPathsForUninstallReport returns every healthy
// native binary currently under versions/. It is used by the Native
// uninstall flow to tell the user, transparently, what survived the
// shim-only cleanup. Read-only.
func listClaudeNativeVersionsPathsForUninstallReport() []string {
	entries, err := scanClaudeNativeVersionsDir()
	if err != nil || len(entries) == 0 {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Path)
	}
	return out
}

// claudeNativeBinDirectoriesForPATH returns the directories that should be
// added to enhanced PATH so the resolver can find claude via the standard
// Native install layout. It always includes ~/.local/bin (POSIX) /
// %USERPROFILE%\.local\bin (Windows) regardless of whether the shim exists,
// as long as versions/ holds a healthy native binary. This breaks the
// historical chicken-and-egg where the shim had to already exist for PATH
// injection to fire (R1 in baize exploration report).
//
// The function tolerates the absence of versions/ -- callers are expected
// to also fall back to the legacy "shim-only" PATH injection in
// fix_dispatcher.go's buildEnhancedEnv.
func claudeNativeBinDirectoriesForPATH() []string {
	entries, err := scanClaudeNativeVersionsDir()
	if err != nil || len(entries) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := []string{}
	addDir := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" || dir == "." {
			return
		}
		cleaned := filepath.Clean(dir)
		key := normalizePathKeyForNative(cleaned, runtimeGOOS)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, cleaned)
	}

	// Native bin dir: ~/.local/bin on POSIX, %USERPROFILE%\.local\bin on
	// Windows. This matches the layout the Anthropic installer uses when it
	// creates the shim.
	homes := claudeNativeHomeCandidates()
	for _, home := range homes {
		addDir(filepath.Join(home, ".local", "bin"))
	}
	return out
}

// normalizePathKeyForNative mirrors internal/platform.normalizePathKey so
// deduplication in native_versions.go stays consistent with the lower-level
// PATH builder without dragging in an extra dependency.
func normalizePathKeyForNative(path string, goos string) string {
	if goos == "windows" {
		return strings.ToLower(path)
	}
	return path
}

