package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var darwinBaselinePATH = []string{"/opt/homebrew/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}

func resolveExecutableWithEnvForOS(osName string, command string, env []string) (string, string) {
	if resolved := resolveCommandPathForOS(osName, command, env); resolved != "" {
		if isAbsoluteOrExplicitPath(command) {
			return resolved, "explicit-path"
		}
		return resolved, "path-search"
	}
	if osName == "darwin" {
		if resolved := resolveCommandViaShellFallback(command, env); resolved != "" {
			return resolved, "fallback"
		}
	}
	if osName == currentOS() {
		if resolved, err := exec.LookPath(command); err == nil {
			return resolved, "ambient-path"
		}
	}
	return "", "missing"
}

func resolveCommandPath(command string, env []string) string {
	return resolveCommandPathForOS(currentOS(), command, env)
}

func currentOS() string {
	return runtime.GOOS
}

func resolveCommandPathForOS(osName string, command string, env []string) string {
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

	_, pathValue, _, _ := buildEffectiveEnvForOS(osName, env)
	for _, dir := range filepath.SplitList(pathValue) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		candidate := filepath.Join(dir, trimmed)
		if fileExists(candidate) {
			return candidate
		}
		if osName == "windows" && filepath.Ext(candidate) == "" {
			for _, ext := range []string{".exe", ".cmd", ".bat"} {
				if fileExists(candidate + ext) {
					return candidate + ext
				}
			}
		}
	}
	return ""
}

func buildEffectiveEnvForOS(osName string, env []string) ([]string, string, []string, []string) {
	vars := append([]string(nil), env...)
	pathValue := envValue(vars, "PATH")
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}

	pathSources := []string{}

	addedEntries := []string{}
	callerEntries := []string{}
	inheritedEntries := []string{}
	seen := map[string]struct{}{}
	for _, entry := range filepath.SplitList(pathValue) {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		key := normalizePathKey(trimmed, osName)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		callerEntries = append(callerEntries, trimmed)
	}

	entries := make([]string, 0, len(callerEntries)+len(inheritedEntries)+len(darwinBaselinePATH))
	entries = append(entries, callerEntries...)
	if len(callerEntries) > 0 {
		pathSources = append(pathSources, "app-env")
	}

	if osName == "darwin" {
		for _, entry := range darwinBaselinePATH {
			key := normalizePathKey(entry, osName)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			entries = append(entries, entry)
			addedEntries = append(addedEntries, entry)
		}
		if len(addedEntries) > 0 {
			pathSources = append(pathSources, "controlled-additions")
		}
	}

	if osName != currentOS() {
		for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
			trimmed := strings.TrimSpace(entry)
			if trimmed == "" {
				continue
			}
			key := normalizePathKey(trimmed, osName)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			inheritedEntries = append(inheritedEntries, trimmed)
		}
	}

	entries = append(entries, inheritedEntries...)
	if len(inheritedEntries) > 0 {
		pathSources = append(pathSources, "inherited")
	}

	effectivePATH := strings.Join(entries, string(os.PathListSeparator))
	vars = setEnvValue(vars, "PATH", effectivePATH)
	return vars, effectivePATH, addedEntries, pathSources
}

func setEnvValue(env []string, key string, value string) []string {
	updated := false
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			out = append(out, entry)
			continue
		}
		if strings.EqualFold(parts[0], key) {
			if !updated {
				out = append(out, key+"="+value)
				updated = true
			}
			continue
		}
		out = append(out, entry)
	}
	if !updated {
		out = append(out, key+"="+value)
	}
	return out
}

func normalizePathKey(path string, osName string) string {
	if osName == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func resolveCommandViaShellFallback(command string, env []string) string {
	for _, shell := range []string{"/bin/zsh", "/bin/bash", "/bin/sh"} {
		if !fileExists(shell) {
			continue
		}
		cmd := exec.Command(shell, "-lc", "command -v -- "+quoteShellLiteral(command))
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		resolved := strings.TrimSpace(string(out))
		if resolved != "" && fileExists(resolved) {
			return resolved
		}
	}
	return ""
}

func quoteShellLiteral(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `"'"'`) + "'"
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
