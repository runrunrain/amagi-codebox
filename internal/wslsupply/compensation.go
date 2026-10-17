package wslsupply

// compensation.go — 普通文件写入的条件撤销记录（design §七「桌面与远程执行
// 边界」：效果的 Abort 只撤销本次产生且当前 hash 仍匹配的普通文件写入；A2
// 凭据刷新是持久认证状态，不随 PTY 启动失败回滚成旧 token）。

import (
	"path/filepath"
	"strings"
)

// CompensationHandle 是一次已提交普通文件写入的撤销能力（不透明句柄，仅
// Coordinator.Compensate 消费）。持有前像字节（仅内存，进程退出即弃）；
// sensitive 记录本次写入的敏感度，撤销恢复前像时沿用同一清理策略（M2）。
// S3 技能目录提交扩展：isDir=true 时 writtenTreeHash 是写入树 hash、
// prevFiles 是前像树（超限/不可核验未快照 → prevSnapshot=false，撤销按
// unavailable 处置——诚实标注而非假装可恢复）。
type CompensationHandle struct {
	unit        UnitID
	target      TargetRef
	relPath     string
	uncPath     string
	linuxPath   string
	writtenHash string
	prev        []byte
	prevExisted bool
	sensitive   bool

	// 技能/资源目录专用（S3；一致性修正：与资源统一真实持久前像生命周期）。
	isDir           bool
	writtenTreeHash string
	prevTreeHash    string // 提交时验证过的基准树 hash（恢复后复验用）
	prevFiles       []skillPreimageEntry
	prevSnapshot    bool

	// 持久操作事实（攻坚契约）：OpID 关联 intent/settled；backup/staging/
	// displaced 是目标根下相对路径（恢复材料持久位置）。内存正文仅缓存。
	opID        string
	targetKey   string
	backupRel   string
	stagingRel  string
	displacedRel string
}

// HasUndo 报告句柄是否携带可撤销写入。
func (h *CompensationHandle) HasUndo() bool { return h != nil }

// 补偿处置分类。
const (
	// CompensationConfirmed：撤销完成（或无需撤销）。
	CompensationConfirmed = "confirmed"
	// CompensationSkipped：目标已变化，外部内容保留（CAS 失配）。
	CompensationSkipped = "skipped-target-changed"
	// CompensationUnavailable：撤销尝试失败（保留为待核实债务）。
	CompensationUnavailable = "unavailable"
	// CompensationIndeterminate：目标不可读，无法判定。
	CompensationIndeterminate = "indeterminate"
)

// CompensationOutcome 是单个撤销记录的处置结果（脱敏：不含内容）。
type CompensationOutcome struct {
	Unit        UnitID
	RelPath     string
	Disposition string
	Message     string
}

// newCompensationHandle 由 submitFile 构造（ coordinator 内部使用）。
func newCompensationHandle(target TargetRef, relPath string, content, prev []byte, prevExisted, sensitive bool) *CompensationHandle {
	uncPath := filepath.Join(target.UNCRoot, filepath.FromSlash(relPath))
	linuxPath := target.LinuxRoot + "/" + strings.TrimPrefix(relPath, "/")
	return &CompensationHandle{
		target:      target,
		relPath:     relPath,
		uncPath:     uncPath,
		linuxPath:   linuxPath,
		writtenHash: hexHash(content),
		prev:        prev,
		prevExisted: prevExisted,
		sensitive:   sensitive,
	}
}
