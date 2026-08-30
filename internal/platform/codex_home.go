package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CodexHomeDir returns the state/config directory used by a Codex process
// launched with env. CODEX_HOME is authoritative; otherwise Codex uses the
// standard ~/.codex directory.
func CodexHomeDir(env []string) (string, error) {
	if configured := strings.TrimSpace(envValue(env, "CODEX_HOME")); configured != "" {
		return filepath.Clean(configured), nil
	}
	// Prefer the home from the caller-provided env before falling back to this
	// process's home: the directory belongs to the Codex process launched with
	// env, not to us (fixes TestCodexHomeDirFallsBackToUserCodexDirectory on
	// Windows, where os.UserHomeDir reads USERPROFILE and ignores passed HOME).
	home := strings.TrimSpace(envValue(env, "HOME"))
	if home == "" {
		home = strings.TrimSpace(envValue(env, "USERPROFILE"))
	}
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", fmt.Errorf("get user home: %w", err)
		}
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("get user home: empty path")
	}
	return filepath.Join(home, ".codex"), nil
}
