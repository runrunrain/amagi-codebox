package remote

// Snapshot best-effort replacement + three-state read-back reconcile + bounded
// temp namespace (design §7.5). os.Rename is treated as a NON-atomic best-effort
// projection operation on every platform; the state is determined SOLELY by
// exact old/next byte read-back. Any comment or logic claiming Windows rename
// atomicity is forbidden. Temp files are created O_EXCL 0600 with a 22-char
// RawURL random suffix, never followed (Lstat no-follow), and an unsafe
// namespace (symlink/reparse/non-regular/owner-mismatch/oversize/invalid
// content) blocks new pair/seen snapshot writes but NEVER blocks a revoke
// ledger commit/fence/Terminate.

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const (
	// MaxSnapshotBytes bounds a canonical snapshot (C-010: ≤ 1MiB).
	MaxSnapshotBytes = 1 << 20
	// MaxSnapshotTempFiles bounds the number of owned temp files (§7.5: 8).
	MaxSnapshotTempFiles = 8
	// MaxSnapshotTempBytes bounds aggregate owned temp bytes (§7.5: 8MiB).
	MaxSnapshotTempBytes = 8 << 20
	snapshotTempPrefix   = ".devices.json.tmp-"
	snapshotTempRandLen  = 16 // 16 bytes → 22-char RawURL suffix
)

// snapshotRenameFn is the rename seam (production = os.Rename). It is a
// best-effort, non-authoritative operation; the three-state is decided solely
// by exact read-back. Tests swap it to inject rename failure / torn writes.
var snapshotRenameFn = os.Rename

// snapshotReconcileResult carries the three-state outcome and the exact temp
// path/staging bytes for classified cleanup.
type snapshotReconcileResult struct {
	state         StoreMutationState
	tempPath      string
	stagingBytes  []byte
	tempCreated   bool
	selfReadOK    bool
	cleanupFailed bool
}

// tempNamespaceScanResult summarizes a startup temp-namespace scan.
type tempNamespaceScanResult struct {
	unsafe        bool
	leftoverCount int
	leftoverBytes int64
	cleanupFailed bool
}

