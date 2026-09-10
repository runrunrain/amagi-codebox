package remote

// start_retry.go — bounded Start retry engine for the Startup restore path
// (P3-B③; 素材B ⑥: 启动恢复时端口占用仅 Warn → 可观察的自愈/对齐).
//
// Server.Start keeps its single-attempt semantics (listen failure returns
// immediately); StartWithRetry layers the bounded backoff schedule on top for
// callers that own a transient-conflict window — currently only the App's
// Startup restore path, where the previous instance's listener may still be
// mid-shutdown. Persistent conflicts exhaust the schedule and surface the last
// error so the App can align the observable state (startup warning + status
// bindings) instead of blocking boot.

import (
	"context"
	"time"
)

// StartWithRetry runs Server.Start with a bounded retry schedule: one immediate
// attempt plus one attempt per delay entry (sleep-then-Start, in order).
// Returns nil on the first successful Start (Start is idempotent: an
// already-running server returns nil immediately). After all attempts fail it
// returns the LAST error; a cancelled/shutdown parent context stops the loop
// between attempts and surfaces the pending error (never a bare nil).
//
// onFailedAttempt (optional, may be nil) is invoked after each FAILED attempt
// with the 0-based attempt index and its error — the seam tests use to free a
// transiently-occupied port mid-schedule deterministically.
func (s *Server) StartWithRetry(parentCtx context.Context, delays []time.Duration, onFailedAttempt func(attempt int, err error)) error {
	var lastErr error
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			i := attempt - 1
			if i >= len(delays) {
				return lastErr
			}
			select {
			case <-time.After(delays[i]):
			case <-parentCtx.Done():
				if lastErr == nil {
					lastErr = parentCtx.Err()
				}
				return lastErr
			}
		}
		err := s.Start(parentCtx)
		if err == nil {
			return nil
		}
		lastErr = err
		if onFailedAttempt != nil {
			onFailedAttempt(attempt, err)
		}
	}
}
