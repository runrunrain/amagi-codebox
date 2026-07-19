package envcheck

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"amagi-codebox/internal/platform"
)

func TestNPMGlobalCommandCandidatesUseNPMRootAndPackageManifest(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "npm-prefix")
	npmRoot := filepath.Join(prefix, "lib", "node_modules")
	packageRoot := filepath.Join(npmRoot, "@openai", "codex")
	if err := os.MkdirAll(filepath.Join(packageRoot, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir package root: %v", err)
	}
	packageBinary := filepath.Join(packageRoot, "bin", "codex.js")
	if err := os.WriteFile(packageBinary, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatalf("write package binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"@openai/codex","bin":{"codex":"bin/codex.js"}}`), 0o600); err != nil {
		t.Fatalf("write package manifest: %v", err)
	}

	candidates := codexNPMGlobalExecutableCandidatesWithRoot(prefix, npmRoot)
	wants := []string{
		filepath.Join(npmRoot, ".bin", "codex"),
		packageBinary,
	}
	for _, want := range wants {
		found := false
		for _, candidate := range candidates {
			if sameNormalizedPath(candidate, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("candidates should include %q, got %v", want, candidates)
		}
	}
}

func TestSourceAwareOpenCodeHomebrewUpdateUsesDetectedExecutable(t *testing.T) {
	svc := newTestService()
	path := "/opt/homebrew/Cellar/opencode/1.18.3/bin/opencode"
	cmds, err := svc.installCommands(ToolOpenCode, installOperationUpdate, &CheckStatus{
		Installed:      true,
		InstallMethod:  InstallMethodHomebrew,
		ExecutablePath: path,
	}, ClaudeInstallAuto)
	if err != nil {
		t.Fatalf("installCommands: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("commands = %d, want 1", len(cmds))
	}
	if cmds[0].path != path || len(cmds[0].args) != 3 || cmds[0].args[0] != "upgrade" || cmds[0].args[1] != "--method" || cmds[0].args[2] != "brew" {
		t.Fatalf("Homebrew OpenCode must use its own updater, got path=%q args=%v", cmds[0].path, cmds[0].args)
	}
}

func TestClaudeAndOpenCodeVersionUseEnhancedEnvOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("GUI Node shebang regression is specific to macOS")
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

	for _, tc := range []struct {
		name   string
		output string
		check  func(*Service) error
	}{
		{
			name:   "Claude",
			output: "Claude Code v2.1.215",
			check: func(s *Service) error {
				_, err := s.claudeVersion(filepath.Join(tempHome, ".local", "node", "lib", "node_modules", "@anthropic-ai", "claude-code", "bin", "claude"))
				return err
			},
		},
		{
			name:   "OpenCode",
			output: "opencode v1.18.3",
			check: func(s *Service) error {
				_, _, err := s.openCodeVersionWithDiagnostics(filepath.Join(tempHome, ".local", "node", "lib", "node_modules", "opencode-ai", "bin", "opencode"))
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &enhancedEnvVersionRunner{requiredPathEntry: nodeDir, output: tc.output}
			if err := tc.check(NewServiceWithRunner(runner)); err != nil {
				t.Fatalf("version check: %v", err)
			}
			if len(runner.calls) != 1 {
				t.Fatalf("runner calls = %d, want 1", len(runner.calls))
			}
			if !envPathContainsEntry(envValueFromList(runner.calls[0].Env, "PATH"), nodeDir) {
				t.Fatalf("enhanced PATH %q does not contain node dir %q", envValueFromList(runner.calls[0].Env, "PATH"), nodeDir)
			}
		})
	}
}

type enhancedEnvVersionRunner struct {
	requiredPathEntry string
	output            string
	calls             []platform.CommandSpec
}

func (r *enhancedEnvVersionRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.calls = append(r.calls, spec)
	if !envPathContainsEntry(envValueFromList(spec.Env, "PATH"), r.requiredPathEntry) {
		return &platform.ProcessResult{Stderr: "env: node: No such file or directory"}, errors.New("exit status 127")
	}
	return &platform.ProcessResult{Stdout: r.output}, nil
}

func (r *enhancedEnvVersionRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}
