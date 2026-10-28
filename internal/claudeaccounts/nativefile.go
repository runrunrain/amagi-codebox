package claudeaccounts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"amagi-codebox/internal/platform"
)

// canonicalJSON 返回投影的规范（compact）形态。池 envelope 经
// encoding/json 往返后 RawMessage 会被 compact 化，因此所有跨存储边界
// 的投影比较与入库统一采用 compact 形态，避免格式差异被误判为内容变化。
func canonicalJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return append([]byte(nil), raw...)
	}
	return buf.Bytes()
}

// equalJSON 语义比较两段 JSON 投影（规范形态后字节比较）。
func equalJSON(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	ca, cb := canonicalJSON(a), canonicalJSON(b)
	return bytes.Equal(ca, cb)
}

// readStableAttempts 是 ReadStable 的有限重试次数（design §4.3）。
const readStableAttempts = 3

// readStableFile 按 lstat→读→stat→hash→重读读取一个稳定文件快照：
// 拒绝符号链接与非普通文件；两次读取内容不一致视为写入者未静止，重试。
// 返回字节内容与整文件 revision（受管字段 hash 由调用方补充）。
func readStableFile(path string) ([]byte, FileRevision, error) {
	var lastErr error
	for attempt := 0; attempt < readStableAttempts; attempt++ {
		linfo, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, FileRevision{}, NewErr(ReasonNeedsReconcile, "原生文件缺失（%s）", filepath.Base(path))
			}
			return nil, FileRevision{}, NewErr(ReasonNeedsReconcile, "原生文件不可访问（%s）", filepath.Base(path))
		}
		if linfo.Mode()&os.ModeSymlink != 0 {
			return nil, FileRevision{}, NewErr(ReasonNeedsReconcile, "原生文件是符号链接，拒绝读取（%s）", filepath.Base(path))
		}
		if !linfo.Mode().IsRegular() {
			return nil, FileRevision{}, NewErr(ReasonNeedsReconcile, "原生位置不是普通文件（%s）", filepath.Base(path))
		}
		first, err := os.ReadFile(path)
		if err != nil {
			lastErr = NewErr(ReasonNeedsReconcile, "原生文件读取失败（%s）", filepath.Base(path))
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			lastErr = NewErr(ReasonNeedsReconcile, "原生文件在读取中变化（%s）", filepath.Base(path))
			continue
		}
		second, err := os.ReadFile(path)
		if err != nil {
			lastErr = NewErr(ReasonNeedsReconcile, "原生文件二次读取失败（%s）", filepath.Base(path))
			continue
		}
		if HashRevision(first) != HashRevision(second) {
			lastErr = NewErr(ReasonNeedsReconcile, "原生文件在读取期间被并发写入（%s）", filepath.Base(path))
			continue
		}
		return second, FileRevision{
			SHA256Hex: HashRevision(second),
			MtimeNS:   info.ModTime().UnixNano(),
			Ino:       statInode(info),
			Size:      info.Size(),
		}, nil
	}
	if lastErr == nil {
		lastErr = NewErr(ReasonNeedsReconcile, "原生文件读取不稳定（%s）", filepath.Base(path))
	}
	return nil, FileRevision{}, lastErr
}

// fileRevisionWithManaged 在整文件 revision 上补充受管字段 hash。
func fileRevisionWithManaged(rev FileRevision, managed []byte) FileRevision {
	rev.ManagedHash = HashRevision(managed)
	return rev
}

// AtomicWriteResult 记录原子写的结果事实（design §4.4：记录 renamed=true
// 与目录 sync 失败的区别，恢复不得假定失败表示未改文件）。
type AtomicWriteResult struct {
	// Renamed 为 true 表示 rename 已完成，目标路径内容已被替换。
	Renamed bool
	// Step 记录失败（或成功）发生的步骤：create-temp/chmod-temp/write-temp/
	// sync-temp/close-temp/rename/sync-directory。
	Step string
}

// writeFileOps 抽象原子写的文件系统原语，供测试按精确步骤注入故障
// （design §10.1：故障注入用精确步骤，不用 sleep 猜并发）。
type writeFileOps interface {
	CreateTemp(dir, pattern string) (*os.File, error)
	Sync(f *os.File) error
	Close(f *os.File) error
	Rename(oldPath, newPath string) error
	Remove(path string) error
	SyncDir(dir string) error
	Chmod(path string, mode os.FileMode) error
}

type osWriteFileOps struct{}

