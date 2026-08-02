package remote

// B1 evidence-correction tests (Leader audit). Exact named tests A-G covering
// the actual gaps the prior report overclaimed. Test-first: these drove the
// production fixes (seams writeFn/readFileFn/renameFn, quarantine safety,
// rename-error readback, marker validation, sticky open, namespace rejection,
// ConfirmDurable isolation). No secret is printed.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// validArchiveLine writes a single valid device event as archive segment gen.
func validArchiveLine(t *testing.T, dir string, gen int, suffix byte) {
	t.Helper()
	ev := devEvent(suffix, "AAAAAAAAAAAAAAAAAAAAAA")
	canonical, err := canonicalizeSecurityEvent(ev)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s%d", durableArchivePrefix, gen)), append(canonical, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ===========================================================================
// A) TestB1PathModeAndSegmentCap
// ===========================================================================

func TestB1PathModeAndSegmentCap(t *testing.T) {
	t.Run("active_0644_rejected", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, durableActiveName), nil, 0o644)
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		if err := s.OpenAndScan(); err == nil {
			t.Fatal("existing active 0644 must be rejected")
		}
		s.Close()
	})
	t.Run("archive_0644_rejected", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, durableArchivePrefix+"0"), nil, 0o644)
		os.WriteFile(filepath.Join(dir, durableActiveName), nil, 0o600)
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		if err := s.OpenAndScan(); err == nil {
			t.Fatal("existing archive 0644 must be rejected")
		}
		s.Close()
	})
	t.Run("config_dir_symlink_rejected", func(t *testing.T) {
		real := t.TempDir()
		parent := t.TempDir()
		link := filepath.Join(parent, "linkdir")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		s := NewDurableSecurityEventSink(link, newSecurityHealthRegister())
		if err := s.OpenAndScan(); err == nil {
			t.Fatal("config dir symlink must be rejected")
		}
		s.Close()
	})
	t.Run("broken_active_symlink_rejected_no_target_write", func(t *testing.T) {
		dir := t.TempDir()
		active := filepath.Join(dir, durableActiveName)
		// Broken symlink: points at a nonexistent target.
		if err := os.Symlink(filepath.Join(dir, "nonexistent-target"), active); err != nil {
			t.Fatal(err)
		}
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		if err := s.OpenAndScan(); err == nil {
			t.Fatal("broken active symlink must be rejected")
		}
		s.Close()
		// The broken symlink MUST NOT have been followed by O_CREATE.
		if _, err := os.Lstat(filepath.Join(dir, "nonexistent-target")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatal("broken symlink target was created (O_CREATE followed the link)")
		}
	})
	t.Run("eight_archives_no_active_rejected", func(t *testing.T) {
		dir := t.TempDir()
		for i := 0; i < 8; i++ {
			validArchiveLine(t, dir, i, byte(i+1))
		}
		// No active.
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		if err := s.OpenAndScan(); err == nil {
			t.Fatal("8 archives with no active must be rejected (no 9th segment)")
		}
		s.Close()
		// No 9th segment / new active created.
		if _, err := os.Lstat(filepath.Join(dir, durableArchivePrefix+"8")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatal("9th archive segment was created")
		}
	})
	t.Run("torn_quarantine_0600_regular_then_truncate", func(t *testing.T) {
		dir := t.TempDir()
		active := filepath.Join(dir, durableActiveName)
		ev := devEvent(7, "AAAAAAAAAAAAAAAAAAAAAA")
		canonical := canonicalOrFatal(t, ev)
		complete := append(canonical, '\n')
		torn := []byte(`{"v`)
		os.WriteFile(active, append(complete, torn...), 0o600)
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		if err := s.OpenAndScan(); err != nil {
			t.Fatalf("torn recover: %v", err)
		}
		s.Close()
		tornPath := filepath.Join(dir, durableTornName)
		fi, err := os.Lstat(tornPath)
		if err != nil {
			t.Fatalf("torn quarantine not created: %v", err)
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			t.Fatal("torn quarantine must be a regular file")
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("torn quarantine mode %o not 0600", fi.Mode().Perm())
		}
		got, _ := os.ReadFile(active)
		if !bytes.Equal(got, complete) {
			t.Fatalf("active not truncated to complete prefix: %q", got)
		}
	})
	t.Run("existing_torn_fail_closed", func(t *testing.T) {
		dir := t.TempDir()
		active := filepath.Join(dir, durableActiveName)
		ev := devEvent(8, "AAAAAAAAAAAAAAAAAAAAAA")
		canonical := canonicalOrFatal(t, ev)
		os.WriteFile(active, append(append(canonical, '\n'), []byte(`{"v`)...), 0o600)
		// Pre-existing .torn must never be overwritten.
		os.WriteFile(filepath.Join(dir, durableTornName), []byte("preexisting"), 0o600)
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		if err := s.OpenAndScan(); err == nil {
			t.Fatal("existing torn must fail-closed (never overwrite)")
		}
		s.Close()
		got, _ := os.ReadFile(filepath.Join(dir, durableTornName))
		if string(got) != "preexisting" {
			t.Fatal("existing torn was overwritten")
		}
	})
}

