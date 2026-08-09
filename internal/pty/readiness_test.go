package pty

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExactPTYReadinessShellAttachWaitsForObservableShellOutput(t *testing.T) {
	pumpsReady := make(chan struct{})
	exited := make(chan struct{})
	shellReady := make(chan struct{})
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		result <- waitExactPTYReadiness(context.Background(), "shell", pumpsReady, exited, shellReady, true)
	}()
	<-started
	close(pumpsReady)
	select {
	case err := <-result:
		t.Fatalf("shell attach became ready before shell output: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(shellReady)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("shell attach ready result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shell output did not open readiness barrier")
	}
}

func TestExactPTYReadinessDirectDoesNotRequireShellOutput(t *testing.T) {
	pumpsReady := make(chan struct{})
	exited := make(chan struct{})
	close(pumpsReady)
	if err := waitExactPTYReadiness(context.Background(), "direct", pumpsReady, exited, nil, false); err != nil {
		t.Fatalf("direct readiness: %v", err)
	}
}

func TestExactPTYReadinessProjectsExitAndTimeout(t *testing.T) {
	t.Run("exit", func(t *testing.T) {
		pumpsReady := make(chan struct{})
		exited := make(chan struct{})
		close(exited)
		err := waitExactPTYReadiness(context.Background(), "exited", pumpsReady, exited, nil, false)
		if err == nil || !strings.Contains(err.Error(), "exited before PTY ready") {
			t.Fatalf("exit projection = %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := waitExactPTYReadiness(ctx, "timeout", make(chan struct{}), make(chan struct{}), nil, false)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("timeout projection = %v", err)
		}
	})
}
