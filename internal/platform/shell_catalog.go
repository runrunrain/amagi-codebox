package platform

import (
	"os"
	"path/filepath"
	"strings"
)

func defaultShellCatalog(capabilities PlatformCapabilities) []ShellDescriptor {
	entries := shellCandidates(capabilities.OS)
	result := make([]ShellDescriptor, 0, len(entries))
	for _, entry := range entries {
		resolved := resolveBinaryFromCandidatesForOS(capabilities.OS, entry.candidates, nil)
		descriptor := ShellDescriptor{
			Key:          entry.key,
			Label:        entry.label,
			ResolvedPath: resolved,
			Available:    resolved != "",
			IsDefault:    entry.key == capabilities.DefaultShellKey,
		}
		result = append(result, descriptor)
	}
	return result
}

type shellCandidate struct {
	key        string
	label      string
	candidates []string
}

func shellCandidates(osName string) []shellCandidate {
	switch osName {
	case "windows":
		return []shellCandidate{
			{key: "pwsh", label: "PowerShell 7", candidates: []string{"pwsh.exe", `C:/Program Files/PowerShell/7/pwsh.exe`}},
			{key: "powershell", label: "Windows PowerShell", candidates: []string{"powershell.exe"}},
			{key: "cmd", label: "Command Prompt", candidates: []string{"cmd.exe"}},
		}
	default:
		return []shellCandidate{
			{key: "zsh", label: "zsh", candidates: []string{"/bin/zsh", "zsh"}},
			{key: "bash", label: "bash", candidates: []string{"/bin/bash", "bash"}},
			{key: "fish", label: "fish", candidates: []string{"/opt/homebrew/bin/fish", "/usr/local/bin/fish", "fish"}},
			{key: "pwsh", label: "PowerShell 7", candidates: []string{"/opt/homebrew/bin/pwsh", "/usr/local/bin/pwsh", "pwsh"}},
		}
	}
}

func resolveBinaryFromCandidates(candidates []string, env []string) string {
	return resolveBinaryFromCandidatesForOS(currentOS(), candidates, env)
}

func resolveBinaryFromCandidatesForOS(osName string, candidates []string, env []string) string {
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if resolved := resolveCommandPathWithoutPreferredDefaultForOS(osName, candidate, env); resolved != "" {
			return resolved
		}
	}
	return ""
}

func isAbsoluteOrExplicitPath(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if filepath.IsAbs(trimmed) {
		return true
	}
	return strings.Contains(trimmed, "/") || strings.Contains(trimmed, `\`)
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
