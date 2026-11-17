//go:build !windows

package claudeaccounts

import "syscall"

// statInode 返回文件的 inode 号（用于文件 revision 辅助判定）。
func statInode(info osFileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
