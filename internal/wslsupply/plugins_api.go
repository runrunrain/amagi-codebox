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
// 而不是 error（与 RunUnits 的 fail-closed 消费面一致）。
func (c *Coordinator) RunPluginOperation(ctx context.Context, distro string, op PluginOperation) PluginOpResult {
	opID := newPluginOpID(op.Kind)
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
	return c.plugins.RunPluginOperation(ctx, target, op)
}

// ProbeWSLPlugins 显式检查 WSL 侧插件面（允许唤醒当前目标；只读不写）：
// pi 可用性 + distro settings/实体（UNC 读）+ 登记面汇总，缓存快照。
func (c *Coordinator) ProbeWSLPlugins(ctx context.Context, distro string) (WSLPluginSnapshot, error) {
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
