package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var darwinBaselinePATH = []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}

var pathLookupUserHomeDir = os.UserHomeDir

func resolveExecutableWithEnvForOS(osName string, command string, env []string) (string, string) {
	if resolved := resolveCommandPathForOS(osName, command, env); resolved != "" {
		if isAbsoluteOrExplicitPath(command) {
			return resolved, "explicit-path"
		}
		return resolved, "path-search"
	}
	if osName == "darwin" {
		if resolved := resolveCommandViaShellFallback(command, env, nil); resolved != "" {
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

func BuildEffectiveEnv(env []string) []string {
	vars, _, _, _ := buildEffectiveEnvForOS(currentOS(), env)
	return vars
}

func currentOS() string {
	return runtime.GOOS
}

func resolveCommandPathForOS(osName string, command string, env []string) string {
	return resolveCommandPathForOSWithOptions(osName, command, env, true)
}

func resolveCommandPathForOSWithOptions(osName string, command string, env []string, preferDefault bool) string {
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
	if preferDefault {
		if resolved := resolvePreferredDefaultCommandPathForOS(osName, trimmed, env); resolved != "" {
			return resolved
		}
	}

	_, pathValue, _, _ := buildEffectiveEnvForOS(osName, env)
	for _, dir := range splitPathListForOS(osName, pathValue) {
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

func resolveCommandPathWithoutPreferredDefaultForOS(osName string, command string, env []string) string {
	return resolveCommandPathForOSWithOptions(osName, command, env, false)
}

func buildEffectiveEnvForOS(osName string, env []string) ([]string, string, []string, []string) {
	vars := append([]string(nil), env...)
	callerPATH := envValue(vars, "PATH")
	callerProvidedPATH := hasEnvValue(vars, "PATH")
	inheritedPATH := os.Getenv("PATH")

	pathSources := []string{}

	addedEntries := []string{}
	callerEntries := []string{}
	inheritedEntries := []string{}
	seen := map[string]struct{}{}
	for _, entry := range splitPathListForOS(osName, callerPATH) {
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

	entries := make([]string, 0, len(callerEntries)+len(inheritedEntries)+len(darwinBaselinePATH)+2)
	entries = append(entries, callerEntries...)
	if len(callerEntries) > 0 {
		pathSources = append(pathSources, "app-env")
	}

	if osName == "darwin" {
		for _, entry := range darwinControlledPATHCandidates(vars) {
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

	if osName == "windows" {
		for _, entry := range windowsControlledPATHCandidates(vars) {
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

	if !callerProvidedPATH {
		for _, entry := range splitPathListForOS(osName, inheritedPATH) {
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

	effectivePATH := strings.Join(entries, pathListSeparatorForOS(osName))
	vars = setEnvValue(vars, "PATH", effectivePATH)
	return vars, effectivePATH, addedEntries, pathSources
}

func resolvePreferredDefaultCommandPathForOS(osName string, command string, env []string) string {
	if osName != "darwin" || command != "claude" {
		return ""
	}
	for _, dir := range userNativeBinCandidatesForOS(osName, env) {
		candidate := filepath.Join(dir, command)
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func darwinControlledPATHCandidates(env []string) []string {
	candidates := append([]string{}, userLocalBinCandidatesForOS("darwin", env)...)
	candidates = append(candidates, darwinBaselinePATH...)
	return candidates
}

func userLocalBinCandidatesForOS(osName string, env []string) []string {
	return userBinCandidatesForOS(osName, env, [][]string{
		{".local", "bin"},
		{".local", "node", "bin"},
		{".npm-global", "bin"},
	})
}

func userNativeBinCandidatesForOS(osName string, env []string) []string {
	return userBinCandidatesForOS(osName, env, [][]string{{".local", "bin"}})
}

func userBinCandidatesForOS(osName string, env []string, suffixes [][]string) []string {
	candidates := []string{}
	seen := map[string]struct{}{}
	appendCandidate := func(base string) {
		base = strings.TrimSpace(base)
		if base == "" {
			return
		}
		for _, suffix := range suffixes {
			dirParts := append([]string{base}, suffix...)
			dir := filepath.Join(dirParts...)
			normalized := normalizePathKey(filepath.Clean(dir), osName)
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			candidates = append(candidates, dir)
		}
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		appendCandidate(envValue(env, key))
	}
	if home, err := pathLookupUserHomeDir(); err == nil {
		appendCandidate(home)
	}
	return candidates
}

func windowsControlledPATHCandidates(env []string) []string {
	candidates := []string{}
	for _, key := range []string{"APPDATA", "LOCALAPPDATA"} {
		base := strings.TrimSpace(envValue(env, key))
		if base == "" {
			continue
		}
		candidates = append(candidates, strings.TrimRight(base, `/\`)+`\npm`)
	}
	for _, key := range []string{"USERPROFILE", "HOME"} {
		base := strings.TrimSpace(envValue(env, key))
		if base == "" {
			continue
		}
		candidates = append(candidates, strings.TrimRight(base, `/\`)+`\.local\bin`)
	}
	return candidates
}

func splitPathListForOS(osName string, value string) []string {
	if osName == "windows" {
		return strings.Split(value, ";")
	}
	return filepath.SplitList(value)
}

func pathListSeparatorForOS(osName string) string {
	if osName == "windows" {
		return ";"
	}
	return string(os.PathListSeparator)
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

func resolveCommandViaShellFallback(command string, env []string, selectedShell *ResolvedShell) string {
	for _, shell := range shellResolutionOrder(selectedShell) {
		if !fileExists(shell.Path) {
			continue
		}
		args := buildShellResolveArgs(shell, command)
		cmd := exec.Command(shell.Path, args[0], args[1])
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

func shellResolutionOrder(selectedShell *ResolvedShell) []ResolvedShell {
	ordered := make([]ResolvedShell, 0, 4)
	seen := map[string]struct{}{}
	appendShell := func(shell ResolvedShell) {
		if strings.TrimSpace(shell.Path) == "" {
			return
		}
		if _, ok := seen[shell.Path]; ok {
			return
		}
		seen[shell.Path] = struct{}{}
		ordered = append(ordered, shell)
	}
	if selectedShell != nil {
		appendShell(*selectedShell)
	}
	appendShell(ResolvedShell{Key: "zsh", Path: "/bin/zsh"})
	appendShell(ResolvedShell{Key: "bash", Path: "/bin/bash"})
	appendShell(ResolvedShell{Key: "sh", Path: "/bin/sh"})
	return ordered
}

func buildShellResolveArgs(shell ResolvedShell, command string) []string {
	probe := "command -v -- " + quoteShellLiteral(command)
	bootstrapArg := "-lc"
	switch shell.Key {
	case "zsh", "bash":
		bootstrapArg = "-ilc"
	case "sh", "fish", "":
		bootstrapArg = "-lc"
	default:
		if strings.TrimSpace(shell.BootstrapArg) != "" {
			bootstrapArg = shell.BootstrapArg
		}
	}
	return []string{bootstrapArg, probe}
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

func hasEnvValue(env []string, key string) bool {
	for _, entry := range env {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(parts[0], key) {
			return true
		}
	}
	return false
}
