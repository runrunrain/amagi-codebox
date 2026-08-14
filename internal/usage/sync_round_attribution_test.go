package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestSyncSessionUsage_OwnRoundNotHijackedByQueuedBackgroundRound covers the
// diting final-review Major finding: with the old "SyncAll() then re-lock and
// read s.syncMeta" shape, the foreground SyncSessionUsage could lose the mutex
// to a background ticker round that had already queued on s.mu in the
// unlock→relock window. The foreground call then blocked for that whole extra
// round (up to 10 minutes) and reported the BACKGROUND round's stats
// (RecordsAdded/ProcessedCount/FilesScanned/Errors) instead of its own.
//
// Deterministic choreography:
//
//  1. s.mu is pre-starved (see below) so lock handoffs are FIFO-strict: the
//     sync.Mutex starvation mode (a waiter blocked >1ms makes Unlock hand
//     ownership directly to the front waiter, no barging by running callers)
//     guarantees bg acquires the mutex right after fg's round releases it —
//     exactly the production window the finding describes.
//  2. fg SyncSessionUsage runs round #0 (2 planted records), parks in the
//     syncRoundStart seam holding s.mu; bg SyncAll queues on s.mu.
//  3. Releasing round #0 lets fg's round finish; bg starts round #1 and parks
//     in the seam holding s.mu.
//  4. While bg's round #1 is still parked, fg must ALREADY have returned with
//     its OWN round's numbers. The old shape re-locked here — queued behind
//     round #1 — so this test fails there by timeout (or by mis-attributed
//     stats if bg's round is fast enough to complete inside the window).
//
// Run with -race:
//
//	go test -race ./internal/usage -run TestSyncSessionUsage_OwnRound
func TestSyncSessionUsage_OwnRoundNotHijackedByQueuedBackgroundRound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("t.Setenv HOME does not steer os.UserHomeDir on windows")
	}

	// Redirect os.UserHomeDir so SyncAll scans only our fixture tree.
	home := t.TempDir()
	t.Setenv("HOME", home)

	proj := filepath.Join(home, ".claude", "projects", "-fixture-")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var body string
	for i := 0; i < 2; i++ {
		body += fmt.Sprintf(`{"type":"assistant","message":{"id":"msg-%d","model":"fixture-model","usage":{"input_tokens":3,"output_tokens":5}},"timestamp":"2026-01-02T03:04:05Z"}`+"\n", i)
	}
	if err := os.WriteFile(filepath.Join(proj, "session.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	s := NewService(t.TempDir(), nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	// Per-round gates: each round announces its start on `started` and parks in
	// the seam (under s.mu) until the test releases its gate.
	var (
		gateMu     sync.Mutex
		roundIndex int
		gates      = map[int]chan struct{}{}
	)
	started := make(chan int, 4)
	releaseRound := func(i int) {
		gateMu.Lock()
		defer gateMu.Unlock()
		if g, ok := gates[i]; ok {
			close(g)
			delete(gates, i)
		}
	}
	s.syncRoundStart = func() {
		gateMu.Lock()
		i := roundIndex
		roundIndex++
		g := make(chan struct{})
		gates[i] = g
		gateMu.Unlock()
		started <- i
		<-g
	}

	// Pre-starve s.mu. The test goroutine holds the lock; a sacrificial waiter
	// (w1) parks on it and accumulates >1ms of wait; the test then
	// unlock→re-lock barges past w1's wake so w1 re-queues as a starving waiter
	// (LIFO front) and sets the mutex's starving bit. From that point on,
	// every Unlock hands ownership directly to the front waiter, so the fg/bg
	// queue order decides execution order deterministically.
	s.mu.Lock()
	w1Done := make(chan struct{})
	w1Meta := make(chan SyncRunMeta, 1)
	go func() {
		s.mu.Lock()
		// 携带一次真实的被保护状态读取（顺带断言任何轮次开始前 syncMeta 为零值），
		// 否则空临界区会被 staticcheck SA2001 拦下；该 waiter 的存在意义本身
		// 就是占据互斥量队列（饥饿预热）。
		w1Meta <- s.syncMeta
		s.mu.Unlock()
		close(w1Done)
	}()
	time.Sleep(5 * time.Millisecond) // w1 is parked; its wait exceeds 1ms

	type fgOutcome struct {
		res SyncResult
		err error
	}
	fgDone := make(chan fgOutcome, 1)
	go func() {
		res, err := s.SyncSessionUsage() // queues behind w1
		fgDone <- fgOutcome{res: res, err: err}
	}()
	time.Sleep(5 * time.Millisecond) // fg is parked ahead of bg

	bgDone := make(chan error, 1)
	go func() {
		bgDone <- s.SyncAll() // queues behind fg: "ticker fired while fg runs"
	}()
	time.Sleep(20 * time.Millisecond) // bg is parked

	s.mu.Unlock() // wake w1; queue: [fg, bg]
	// Running caller re-acquires before w1 can be scheduled (barging), so w1
	// re-queues as a starving waiter (LIFO front) and sets the starving bit.
	// The critical section must span the sleep so w1 cannot acquire ownership
	// in between; touching the guarded state keeps it a real section.
	s.mu.Lock()
	dbReady := s.db != nil
	time.Sleep(1 * time.Millisecond) // w1 re-queues with the starving bit set
	s.mu.Unlock()                    // handoff → w1 → w1 unlocks → handoff → fg round #0 starts
	if !dbReady {
		t.Fatal("usage db not loaded")
	}

	// Round #0 = fg's round: runs and parks in the seam holding s.mu.
	round0 := mustRecvRoundStart(t, started, "round #0 (foreground)")
	releaseRound(round0)

	// Round #1 = bg's round: fg's round-end Unlock hands it the mutex.
	round1 := mustRecvRoundStart(t, started, "round #1 (background)")

	// bg's round #1 is parked holding s.mu. fg must already be done: it took
	// round #0's meta inside its own critical section and must neither wait on
	// nor read round #1.
	select {
	case out := <-fgDone:
		if out.err != nil {
			releaseRound(round1)
			t.Fatalf("SyncSessionUsage returned error: %v", out.err)
		}
		if out.res.RecordsAdded != 2 || out.res.ProcessedCount != 2 {
			releaseRound(round1)
			t.Fatalf("foreground reported RecordsAdded=%d ProcessedCount=%d, want 2/2 (its own round #0); stats were hijacked by a later round", out.res.RecordsAdded, out.res.ProcessedCount)
		}
		if out.res.FilesScanned != 1 {
			releaseRound(round1)
			t.Fatalf("foreground FilesScanned=%d, want 1 (fixture jsonl only)", out.res.FilesScanned)
		}
	case <-time.After(5 * time.Second):
		// Unblock the parked round so goroutines drain before failing.
		releaseRound(round1)
		<-bgDone
		t.Fatal("SyncSessionUsage blocked behind the queued background round instead of returning its own round's result")
	}

	releaseRound(round1)
	if err := <-bgDone; err != nil {
		t.Fatalf("background SyncAll: %v", err)
	}
	<-w1Done
	if m := <-w1Meta; !m.StartedAt.IsZero() {
		t.Fatalf("sacrificial waiter saw non-zero syncMeta before any round: %+v", m)
	}

	// The background round ran second and must have found nothing new —
	// proving the foreground's 2/2 really belonged to round #0, not to a
	// leftover s.syncMeta value.
	s.mu.Lock()
	bgMeta := s.syncMeta
	s.mu.Unlock()
	if bgMeta.RecordsAdded != 0 {
		t.Fatalf("background round RecordsAdded=%d, want 0 (fixture already synced by round #0)", bgMeta.RecordsAdded)
	}
}

func mustRecvRoundStart(t *testing.T, ch <-chan int, what string) int {
	t.Helper()
	select {
	case i := <-ch:
		return i
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return -1
	}
}