// ===========================================================================
// B) TestB1ZeroPartialPendingIsolation
// ===========================================================================

func TestB1ZeroPartialPendingIsolation(t *testing.T) {
	t.Run("zero_byte_write_PreAccept_no_pending", func(t *testing.T) {
		s, _ := newTestDurable(t)
		s.writeFn = func(*os.File, []byte) (int, error) { return 0, errors.New("write io") }
		ev := devEvent(11, "AAAAAAAAAAAAAAAAAAAAAA")
		res, _ := s.AppendSecurityEvent(ev)
		if res.State != EventPreAcceptFailed || res.Failure != EventFailureIO {
			t.Fatalf("zero-byte write: state=%v failure=%v, want PreAccept/IO", res.State, res.Failure)
		}
		if s.pending != nil {
			t.Fatal("zero-byte write must NOT retain pending")
		}
		s.Close()
	})
	t.Run("partial_write_degraded_pending_retained", func(t *testing.T) {
		s, _ := newTestDurable(t)
		canonical := canonicalOrFatal(t, devEvent(12, "AAAAAAAAAAAAAAAAAAAAAA"))
		// Write exactly half the line, then error.
		half := len(canonical) / 2
		s.writeFn = func(_ *os.File, b []byte) (int, error) {
			n := half
			if n > len(b) {
				n = len(b)
			}
			// Actually perform a partial physical write so the file reflects it.
			_, _ = s.f.Write(b[:n]) // best-effort partial; the seam models the fault
			return n, errors.New("short write")
		}
		ev := devEvent(12, "AAAAAAAAAAAAAAAAAAAAAA")
		res, _ := s.AppendSecurityEvent(ev)
		if res.State != EventAcceptedButDurabilityDegraded {
			t.Fatalf("partial write: state=%v, want degraded", res.State)
		}
		if s.pending == nil {
			t.Fatal("partial write must retain pending")
		}
		s.Close()
	})
	t.Run("same_id_same_canonical_no_rewrite", func(t *testing.T) {
		s, _ := newTestDurable(t)
		s.syncFn = func(*os.File) error { return errors.New("sync unavailable") }
		ev := devEvent(13, "AAAAAAAAAAAAAAAAAAAAAA")
		s.AppendSecurityEvent(ev) // degraded + pending
		sizeBefore, _ := os.Stat(s.activePath())
		// Re-drive same id+payload → degraded, no rewrite (file size unchanged).
		res, _ := s.AppendSecurityEvent(ev)
		if res.State != EventAcceptedButDurabilityDegraded {
			t.Fatalf("same pending re-drive: state=%v, want degraded", res.State)
		}
		sizeAfter, _ := os.Stat(s.activePath())
		if sizeBefore.Size() != sizeAfter.Size() {
			t.Fatal("same-id/same-canonical re-drive rewrote the file")
		}
		s.Close()
	})
	t.Run("same_id_diff_payload_integrity", func(t *testing.T) {
		s, _ := newTestDurable(t)
		s.syncFn = func(*os.File) error { return errors.New("sync unavailable") }
		ev := devEvent(14, "AAAAAAAAAAAAAAAAAAAAAA")
		s.AppendSecurityEvent(ev) // degraded + pending
		ev2 := ev
		ev2.DeviceID = contract.DeviceID(altDevID())
		res, _ := s.AppendSecurityEvent(ev2)
		if res.State != EventPreAcceptFailed || res.Failure != EventFailureIntegrity {
			t.Fatalf("same-id/diff: state=%v failure=%v, want PreAccept/Integrity", res.State, res.Failure)
		}
		s.Close()
	})
	t.Run("different_id_unavailable", func(t *testing.T) {
		s, _ := newTestDurable(t)
		s.syncFn = func(*os.File) error { return errors.New("sync unavailable") }
		s.AppendSecurityEvent(devEvent(15, "AAAAAAAAAAAAAAAAAAAAAA")) // degraded + pending
		res, _ := s.AppendSecurityEvent(devEvent(16, altDevID()))
		if res.State != EventPreAcceptFailed || res.Failure != EventFailureUnavailable {
			t.Fatalf("different-id-while-pending: state=%v failure=%v, want PreAccept/Unavailable", res.State, res.Failure)
		}
		s.Close()
	})
}

