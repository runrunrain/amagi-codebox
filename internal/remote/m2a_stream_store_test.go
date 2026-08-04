package remote

// m2a_stream_store_test.go — Tests for SessionStreamStore (design §7).
//
// Tests: Seq assignment, eviction, framesAfter, rangeFrames, originComplete,
// earliestSeq/latestSeq sentinel semantics.

import (
	"testing"

	"amagi-codebox/internal/remote/contract"
)

func TestSessionStreamStore_EmptySentinel(t *testing.T) {
	store := NewSessionStreamStore()
	earliest, latest := store.SeqBounds("s1")
	if earliest != 0 || latest != 0 {
		t.Fatalf("empty stream: expected (0,0), got (%d,%d)", earliest, latest)
	}
}

func TestSessionStreamStore_OutputSeqAssignment(t *testing.T) {
	store := NewSessionStreamStore()
	s1 := store.EnsureStream("s1")

	// First output = Seq 1.
	seq1 := s1.appendOutput([]byte("hello"))
	if seq1 != 1 {
		t.Fatalf("first output seq: expected 1, got %d", seq1)
	}
	// Second output = Seq 2.
	seq2 := s1.appendOutput([]byte("world"))
	if seq2 != 2 {
		t.Fatalf("second output seq: expected 2, got %d", seq2)
	}

	earliest, latest := store.SeqBounds("s1")
	if earliest != 1 || latest != 2 {
		t.Fatalf("bounds: expected (1,2), got (%d,%d)", earliest, latest)
	}
}

func TestSessionStreamStore_BoundarySeq(t *testing.T) {
	store := NewSessionStreamStore()
	s1 := store.EnsureStream("s1")

	seq1 := s1.appendOutput([]byte("a"))
	seq2 := s1.appendBoundary()
	seq3 := s1.appendOutput([]byte("b"))

	if seq1 != 1 || seq2 != 2 || seq3 != 3 {
		t.Fatalf("seq: expected 1,2,3 got %d,%d,%d", seq1, seq2, seq3)
	}
}

func TestSessionStreamStore_FramesAfter(t *testing.T) {
	store := NewSessionStreamStore()
	s1 := store.EnsureStream("s1")
	s1.appendOutput([]byte("a"))
	s1.appendOutput([]byte("b"))
	s1.appendOutput([]byte("c"))

	// All frames.
	all := store.FramesAfter("s1", nil)
	if len(all) != 3 {
		t.Fatalf("all frames: expected 3, got %d", len(all))
	}

	// After seq 1.
	last := contract.Seq(1)
	after := store.FramesAfter("s1", &last)
	if len(after) != 2 {
		t.Fatalf("after seq 1: expected 2, got %d", len(after))
	}
	if after[0].seq != 2 || after[1].seq != 3 {
		t.Fatalf("after seq 1: expected seqs 2,3 got %d,%d", after[0].seq, after[1].seq)
	}
}

func TestSessionStreamStore_RangeFrames(t *testing.T) {
	store := NewSessionStreamStore()
	s1 := store.EnsureStream("s1")
	s1.appendOutput([]byte("a"))
	s1.appendOutput([]byte("b"))
	s1.appendOutput([]byte("c"))
	s1.appendOutput([]byte("d"))

	// Range [2,3] fully retained.
	frames, ok := store.RangeFrames("s1", 2, 3)
	if !ok || len(frames) != 2 {
		t.Fatalf("range [2,3]: expected 2 frames ok, got %d ok=%v", len(frames), ok)
	}

	// Range [0,2] not retained (seq starts at 1, 0 invalid).
	_, ok = store.RangeFrames("s1", 0, 2)
	if ok {
		t.Fatal("range [0,2]: expected not ok")
	}
}

func TestSessionStreamStore_OriginComplete(t *testing.T) {
	store := NewSessionStreamStore()
	s1 := store.EnsureStream("s1")
	if !store.OriginComplete("s1") {
		t.Fatal("empty stream should be origin complete")
	}

	// Add many frames to trigger eviction.
	for i := 0; i < streamMaxFrames+10; i++ {
		s1.appendOutput([]byte("x"))
	}
	if store.OriginComplete("s1") {
		t.Fatal("after eviction, origin should not be complete")
	}

	// earliestSeq should be > 1 after eviction.
	earliest, _ := store.SeqBounds("s1")
	if earliest <= 1 {
		t.Fatalf("after eviction earliest should be > 1, got %d", earliest)
	}
}

func TestSessionStreamStore_RemoveStream(t *testing.T) {
	store := NewSessionStreamStore()
	s1 := store.EnsureStream("s1")
	s1.appendOutput([]byte("a"))

	store.RemoveStream("s1")
	earliest, latest := store.SeqBounds("s1")
	if earliest != 0 || latest != 0 {
		t.Fatalf("after remove: expected (0,0), got (%d,%d)", earliest, latest)
	}
}
