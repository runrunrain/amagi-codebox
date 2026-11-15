package sharedmcp

// atomic.go — 本包的原子私写原语。语义镜像 internal/fsutil.WritePrivateFile
//（同目录 tmp → write → sync → close → chmod 0600 → rename，Windows 目标
// 被占用时回退直接覆写），但不直接代理 fsutil：单测需要按精确步骤注入
// 故障验证原子性（spec §2.4「osWriteFile/rename var hook 注入失败 → 目标
// 文件字节不变」，sharedskills.renameDir 可覆写 var 先例），故原语以包级
// var 暴露。另提供保留既有 mode 的变体（Claude 写入器用：.claude.json 是
// 既有用户资产，不收紧其权限，spec §2.2）。

import (
	"os"
	"path/filepath"
	"runtime"
)

// 可覆写的文件系统原语（测试注入故障用；生产指向 os 实现）。
var (
	atomicCreateTemp = os.CreateTemp
	atomicRename     = os.Rename
	atomicWriteFile  = os.WriteFile
	atomicChmod      = os.Chmod
	atomicRemove     = os.Remove
)

// writeAtomicPrivate 以 0600 原子写 path（新建即 0600；有意收紧——登记/
// 状态/Pi/OpenCode/Codex 目标文件含密钥类值，spec §2.1 权限收紧声明）。
func writeAtomicPrivate(path string, data []byte) error {
	return writeAtomic(path, data, 0o600, false)
}

// writeAtomicPreservingMode 原子写 path 但保留目标既有权限（文件已存在时
// 沿用其 perm；不存在则 0600）。用于 .claude.json 等既有用户资产。
func writeAtomicPreservingMode(path string, data []byte) error {
	return writeAtomic(path, data, 0o600, true)
}

// writeAtomic 是原子写实现：同目录临时文件 → 写入+fsync+关闭 → chmod 目标
// 权限 → rename 替换（Windows rename 失败且目标被锁时回退直接覆写并
// chmod）。任一步失败即清理临时文件，目标文件保持原字节（原子性）。
func writeAtomic(path string, data []byte, perm os.FileMode, preserveExisting bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return NewErr(KindIO, "创建目录失败：%s（%v）", dir, err)
	}
	target := perm
	if preserveExisting {
		if info, err := os.Stat(path); err == nil {
			target = info.Mode().Perm()
		}
	}
	tmp, err := atomicCreateTemp(dir, "."+filepath.Base(path)+".sharedmcp-*")
	if err != nil {
		return NewErr(KindIO, "创建临时文件失败：%s（%v）", dir, err)
	}
	tmpPath := tmp.Name()
	closed := false
	cleanup := true
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if cleanup {
			_ = atomicRemove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return NewErr(KindIO, "写临时文件失败：%s（%v）", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		return NewErr(KindIO, "同步临时文件失败：%s（%v）", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return NewErr(KindIO, "关闭临时文件失败：%s（%v）", tmpPath, err)
	}
	closed = true
	if err := atomicChmod(tmpPath, target); err != nil {
		return NewErr(KindIO, "设置临时文件权限失败：%s（%v）", tmpPath, err)
	}
	if err := atomicRename(tmpPath, path); err != nil {
		// Windows 上目标被外部句柄锁定时 rename 失败：回退直接覆写（fsutil
		// 先例），失败则目标未被触碰，如实报错。
		if runtime.GOOS == "windows" {
			if werr := atomicWriteFile(path, data, target); werr == nil {
				_ = atomicChmod(path, target)
				cleanup = false
				return nil
			}
		}
		return NewErr(KindIO, "原子替换文件失败：%s（%v）", path, err)
	}
	cleanup = false
	return nil
}
