package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func resolveExecutableWithEnv(command string, env []string) (string, string) {
	if resolved := resolveCommandPath(command, env); resolved != "" {
		if isAbsoluteOrExplicitPath(command) {
			return resolved, "explicit-path"
		}
		return resolved, "path-search"
	}
	if resolved, err := exec.LookPath(command); err == nil {
		return resolved, "ambient-path"
	}
	return "", "missing"
}

func resolveCommandPath(command string, env []string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return ""
	}
	if isAbsoluteOrExplicitPath(trimmed) {
		if fileExists(trimmed) {
			return trimmed
		}
		return ""
	}

	pathValue := envValue(env, "PATH")
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}
	for _, dir := range filepath.SplitList(pathValue) {
		candidate := filepath.Join(dir, trimmed)
		if fileExists(candidate) {
			return candidate
		}
		if runtime.GOOS == "windows" && filepath.Ext(candidate) == "" {
			for _, ext := range []string{".exe", ".cmd", ".bat"} {
				if fileExists(candidate + ext) {
					return candidate + ext
				}
			}
		}
	}
	return ""
}

func envValue(env []string, key string) string {
	for _, entry := range env {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(parts[0], key) {
			return parts[1]
		}
	}
	return ""
}
