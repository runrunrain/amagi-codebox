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
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get user home: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("get user home: empty path")
	}
	return filepath.Join(home, ".codex"), nil
}
