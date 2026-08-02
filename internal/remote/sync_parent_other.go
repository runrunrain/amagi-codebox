//go:build !darwin && !windows

package remote

import (
	"os"
	"syscall"
)

// syncParentDir performs a best-effort directory-entry flush on other Unix-like
// platforms. It is a build-tag capability, NOT a security authority: it only
// improves durability evidence and is never a commit condition for the
// revocation ledger.
func syncParentDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// tempOwnerOK reports whether a temp file is owned by the current (effective)
// user.
func tempOwnerOK(info os.FileInfo) bool {
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return sys.Uid == uint32(os.Getuid())
}
