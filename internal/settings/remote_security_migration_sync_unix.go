//go:build !windows

// Package settings — syncParentDir for non-Windows platforms.
//
// This is a best-effort directory-entry flush (open dir fd + fsync). It only
// improves durability EVIDENCE; it is NEVER a commit condition for the
// migration state machine (the authority is the candidate Sync + byte readback
// classification). No physical-atomicity is claimed; power-loss/controller-cache
// durability remains unknown.

package settings

import "os"

func syncParentDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
