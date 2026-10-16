package main

// app_wsl_plugins.go — P3-S5 插件面板目标化的 App 编排面：
//
//   - piplugin 面板操作（install/remove/update）目标化：Windows（默认）/
//     当前 WSL distro / 同时应用；每目标独立结果（Windows 成功 ≠ WSL 成功，
//     不合并报喜）；
//   - 「同时应用」的重放锚点：Windows 侧操作成功后读取实体解析版本/commit，
//     WSL 重放固定到同一版本/ref（宿主一次 update 不等价为目标再次追 latest）；
//     Windows 侧失败则 WSL 侧 skipped（无锚点，不盲装）；
//   - WSL 侧状态面：缓存快照（不唤醒停机 distro）+ 显式检查（允许唤醒）；
//   - 其他 CLI（claude/codex/opencode）仅目标状态占位（原生操作契约未验证，
//     不开放操作入口）；omp 排除（B7 主上裁定）。
//
// 结构性保证：插件重放不经 RunUnits/Plan/Apply 单元词汇表——启动链与保存
// 推送路径无法触达插件安装；保存事件只标脏（沿用 S1 调度规则，停机 distro
// 不因任何面板保存被唤醒）。

import (
	"context"
	"fmt"
	"strings"
	"time"

	"amagi-codebox/internal/piplugin"
	"amagi-codebox/internal/wslsupply"
)

// ---------------------------------------------------------------------------
// 目标支持描述（占位面数据源）
// ---------------------------------------------------------------------------

// WSLPluginTargetEntry 是一个 CLI 的 WSL 插件面支持状态。
type WSLPluginTargetEntry struct {
	CLI     string `json:"cli"`     // pi / claude / codex / opencode / omp
	Support string `json:"support"` // operations / status-placeholder / excluded
	Detail  string `json:"detail"`
}

// GetWSLPluginTargetSupport 返回各 CLI 插件面板的 WSL 目标支持描述（静态
// 契约面；omp 排除是 B7 主上裁定的声明，不是待办）。
func (a *App) GetWSLPluginTargetSupport() []WSLPluginTargetEntry {
	return []WSLPluginTargetEntry{
		{CLI: "pi", Support: "operations", Detail: "支持目标化安装/更新/移除：WSL 侧经 distro 内原生 pi CLI 重放（npm 固定版本、git 固定 ref），每目标独立结果"},
		{CLI: "claude", Support: "status-placeholder", Detail: "原生操作契约未验证：仅展示目标状态占位，不开放 WSL 侧操作入口"},
		{CLI: "codex", Support: "status-placeholder", Detail: "原生操作契约未验证：仅展示目标状态占位，不开放 WSL 侧操作入口"},
		{CLI: "opencode", Support: "status-placeholder", Detail: "原生操作契约未验证：仅展示目标状态占位，不开放 WSL 侧操作入口"},
		{CLI: "omp", Support: "excluded", Detail: "omp 不纳入 WSL 供给（B7 裁定保持关闭）"},
	}
}

// ---------------------------------------------------------------------------
// 协调器能力切片
// ---------------------------------------------------------------------------

// piPluginReplayTimeout 是 App 编排面给插件重放/探测的默认 deadline（对齐
// internal/piplugin 的 piCommandTimeout=3 分钟惯例）：ctx 到期经
// defaultPluginCommandRunner（exec.CommandContext）终止 wsl.exe，错误归类为
// 结构化 timeout（单次尝试不重试），挂起命令不再长期持有 opMu。
const piPluginReplayTimeout = 3 * time.Minute

// wslPluginCoordinator 是插件重放面消费的协调器能力切片（nil 表示未接线）。
type wslPluginCoordinator interface {
	RunPluginOperation(ctx context.Context, distro string, op wslsupply.PluginOperation) wslsupply.PluginOpResult
	ProbeWSLPlugins(ctx context.Context, distro string) (wslsupply.WSLPluginSnapshot, error)
	WSLPluginsCachedSnapshot(distro string) wslsupply.WSLPluginSnapshot
	PluginRegistrationSummary() wslsupply.PluginRegistrationSummary
}

func (a *App) wslPluginCoordinator() wslPluginCoordinator {
	if a == nil || a.WSLSupply == nil {
		return nil
	}
	coord, _ := a.WSLSupply.(wslPluginCoordinator)
	return coord
}

// wslPluginSourceParserAdapter 把 piplugin 的解析语义适配为 wslsupply 接缝
// （单一来源：解析规则只在 piplugin 维护）。
func wslPluginSourceParserAdapter(raw string) wslsupply.PluginSourceSpec {
	spec := piplugin.ParseSourceSpec(raw)
	return wslsupply.PluginSourceSpec{
		Type: spec.Type, Name: spec.Name, Ref: spec.Ref,
		Host: spec.Host, Path: spec.Path, LocalPath: spec.LocalPath,
	}
}

// ---------------------------------------------------------------------------
// 目标化操作（每目标独立结果）
// ---------------------------------------------------------------------------

// 合法目标值。
const (
	piPluginTargetWindows = "windows"
	piPluginTargetWSL     = "wsl"
	piPluginTargetBoth    = "both"
)

