package wslsupply

// operation.go — 攻坚契约的持久操作事实模型（attack-contract「持久事实与
// 状态接口」）：每次不可逆写前先落一个不可变、可重启读取的 OperationIntent；
// 结果消费 SettledOperations + 内存 OperationObservation；GetStatus 与启动
// 资格消费同一事实。intent 不含任何正文/秘密（A2 另有无 hash 摘要）。

import (
	"path/filepath"
	"strings"
	"time"
)

// OpKind 是操作的类别标签（持久在 intent 里，供重启后分类处置）。
type OpKind string

const (
	// OpKindWrite：普通/敏感文件写（替换或创建）。
	OpKindWrite OpKind = "write"
	// OpKindRemove：撤回移除（消费路径删除/隔离）。
	OpKindRemove OpKind = "remove"
	// OpKindDirSwap：目录树交换（skills/resources 目录单元）。
	OpKindDirSwap OpKind = "dir-swap"
	// OpKindCompensate：补偿（ParentOpID 指向被补偿的原操作）。
	OpKindCompensate OpKind = "compensate"
	// OpKindA2Sync：A2 登录凭据同步的待确认摘要（无 hash/token，仅目标与
	// 方向/阶段；重启重新真实判定，不按摘要重放）。
	OpKindA2Sync OpKind = "a2-sync"
	// OpKindPluginOp：插件原生 install/update/remove 的可能副作用摘要（无
	// 秘密；不自动授予管理权，不自动重放）。
	OpKindPluginOp OpKind = "plugin-op"
)

// OpEffect 是目标侧实际效果的分类（观察事实，不是愿望）。
type OpEffect string

const (
	// OpEffectNone：尚未对目标产生任何写入。
	OpEffectNone OpEffect = "none"
	// OpEffectCommitted：目标已是本次写入字节（已读回验证）。
	OpEffectCommitted OpEffect = "committed"
	// OpEffectUnknown：发生了不可判定的移动/发布（读回失败或状态未知）。
	OpEffectUnknown OpEffect = "unknown"
	// OpEffectRestored：目标已恢复为前像（读回验证）。
	OpEffectRestored OpEffect = "restored"
	// OpEffectRemoved：消费路径已确认 absent。
	OpEffectRemoved OpEffect = "removed"
)

// OpRegistration 是登记（主清单）侧的事实。
type OpRegistration string

const (
	// OpRegistrationPending：intent 在盘，主清单尚未确认本操作结论。
	OpRegistrationPending OpRegistration = "pending"
	// OpRegistrationConfirmed：主清单已保存本操作结论（settled）。
	OpRegistrationConfirmed OpRegistration = "confirmed"
)

// OpRecovery 是恢复材料的可用性观察。
type OpRecovery string

const (
	// OpRecoveryNone：无恢复需求。
	OpRecoveryNone OpRecovery = "none"
	// OpRecoveryAvailable：已验证的持久前像/prev 树可用。
	OpRecoveryAvailable OpRecovery = "available"
	// OpRecoveryConflict：外部版本/后改占据目标，不能自动覆盖。
	OpRecoveryConflict OpRecovery = "conflict"
	// OpRecoveryUnavailable：恢复材料损坏/不可读/权限不可证。
	OpRecoveryUnavailable OpRecovery = "unavailable"
)

// Consumption 是敏感单元的启动消费资格（默认零值空串 = 未判定，不得当作
// safe 消费；gate 对敏感单元必须显式拿到 safe/absent/user-owned 才放行）。
type Consumption string

const (
	// ConsumptionNotApplicable：非敏感单元，无消费资格语义。
	ConsumptionNotApplicable Consumption = "not-applicable"
	// ConsumptionAbsent：当前消费路径确认不存在该副本。
	ConsumptionAbsent Consumption = "absent"
	// ConsumptionSafe：副本受管且安全（如权限可证 0600）。
	ConsumptionSafe Consumption = "safe"
	// ConsumptionUnsafe：副本已知不安全（权限/撤回未确认）。
	ConsumptionUnsafe Consumption = "unsafe"
	// ConsumptionUnknown：无法判定（未知归属/不可读/未完成操作）。
	ConsumptionUnknown Consumption = "unknown"
	// ConsumptionUserOwned：副本归属用户（detached/手改保留）。
	ConsumptionUserOwned Consumption = "user-owned"
)

