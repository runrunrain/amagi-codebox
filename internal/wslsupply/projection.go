package wslsupply

// projection.go — TrackedProjection 单元接缝（design §三/§七）。
//
// S0 只留接口：生产不注册任何投影适配器（普通配置分发是 S1+ 批次）；测试经
// RegisterProjection 注册桩适配器驱动完整 Plan/Apply/Compensate 链路，钉住
// 接缝契约。归属判定要点（S1 实现时逐条兑现）：
//   - 无记录、目标已存在 → unmanaged-existing，冻结待确认，mtime 不授权覆盖；
//   - 无记录、目标缺失 → 仅明确允许的默认托管产物可自动创建；
//   - 有记录、目标等于上次写入 hash → 来源未变跳过，来源改变才更新；
//   - 有记录、目标变化/被删除 → 本地修改，冻结该单元，不以 mtime 仲裁。

import (
	"context"
	"encoding/hex"
)

// ProjectionAdapter 是一个投影单元的格式适配器接缝。
//
// S1 起生产适配器（pi/managed-models、shared/vision-export、shared/modality-kb）
// 由装配层注册；S0 的测试桩同接口驱动。归属判定要点（适配器 Plan 内兑现）：
//   - TrackedProjection：无记录+目标已存在 → unmanaged-existing 冻结；有记录+
//     目标变化/被删 → 本地修改冻结；无变化跳过。
//   - ManagedNamespace：从业务源生成、保留域外字段；无记录+目标存在不冻结
//     （合并保留用户内容，见各适配器）；损坏目标拒绝不覆盖。
//   - 需要同意的单元（RequiresConsent）：协调器在 Plan 前统一拦截（awaiting-consent）。
type ProjectionAdapter interface {
	// Unit 返回单元标识（CLI 作用域内唯一，形如 "<cli>/<name>"）。
	Unit() UnitID
	// Policy 返回单元归属策略（TrackedProjection / ManagedNamespace）。
	Policy() Policy
	// Sensitive 报告候选是否携带凭据/密钥正文：敏感单元 chmod 补偿失败时
	// 删除落盘内容（宽松权限明文不留盘）；普通单元优先恢复前像（M2）。
	Sensitive() bool
	// RequiresConsent 报告首次发布是否需要 per-target 明确同意（密钥跨界
	// 授权；仅视觉清单共享单元为 true）。
	RequiresConsent() bool
	// Files 返回该单元关心的目标相对路径（供 Inspect 批量探测）。
	Files() []string
	// Plan 纯计算：依据请求、快照与登记推导动作（含预期目标 hash 与候选
	// 字节数）。不写盘、不 spawn、不产出候选正文。
	Plan(req SupplyRequest, snap *Snapshot, state *StateEntry) (PlannedUnit, error)
	// Candidate 在 Apply 阶段生成目标候选字节与规范化来源 hash（仅内存）。
	// state 是登记基准（键级适配器据此做逐键冲突判定；ManagedNamespace
	// 适配器可忽略）。
	Candidate(ctx context.Context, target TargetRef, state *StateEntry) (ProjectionCandidate, error)
}

// KeyedAdapter 是键级 TrackedProjection 适配器（S2：pi/settings、pi/amagi、
// pi/mcp）的可选能力面：逐键预览（检查/预览动作）与预览后接管。
type KeyedAdapter interface {
	ProjectionAdapter
	// PreviewKeys 返回逐键判定（脱敏：仅方向与原因）；blockedCode 非空表示
	// 单元整体不可分析（source-corrupt / target-corrupt / detached 等）。
	PreviewKeys(snap *Snapshot, state *StateEntry) (keys []KeyPreview, blockedCode string)
	// AdoptCandidate 生成「确认接管」候选：托管域内键以当前来源覆写，域外
	// 内容保留。CAS 由协调器在调用前完成。
	AdoptCandidate(ctx context.Context, target TargetRef, state *StateEntry) (ProjectionCandidate, error)
}

// KeyPreview 是预览面上单个键的脱敏动作描述。
type KeyPreview struct {
	Key    string
	Action string // add / update / remove / no-change / keep-frozen / conflict / unmanaged-existing
	Reason string // 中文，不含内容差异与密钥
}

// ProjectionCandidate 是 Candidate 阶段的产物。SourceHash 是「规范化业务内容」
// 的 hash（不含 updated_at 之类的时问戳字段——来源时间变化但业务内容不变时
// 零动作）；Content 才是实际落盘字节（可能与 SourceHash 基准不同）。Notes 是
// 逐项诊断（部分构建失败/冲突保留等），协调器并入结果 Detail 供状态页与日志
// 消费（不含密钥正文）。
//
// S2 键级适配器扩展：Subs 是合并后的完整键级基准（键级 TrackedProjection 的
// 登记面）；Outcome 为空时按 written 登记，partial 表示存在保持现状的键。
type ProjectionCandidate struct {
	Content          []byte
	SourceHash       string
	TransformVersion string
	Notes            []string
	Subs             map[string]SubHash
	Outcome          string
}

// RegisterProjection 注册一个投影单元适配器（S0 仅供测试；S1+ 由装配层注册
// 真实分发适配器）。
func (c *Coordinator) RegisterProjection(a ProjectionAdapter) {
	if a == nil {
		return
	}
	c.projections[a.Unit()] = a
}

// hexEncode 是 encoding/hex 的统一出口（包内共用）。
func hexEncode(b []byte) string { return hex.EncodeToString(b) }

// hexHash 是 sha256 hex 的一致出口。
func hexHash(b []byte) string {
	sum := sha256Sum(b)
	return hexEncode(sum[:])
}