func (osWriteFileOps) CreateTemp(dir, pattern string) (*os.File, error) {
	return os.CreateTemp(dir, pattern)
}
func (osWriteFileOps) Sync(f *os.File) error { return f.Sync() }
func (osWriteFileOps) Close(f *os.File) error { return f.Close() }
func (osWriteFileOps) Rename(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }
func (osWriteFileOps) Remove(path string) error { return os.Remove(path) }
func (osWriteFileOps) SyncDir(dir string) error { return platform.SyncConfigDirectory(dir) }
func (osWriteFileOps) Chmod(path string, mode os.FileMode) error { return os.Chmod(path, mode) }

// writeAtomicFile 同目录临时文件 0600→write→fsync→close→rename→父目录
// fsync（design §4.4，语义参考 launch_executor.go:593-643）。不对已存在
// 目录做 chmod，绝不对 HOME 做 chmod 0700；只有新建的专用子目录用 0700。
// 失败时清理未 rename 的临时文件；崩溃残留仅在确认属于本次事务后清理
// （见 service 恢复流程），本函数不管辖。
func writeAtomicFile(path string, content []byte, ops writeFileOps) (AtomicWriteResult, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return AtomicWriteResult{Step: "mkdir"}, NewErr(ReasonNeedsReconcile, "原生文件目录不可用")
	}
	// 记录原文件 mode（若存在）：rename 后恢复，避免无谓改变他人文件的
	// 权限语义；新文件保持 0600。
	origMode := os.FileMode(0)
	if info, err := os.Stat(path); err == nil {
		origMode = info.Mode().Perm()
	}
	tmp, err := ops.CreateTemp(dir, "."+filepath.Base(path)+".claudeaccounts-*")
	if err != nil {
		return AtomicWriteResult{Step: "create-temp"}, NewErr(ReasonNeedsReconcile, "临时文件创建失败")
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = ops.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return AtomicWriteResult{Step: "chmod-temp"}, NewErr(ReasonNeedsReconcile, "临时文件权限设置失败")
	}
	if _, err := tmp.Write(content); err != nil {
		return AtomicWriteResult{Step: "write-temp"}, NewErr(ReasonNeedsReconcile, "临时文件写入失败")
	}
	if err := ops.Sync(tmp); err != nil {
		return AtomicWriteResult{Step: "sync-temp"}, NewErr(ReasonNeedsReconcile, "临时文件 fsync 失败")
	}
	if err := ops.Close(tmp); err != nil {
		closed = true
		return AtomicWriteResult{Step: "close-temp"}, NewErr(ReasonNeedsReconcile, "临时文件关闭失败")
	}
	closed = true
	if err := ops.Rename(tmpPath, path); err != nil {
		return AtomicWriteResult{Step: "rename"}, NewErr(ReasonNeedsReconcile, "原子替换失败")
	}
	if origMode != 0 && origMode.Perm() != 0o600 {
		if err := ops.Chmod(path, origMode.Perm()); err != nil {
			return AtomicWriteResult{Renamed: true, Step: "chmod-target"}, NewErr(ReasonNeedsReconcile, "目标文件权限恢复失败（文件已替换）")
		}
	}
	if err := ops.SyncDir(dir); err != nil {
		// rename 已完成：文件已变，目录未落盘。区别记录，不回滚。
		return AtomicWriteResult{Renamed: true, Step: "sync-directory"}, NewErr(ReasonNeedsReconcile, "目录 fsync 失败（文件已替换）")
	}
	return AtomicWriteResult{Renamed: true, Step: "sync-directory"}, nil
}

// sjsonSetRawBytes 用 sjson 做字段级 setRaw，保留其余字节片段。
func sjsonSetRawBytes(jsonBytes []byte, path string, value []byte) ([]byte, error) {
	return sjson.SetRawBytes(jsonBytes, path, value)
}

// topKeyNames 列出 JSON 对象的顶层键名（排序稳定），用于「非拥有字段未
// 损坏」校验：只比较键名集合与值，不复制值出存储边界。
func topKeyNames(data []byte) []string {
	root := gjson.ParseBytes(data)
	if !root.IsObject() {
		return nil
	}
	names := make([]string, 0, len(root.Map()))
	for name := range root.Map() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sameStringSet 报告两个有序键名切片是否为同一集合。
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// validateJSONBytes 校验字节是合法 JSON（design §4.3：拒绝非法 JSON）。
func validateJSONBytes(data []byte, what string) error {
	if !gjson.ValidBytes(data) {
		return NewErr(ReasonUnsupportedCredentialSchema, "%s 不是合法 JSON", what)
	}
	return nil
}

// ctxShort 检查 ctx 已取消（用于长事务各阶段入口；取消不抹除已写事实，
// 由调用方 journal 处理，design §10.1 swap/reconcile 行）。
func ctxShort(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return NewErr(ReasonNeedsReconcile, "操作已取消（已写入内容以回读为准）: %v", ctxErrString(err))
	}
	return nil
}

func ctxErrString(err error) string { return fmt.Sprint(err) }
