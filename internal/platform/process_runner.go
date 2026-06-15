package platform

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
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

type ProcessOutputEvent struct {
	Stream string
	Data   string
	At     time.Time
}

type EvidenceRunResult struct {
	Result           *ProcessResult
	EvidenceObserved bool
	EvidenceTimedOut bool
}

type EvidenceProcessRunner interface {
	RunWithEvidence(ctx context.Context, spec CommandSpec, evidenceTimeout time.Duration, onOutput func(ProcessOutputEvent)) (*EvidenceRunResult, error)
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
	spec = wrapWindowsScript(spec)
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
	spec = wrapWindowsScript(spec)
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

func (processRunner) RunWithEvidence(ctx context.Context, spec CommandSpec, evidenceTimeout time.Duration, onOutput func(ProcessOutputEvent)) (*EvidenceRunResult, error) {
	if strings.TrimSpace(spec.Path) == "" {
		return nil, fmt.Errorf("process path is required")
	}
	if evidenceTimeout <= 0 {
		evidenceTimeout = time.Second
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	spec = wrapWindowsScript(spec)
	cmd := exec.CommandContext(runCtx, spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append([]string(nil), spec.Env...)
	}
	if stdin, ok := spec.Stdin.(interface{ Read([]byte) (int, error) }); ok {
		cmd.Stdin = stdin
	}
	applyProcessPolicy(cmd, spec.Policy)

	var stdout safeBuffer
	var stderr safeBuffer
	evidenceCh := make(chan struct{}, 1)
	notifyOutput := func(event ProcessOutputEvent) {
		if onOutput != nil {
			onOutput(event)
		}
		select {
		case evidenceCh <- struct{}{}:
		default:
		}
	}
	cmd.Stdout = evidenceWriter{stream: "stdout", buffer: &stdout, notify: notifyOutput}
	cmd.Stderr = evidenceWriter{stream: "stderr", buffer: &stderr, notify: notifyOutput}

	if err := cmd.Start(); err != nil {
		return &EvidenceRunResult{Result: &ProcessResult{Cmd: cmd}}, err
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	makeResult := func(evidenceObserved bool, evidenceTimedOut bool) *EvidenceRunResult {
		return &EvidenceRunResult{
			Result: &ProcessResult{
				Stdout: strings.TrimSpace(stdout.String()),
				Stderr: strings.TrimSpace(stderr.String()),
				Cmd:    cmd,
			},
			EvidenceObserved: evidenceObserved,
			EvidenceTimedOut: evidenceTimedOut,
		}
	}

	timer := time.NewTimer(evidenceTimeout)
	defer timer.Stop()
	evidenceObserved := false

	for {
		select {
		case <-evidenceCh:
			evidenceObserved = true
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			// Evidence was observed, so the caller may wait for the normal command
			// timeout to decide success/failure.
			err := <-waitCh
			return makeResult(true, false), err
		case err := <-waitCh:
			if err == nil || stdout.String() != "" || stderr.String() != "" {
				evidenceObserved = true
			}
			return makeResult(evidenceObserved, false), err
		case <-timer.C:
			if stdout.String() != "" || stderr.String() != "" {
				err := <-waitCh
				return makeResult(true, false), err
			}
			killProcessTree(cmd)
			cancel()
			err := <-waitCh
			if err == nil {
				err = context.DeadlineExceeded
			}
			return makeResult(false, true), err
		case <-ctx.Done():
			killProcessTree(cmd)
			cancel()
			err := <-waitCh
			if err == nil {
				err = ctx.Err()
			}
			return makeResult(evidenceObserved, false), err
		}
	}
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type evidenceWriter struct {
	stream string
	buffer *safeBuffer
	notify func(ProcessOutputEvent)
}

func (w evidenceWriter) Write(p []byte) (int, error) {
	n, err := w.buffer.Write(p)
	if len(p) > 0 && w.notify != nil {
		w.notify(ProcessOutputEvent{Stream: w.stream, Data: string(p), At: time.Now()})
	}
	return n, err
}

func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "taskkill", "/PID", fmt.Sprintf("%d", cmd.Process.Pid), "/T", "/F").Run()
	}
	_ = cmd.Process.Kill()
}
