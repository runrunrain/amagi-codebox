package claudeaccounts

import "os"

// osFileInfo 是 statInode 的平台无关签名（避免在平台文件重复导入 io/fs）。
type osFileInfo = os.FileInfo