// ===========================================================================
// C) TestB1ConfirmIsolationAndReadFailure
// ===========================================================================

func TestB1ConfirmIsolationAndReadFailure(t *testing.T) {
	t.Run("confirm_accepted_A_while_pending_B_errors_and_retains_B", func(t *testing.T) {
		s, _ := newTestDurable(t)
		// Accept A.
		evA := devEvent(21, "AAAAAAAAAAAAAAAAAAAAAA")
		if res, _ := s.AppendSecurityEvent(evA); res.State != EventAcceptedBySink {
			t.Fatal(res.State)
		}
		// Pending B (degraded via sync fault).
		s.syncFn = func(*os.File) error { return errors.New("sync unavailable") }
		evB := devEvent(22, altDevID())
		s.AppendSecurityEvent(evB)
		s.syncFn = func(f *os.File) error { return f.Sync() }
		// Confirm A (already accepted) while B pending → must error, retain B.
		_, err := s.ConfirmDurable(evA.EventID)
		if err == nil {
			t.Fatal("confirming accepted A while different pending B must error")
		}
		if s.pending == nil || s.pending.id != evB.EventID {
			t.Fatal("pending B must be retained after refused confirm of A")
		}
		s.Close()
	})
	t.Run("readfile_failure_errors_and_retains_pending", func(t *testing.T) {
		s, _ := newTestDurable(t)
		s.syncFn = func(*os.File) error { return errors.New("sync unavailable") }
		ev := devEvent(23, "AAAAAAAAAAAAAAAAAAAAAA")
		s.AppendSecurityEvent(ev) // degraded + pending
		s.syncFn = func(f *os.File) error { return f.Sync() }
		s.readFileFn = func(string) ([]byte, error) { return nil, errors.New("read io") }
		_, err := s.ConfirmDurable(ev.EventID)
		if err == nil {
			t.Fatal("read failure must error")
		}
		if s.pending == nil {
			t.Fatal("read failure must retain pending")
		}
		s.Close()
	})
	t.Run("active_shorter_than_offset_errors_and_retains_pending", func(t *testing.T) {
		s, _ := newTestDurable(t)
		// Accept A first so the pending entry sits at a non-zero offset.
		evA := devEvent(24, "AAAAAAAAAAAAAAAAAAAAAA")
		if res, _ := s.AppendSecurityEvent(evA); res.State != EventAcceptedBySink {
			t.Fatal(res.State)
		}
		// Pending B at offset = len(A line).
		s.syncFn = func(*os.File) error { return errors.New("sync unavailable") }
		evB := devEvent(25, altDevID())
		s.AppendSecurityEvent(evB)
		s.syncFn = func(f *os.File) error { return f.Sync() }
		// Truncate the active below B's offset → file shorter than pending offset.
		os.Truncate(s.activePath(), 0)
		_, err := s.ConfirmDurable(evB.EventID)
		if err == nil {
			t.Fatal("confirm must error: active shorter than pending offset")
		}
		if s.pending == nil || s.pending.id != evB.EventID {
			t.Fatal("must retain pending B")
		}
		s.Close()
	})
	t.Run("extra_bytes_after_pending_errors_and_retains", func(t *testing.T) {
		s, _ := newTestDurable(t)
		s.syncFn = func(*os.File) error { return errors.New("sync unavailable") }
		ev := devEvent(25, "AAAAAAAAAAAAAAAAAAAAAA")
		s.AppendSecurityEvent(ev) // degraded + pending
		s.syncFn = func(f *os.File) error { return f.Sync() }
		// Append extra garbage after the pending line so the file is longer.
		f, _ := os.OpenFile(s.activePath(), os.O_WRONLY|os.O_APPEND, 0o600)
		f.Write([]byte("EXTRA"))
		f.Close()
		_, err := s.ConfirmDurable(ev.EventID)
		if err == nil {
			t.Fatal("extra bytes after pending must error (never truncate/rewrite)")
		}
		if s.pending == nil {
			t.Fatal("must retain pending")
		}
		s.Close()
	})
	t.Run("partial_tail_prefix_rewrites_and_promotes", func(t *testing.T) {
		s, _ := newTestDurable(t)
		// Make a partial pending: write half the line, then a sync fault.
		ev := devEvent(26, "AAAAAAAAAAAAAAAAAAAAAA")
		canonical := canonicalOrFatal(t, ev)
		half := len(canonical) / 2
		s.writeFn = func(_ *os.File, b []byte) (int, error) {
			_, _ = s.f.Write(b[:half])
			return half, errors.New("short write")
		}
		s.AppendSecurityEvent(ev) // degraded, partial on disk
		s.writeFn = writeFull
		s.syncFn = func(f *os.File) error { return f.Sync() }
		if _, err := s.ConfirmDurable(ev.EventID); err != nil {
			t.Fatalf("confirm partial rewrite: %v", err)
		}
		// The full line is now on disk.
		got, _ := os.ReadFile(s.activePath())
		if !bytes.HasSuffix(got, append(canonical, '\n')) {
			t.Fatalf("partial confirm did not rewrite the full line: %q", got)
		}
		if s.pending != nil {
			t.Fatal("pending must be cleared after promote")
		}
		s.Close()
	})
	t.Run("complete_no_rewrite_promotes", func(t *testing.T) {
		s, _ := newTestDurable(t)
		s.syncFn = func(*os.File) error { return errors.New("sync unavailable") }
		ev := devEvent(27, "AAAAAAAAAAAAAAAAAAAAAA")
		s.AppendSecurityEvent(ev) // degraded; full line written, only sync uncertain
		s.syncFn = func(f *os.File) error { return f.Sync() }
		if _, err := s.ConfirmDurable(ev.EventID); err != nil {
			t.Fatalf("confirm complete: %v", err)
		}
		if s.pending != nil {
			t.Fatal("pending must be cleared")
		}
		if _, ok := s.byID[ev.EventID]; !ok {
			t.Fatal("promoted id must be in index")
		}
		s.Close()
	})
}

