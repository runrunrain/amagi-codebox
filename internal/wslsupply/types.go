// Package wslsupply — WSL agent 配置供给协调器（P3-S0 最小骨架）。
//
// 责任边界（design p3-config-supply §七组件责任表）：只做策略、清单、计划、
// 状态与调度骨架；不导入 App/Wails/插件 UI；不决定模型业务语义、不解析 OAuth
// token。S0 的 Plan/Apply 单元范围只有一类：A2 凭据（auth.json 经 A2Auth 策略
// 适配器，判定算法与安全写由 internal/launcher 承担，语义零变化）；普通配置的
// TrackedProjection 单元只留接缝与测试桩，不实现分发（S1+）。
//
// 执行边界统一：桌面启动闸门与 remote/v1 effect 共用本协调器（RunUnits），
// 字节走 UNC 不进 wsl.exe 命令行；remote 链路 BuildPlan 保持纯读，计划只携带
// 待供给描述，Inspect/Plan/Apply 全部在 effect 的 Apply 阶段执行。
package wslsupply

import (
	"errors"
	"time"

	"amagi-codebox/internal/launcher"
)

// CLI 是供给目标的 CLI 标识。S0 只有 pi；S1 增加共享作用域。
type CLI string

// CLIPi 是 Pi（登录凭据 A2 + 托管 models.json 分发）。
const CLIPi CLI = "pi"

// CLICommon 是共享 ~/.agents 小文件的作用域（provider 无关的跨 CLI 资产：
// 视觉清单、多模态学习层）。
const CLICommon CLI = "shared"

// CLIClaude 是 Claude Code 用户配置导入作用域（P3-S4）：目标根是 distro
// 用户 HOME 本身——因为用户级 MCP 定义在 ~/.claude.json（HOME 根文件），
// 其余资源在 ~/.claude/ 子树。相对路径均以「.claude*」前缀表达，绝不全树
// 探测（只 stat 已注册单元文件）。
const CLIClaude CLI = "claude"

// CLICodex 是 Codex 用户配置导入作用域（P3-S4）：目标根 ~/.codex（config.toml
// + AGENTS.md + skills/）。
const CLICodex CLI = "codex"

// cliTargetRoot 返回 CLI 在 distro 用户 Home 下的目标根相对路径（空串 = 根
// 即 HOME）。未知 CLI 返回错误（SupplyRequest 拒绝 unknown CLI，design §七）。
func cliTargetRoot(cli CLI) (string, bool) {
	switch cli {
	case CLIPi:
		return ".pi/agent", true
	case CLICommon:
		return ".agents", true
	case CLIClaude:
		return "", true
	case CLICodex:
		return ".codex", true
	default:
		return "", false
	}
}

// GenerationUnknown 表示拿不到稳定 distro 身份标识时的 generation 值
// （design §七：按未知处理，不猜测继承）。
const GenerationUnknown = "unknown"

// UnitID 是最小登记单元标识（CLI 作用域内唯一）。
type UnitID string

// UnitPiA2Auth 是 Pi 登录凭据（auth.json）的 A2 双向新鲜度单元——唯一自动
// 「双向新者为准」对象（design §三归属策略表）。
const UnitPiA2Auth UnitID = "pi/a2-auth"

// S1 托管面单元（design §四/§八批次 S1）：
const (
	// UnitPiManagedModels：distro models.json 的 amagi-* provider 期望集 +
	// 登录预设 contextWindow 窄字段托管（ManagedNamespace）。
	UnitPiManagedModels UnitID = "pi/managed-models"
	// UnitSharedVisionExport：~/.agents/amagi-media-models.json 单向受管导出
	//（内嵌 api_key，per-target 首次发布须明确同意）。
	UnitSharedVisionExport UnitID = "shared/vision-export"
	// UnitSharedModalityKB：~/.agents/amagi-modalities.json 按规范化模型 ID
	// 补缺（不加害 distro 既有结论，不反写宿主）。
	UnitSharedModalityKB UnitID = "shared/modality-kb"
)

// S4 用户配置导入单元（design §四 Claude Code 表与 Codex 表：server 级/字段级
// 白名单投影，需选择 + per-target 同意，未知字段 LocalOnly）。
const (
	// UnitClaudeMCP：~/.claude.json 的 mcpServers/<name> server 级投影（仅用户
	// 选择的 server；projects/trust/onboarding/账户状态/未知键绝不跨界）。
	UnitClaudeMCP UnitID = "claude/mcp"
	// UnitClaudeSettings：~/.claude/settings.json 字段级白名单偏好投影。
	UnitClaudeSettings UnitID = "claude/settings"
	// UnitCodexConfig：~/.codex/config.toml 的 mcp_servers/<name> 与选中的
	// profiles/<name> TOML 节投影（保真编辑：非目标节与注释保留）。
	UnitCodexConfig UnitID = "codex/config"
)