// generateTempSuffix returns a 22-char canonical RawURL suffix (16 random bytes).
func generateTempSuffix(r io.Reader) (string, error) {
	buf := make([]byte, snapshotTempRandLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// validTempSuffix reports whether suffix is a canonical 22-char RawURL encoding
// of 16 bytes (no padding).
func validTempSuffix(suffix string) bool {
	if len(suffix) != base64.RawURLEncoding.EncodedLen(snapshotTempRandLen) {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(suffix)
	if err != nil {
		return false
	}
	return len(b) == snapshotTempRandLen
}

// isSnapshotTempName reports whether name matches the exact temp namespace.
func isSnapshotTempName(name string) (suffix string, ok bool) {
	if len(name) <= len(snapshotTempPrefix) {
		return "", false
	}
	if name[:len(snapshotTempPrefix)] != snapshotTempPrefix {
		return "", false
	}
	s := name[len(snapshotTempPrefix):]
	if !validTempSuffix(s) {
		return "", false
	}
	return s, true
}

// startupTempCleanup scans the snapshot directory for exact temp-namespace
// entries. Safe (regular/no-symlink/owner-OK/≤1MiB/strict-valid content with
// matching StoreID) entries are deleted and NEVER promoted. Any unsafe entry
// (symlink/reparse/non-regular/owner-mismatch/oversize/invalid suffix or
// content) sets unsafe=true and is left in place. Leftover safe files (cleanup
// failure) are counted toward the budget. expectedStoreID validates temp content
// belongs to this store; a StoreID mismatch is unsafe.
func startupTempCleanup(dir, expectedStoreID string) tempNamespaceScanResult {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return tempNamespaceScanResult{unsafe: true}
	}
	res := tempNamespaceScanResult{}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		_, ok := isSnapshotTempName(ent.Name())
		if !ok {
			continue
		}
		full := filepath.Join(dir, ent.Name())
		info, err := os.Lstat(full)
		if err != nil {
			// Cannot stat: treat as unsafe, do not touch.
			res.unsafe = true
			continue
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeIrregular != 0 {
			res.unsafe = true
			continue
		}
		if !tempOwnerOK(info) {
			res.unsafe = true
			continue
		}
		if info.Size() > MaxSnapshotBytes {
			res.unsafe = true
			continue
		}
		// Strict-validate content + StoreID before ever deleting.
		content, err := os.ReadFile(full)
		if err != nil || !snapshotContentStoreIDMatches(content, expectedStoreID) {
			res.unsafe = true
			continue
		}
		if err := os.Remove(full); err != nil {
			res.cleanupFailed = true
			// Count toward budget (overflow-safe).
			if res.leftoverCount < MaxSnapshotTempFiles {
				res.leftoverCount++
			}
			if res.leftoverBytes <= MaxSnapshotTempBytes-info.Size() {
				res.leftoverBytes += info.Size()
			} else {
				res.leftoverBytes = MaxSnapshotTempBytes + 1
			}
		}
	}
	return res
}

// snapshotContentStoreIDMatches returns true only if content is a strict-valid
// snapshot whose storeId equals expectedStoreID. It never promotes an invalid
// file to "safe".
func snapshotContentStoreIDMatches(content []byte, expectedStoreID string) bool {
	fields, err := strictJSONObject(content)
	if err != nil {
		return false
	}
	v, ok := fields["storeId"]
	if !ok {
		return false
	}
	var sid string
	if err := unmarshalJSON(v, &sid); err != nil {
		return false
	}
	return sid == expectedStoreID
}

// replaceSnapshot performs the best-effort replacement + exact read-back
// reconcile. The caller holds storeMu and has already verified the temp budget.
// oldBytes is the proven-current canonical bytes; nextBytes is the candidate.
// Both must be ≤ MaxSnapshotBytes. Returns the three-state classification plus
// the exact temp path/staging bytes for classified cleanup.
func replaceSnapshot(target string, oldBytes, nextBytes []byte, r io.Reader) (snapshotReconcileResult, error) {
	res := snapshotReconcileResult{}
	if len(nextBytes) > MaxSnapshotBytes {
		res.state = StoreIndeterminateFailClosed
		return res, closedStoreErr(storeErrCapacity)
	}
	dir := filepath.Dir(target)
	suffix, err := generateTempSuffix(r)
	if err != nil {
		res.state = StoreIndeterminateFailClosed
		return res, closedStoreErr(storeErrEntropy)
	}
	tempPath := filepath.Join(dir, snapshotTempPrefix+suffix)
	f, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		res.state = StoreIndeterminateFailClosed
		return res, closedStoreErr(storeErrIndeterminate)
	}
	if _, err := writeFull(f, nextBytes); err != nil {
		f.Close()
		_ = os.Remove(tempPath)
		res.state = StoreNotCommitted
		return res, nil
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tempPath)
		res.state = StoreNotCommitted
		return res, nil
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tempPath)
		res.state = StoreNotCommitted
		return res, nil
	}
	res.tempPath = tempPath
	res.stagingBytes = nextBytes
	res.tempCreated = true

	// Strict canonical self-read of the temp before rename.
	selfRead, err := os.ReadFile(tempPath)
	if err != nil || !bytesEqual(selfRead, nextBytes) {
		res.selfReadOK = false
		// Target untouched (no rename yet) → proven old → NotCommitted.
		res.state = StoreNotCommitted
		return res, nil
	}
	res.selfReadOK = true

	// Best-effort rename; nil/error does NOT decide state. The seam allows fault
	// injection in tests (the rename outcome is non-deterministic by design, so
	// tests inject rename failure / torn-write to exercise the three states).
	_ = snapshotRenameFn(tempPath, target)

	// Exact read-back reconcile decides the three-state.
	readback, rerr := os.ReadFile(target)
	if rerr != nil {
		res.state = StoreIndeterminateFailClosed
		return res, nil
	}
	switch {
	case bytesEqual(readback, nextBytes):
		res.state = StoreCommitted
		res.cleanupFailed = cleanupTempResult(res.tempPath, res.stagingBytes, false)
	case bytesEqual(readback, oldBytes):
		res.state = StoreNotCommitted
		res.cleanupFailed = cleanupTempResult(res.tempPath, res.stagingBytes, false)
	default:
		res.state = StoreIndeterminateFailClosed
		// Indeterminate retains the temp as reconcile evidence (no cleanup).
	}
	return res, nil
}

// cleanupTempResult removes the exact temp if retain is false; returns true if
// cleanup was attempted but failed (so the caller records health). ENOENT and
// the retain case return false.
func cleanupTempResult(tempPath string, staging []byte, retain bool) bool {
	if retain || tempPath == "" {
		return false
	}
	if err := cleanupExactTemp(tempPath, staging); err != nil {
		return true
	}
	return false
}

// cleanupExactTemp removes the exact temp path created by one operation, but
// ONLY if it is regular/no-symlink, ≤1MiB, and byte-equal to the validated
// staging bytes. ENOENT is treated as already-clean. Any unsafe condition or
// Remove failure returns an error so the caller records health; it NEVER
// changes an already-classified mutation state.
func cleanupExactTemp(tempPath string, stagingBytes []byte) error {
	info, err := os.Lstat(tempPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("snapshot temp cleanup: unsafe temp (not regular)")
	}
	if info.Size() > MaxSnapshotBytes {
		return errors.New("snapshot temp cleanup: temp oversize")
	}
	content, err := os.ReadFile(tempPath)
	if err != nil {
		return err
	}
	if !bytesEqual(content, stagingBytes) {
		return errors.New("snapshot temp cleanup: content mismatch")
	}
	return os.Remove(tempPath)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ensure crypto/rand is referenced as the production default random source.
var _ = rand.Reader