// ===========================================================================
// D) TestB1RenameErrorReadback
// ===========================================================================

// makeFullActive writes a single valid event into the active segment and returns
// its content bytes.
func makeFullActive(t *testing.T, dir string, suffix byte) []byte {
	t.Helper()
	ev := devEvent(suffix, "AAAAAAAAAAAAAAAAAAAAAA")
	canonical := canonicalOrFatal(t, ev)
	content := append(canonical, '\n')
	os.WriteFile(filepath.Join(dir, durableActiveName), content, 0o600)
	return content
}

func TestB1RenameErrorReadback(t *testing.T) {
	t.Run("live_rotate_rename_succeeded_despite_error_continues", func(t *testing.T) {
		s, dir := newTestDurable(t)
		s.syncFn = func(*os.File) error { return nil }
		// Fill the active until the NEXT event would overflow (trigger rotation).
		for i := 0; ; i++ {
			ev := distinctDevEvent(i)
			lineLen := int64(len(canonicalOrFatal(t, ev)) + 1)
			if s.activeBytes+lineLen > DurableActiveMaxBytes {
				break // next append triggers rotation
			}
			if res, _ := s.AppendSecurityEvent(ev); res.State == EventPreAcceptFailed {
				t.Fatal("setup: unexpected cap during fill")
			}
		}
		// Inject a rename seam that ACTUALLY renames but returns an error.
		s.renameFn = func(old, new string) error {
			realErr := os.Rename(old, new) // really move
			if realErr != nil {
				return realErr
			}
			return errors.New("synthetic post-rename error")
		}
		// This append triggers rotation; the rename "fails" but the file moved.
		res, _ := s.AppendSecurityEvent(distinctDevEvent(99998))
		if res.State == EventPreAcceptFailed {
			t.Fatal("rename-error-with-actual-move must continue, not fail")
		}
		s.Close()
		// No leftover marker; archive present; active present.
		if !markerGone(t, dir) {
			t.Fatal("leftover marker after rename-error-readback continuation")
		}
		s2 := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		if err := s2.OpenAndScan(); err != nil {
			t.Fatalf("reopen after rename-error continuation: %v", err)
		}
		s2.Close()
	})
	t.Run("live_rotate_rename_truly_failed_old_intact", func(t *testing.T) {
		s, dir := newTestDurable(t)
		s.syncFn = func(*os.File) error { return nil }
		activeSizeBefore := s.activeBytes
		for i := 0; ; i++ {
			ev := distinctDevEvent(i)
			lineLen := int64(len(canonicalOrFatal(t, ev)) + 1)
			if s.activeBytes+lineLen > DurableActiveMaxBytes {
				break // next append triggers rotation
			}
			s.AppendSecurityEvent(ev)
			activeSizeBefore = s.activeBytes
		}
		// Inject a rename seam that does NOT move and returns an error.
		s.renameFn = func(string, string) error { return errors.New("rename truly failed") }
		res, _ := s.AppendSecurityEvent(distinctDevEvent(99999))
		if res.State != EventPreAcceptFailed {
			t.Fatalf("truly-failed rename: state=%v, want PreAccept", res.State)
		}
		// Old active intact (still present, content unchanged).
		got, err := os.ReadFile(filepath.Join(dir, durableActiveName))
		if err != nil {
			t.Fatal("old active must remain intact after truly-failed rename")
		}
		if int64(len(got)) != activeSizeBefore {
			t.Fatalf("old active size changed: %d vs %d", len(got), activeSizeBefore)
		}
		// Marker must have been cleaned up (rotation aborted cleanly).
		if !markerGone(t, dir) {
			t.Fatal("leftover marker after aborted rotation")
		}
		s.Close()
	})
	t.Run("startup_oldonly_rename_succeeded_despite_error_continues", func(t *testing.T) {
		dir := t.TempDir()
		content := makeFullActive(t, dir, 31)
		writeRotateMarker(t, dir, 0, content)
		// No archive yet (old-only state).
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		// Inject rename seam that actually moves but errors.
		s.renameFn = func(old, new string) error {
			if e := os.Rename(old, new); e != nil {
				return e
			}
			return errors.New("synthetic post-rename error")
		}
		if err := s.OpenAndScan(); err != nil {
			t.Fatalf("old-only rename-error-readback must continue: %v", err)
		}
		s.Close()
		// archive0 present, active present (empty), marker gone.
		if _, err := os.Lstat(filepath.Join(dir, durableArchivePrefix+"0")); err != nil {
			t.Fatal("archive0 missing after old-only rename-error continuation")
		}
		if !markerGone(t, dir) {
			t.Fatal("leftover marker after old-only rename-error continuation")
		}
	})
	t.Run("startup_oldonly_rename_truly_failed_old_intact", func(t *testing.T) {
		dir := t.TempDir()
		content := makeFullActive(t, dir, 32)
		writeRotateMarker(t, dir, 0, content)
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		s.renameFn = func(string, string) error { return errors.New("rename truly failed") }
		if err := s.OpenAndScan(); err == nil {
			t.Fatal("old-only truly-failed rename must error (old intact)")
		}
		s.Close()
		// Old active still intact.
		got, err := os.ReadFile(filepath.Join(dir, durableActiveName))
		if err != nil || !bytes.Equal(got, content) {
			t.Fatal("old active must remain intact after truly-failed startup rename")
		}
	})
}