// Policy 是单元归属策略名（design §三）。
type Policy string

const (
	// PolicyA2Auth：Pi auth.json 专属，沿用 A2 双向 hash+mtime。
	PolicyA2Auth Policy = "A2Auth"
	// PolicyTrackedProjection：普通配置投影（S0 仅接缝与测试桩，S1+ 分发）。
	PolicyTrackedProjection Policy = "TrackedProjection"
	// PolicyManagedNamespace：CodeBox 拥有的命名空间托管（amagi-* provider、
	// 已声明的托管文件）——从业务源生成、保留域外字段、按既有所有权更新，
	// mtime 不决定方向（design §三归属策略表）。
	PolicyManagedNamespace Policy = "ManagedNamespace"
)

// ErrTargetCorrupt 表示目标文件存在但未通过严格语法/schema 校验（两级损坏
// 保护的第一手信号）：适配器在 Candidate 阶段包装本哨兵，协调器据此给出
// target-corrupt 明细，调用方（启动闸门）可区分「损坏不覆盖」与普通写失败。
var ErrTargetCorrupt = errors.New("wsl supply target file is corrupt")

// Reason 触发供给的原因（design §三时机）。
type Reason string

const (
	ReasonLaunch  Reason = "launch"
	ReasonSave    Reason = "save"
	ReasonManual  Reason = "manual"
	ReasonInstall Reason = "install"
)

// TargetRef 描述一个确定的 WSL 供给目标（design §七 TargetRef 契约）：
// 来自最终 WSL 执行目标（初版默认 distro，无新增选择器）；generation 由可取得
// 的 distro 身份标识与 UID/Home 组成，拿不到稳定身份按 unknown 处理。
type TargetRef struct {
	Distro     string // wsl.exe -d 目标
	CLI        CLI
	UID        string // distro 默认用户 uid；探测不可用为 ""
	Home       string // distro 默认用户 $HOME（Linux 绝对路径）
	LinuxRoot  string // 目标根（Linux 路径，如 /home/u/.pi/agent）
	UNCRoot    string // 目标根（Windows UNC，字节提交通道）
	Generation string // 身份代；GenerationUnknown 表示未知
	// MachineID 是 distro 稳定身份标识（构造 launcher 完整目标用；探测不可得为空）。
	MachineID string
	// LegacyGeneration 是同一次 Identity 探测导出的旧公式指纹
	//（sha256(machine-id|uid|home)，无 distro）：v1 逐目标核验用；不重新走
	// cached Home 探测；machine-id 缺失时为 GenerationUnknown。
	LegacyGeneration string
}

// SameIdentity 报告两个目标是否指向同一身份面（distro+uid+home+root+代）。
// Apply 前用它复核目标未被替换（拒绝旧计划）。Generation 是完整身份指纹
// （含 distro 名与 machine-id/uid/home，见 generationFor）：计划后同名 distro
// 被重装/克隆/换默认用户，即使 uid/home/根路径全同，代不同也必须拒绝旧计划
// （SA-01：旧环境的授权不得把用户设置/敏感 MCP 导入新身份）。
func (t TargetRef) SameIdentity(other TargetRef) bool {
	return t.Distro == other.Distro && t.UID == other.UID && t.Home == other.Home &&
		t.LinuxRoot == other.LinuxRoot && t.CLI == other.CLI && t.Generation == other.Generation
}

// FileMeta 是 Inspect 批量探测得到的单文件元数据。ModTime 为 UNC 视图呈现值，
// 仅作展示与普通单元辅助信息；A2 新陈判定的权威 mtime 仍在适配器内经 distro
// 内 stat 读取。Hash 仅对普通（TrackedProjection）单元文件计算且只存在干
// 内存快照；A2 凭据文件恒空——凭据 hash 不进快照、计划、状态或任何 API。
type FileMeta struct {
	RelPath string
	Exists  bool
	Size    int64
	ModTime time.Time
	Hash    string
}

