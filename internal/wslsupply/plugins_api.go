package wslsupply

// plugins_api.go — 协调器对插件重放作用域的门面入口（P3-S5）。插件操作与
// 文件同步分开授权与状态（design §四）：这些入口不经 RunUnits/Plan/Apply
// 单元词汇表，启动链（ReasonLaunch）结构性无法触达插件安装——启动路径只
// 经 PluginRegistrationSummary 的状态面提示缺失，不自动联网安装。

import (
	"context"
	"fmt"
	"strings"
)

// RunPluginOperation 在指定 distro 上执行一次插件重放（显式目标动作，允许
// 唤醒当前目标；单次尝试不重试）。目标不可解析时返回结构化 blocked 结果
// 而不是 error（与 RunUnits 的 fail-closed 消费面一致）。SA16：已取消/到期
// 的请求在目标解析前立即按 ctx 归因返回——不触发任何身份探测/命令面。
func (c *Coordinator) RunPluginOperation(ctx context.Context, distro string, op PluginOperation) PluginOpResult {
	opID := newPluginOpID(op.Kind)
	if err := ctx.Err(); err != nil {
		code := classifyRunnerError(ctx, err)
		return PluginOpResult{Source: op.Source, Kind: op.Kind, Outcome: PluginOutcomeFailed,
			ErrCode: code, Detail: "请求开始前 ctx 已结束，未解析目标、未执行任何 distro 命令（" + mapLifecycleCodeDetail(code) + "）", OpID: opID}
	}
	target, err := c.ResolveTarget(ctx, strings.TrimSpace(distro), CLIPi)
	if err != nil {
		return PluginOpResult{Source: op.Source, Kind: op.Kind, Outcome: PluginOutcomeBlocked,
			ErrCode: PluginErrTargetUnresolved,
			Detail:  fmt.Sprintf("WSL 目标不可解析（%s）：%v", distro, err), OpID: opID}
	}
	if c.plugins == nil {
		return PluginOpResult{Source: op.Source, Kind: op.Kind, Outcome: PluginOutcomeBlocked,
			ErrCode: PluginErrParserUnavailable, Detail: "插件重放作用域未装配", OpID: opID}
	}
	// 攻坚契约 PR07：一次插件操作与身份复核持 applyMu（filters 与 pi/settings
	// 投影共用实际写锁；锁序 applyMu→opMu→短 state.mu，state 锁不跨网络/UNC
	// I/O——接受显式安装暂时阻塞供给的代价）。
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	if err := ctx.Err(); err != nil {
		code := classifyRunnerError(ctx, err)
		return PluginOpResult{Source: op.Source, Kind: op.Kind, Outcome: PluginOutcomeFailed,
			ErrCode: code, Detail: "等锁后请求已结束，未执行任何 distro 命令（" + mapLifecycleCodeDetail(code) + "）", OpID: opID}
	}
	return c.plugins.RunPluginOperation(ctx, target, op)
}

// ProbeWSLPlugins 显式检查 WSL 侧插件面（允许唤醒当前目标；只读不写）：
// pi 可用性 + distro settings/实体（UNC 读）+ 登记面汇总，缓存快照。SA16：
// 已取消/到期的检查不唤醒目标，如实返回取消原因。
func (c *Coordinator) ProbeWSLPlugins(ctx context.Context, distro string) (WSLPluginSnapshot, error) {
	if err := ctx.Err(); err != nil {
		code := classifyRunnerError(ctx, err)
		return WSLPluginSnapshot{Distro: strings.TrimSpace(distro), Available: false,
			Reason: mapLifecycleCodeDetail(code), Items: []WSLPluginItem{}}, nil
	}
	target, err := c.ResolveTarget(ctx, strings.TrimSpace(distro), CLIPi)
	if err != nil {
		return WSLPluginSnapshot{}, fmt.Errorf("resolve wsl supply target: %w", err)
	}
	if c.plugins == nil {
		return WSLPluginSnapshot{}, fmt.Errorf("plugin scope unavailable")
	}
	return c.plugins.Probe(ctx, target), nil
}

// WSLPluginsCachedSnapshot 返回缓存快照（只读，不唤醒 distro；从未检查时
// Checked=false——诚实呈现，不冒充已检查）。
func (c *Coordinator) WSLPluginsCachedSnapshot(distro string) WSLPluginSnapshot {
	if c.plugins == nil {
		return WSLPluginSnapshot{Distro: distro, Items: []WSLPluginItem{}}
	}
	return c.plugins.CachedSnapshot(strings.TrimSpace(distro))
}

// PluginRegistrationSummary 返回插件登记摘要（读状态清单缓存，不唤醒；
// 状态页/启动提示缺失插件的数据源）。
func (c *Coordinator) PluginRegistrationSummary() PluginRegistrationSummary {
	if c.plugins == nil {
		return PluginRegistrationSummary{}
	}
	st := c.state.load()
	return c.plugins.RegistrationSummary(st.Generation)
}
