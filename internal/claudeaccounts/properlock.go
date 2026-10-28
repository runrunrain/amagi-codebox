package claudeaccounts

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ProperLock 是与 Pi proper-lockfile 兼容的原生短写锁（design §4.3）：
// 锁形态为目标文件旁的 `<file>.lock` 目录 + 目录内 mtime 文件（内容为
// 时间戳），realpath=false。获取 = mkdir（已存在即 busy）；释放 = 校验
// mtime 内容后递归删除锁目录。
//
// 【待实测】该兼容实现与 Pi 安装版本的竞争测试属于 §10.2 真机验证项；
// v1 不自动清理未知/过期原生锁（design §4.3）：发现锁目录一律 busy。
type ProperLock struct {
	dir      string
	mtimePath string
	token    string
}

// AcquireProperLock 尝试获取 targetFilePath 的兼容锁。
func AcquireProperLock(targetFilePath string, now time.Time) (*ProperLock, error) {
	dir := targetFilePath + ".lock"
	if err := os.Mkdir(dir, 0o755); err != nil {
		if os.IsExist(err) {
			return nil, NewErr(ReasonNativeLockBusy, "原生写锁被占用（存在 %s）", filepath.Base(dir))
		}
		return nil, NewErr(ReasonNativeLockBusy, "原生写锁目录不可创建")
	}
	token := strconv.FormatInt(now.UnixMilli(), 10)
	mtimePath := filepath.Join(dir, "mtime")
	// mtime 文件内容与 proper-lockfile 同形态：十进制毫秒时间戳。
	if err := os.WriteFile(mtimePath, []byte(token), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return nil, NewErr(ReasonNativeLockBusy, "原生写锁 mtime 文件不可写")
	}
	return &ProperLock{dir: dir, mtimePath: mtimePath, token: token}, nil
}

// Release 释放锁：只有 mtime 文件内容仍为自己写入的 token 时才删除，
// 防止误删他人后来获取的锁；被接管时不删除并返回错误。
func (l *ProperLock) Release() error {
	if l == nil {
		return nil
	}
	data, err := os.ReadFile(l.mtimePath)
	if err != nil {
		// mtime 文件缺失/不可读：锁目录可能已被外部接管，不再删除。
		return NewErr(ReasonNativeLockBusy, "原生写锁 mtime 文件不可读，不删除锁目录")
	}
	if string(data) != l.token {
		return NewErr(ReasonNativeLockBusy, "原生写锁已被外部接管，不删除锁目录")
	}
	return os.RemoveAll(l.dir)
}
