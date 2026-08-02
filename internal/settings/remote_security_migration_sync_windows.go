//go:build windows

// Package settings — syncParentDir for Windows.
//
// os.Rename is NOT atomic on Windows and Microsoft has not proven directory-
// entry power-loss atomicity. This no-op NEVER claims parity with POSIX
// fsync-on-directory and is NEVER a commit condition for the migration state
// machine. The authority is the candidate Sync (FlushFileBuffers) + byte
// readback classification. Classification is by byte readback ONLY on Windows.

package settings

// syncParentDir is a no-op on Windows: directory fsync is not a proven
// durability mechanism, and it is never a commit condition for the migration
// state machine.
func syncParentDir(_ string) error {
	return nil
}
