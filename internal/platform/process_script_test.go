package platform

import (
	"runtime"
	"strings"
	"testing"
)

func TestWrapWindowsScript(t *testing.T) {
	tests := []struct {
		name          string
		inputPath     string
		inputArgs     []string
		wantPath      string
		wantArgsStart []string // First N args we expect
		wantWrapped   bool
	}{
		{
			name:          "cmd script gets wrapped",
			inputPath:     "C:\\npm\\claude.cmd",
			inputArgs:     []string{"version"},
			wantPath:      "cmd.exe", // Should be resolved to cmd.exe on Windows
			wantArgsStart: []string{"/c", "C:\\npm\\claude.cmd", "version"},
			wantWrapped:   true,
		},
		{
			name:          "bat script gets wrapped",
			inputPath:     "C:\\scripts\\build.bat",
			inputArgs:     []string{"--release"},
			wantPath:      "cmd.exe",
			wantArgsStart: []string{"/c", "C:\\scripts\\build.bat", "--release"},
			wantWrapped:   true,
		},
		{
			name:          "ps1 script gets wrapped via cmd.exe",
			inputPath:     "C:\\scripts\\setup.ps1",
			inputArgs:     []string{"-Verbose"},
			wantPath:      "cmd.exe",
			wantArgsStart: []string{"/c", "C:\\scripts\\setup.ps1", "-Verbose"},
			wantWrapped:   true,
		},
		{
			name:          "exe is not wrapped",
			inputPath:     "C:\\Program Files\\claude.exe",
			inputArgs:     []string{"help"},
			wantPath:      "C:\\Program Files\\claude.exe",
			wantArgsStart: []string{"help"},
			wantWrapped:   false,
		},
		{
			name:          "no extension is not wrapped",
			inputPath:     "C:\\bin\\claude",
			inputArgs:     []string{"version"},
			wantPath:      "C:\\bin\\claude",
			wantArgsStart: []string{"version"},
			wantWrapped:   false,
		},
		{
			name:          "empty path returns unchanged",
			inputPath:     "",
			inputArgs:     []string{"arg1"},
			wantPath:      "",
			wantArgsStart: []string{"arg1"},
			wantWrapped:   false,
		},
		{
			name:          "unknown extension is not wrapped",
			inputPath:     "C:\\data\\config.json",
			inputArgs:     []string{},
			wantPath:      "C:\\data\\config.json",
			wantArgsStart: []string{},
			wantWrapped:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := CommandSpec{
				Path: tt.inputPath,
				Args: tt.inputArgs,
				Env:  []string{}, // Empty env to avoid flaky test dependencies
			}

			got := wrapWindowsScript(spec)

			if runtime.GOOS == "windows" {
				if tt.wantWrapped {
					// On Windows, wrapped scripts should have cmd.exe as path
					if !strings.Contains(strings.ToLower(got.Path), "cmd.exe") {
						t.Errorf("wrapWindowsScript() Path = %v, want contain cmd.exe", got.Path)
					}
					// Check args prefix (skip first arg which is the resolved cmd.exe path)
					if len(got.Args) < 2 {
						t.Fatalf("wrapWindowsScript() Args len = %v, want >= 2", len(got.Args))
					}
					gotArgsStart := got.Args[1:] // Skip cmd.exe path
					if !argsMatchPrefix(gotArgsStart, tt.wantArgsStart) {
						t.Errorf("wrapWindowsScript() Args = %v, want args starting with %v", gotArgsStart, tt.wantArgsStart)
					}
				} else {
					// Not wrapped: path and args should be unchanged
					if got.Path != tt.wantPath {
						t.Errorf("wrapWindowsScript() Path = %v, want %v", got.Path, tt.wantPath)
					}
					if !argsMatchPrefix(got.Args, tt.wantArgsStart) {
						t.Errorf("wrapWindowsScript() Args = %v, want args starting with %v", got.Args, tt.wantArgsStart)
					}
				}
			} else {
				// Non-Windows: nothing should ever be wrapped
				if got.Path != tt.inputPath {
					t.Errorf("wrapWindowsScript() on non-Windows Path = %v, want %v", got.Path, tt.inputPath)
				}
				if len(got.Args) != len(tt.inputArgs) {
					t.Errorf("wrapWindowsScript() on non-Windows Args len = %v, want %v", len(got.Args), len(tt.inputArgs))
				}
				for i, arg := range got.Args {
					if arg != tt.inputArgs[i] {
						t.Errorf("wrapWindowsScript() on non-Windows Args[%d] = %v, want %v", i, arg, tt.inputArgs[i])
					}
				}
			}
		})
	}
}

// argsMatchPrefix checks if got starts with want (allowing extra args at the end)
func argsMatchPrefix(got, want []string) bool {
	if len(got) < len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestResolveWindowsCmdExe(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Skipping Windows-specific test on non-Windows platform")
	}

	// Test with empty env (should fallback to bare cmd.exe or PATH resolution)
	got := resolveWindowsCmdExe([]string{})
	if got == "" {
		t.Error("resolveWindowsCmdExe() should never return empty string on Windows")
	}

	// Test with SystemRoot set (common Windows env)
	spec := CommandSpec{
		Path: "C:\\test.cmd",
		Args: []string{},
		Env:  []string{"SystemRoot=C:\\Windows"},
	}
	wrapped := wrapWindowsScript(spec)
	if !strings.Contains(strings.ToLower(wrapped.Path), "cmd.exe") {
		t.Errorf("wrapWindowsScript with SystemRoot should resolve cmd.exe, got %v", wrapped.Path)
	}
}

func TestWrapWindowsScriptPreservesFields(t *testing.T) {
	spec := CommandSpec{
		Path:   "C:\\test.cmd",
		Args:   []string{"arg1"},
		Dir:    "C:\\workdir",
		Env:    []string{"KEY=value"},
		Policy: ProcessPolicy{HideConsoleWindow: true},
		Stdin:  strings.NewReader("input"),
	}

	got := wrapWindowsScript(spec)

	// All non-Path/Args fields should be preserved
	if got.Dir != spec.Dir {
		t.Errorf("wrapWindowsScript() Dir = %v, want %v", got.Dir, spec.Dir)
	}
	if len(got.Env) != len(spec.Env) {
		t.Errorf("wrapWindowsScript() Env len = %v, want %v", len(got.Env), len(spec.Env))
	}
	if got.Policy.HideConsoleWindow != spec.Policy.HideConsoleWindow {
		t.Errorf("wrapWindowsScript() Policy = %v, want %v", got.Policy, spec.Policy)
	}
	if got.Stdin != spec.Stdin {
		t.Errorf("wrapWindowsScript() Stdin not preserved")
	}
}
