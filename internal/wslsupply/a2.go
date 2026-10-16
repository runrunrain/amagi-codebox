package wslsupply

// a2.go — A2 凭据策略适配器（design §三「A2 的迁移边界」）。
//
// 只通过现有 launcher.SyncWSLPiLoginAuth 适配器执行 A2 语义，不让通用导入器
// 接触 auth：SHA-256+mtime 双向判定、2s 容差、DistroTieKept、探测失败零写入、
// DistroHasCopy 降级、NoAuth 拒绝、chmod+清理双失败拒绝、UNC 原子写——全部在
// launcher 内，本包只做调度与状态记录。字节透明：本包永不解码 auth 字段，
// 永不把内容写进日志/错误/状态。

import (
	"amagi-codebox/internal/launcher"
)

// A2Adapter 是 A2 单元的专用适配器接缝（测试注入假实现；生产为
// DefaultA2Adapter → launcher.SyncWSLPiLoginAuth）。
type A2Adapter interface {
	// Files 返回该单元在目标根下的相对路径清单（供 Inspect 批量探测）。
	Files() []string
	// Sync 执行一次 A2 双向同步判定与写入（判定语义零变化）。
	Sync(distro string) (launcher.SyncWSLPiLoginAuthOutcome, error)
}

// DefaultA2Adapter 是生产适配器：auth.json 单文件，算法完全复用
// launcher.SyncWSLPiLoginAuth。
type DefaultA2Adapter struct{}

// Files 实现 A2Adapter。
func (DefaultA2Adapter) Files() []string { return []string{"auth.json"} }

// Sync 实现 A2Adapter。
func (DefaultA2Adapter) Sync(distro string) (launcher.SyncWSLPiLoginAuthOutcome, error) {
	return launcher.SyncWSLPiLoginAuth(distro)
}

// a2OutcomeName 把 launcher outcome 映射为状态记录用的稳定字符串（脱敏，
// 不含错误链中的任何文件内容）。
func a2OutcomeName(outcome launcher.SyncWSLPiLoginAuthOutcome) string {
	switch outcome {
	case launcher.SyncWSLPiLoginAuthCopied:
		return "copied"
	case launcher.SyncWSLPiLoginAuthFresh:
		return "fresh"
	case launcher.SyncWSLPiLoginAuthDistroNewer:
		return "distro-newer"
	case launcher.SyncWSLPiLoginAuthDistroTieKept:
		return "tie-kept-distro"
	case launcher.SyncWSLPiLoginAuthDistroHasCopy:
		return "distro-has-copy"
	case launcher.SyncWSLPiLoginAuthDistroNoAuth:
		return "distro-no-auth"
	default:
		return "unknown"
	}
}

// classifyA2 把 A2 适配器结果映射为单元结果分类（启动闸门消费的降级/拒绝
// 语义与桌面既有闸门一致：HasCopy 降级、NoAuth fail-closed）。
func classifyA2(unit UnitID, outcome launcher.SyncWSLPiLoginAuthOutcome, err error) UnitResult {
	res := UnitResult{
		Unit:   unit,
		Policy: PolicyA2Auth,
		Detail: a2OutcomeName(outcome),
		A2:     &A2Result{Outcome: outcome, Err: err},
	}
	switch outcome {
	case launcher.SyncWSLPiLoginAuthCopied, launcher.SyncWSLPiLoginAuthDistroNewer:
		res.Outcome = UnitApplied
	case launcher.SyncWSLPiLoginAuthFresh:
		res.Outcome = UnitFresh
	case launcher.SyncWSLPiLoginAuthDistroTieKept:
		res.Outcome = UnitApplied
		res.Detail = a2OutcomeName(outcome)
	case launcher.SyncWSLPiLoginAuthDistroHasCopy:
		res.Outcome = UnitWarnDegraded
		res.Err = err
	case launcher.SyncWSLPiLoginAuthDistroNoAuth:
		res.Outcome = UnitFailed
		res.Err = err
	default:
		res.Outcome = UnitFailed
		res.Err = err
	}
	return res
}
