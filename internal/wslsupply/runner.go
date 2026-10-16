package wslsupply

// runner.go — 供给运行器接口：App 桌面闸门与 launchplan 执行器共用的最小面
//（S0 统一执行边界的类型铆钉）。生产实现是 *Coordinator；测试用假运行器替换。
import (
	"context"
)

// Runner 是启动链消费的最小供给接口。
type Runner interface {
	// RunUnits 解析目标并执行 Inspect/Plan/Apply（顺序在 PTY 之前由调用方保证）。
	RunUnits(ctx context.Context, distro string, cli CLI, reason Reason, units []UnitID) (RunResult, error)
	// MarkDirty 保存事件后发布来源 revision（不探测、不调度）。
	MarkDirty(source string, revision uint64) error
	// GetStatus 返回脱敏状态（读缓存，不触发安装或同步）。
	GetStatus(distro string, cli CLI) TargetStatus
	// Compensate 条件撤销本次产生的普通文件写入（A2 不参与补偿）。
	Compensate(ctx context.Context, handles ...*CompensationHandle) []CompensationOutcome
}