// Snapshot 是一次批量只读探测的结果（design §七 Inspect 契约：可启动只读探测，
// 不写；供显式检查/执行阶段使用，不放 BuildPlan）。
type Snapshot struct {
	Target      TargetRef
	Files       []FileMeta
	InspectedAt time.Time
}

// File 查询单元文件元数据（未探测到返回零值）。
func (s *Snapshot) File(relPath string) (FileMeta, bool) {
	for i := range s.Files {
		if s.Files[i].RelPath == relPath {
			return s.Files[i], true
		}
	}
	return FileMeta{}, false
}

// Action 是计划阶段的单元动作（design §七 Plan 契约：纯计算产物）。
type Action uint8

const (
	// ActionNone：无需动作（快照与登记一致或单元未选中）。
	ActionNone Action = iota
	// ActionSyncA2：A2 适配器同步；新陈判定在适配器 Apply 时进行（S0 唯一
	// 生产单元动作，判定语义零变化）。
	ActionSyncA2
	// ActionWriteProjection：TrackedProjection 写入（S0 仅测试桩可达）。
	ActionWriteProjection
	// ActionBlocked：阻断项，原因见 BlockedReason。
	ActionBlocked
	// ActionRemoveProjection：撤回——移除上次受管写入（SA-09：宿主侧已删除
	// 敏感导出源且目标副本仍等于上次写入时，删除目标副本而不是留着旧密钥）。
	ActionRemoveProjection
)

// PlannedUnit 是计划内单个单元的动作描述。计划不携带候选正文（敏感候选仅在
// Apply 阶段内存中存在，design §七 Plan 契约）。
type PlannedUnit struct {
	Unit             UnitID
	Policy           Policy
	Action           Action
	BlockedReason    string
	TransformVersion string
	// Freeze 为 true 时表示本单元因目标侧本地修改/存在而进入冻结态，Apply
	// 阶段把冻结原因记入状态清单（GetStatus 供 UI 显示「冻结」；ManagedNamespace
	// 单元恒 false——命名空间内不存在冻结条件）。
	Freeze bool
	// RelPath 是投影单元的目标相对路径（A2 单元为空，路径由适配器固定）。
	RelPath string
	// TrackedProjection 单元（含测试桩）的预期目标 hash（hex）与候选字节数；
	// A2 单元恒为空——凭据 hash 不进计划、不进状态、不进任何 API。
	ExpectedTargetHash string
	CandidateSize      int64
}

// SupplyRequest 是一次供给请求（design §七：不接受前端任意绝对目标路径；
// unknown CLI/单元拒绝）。
type SupplyRequest struct {
	Target         TargetRef
	Reason         Reason
	SourceRevision uint64
	SelectedUnits  []UnitID
	RequiredUnits  []UnitID
}

// SupplyPlan 是 Plan(request, snapshot) 的纯计算产物。
type SupplyPlan struct {
	Request SupplyRequest
	Units   []PlannedUnit
}

// UnitOutcome 是 Apply 阶段单元结果分类（供启动闸门/状态页消费）。
type UnitOutcome uint8

const (
	// UnitApplied：已写入/已同步成功。
	UnitApplied UnitOutcome = iota
	// UnitFresh：无需动作（内容一致）。
	UnitFresh
	// UnitWarnDegraded：降级放行（A2 DistroHasCopy：distro 已有副本，可能是
	// 旧凭据；不宣称凭据有效）。
	UnitWarnDegraded
	// UnitFailed：失败（含 fail-closed 分类；Detail/Err 不含凭据内容）。
	UnitFailed
	// UnitBlocked：计划期阻断（目标不可解析/单元不可用）。
	UnitBlocked
)

func (o UnitOutcome) String() string {
	switch o {
	case UnitApplied:
		return "applied"
	case UnitFresh:
		return "fresh"
	case UnitWarnDegraded:
		return "warn-degraded"
	case UnitFailed:
		return "failed"
	case UnitBlocked:
		return "blocked"
	default:
		return "unknown"
	}
}

// A2Result 原样透传 launcher 的 A2 同步结果（outcome 枚举与错误），供启动闸门
// 映射；统一的是执行边界，不是算法。
type A2Result struct {
	Outcome launcher.SyncWSLPiLoginAuthOutcome
	Err     error
}