// OperationIntent 是一次操作的写前持久事实（不可变、不含正文）。落点：
// 配置目录 wsl-config-supply-pending/<opID>.json，私有 0600，唯一名，写入
// 后读回验证才可改变目标。路径字段全部是相对目标根的内部生成相对路径，
// 不接受客户端绝对路径。
type OperationIntent struct {
	Version     int       `json:"version"`
	OpID        string    `json:"opId"`
	Kind        OpKind    `json:"kind"`
	TargetKey   string    `json:"targetKey"` // 完整身份（generation+CLI 作用域；unknown 代带 distro 前缀）
	TargetDistro string   `json:"targetDistro"`
	TargetHome  string    `json:"targetHome,omitempty"`
	// TargetCLI 是目标 CLI 作用域（pi/shared/claude/codex）。同一 distro 身份的
	// 多个 CLI 目标根（如 .pi/agent 与 .agents）共享 generation 但 RelPath 各自
	// 相对各自根解释——reconcile/清理由它（或 Unit 前缀派生）验证落点归属，
	// 不得拿当前调用者的目标根解释其它作用域的 RelPath。
	TargetCLI   string    `json:"targetCli,omitempty"`
	Unit        UnitID    `json:"unit,omitempty"`
	RelPath     string    `json:"relPath,omitempty"`
	Sensitive   bool      `json:"sensitive,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	// 指纹（普通敏感单元按契约记录；A2 摘要恒空）。
	BeforeExists bool   `json:"beforeExists"`
	BeforeHash   string `json:"beforeHash,omitempty"`
	AfterHash    string `json:"afterHash,omitempty"`
	// 恢复材料（相对目标根；目录附结构指纹）。
	BackupRel    string `json:"backupRel,omitempty"`
	StagingRel   string `json:"stagingRel,omitempty"`
	DisplacedRel string `json:"displacedRel,omitempty"`
	PrevTreeHash string `json:"prevTreeHash,omitempty"`
	// ProposedEntry 是登记侧拟写基准（仅基准字段，不提供授权恢复能力）。
	ProposedEntry *StateEntry `json:"proposedEntry,omitempty"`
	ParentOpID    string      `json:"parentOpId,omitempty"`
	// Register 是 finalize 阶段在主清单 mutate 内执行的登记闭包（StateEntry
	// 与 SettledOperations 同次保存；nil = 外部提交只写 settled）。
	Register func(st *StateFile) `json:"-"`
	// PluginMeta：插件操作摘要（source/ref/filters，无秘密）。
	PluginMeta map[string]string `json:"pluginMeta,omitempty"`
}

// SettledOperation 是已确认操作结论（与对应 StateEntry 更新同次主清单保存；
// 作为终结墓碑保留到材料清理完成）。
type SettledOperation struct {
	TargetKey     string    `json:"targetKey"`
	Unit          UnitID    `json:"unit,omitempty"`
	Disposition   string    `json:"disposition"` // committed/restored/removed/not-applied/user-resolved
	RelatedOpIDs  []string  `json:"relatedOpIds,omitempty"`
	SettledAt     time.Time `json:"settledAt"`
	AfterHash     string    `json:"afterHash,omitempty"`
}

// OperationObservation 是进程内对一次操作的当前观察（独立于 durable 清单；
// 锁内深复制；重启由 intent+目标重建）。
type OperationObservation struct {
	OpID         string      `json:"opId"`
	TargetKey    string      `json:"targetKey,omitempty"`
	Unit         UnitID      `json:"unit,omitempty"`
	Effect       OpEffect    `json:"effect"`
	Registration OpRegistration `json:"registration"`
	Recovery     OpRecovery  `json:"recovery"`
	ErrorCode    string      `json:"errorCode,omitempty"`
	Detail       string      `json:"detail,omitempty"`
	At           time.Time   `json:"at"`
}

// CommitRequest 是统一文件/目录提交请求（applyProjection、文件 TakeOver、
// 资源文件、插件 settings 共用）。Unit 与候选登记必须写前已知。
type CommitRequest struct {
	Target         TargetRef
	Unit           UnitID
	RelPath        string
	ExpectedBefore string // 计划/预览时的目标 hash（空 = 预期 absent）
	Candidate      []byte
	// Register 是 finalize 阶段的登记闭包（写 StateEntry；nil = 外部提交，
	// 只写 settled）。与 settled 同次主清单保存。
	Register       func(st *StateFile)
	// PluginMeta：外部提交（插件 filters 等）的脱敏摘要元数据（无秘密）。
	PluginMeta map[string]string
	// SkipBeforeCAS：调用方已用自己的树 hash 形态完成 CAS（资源/技能目录
	// 单元），commitFile 跳过字节级 ExpectedBefore 复核。
	SkipBeforeCAS bool
	Sensitive      bool
	Kind           OpKind // 默认 write
	// Dir 交换（submitDirUnit 路径）。
	Dir        bool
	DirFiles   []skillWriteFile
	DirTreeHash string
	// RegisterExternal：不写 StateEntry 但仍写 settled（插件 settings 等）。
	RegisterExternal bool
}

// CommitReceipt 是提交生命周期的事实回执。error 非 nil 时 receipt 仍必须
// 保留（已发生副作用、intent 位置、恢复材料都在里面）。
type CommitReceipt struct {
	OpID         string
	TargetKey    string
	Unit         UnitID
	RelPath      string
	Kind         OpKind
	Effect       OpEffect
	Registration OpRegistration
	Recovery     OpRecovery
	BeforeExists bool
	BeforeHash   string
	AfterHash    string
	BackupRel    string
	StagingRel   string
	DisplacedRel string
	Err          error
	// ErrDetail 是结构化错误码（write-failed 等，供 UnitResult.Detail）。
	ErrDetail string
	// UndoLease：已确认提交且撤销租用仍在本进程（成功路径非 nil）。
	UndoLease *CompensationHandle
}

// pendingDirName 是 intent 存储目录名（配置目录下）。
const pendingDirName = "wsl-config-supply-pending"

// pendingIntentPath 返回某 opID 的 intent 落点。
func pendingIntentPath(configDir, opID string) string {
	return filepath.Join(configDir, pendingDirName, opID+".json")
}

// targetKeyOf 构造完整目标身份键：可验证代用 generation；unknown 代拼
// distro（避免所有 unknown 目标共享一个键位互相误伤）。CLI 作用域入键：
// 同一 distro 身份的多个 CLI 目标根（pi/.pi/agent、shared/.agents 等）共享
// generation——不带作用域的键会把 shared 的未决操作错投给 pi 的目标根解释
//（RelPath 各自相对各自根），已证实会造成「pi reconcile 把 shared intent 判
// not-applied 并清未决」的错位。
func targetKeyOf(t TargetRef) string {
	scope := string(t.CLI)
	if t.Generation != "" && t.Generation != GenerationUnknown {
		return t.Generation + "|" + scope
	}
	return "unknown:" + t.Distro + "|" + scope
}

// cliScopeOfUnit 从单元前缀派生 CLI 作用域（旧/不完整 intent 的归属验证
// 兜底；TargetCLI 字段优先）。
func cliScopeOfUnit(u UnitID) CLI {
	switch {
	case strings.HasPrefix(string(u), "pi/"):
		return CLIPi
	case strings.HasPrefix(string(u), "shared/"):
		return CLICommon
	case strings.HasPrefix(string(u), "claude/"):
		return CLIClaude
	case strings.HasPrefix(string(u), "codex/"):
		return CLICodex
	}
	return ""
}

// intentScope 返回 intent 声明或可派生的 CLI 作用域（空 = 不可判）。
func intentScope(op OperationIntent) CLI {
	if op.TargetCLI != "" {
		return CLI(op.TargetCLI)
	}
	return cliScopeOfUnit(op.Unit)
}

// newOpID 生成唯一操作 ID（时间有序 + 随机后缀）。
func newOpID() string {
	return formatOpID(time.Now())
}