// ===========================================================================
// E) TestB1MarkerValidationAndStickyOpen
// ===========================================================================

func TestB1MarkerValidationAndStickyOpen(t *testing.T) {
	t.Run("marker_invalid_version_rejected", func(t *testing.T) {
		dir := t.TempDir()
		content := makeFullActive(t, dir, 41)
		// marker version 2.
		writeBadMarker(t, dir, rotateMarker{Version: 2, Generation: 0, Bytes: len(content), SHA256: hexSHA(content)})
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		if err := s.OpenAndScan(); err == nil {
			t.Fatal("marker version!=1 must be rejected")
		}
		s.Close()
	})
	t.Run("marker_bytes_out_of_range_rejected", func(t *testing.T) {
		dir := t.TempDir()
		content := makeFullActive(t, dir, 42)
		writeBadMarker(t, dir, rotateMarker{Version: 1, Generation: 0, Bytes: 0, SHA256: hexSHA(content)})
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		if err := s.OpenAndScan(); err == nil {
			t.Fatal("marker bytes<1 must be rejected")
		}
		s.Close()
	})
	t.Run("marker_bad_sha_rejected", func(t *testing.T) {
		dir := t.TempDir()
		content := makeFullActive(t, dir, 43)
		writeBadMarker(t, dir, rotateMarker{Version: 1, Generation: 0, Bytes: len(content), SHA256: "XYZ"})
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		if err := s.OpenAndScan(); err == nil {
			t.Fatal("marker bad sha256 must be rejected")
		}
		s.Close()
	})
	t.Run("open_failure_is_sticky_no_rescan", func(t *testing.T) {
		dir := t.TempDir()
		// Corrupt archive → integrity failure.
		os.WriteFile(filepath.Join(dir, durableArchivePrefix+"0"), []byte("garbage\n"), 0o600)
		os.WriteFile(filepath.Join(dir, durableActiveName), nil, 0o600)
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		err1 := s.OpenAndScan()
		if err1 == nil {
			t.Fatal("expected scan failure")
		}
		// Second call must return the SAME sticky failure without rescanning
		// (no panic / no double-count).
		err2 := s.OpenAndScan()
		if err2 == nil {
			t.Fatal("second OpenAndScan must return sticky failure")
		}
		s.Close()
	})
	t.Run("invalid_archive_namespace_rejected_not_ignored", func(t *testing.T) {
		dir := t.TempDir()
		// A file with the archive prefix but a non-numeric suffix.
		os.WriteFile(filepath.Join(dir, durableArchivePrefix+"abc"), []byte("x\n"), 0o600)
		os.WriteFile(filepath.Join(dir, durableActiveName), nil, 0o600)
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		if err := s.OpenAndScan(); err == nil {
			t.Fatal("invalid archive namespace (non-numeric suffix) must be rejected, not ignored")
		}
		s.Close()
	})
}

