//go:build !windows

package platform

import "os"

// SyncConfigDirectory fsyncs a directory so a just-renamed/removed file inside
// it is durable before the caller reports success. On non-Windows platforms a
// plain open+Sync is sufficient.
func SyncConfigDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
