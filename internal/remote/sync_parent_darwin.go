//go:build darwin

package remote

import (
	"os"
	"syscall"
)

// syncParentDir performs a best-effort directory-entry flush after a snapshot
// rename on Darwin. It only improves durability EVIDENCE; it is never a commit
// condition for the revocation ledger (the revoke authority is the ledger
// commit File.Sync, never a rename or parent sync). Darwin fsync on a directory
// fd flushes the volume buffer for directory entries to the extent the OS/hardware
// honors it; power-loss/controller-cache durability remains unknown.
func syncParentDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// tempOwnerOK reports whether a temp file is owned by the current (effective)
// user. Defense-in-depth against another user's file in the store directory.
func tempOwnerOK(info os.FileInfo) bool {
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return sys.Uid == uint32(os.Getuid())
}
