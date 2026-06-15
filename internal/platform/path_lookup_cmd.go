package platform

import (
	"path/filepath"
	"runtime"
	"strings"
)

// resolveWindowsCmdExe resolves a trusted cmd.exe path from environment variables.
// Resolution order: ComSpec → SystemRoot/windir\System32\cmd.exe → PATH → bare "cmd.exe" fallback.
// This is a shared helper used by resolver (for shell attach) and process_runner (for script wrapping).
func resolveWindowsCmdExe(env []string) string {
	if runtime.GOOS != "windows" {
		return ""
	}

	// Try ComSpec first
	if comspec := strings.TrimSpace(envValue(env, "ComSpec")); comspec != "" {
		if isTrustedWindowsCmdCandidate(comspec) && fileExists(comspec) {
			return comspec
		}
	}

	// Try SystemRoot or windir
	for _, envKey := range []string{"SystemRoot", "windir"} {
		root := strings.TrimSpace(envValue(env, envKey))
		if root == "" || !isWindowsAbsolutePath(root) {
			continue
		}
		candidate := filepath.Join(root, "System32", "cmd.exe")
		if isTrustedWindowsCmdCandidate(candidate) && fileExists(candidate) {
			return candidate
		}
	}

	// Try PATH
	if resolvedPath := resolveCommandPathForOS("windows", "cmd.exe", env); resolvedPath != "" {
		return resolvedPath
	}

	// Fallback to bare cmd.exe (will let Windows resolve it)
	return "cmd.exe"
}
