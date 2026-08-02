//go:build windows

package remote

import "os"

// syncParentDir is UNSUPPORTED as a proven durability mechanism on Windows. Go
// documents that os.Rename is NOT atomic on Windows, and Microsoft has not
// proven MoveFileEx/ReplaceFile or directory-entry power-loss atomicity. This
// no-op therefore NEVER claims parity with POSIX fsync-on-directory, and it is
// NEVER a commit condition for the revocation ledger. The revoke authority is
// the ledger commit File.Sync (FlushFileBuffers API contract) only; physical
// power-loss/controller-cache durability remains unknown.
func syncParentDir(dir string) error {
	_ = dir
	return nil
}

// tempOwnerOK is a no-op on Windows: there is no portable uid model matching
// the POSIX check, and the store directory is 0700 user-owned (defense-in-depth
// only). The critical temp-safety checks (no-follow Lstat, regular, no-symlink,
// ≤1MiB, strict-valid content with matching StoreID) remain enforced.
func tempOwnerOK(info os.FileInfo) bool {
	_ = info
	return true
}
