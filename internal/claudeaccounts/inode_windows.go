//go:build windows

package claudeaccounts

// statInode 在 Windows 上无 inode，返回 0（mtime/size/hash 仍可用）。
func statInode(info osFileInfo) uint64 { return 0 }