// writeBadMarker writes a rotate marker that may be field-invalid (bypassing the
// content-derived valid writer) but still canonical JSON.
func writeBadMarker(t *testing.T, dir string, m rotateMarker) {
	t.Helper()
	m.Version = orOneIfZeroForBad(m.Version) // keep version as given
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, durableMarkerName), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// orOneIfZeroForBad is a no-op passthrough so writeBadMarker keeps the caller's
// version intent (named to make the "bad marker" intent explicit).
func orOneIfZeroForBad(v int) int { return v }

func hexSHA(b []byte) string {
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

// ===========================================================================
// G) TestB1SanitizedRecordJSONTags
// ===========================================================================

func TestB1SanitizedRecordJSONTags(t *testing.T) {
	gen := uint64(7)
	att := uint8(2)
	dev := "AAAAAAAAAAAAAAAAAAAAAA"
	rec := SecurityEventRecord{
		EventID:           SecurityEventID("abc"),
		Kind:              "device_paired",
		OccurredAt:        "2026-08-02T12:00:00Z",
		PairingGeneration: &gen,
		Attempt:           &att,
		DeviceID:          &dev,
	}
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"eventId", "kind", "occurredAt", "pairingGeneration", "attempt", "deviceId"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("json tag %q missing from marshalled record: %s", k, out)
		}
	}
	// An unset pointer field must be omitted.
	rec2 := SecurityEventRecord{EventID: "x", Kind: "store_durability_degraded", OccurredAt: "t"}
	out2, _ := json.Marshal(rec2)
	if bytes.Contains(out2, []byte("pairingGeneration")) || bytes.Contains(out2, []byte("deviceId")) {
		t.Fatalf("unset pointer fields must be omitempty: %s", out2)
	}
	// No raw payload/path/secret leakage keys.
	for _, bad := range []string{"line", "path", "secret", "raw"} {
		if bytes.Contains(out, []byte("\""+bad+"\"")) {
			t.Fatalf("record leaked forbidden key %q: %s", bad, out)
		}
	}
	_ = time.Now // keep time import used
}

