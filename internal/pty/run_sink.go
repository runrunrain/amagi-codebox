package pty

// run_sink.go — shared RunEventSink interface for raw PTY output/exit routing.

import "time"

// ptyCloseWaitTimeout is the bounded wait for a PTY read loop / process to exit
// during Close (M-009). A stuck read/process must not hold a control-gate
// deadline indefinitely; after this deadline Close returns and the session is
// logically closed (the committer's backendEpoch check drops any late output).
const ptyCloseWaitTimeout = 3 * time.Second

//
// Design authority: fuxi/20260803-m3-a-control-arbitration-design/design.md §8.6
// (M-01). Raw PTY goroutines have NO Wails context and NEVER call EventsEmit
// directly. Instead, at run creation time the control runtime injects a
// RunEventSink + an opaque run handle. The read/wait loops call the sink with
// the handle; the runtime (internal/remote.RunEventProjector) type-asserts the
// handle back to its RunObservationPermit, validates the current run, and emits
// run-tagged Wails events.
//
// The pty package does NOT import internal/remote (avoids import cycle). The
// handle is `any` precisely so the pty package stays decoupled from the permit
// type. Only the runtime that minted the handle ever interprets it.

// RunEventSink is the sole output/exit entry point for raw PTY goroutines.
type RunEventSink interface {
	// OfferOutput delivers a PTY output chunk for the exact run identified by
	// runHandle. seq is the per-session monotonic output counter (for live/snapshot
	// dedup); data is the raw (non-base64) bytes. A nil runHandle or stale handle
	// is a silent no-op.
	OfferOutput(runHandle any, seq uint64, data []byte)
	// OfferExit delivers the process exit for the exact run. A stale/duplicate
	// handle is a silent no-op.
	OfferExit(runHandle any, exitCode uint32, failed bool)
}
