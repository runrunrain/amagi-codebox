package wslsupply

// a2.go — A2 凭据策略适配器（design §三「A2 的迁移边界」）。
//
// 只通过 launcher 的 A2 适配器执行 A2 语义，不让通用导入器接触 auth：
// SHA-256+mtime 双向判定、2s 容差、DistroTieKept、探测失败零写入、
// DistroHasCopy 降级、NoAuth 拒绝、chmod+清理双失败拒绝、UNC 原子写——全部在
// launcher 内，本包只做调度与状态记录。字节透明：本包永不解码 auth 字段，
// 永不把内容写进日志/错误/状态。
//
// 攻坚契约（PR02/PR07）：A2Adapter.Sync 携带 ctx 与完整已解析目标——不重新
// 用 cachedHome 选目标；首个可能改变 auth/权限的动作前保存不含 hash/token
// 的 pending 摘要（OpID、完整目标、方向/阶段、可能副作用），结束保存结构化
// 结果及 settled；摘要写失败时不开始新的同步写；重启重新执行真实判定，
// 不按摘要重放旧 token、不按摘要授消费资格。

import (
	"context"
	"fmt"
	"time"

	"amagi-codebox/internal/launcher"
)

// A2Adapter 是 A2 单元的专用适配器接缝（测试注入假实现；生产为
// DefaultA2Adapter → launcher.SyncWSLPiLoginAuthContext）。
type A2Adapter interface {
	// Files 返回该单元在目标根下的相对路径清单（供 Inspect 批量探测）。
	Files() []string
	// Sync 执行一次 A2 双向同步判定与写入（判定语义零变化）。ctx 贯通到
	// 实际探测/写边界；target 是完整已解析目标（含 UID/MachineID），适配器
	// 不得重新用 cached Home 探测选目标。
	Sync(ctx context.Context, target TargetRef) (launcher.SyncWSLPiLoginAuthOutcome, error)
}

// DefaultA2Adapter 是生产适配器：auth.json 单文件，算法完全复用 launcher。
type DefaultA2Adapter struct{}

// Files 实现 A2Adapter。
func (DefaultA2Adapter) Files() []string { return []string{"auth.json"} }

// Sync 实现 A2Adapter：从 Coordinator 已解析的完整目标构造 launcher
// WSLAgentTarget（Distro/Home/LinuxDir/UNCDir/UID/MachineID），调用
// SyncWSLPiLoginAuthContext——不重新用 cached WSLUserHome 选目标；launcher
// 侧当次身份核验（UID/Home/MachineID 三者相等）与统一消费门（mode600 才
// 可消费）由 G0 切片实现。
func (DefaultA2Adapter) Sync(ctx context.Context, target TargetRef) (launcher.SyncWSLPiLoginAuthOutcome, error) {
	t := launcher.WSLAgentTarget{
		Distro: target.Distro, Home: target.Home,
		LinuxDir: target.LinuxRoot, UNCDir: target.UNCRoot,
		UID: target.UID, MachineID: target.MachineID,
	}
	return launcher.SyncWSLPiLoginAuthContext(ctx, t)
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
// 语义与桌面既有闸门一致：HasCopy 降级、NoAuth fail-closed）。攻坚契约：
// cancellation/deadline 一律失败——成功 outcome + 已取消 ctx 不是 Applied；
// classifyWriteFailure 的「存在即 HasCopy」只能由适配器内部产生 provisional，
// 本函数按最终门结论分类。
func classifyA2(ctx context.Context, unit UnitID, outcome launcher.SyncWSLPiLoginAuthOutcome, err error) UnitResult {
	res := UnitResult{
		Unit:   unit,
		Policy: PolicyA2Auth,
		Detail: a2OutcomeName(outcome),
		A2:     &A2Result{Outcome: outcome, Err: err},
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		res.Outcome = UnitFailed
		res.Detail = "a2-context-canceled"
		if err != nil {
			res.Err = fmt.Errorf("%w; %v", ctxErr, err)
		} else {
			res.Err = ctxErr
		}
		return res
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

// saveA2PendingSummary 在首个可能改变 auth/权限的动作前保存无秘密摘要
// （OpID、完整目标、方向/阶段；不含 hash/token/正文）。复用 pending 存储，
// 单独 Kind。摘要不自动授予管理权，不自动重放。
func (c *Coordinator) saveA2PendingSummary(target TargetRef, opID string) error {
	// 确定性故障接缝（测试注入；生产 faults 恒空）：模拟「摘要持久化失败」。
	// 旧夹具用 POSIX 目录 chmod 0500 注入写失败，Windows 宿主上 chmod 不阻止
	// 写入、表达不了同一语义；接缝与 commitFile 的 stageIntent 同族。
	if err := c.faults.fail(stageIntent); err != nil {
		return err
	}
	return c.state.saveIntent(OperationIntent{
		OpID: opID, Kind: OpKindA2Sync, TargetKey: targetKeyOf(target),
		TargetDistro: target.Distro, TargetHome: target.Home,
		TargetCLI: string(target.CLI),
		Unit: UnitPiA2Auth, RelPath: "auth.json", Sensitive: true,
		PluginMeta: map[string]string{"phase": "sync"},
		CreatedAt:  time.Now(),
	})
}

// finishA2Summary 结束 A2 摘要：保存结构化结果及 settled（无 hash/token）；
// 失败保留摘要为 pending（observation 同步登记，不只 log）。返回 settle 错误
// ——调用方必须据此把单元结果回退为未确认事实（此前只改值副本，返回面仍
// applied+confirmed，已证实的「结果与磁盘摘要不同事实」缺陷）。
func (c *Coordinator) finishA2Summary(target TargetRef, opID string, unitResult UnitResult, outcome launcher.SyncWSLPiLoginAuthOutcome) error {
	settleErr := c.state.mutate(func(st *StateFile) {
		if st.SettledOperations == nil {
			st.SettledOperations = map[string]SettledOperation{}
		}
		disposition := "committed"
		if unitResult.Outcome == UnitFailed || unitResult.Outcome == UnitBlocked {
			disposition = "not-applied"
		}
		st.SettledOperations[opID] = SettledOperation{
			TargetKey: targetKeyOf(target), Unit: UnitPiA2Auth,
			Disposition: disposition, SettledAt: time.Now(),
		}
		st.upsertEntry(StateEntry{
			Unit: UnitPiA2Auth, Policy: PolicyA2Auth, Generation: target.Generation,
			LastOutcome: a2OutcomeName(outcome), LastOperationID: opID,
			LastSuccess: time.Now(), LastErrorCode: sanitizeA2Err(unitResult.Err),
		}, target.Generation)
		st.Distro = target.Distro
	})
	if settleErr != nil {
		c.recordObservation(targetKeyOf(target), opID, UnitPiA2Auth, OpEffectUnknown, OpRegistrationPending, OpRecoveryNone, "a2-state-unpersisted", "a2")
		return settleErr
	}
	_ = c.state.deleteIntent(opID)
	_ = c.state.mutate(func(st *StateFile) {
		if st.SettledOperations != nil {
			delete(st.SettledOperations, opID)
		}
	})
	c.recordObservation(targetKeyOf(target), opID, UnitPiA2Auth, OpEffectCommitted, OpRegistrationConfirmed, OpRecoveryNone, a2OutcomeName(outcome), "a2")
	return nil
}

// sanitizeA2Err 截断 A2 错误为状态码短串（不含凭据内容）。
func sanitizeA2Err(err error) string {
	if err == nil {
		return ""
	}
	return sanitizeErrorCode(err)
}