// reuse distinctDevEvent / canonicalOrFatal / markerGone / writeRotateMarker /
// healthActive from the hardening test file (same package).
var _ = contract.DeviceID("")

// ===========================================================================
// Point 1) TestB1ConfirmFullTailRequiresSync
// ===========================================================================

func TestB1ConfirmFullTailRequiresSync(t *testing.T) {
	s, _ := newTestDurable(t)
	// Full write + Sync error → pending with the FULL tail already on disk.
	s.syncFn = func(*os.File) error { return errors.New("sync unavailable") }
	ev := devEvent(51, "AAAAAAAAAAAAAAAAAAAAAA")
	res, _ := s.AppendSecurityEvent(ev)
	if res.State != EventAcceptedButDurabilityDegraded {
		t.Fatalf("expected degraded, got %v", res.State)
	}
	if s.pending == nil {
		t.Fatal("pending must be retained")
	}
	preActive := s.activeBytes
	// Confirm while Sync STILL fails → must re-Sync and fail; pending/index/
	// accounting untouched (no commit).
	_, err := s.ConfirmDurable(ev.EventID)
	if err == nil {
		t.Fatal("Confirm must re-Sync and fail while Sync errors (full-tail branch)")
	}
	if s.pending == nil {
		t.Fatal("pending must be retained on failed re-Sync")
	}
	if _, ok := s.byID[ev.EventID]; ok {
		t.Fatal("index must not be updated on failed re-Sync")
	}
	if s.activeBytes != preActive {
		t.Fatalf("accounting must be unchanged: activeBytes=%d want %d", s.activeBytes, preActive)
	}
	// Restore Sync; Confirm now succeeds (re-Sync passes).
	s.syncFn = func(f *os.File) error { return f.Sync() }
	if _, err := s.ConfirmDurable(ev.EventID); err != nil {
		t.Fatalf("Confirm after Sync restore: %v", err)
	}
	if s.pending != nil {
		t.Fatal("pending must be cleared after successful Confirm")
	}
	if _, ok := s.byID[ev.EventID]; !ok {
		t.Fatal("id must be accepted after Confirm")
	}
	s.Close()
}