// UnitResult 是 Apply 返回的逐项结果（design §七 Apply 契约：可部分成功但
// 不得虚报整批成功）。
type UnitResult struct {
	Unit    UnitID
	Policy  Policy
	Outcome UnitOutcome
	Detail  string
	Err     error
	// A2 非空时携带 A2 结果（outcome/err）。
	A2 *A2Result
	// Undo 非 nil 时表示本次产生了可条件撤销的普通文件写入（hash 仍匹配才
	// 撤销）；A2 凭据刷新是持久认证状态，永不携带 Undo（design §七）。
	Undo *CompensationHandle
	// OperationID/Effect/Registration/Recovery 是本次操作的持久事实回执
	//（攻坚契约：错误路径 receipt 不丢；启动门消费事实而不是字符串）。
	OperationID  string
	Effect       OpEffect
	Registration OpRegistration
	Recovery     OpRecovery
	// Consumption 是敏感单元的启动消费资格（默认空串 = 未判定，不得当 safe）。
	Consumption Consumption
}

// PendingOperationFact 报告本结果是否携带未确认操作事实（内容已写但登记
// 未持久化/恢复未确认——dirty 进度不得计成功）。
func (r UnitResult) PendingOperationFact() bool {
	if r.OperationID == "" {
		return false
	}
	return r.Registration == OpRegistrationPending || r.Recovery == OpRecoveryUnavailable || r.Recovery == OpRecoveryConflict
}

// RunResult 是一次 Apply/RunUnits 的整批结果。
type RunResult struct {
	Target  TargetRef
	Results []UnitResult
}

// A2Unit 返回 A2 单元结果（未运行返回 nil）。
func (r RunResult) A2Unit() *UnitResult {
	for i := range r.Results {
		if r.Results[i].A2 != nil {
			return &r.Results[i]
		}
	}
	return nil
}

// LaunchBlocked 报告本次供给是否存在阻断启动的失败单元（required 语义由
// 调用方判定；pi/a2-auth 在登录链路恒为必需）。
func (r RunResult) LaunchBlocked() *UnitResult {
	for i := range r.Results {
		if r.Results[i].Outcome == UnitFailed || r.Results[i].Outcome == UnitBlocked {
			return &r.Results[i]
		}
	}
	return nil
}

// UnitStatus 是 GetStatus 返回的脱敏单元状态：不含内容差异、密钥、完整命令
// 或可公开查询的凭据 hash（design §六）。
type UnitStatus struct {
	Unit             UnitID
	Policy           Policy
	LastOutcome      string
	LastSuccess      time.Time
	LastErrorCode    string
	TransformVersion string
	Frozen           bool
	FrozenReason     string
	// Consented 报告需要明确同意的单元（视觉清单共享）当前是否已获得
	// per-target 授权（design §四：密钥跨界授权；非敏感单元恒 false 不展示）。
	Consented bool
	// Detached 报告「保留本地（停跟随）」标记（S2 动作面：零动作直到接管）。
	Detached bool
	// Stale 报告来源已变化待更新（S3 技能面：登记的来源清单 hash 与当前扫描
	// 缓存不一致；脱敏——hash 本体不出包）。
	Stale bool
	// PendingOperation（v3）：目标+单元存在未决（未 settled）操作时给出
	// OpID（空 = 无）；写/撤回已发生但未登记完成的事实必须可见。
	PendingOperation string
	// StateUnpersisted（v3）：内容已提交但主清单登记未持久化。
	StateUnpersisted bool
	// RecoveryPending（v3）：存在待确认的恢复材料（backup/displaced）。
	RecoveryPending bool
	// LastAttemptOutcome（v3）：最近一次尝试的结构化结论（含失败/待确认；
	// 与历史 LastSuccess 区分）。
	LastAttemptOutcome string
}

// TargetStatus 是 GetStatus 的脱敏快照（design §七：读缓存不触发安装或同步；
// 无缓存返回未检查）。
type TargetStatus struct {
	Distro          string
	CLI             CLI
	Home            string
	Generation      string
	Checked         bool
	CheckedAt       time.Time
	DesiredRevision uint64
	PendingSources  map[string]uint64
	Units           []UnitStatus
	// StoreHealth（v3）：main/pending 存储面健康（展示用；不触发探测）。
	StoreHealth StoreHealth `json:"-"`
	// PendingIntents（v3）：该 distro 的未决操作摘要（脱敏）。
	PendingIntents []PendingIntentSummary `json:"-"`
}

// PendingIntentSummary 是未决操作的脱敏摘要（不含正文/hash/秘密）。
type PendingIntentSummary struct {
	OpID    string `json:"opId"`
	Kind    OpKind `json:"kind"`
	Unit    UnitID `json:"unit,omitempty"`
	RelPath string `json:"relPath,omitempty"`
}
