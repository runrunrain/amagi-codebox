//go:build windows

package platform

import (
	"path/filepath"
	"strings"
)

// wrapWindowsScript wraps CommandSpec for Windows script execution (.cmd/.bat/.ps1).
// For .cmd/.bat, uses cmd.exe /c. For .ps1, uses cmd.exe /c to let Windows file association trigger PowerShell.
// For .exe or no extension, returns spec unchanged.
func wrapWindowsScript(spec CommandSpec) CommandSpec {
	if strings.TrimSpace(spec.Path) == "" {
		return spec
	}

	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(spec.Path)))
	switch ext {
	case ".cmd", ".bat":
		// Wrap with cmd.exe /c
		cmdExe := resolveWindowsCmdExe(spec.Env)
		if cmdExe == "" {
			// Fallback: should not happen on Windows, but preserve original spec
			return spec
		}
		newArgs := make([]string, 0, 2+len(spec.Args))
		newArgs = append(newArgs, "/c", spec.Path)
		newArgs = append(newArgs, spec.Args...)
		return CommandSpec{
			Path:   cmdExe,
			Args:   newArgs,
			Dir:    spec.Dir,
			Env:    spec.Env,
			Policy: spec.Policy,
			Stdin:  spec.Stdin,
			Stdout: spec.Stdout,
			Stderr: spec.Stderr,
		}
	case ".ps1":
		// Use cmd.exe /c to let Windows file association trigger PowerShell
		// This matches the behavior of shouldInlineWindowsScriptWrapper in resolver.go
		cmdExe := resolveWindowsCmdExe(spec.Env)
		if cmdExe == "" {
			return spec
		}
		newArgs := make([]string, 0, 2+len(spec.Args))
		newArgs = append(newArgs, "/c", spec.Path)
		newArgs = append(newArgs, spec.Args...)
		return CommandSpec{
			Path:   cmdExe,
			Args:   newArgs,
			Dir:    spec.Dir,
			Env:    spec.Env,
			Policy: spec.Policy,
			Stdin:  spec.Stdin,
			Stdout: spec.Stdout,
			Stderr: spec.Stderr,
		}
	default:
		// .exe, no extension, or other: no wrapping needed
		return spec
	}
}
