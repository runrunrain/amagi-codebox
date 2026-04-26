package platform

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type CommandSpec struct {
	Path   string
	Args   []string
	Dir    string
	Env    []string
	Policy ProcessPolicy
	Stdin  any
	Stdout any
	Stderr any
}

type ProcessResult struct {
	Stdout string
	Stderr string
	Cmd    *exec.Cmd
}

type ProcessRunner interface {
	Start(spec CommandSpec) (*exec.Cmd, error)
	Run(ctx context.Context, spec CommandSpec) (*ProcessResult, error)
}

type processRunner struct{}

func NewProcessRunner() ProcessRunner {
	return processRunner{}
}

func DefaultProcessPolicy() ProcessPolicy {
	return ProcessPolicy{HideConsoleWindow: true}
}

func (processRunner) Start(spec CommandSpec) (*exec.Cmd, error) {
	if strings.TrimSpace(spec.Path) == "" {
		return nil, fmt.Errorf("process path is required")
	}
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append([]string(nil), spec.Env...)
	}
	if stdin, ok := spec.Stdin.(interface{ Read([]byte) (int, error) }); ok {
		cmd.Stdin = stdin
	}
	if stdout, ok := spec.Stdout.(interface{ Write([]byte) (int, error) }); ok {
		cmd.Stdout = stdout
	}
	if stderr, ok := spec.Stderr.(interface{ Write([]byte) (int, error) }); ok {
		cmd.Stderr = stderr
	}
	applyProcessPolicy(cmd, spec.Policy)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func (processRunner) Run(ctx context.Context, spec CommandSpec) (*ProcessResult, error) {
	if strings.TrimSpace(spec.Path) == "" {
		return nil, fmt.Errorf("process path is required")
	}
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append([]string(nil), spec.Env...)
	}
	applyProcessPolicy(cmd, spec.Policy)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &ProcessResult{Stdout: strings.TrimSpace(stdout.String()), Stderr: strings.TrimSpace(stderr.String()), Cmd: cmd}
	if err != nil {
		return result, err
	}
	return result, nil
}
