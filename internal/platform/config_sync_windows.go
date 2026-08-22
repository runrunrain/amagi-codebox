//go:build windows

package platform

import "golang.org/x/sys/windows"

// SyncConfigDirectory fsyncs a directory so a just-renamed/removed file inside
// it is durable before the caller reports success. It opens the directory via
// CreateFile with FILE_FLAG_BACKUP_SEMANTICS (directories need that flag) and
// flushes it with FlushFileBuffers.
func SyncConfigDirectory(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.FlushFileBuffers(handle)
}
