package pty

import (
	"context"
	"fmt"
)

// waitExactPTYReadiness is the platform-neutral state machine used by concrete
// PTY backends. Shell-attach requires observable shell output in addition to
// armed read/wait pumps; direct and inline launches require only the pumps.
func waitExactPTYReadiness(
	ctx context.Context,
	sessionID string,
	pumpsReady <-chan struct{},
	exited <-chan struct{},
	shellReady <-chan struct{},
	requireShell bool,
) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-exited:
		return fmt.Errorf("session %s exited before PTY ready", sessionID)
	case <-pumpsReady:
	}
	select {
	case <-exited:
		return fmt.Errorf("session %s exited at PTY ready barrier", sessionID)
	default:
	}
	if !requireShell {
		return nil
	}
	if shellReady == nil {
		return fmt.Errorf("session %s has no shell-ready signal", sessionID)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-exited:
		return fmt.Errorf("session %s exited before shell ready", sessionID)
	case <-shellReady:
	}
	select {
	case <-exited:
		return fmt.Errorf("session %s exited at shell-ready barrier", sessionID)
	default:
		return nil
	}
}