// PiPluginOpOutcome 是单目标的操作结果（脱敏：不含完整命令输出正文）。
type PiPluginOpOutcome struct {
	Target    string `json:"target"` // windows / wsl
	OK        bool   `json:"ok"`
	Outcome   string `json:"outcome"` // installed/updated/removed/already-ok/blocked/failed/skipped
	ErrCode   string `json:"errCode,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Output    string `json:"output,omitempty"`    // Windows 侧 pi CLI 输出摘要
	PinnedRef string `json:"pinnedRef,omitempty"` // WSL 侧重放锚定的版本/ref
	RefKind   string `json:"refKind,omitempty"`
}

// PiPluginTargetedResult 是一次目标化操作的聚合结果（每目标一条，独立成败）。
type PiPluginTargetedResult struct {
	Target  string              `json:"target"`
	Results []PiPluginOpOutcome `json:"results"`
}

// validatePiPluginTarget 校验目标值。
func validatePiPluginTarget(target string) (string, error) {
	t := strings.TrimSpace(strings.ToLower(target))
	switch t {
	case piPluginTargetWindows, piPluginTargetWSL, piPluginTargetBoth:
		return t, nil
	default:
		return "", fmt.Errorf("未知插件操作目标：%q（可用：windows / wsl / both）", target)
	}
}

// wslOutcome 把协调器结果映射为面板结果（语义透传，不吞错）。
func wslOutcome(res wslsupply.PluginOpResult) PiPluginOpOutcome {
	return PiPluginOpOutcome{
		Target: piPluginTargetWSL, OK: res.OK, Outcome: res.Outcome,
		ErrCode: res.ErrCode, Detail: res.Detail,
		PinnedRef: res.PinnedRef, RefKind: res.RefKind,
	}
}

// runPiPluginTargeted 是三类操作的统一编排：Windows 侧（既有 pi CLI 路径）
// 与 WSL 侧重放按目标组合；「同时应用」的 update/install 以 Windows 实体
// 解析结果为重放锚点，Windows 失败则 WSL skipped（无锚点不盲装）；remove
// 无锚点语义，两侧独立执行。
func (a *App) runPiPluginTargeted(kind, source, target string) (PiPluginTargetedResult, error) {
	t, err := validatePiPluginTarget(target)
	if err != nil {
		return PiPluginTargetedResult{}, err
	}
	out := PiPluginTargetedResult{Target: t, Results: []PiPluginOpOutcome{}}
	if a.PiPlugins == nil {
		return PiPluginTargetedResult{}, fmt.Errorf("Pi 插件服务未初始化")
	}

	var windowsRes *piplugin.CommandResult
	runWindows := t == piPluginTargetWindows || t == piPluginTargetBoth
	if runWindows {
		res, werr := a.runPiPluginWindowsOp(kind, source)
		if werr != nil {
			out.Results = append(out.Results, PiPluginOpOutcome{
				Target: piPluginTargetWindows, Outcome: "failed", ErrCode: "windows-failed", Detail: werr.Error(),
			})
		} else {
			out.Results = append(out.Results, PiPluginOpOutcome{
				Target: piPluginTargetWindows, OK: res.Success, Outcome: piWindowsOutcomeName(kind, res),
				Detail: res.Error, Output: truncateForPanel(res.Output),
			})
			windowsRes = res
		}
	}

	if t != piPluginTargetWSL && t != piPluginTargetBoth {
		return out, nil
	}
	coord := a.wslPluginCoordinator()
	if coord == nil {
		out.Results = append(out.Results, PiPluginOpOutcome{
			Target: piPluginTargetWSL, Outcome: "blocked", ErrCode: "supply-unavailable",
			Detail: "WSL 配置供给未启用，无法重放到 WSL",
		})
		return out, nil
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		out.Results = append(out.Results, PiPluginOpOutcome{
			Target: piPluginTargetWSL, Outcome: "blocked", ErrCode: "target-unresolved",
			Detail: "未找到可用的 WSL 发行版",
		})
		return out, nil
	}

	op := wslsupply.PluginOperation{Source: source, Kind: kind}
	if ext, sk, pr, th := a.PiPlugins.WindowsEntryFilters(source); len(ext)+len(sk)+len(pr)+len(th) > 0 {
		op.Filters = wslsupply.PluginFilters{Extensions: ext, Skills: sk, Prompts: pr, Themes: th}
	}
	// 重放锚点：install/update 的「同时应用」以 Windows 实体解析结果为准
	//（remove 无锚点语义）。Windows 侧未运行（仅 wsl 目标）→ distro 内独立
	// 解析；Windows 侧失败 → WSL skipped（无锚点不盲装，单侧失败独立显示）。
	if kind == wslsupply.PluginOpInstall || kind == wslsupply.PluginOpUpdate {
		if windowsRes != nil && windowsRes.Success {
			op.AnchorRef = a.piPluginHostAnchor(source)
		} else if runWindows && windowsRes == nil {
			// Windows 执行返回错误（结果已记录 failed）：WSL 侧 skipped——
			// 无锚点不盲装；单侧失败独立显示，不合并报喜。
			out.Results = append(out.Results, PiPluginOpOutcome{
				Target: piPluginTargetWSL, Outcome: "skipped", ErrCode: "windows-anchor-missing",
				Detail: "Windows 侧操作失败，无重放锚点；WSL 侧未执行（可单独以 wsl 目标重试）",
			})
			return out, nil
		}
	}
	opCtx, cancel := context.WithTimeout(context.Background(), piPluginReplayTimeout)
	defer cancel()
	res := coord.RunPluginOperation(opCtx, distro, op)
	out.Results = append(out.Results, wslOutcome(res))
	return out, nil
}

// runPiPluginWindowsOp 执行 Windows 侧既有路径。
func (a *App) runPiPluginWindowsOp(kind, source string) (*piplugin.CommandResult, error) {
	switch kind {
	case wslsupply.PluginOpInstall:
		return a.PiPlugins.InstallPackage(source)
	case wslsupply.PluginOpUpdate:
		return a.PiPlugins.UpdatePackage(source)
	case wslsupply.PluginOpRemove:
		return a.PiPlugins.RemovePackage(source)
	default:
		return nil, fmt.Errorf("未知操作类型：%s", kind)
	}
}

// piWindowsOutcomeName 从命令结果推导 Windows 侧 outcome 名。
func piWindowsOutcomeName(kind string, res *piplugin.CommandResult) string {
	if !res.Success {
		return "failed"
	}
	switch kind {
	case wslsupply.PluginOpInstall:
		return "installed"
	case wslsupply.PluginOpUpdate:
		return "updated"
	default:
		return "removed"
	}
}

// piPluginHostAnchor 读取 Windows 实体解析锚点（npm version / git commit；
// 不可得返回空串——WSL 侧回退 distro 内独立解析）。
func (a *App) piPluginHostAnchor(source string) string {
	spec := piplugin.ParseSourceSpec(source)
	switch spec.Type {
	case "npm":
		return a.PiPlugins.ResolvedEntityVersion(source)
	case "git":
		return a.PiPlugins.ResolvedEntityCommit(source)
	default:
		return ""
	}
}

func truncateForPanel(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		return s[:500] + "…"
	}
	return s
}

// InstallPiPackageTargeted 目标化安装（target: windows / wsl / both）。
func (a *App) InstallPiPackageTargeted(source, target string) (PiPluginTargetedResult, error) {
	return a.runPiPluginTargeted(wslsupply.PluginOpInstall, source, target)
}

// UpdatePiPackageTargeted 目标化更新（仅本系统登记管理的 WSL 插件可更新）。
func (a *App) UpdatePiPackageTargeted(source, target string) (PiPluginTargetedResult, error) {
	return a.runPiPluginTargeted(wslsupply.PluginOpUpdate, source, target)
}

// RemovePiPackageTargeted 目标化移除（WSL 本地手动项不动）。
func (a *App) RemovePiPackageTargeted(source, target string) (PiPluginTargetedResult, error) {
	return a.runPiPluginTargeted(wslsupply.PluginOpRemove, source, target)
}

// ---------------------------------------------------------------------------
// WSL 侧状态面（缓存不唤醒 + 显式检查可唤醒）
// ---------------------------------------------------------------------------

// PiPluginWSLStatus 是 WSL 侧插件面板状态聚合（缓存快照 + 登记摘要）。
type PiPluginWSLStatus struct {
	Distro    string                              `json:"distro"`
	Available bool                                `json:"available"`
	Reason    string                              `json:"reason,omitempty"`
	Snapshot  wslsupply.WSLPluginSnapshot         `json:"snapshot"`
	Summary   wslsupply.PluginRegistrationSummary `json:"summary"`
}

// GetPiPluginWSLStatus 返回缓存状态（只读，不唤醒停机 distro；从未检查时
// Checked=false 诚实呈现）。
func (a *App) GetPiPluginWSLStatus() PiPluginWSLStatus {
	out := PiPluginWSLStatus{}
	coord := a.wslPluginCoordinator()
	distro := a.wslDefaultDistro()
	out.Distro = distro
	if distro == "" {
		out.Reason = "未找到可用的 WSL 发行版"
		return out
	}
	out.Available = true
	if coord == nil {
		out.Reason = "WSL 配置供给未启用"
		return out
	}
	out.Snapshot = coord.WSLPluginsCachedSnapshot(distro)
	out.Summary = coord.PluginRegistrationSummary()
	return out
}

// CheckPiPluginWSLStatus 显式检查（允许唤醒当前目标；只读不写任何目标
// 内容），返回最新快照。
func (a *App) CheckPiPluginWSLStatus() PiPluginWSLStatus {
	out := a.GetPiPluginWSLStatus()
	coord := a.wslPluginCoordinator()
	if coord == nil || out.Distro == "" {
		return out
	}
	probeCtx, cancel := context.WithTimeout(context.Background(), piPluginReplayTimeout)
	defer cancel()
	snap, err := coord.ProbeWSLPlugins(probeCtx, out.Distro)
	if err != nil {
		out.Reason = "检查失败：" + err.Error()
		return out
	}
	out.Snapshot = snap
	return out
}
