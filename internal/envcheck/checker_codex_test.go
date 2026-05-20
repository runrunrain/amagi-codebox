package envcheck

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// parseCodexVersion
// ---------------------------------------------------------------------------

func TestParseCodexVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"codex-cli format", "codex-cli 0.87.0", "0.87.0"},
		{"bare version", "0.87.0", "0.87.0"},
		{"version with prefix text", "codex 0.1.0", "0.1.0"},
		{"version with prerelease", "codex 0.87.0-beta.1", "0.87.0-beta.1"},
		{"version with build metadata", "codex 0.87.0+build.123", "0.87.0+build.123"},
		{"multi-line output", "OpenAI Codex CLI\n0.5.0\n", "0.5.0"},
		{"two-part version", "codex 1.0", "1.0"},
		{"four-part version", "codex 1.2.3.4", "1.2.3.4"},
		{"empty string", "", ""},
		{"no version number", "codex-cli", ""},
		{"whitespace padded", "  0.87.0  ", "0.87.0"},
		{"version in sentence", "Installed: v0.87.0", "0.87.0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCodexVersion(tc.input)
			if got != tc.expected {
				t.Errorf("parseCodexVersion(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestRunCodexVersionUsesEnhancedEnvForNodeShebang(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Codex npm shebang enhanced PATH regression is specific to macOS GUI PATH behavior")
	}

	tempHome := t.TempDir()
	nodeDir := filepath.Join(tempHome, ".local", "bin")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatalf("mkdir fake node dir: %v", err)
	}
	writeTestExecutable(t, nodeDir, "node")
	writeTestExecutable(t, nodeDir, "npm")

	t.Setenv("HOME", tempHome)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")

	runner := &codexVersionEnvRunner{requiredPathEntry: nodeDir}
	svc := NewServiceWithRunner(runner)

	version, err := svc.runCodexVersion(filepath.Join(tempHome, ".local", "node", "lib", "node_modules", "@openai", "codex", "bin", "codex.js"), []string{"--version"})
	if err != nil {
		t.Fatalf("runCodexVersion error: %v", err)
	}
	if version != "0.132.0" {
		t.Fatalf("runCodexVersion version = %q, want %q", version, "0.132.0")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(runner.calls))
	}
	if len(runner.calls[0].Env) == 0 {
		t.Fatal("runCodexVersion passed empty Env; expected enhanced environment")
	}
	if !envPathContainsEntry(envValueFromList(runner.calls[0].Env, "PATH"), nodeDir) {
		t.Fatalf("enhanced PATH %q does not contain node dir %q", envValueFromList(runner.calls[0].Env, "PATH"), nodeDir)
	}
}

type codexVersionEnvRunner struct {
	requiredPathEntry string
	calls             []platform.CommandSpec
}

func (r *codexVersionEnvRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.calls = append(r.calls, spec)
	pathValue := envValueFromList(spec.Env, "PATH")
	if !envPathContainsEntry(pathValue, r.requiredPathEntry) {
		return &platform.ProcessResult{Stderr: "env: node: No such file or directory"}, errors.New("exit status 127")
	}
	return &platform.ProcessResult{Stdout: "codex-cli 0.132.0"}, nil
}

func (r *codexVersionEnvRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

func envValueFromList(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func envPathContainsEntry(pathValue string, want string) bool {
	for _, entry := range filepath.SplitList(pathValue) {
		if filepath.Clean(entry) == filepath.Clean(want) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// detectCodexInstallMethod
// ---------------------------------------------------------------------------

func TestDetectCodexInstallMethod(t *testing.T) {
	tests := []struct {
		name       string
		execPath   string
		wantMethod InstallMethod
	}{
		{
			name:       "npm via node_modules",
			execPath:   `C:\Users\alice\AppData\Roaming\npm\node_modules\@openai\codex\codex.cmd`,
			wantMethod: InstallMethodNPM,
		},
		{
			name:       "native default",
			execPath:   `C:\Tools\codex.exe`,
			wantMethod: InstallMethodNative,
		},
		{
			name:       "empty path",
			execPath:   "",
			wantMethod: InstallMethodUnknown,
		},
		{
			name:       "whitespace path",
			execPath:   "   ",
			wantMethod: InstallMethodUnknown,
		},
		{
			name:       "non-npm tools path",
			execPath:   `/usr/local/bin/codex`,
			wantMethod: InstallMethodNative,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectCodexInstallMethod(tc.execPath)
			if got != tc.wantMethod {
				t.Errorf("detectCodexInstallMethod(%q) = %q, want %q", tc.execPath, got, tc.wantMethod)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// normalizeCodexPath
// ---------------------------------------------------------------------------

func TestNormalizeCodexPath(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"empty", "", ""},
		{"whitespace", "   ", ""},
		{"upper case to lower backslash", `C:\Tools\Codex.exe`, `c:/tools/codex.exe`},
		{"forward slash", `C:/Tools/Codex.exe`, `c:/tools/codex.exe`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeCodexPath(tc.input)
			if got != tc.expect {
				t.Errorf("normalizeCodexPath(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}
