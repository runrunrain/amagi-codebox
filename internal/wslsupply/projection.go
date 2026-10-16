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

// ProjectionAdapter 是一个 TrackedProjection 单元的格式适配器接缝。
type ProjectionAdapter interface {
	// Unit 返回单元标识（CLI 作用域内唯一，形如 "<cli>/<name>"）。
	Unit() UnitID
	// Files 返回该单元关心的目标相对路径（供 Inspect 批量探测）。
	Files() []string
	// Plan 纯计算：依据请求、快照与登记推导动作（含预期目标 hash 与候选
	// 字节数）。不写盘、不 spawn、不产出候选正文。
	Plan(req SupplyRequest, snap *Snapshot, state *StateEntry) (PlannedUnit, error)
	// Candidate 在 Apply 阶段生成目标候选字节与转换版本（仅内存）。
	Candidate(ctx context.Context, target TargetRef) (content []byte, transformVersion string, err error)
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