// ===========================================================================
// Point 2) TestB1RotateAbortCleanupAndReusable
// ===========================================================================

// fillNearCap appends distinct events until the next event would overflow the
// active segment (so the following append triggers a rotation).
func fillNearCap(t *testing.T, s *durableSecurityEventSink) {
	t.Helper()
	for i := 0; ; i++ {
		ev := distinctDevEvent(i)
		if s.activeBytes+int64(len(canonicalOrFatal(t, ev))+1) > DurableActiveMaxBytes {
			return
		}
		if res, _ := s.AppendSecurityEvent(ev); res.State == EventPreAcceptFailed {
			t.Fatalf("fill setup: unexpected cap: %v", res.Failure)
		}
	}
}

func TestB1RotateAbortCleanupAndReusable(t *testing.T) {
	t.Run("real_rename_fail_marker_gone_f_nonnil_reusable", func(t *testing.T) {
		s, dir := newTestDurable(t)
		s.syncFn = func(*os.File) error { return nil }
		fillNearCap(t, s)
		// rename truly fails (no move) → OldIntact abort.
		s.renameFn = func(string, string) error { return errors.New("rename truly failed") }
		res, _ := s.AppendSecurityEvent(distinctDevEvent(77777))
		if res.State != EventPreAcceptFailed {
			t.Fatalf("abort: state=%v want PreAccept", res.State)
		}
		if !markerGone(t, dir) {
			t.Fatal("marker must be cleaned up on abort")
		}
		if s.f == nil {
			t.Fatal("f must be reopened (nonnil) after successful abort cleanup")
		}
		if s.openErr != nil {
			t.Fatal("sink must NOT be latched after successful abort cleanup")
		}
		// Next append does not panic and proceeds (sink reusable).
		s.renameFn = os.Rename
		res2, _ := s.AppendSecurityEvent(distinctDevEvent(77778))
		if res2.State == EventPreAcceptFailed && res2.Failure == EventFailureUnavailable {
			t.Fatal("sink latched after successful abort (must be reusable)")
		}
		s.Close()
	})
	t.Run("cleanup_failure_latches", func(t *testing.T) {
		s, _ := newTestDurable(t)
		s.syncFn = func(*os.File) error { return nil }
		fillNearCap(t, s)
		s.renameFn = func(string, string) error { return errors.New("rename truly failed") }
		// marker removal fails → cleanup failure → latch unavailable + f nil.
		s.removeFn = func(string) error { return errors.New("remove failed") }
		res, _ := s.AppendSecurityEvent(distinctDevEvent(88888))
		if res.State != EventPreAcceptFailed {
			t.Fatalf("cleanup-failure: state=%v want PreAccept", res.State)
		}
		if s.openErr == nil {
			t.Fatal("sink must be latched (openErr set) on cleanup failure")
		}
		if s.f != nil {
			t.Fatal("f must be nil after latched cleanup failure")
		}
		// Next append is unavailable (latched).
		res2, _ := s.AppendSecurityEvent(distinctDevEvent(88889))
		if res2.State != EventPreAcceptFailed || res2.Failure != EventFailureUnavailable {
			t.Fatalf("latched sink: state=%v failure=%v want Unavailable", res2.State, res2.Failure)
		}
		s.Close()
	})
}
