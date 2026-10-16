package main

// app_wsl_supply_s4.go — P3-S4 App 编排面：Claude/Codex 用户配置导入（键级
// 单元 + 资源目录单元）与 OpenCode 附加层 MCP 全局冲突覆盖选择。
//
// 边界（design §四 S4 行 + §五转换器复用）：
//   - Claude：~/.claude.json 仅用户选择的 mcpServers（server 级，S2 mcp 转换器
//     与冲突框架复用）；~/.claude/settings.json 白名单字段；CLAUDE.md/
//     commands/agents/skills 目录单元（S3 机制复用）。projects/trust/onboarding/
//     账户状态/未知键 LocalOnly（绝不跨界）。
//   - Codex：~/.codex/config.toml 的 mcp_servers + 选中 profiles（TOML 保真行级
//     编辑）；AGENTS.md/skills 目录单元。trust/sandbox/approval/项目绝对路径/
//     未知命令/auth.json/sessions/history/logs/cache 永不触碰。
//   - OpenCode：附加层机制保留；本批仅增加「distro 全局同名冲突的显式覆盖
//     选择」（默认保留本地不写入，勾选后写入）。
//
// 状态面：GetWSLCliSupplyStatus（每 CLI 一卡：键级单元状态 + 资源扫描状态）；
// 预览复用 S2 五动作框架（检查/预览/应用安全更新/保留本地/确认接管）。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/wslsupply"
)

// ---------------------------------------------------------------------------
// Windows 侧来源（真实文件读取；缺失=键集为空）
// ---------------------------------------------------------------------------

// wslS4HomeDirFn 是宿主 Home 解析接缝（生产 os.UserHomeDir；测试注入临时目录）。
var wslS4HomeDirFn = os.UserHomeDir

type wslClaudeConfigSource struct{ app *App }

func (s *wslClaudeConfigSource) read(rel string) ([]byte, error) {
	home, err := wslS4HomeDirFn()
	if err != nil || home == "" {
		return nil, fmt.Errorf("windows home unavailable: %v", err)
	}
	if s.app == nil {
		return nil, fmt.Errorf("app unavailable")
	}
	data, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 来源缺失：键集为空
		}
		return nil, err
	}
	return data, nil
}

func (s *wslClaudeConfigSource) ClaudeJSON() ([]byte, error) { return s.read(".claude.json") }
func (s *wslClaudeConfigSource) SettingsJSON() ([]byte, error) {
	return s.read(".claude/settings.json")
}

type wslCodexConfigSource struct{ app *App }

func (s *wslCodexConfigSource) ConfigTOML() ([]byte, error) {
	home, err := wslS4HomeDirFn()
	if err != nil || home == "" {
		return nil, fmt.Errorf("windows home unavailable: %v", err)
	}
	if s.app == nil {
		return nil, fmt.Errorf("app unavailable")
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// wslCliResourceLayout 返回 CLI 的资源布局（hostRoot 解析失败返回 nil）。
func wslCliResourceLayout(cli wslsupply.CLI) *wslsupply.ResourceLayout {
	home, err := wslS4HomeDirFn()
	if err != nil || home == "" {
		return nil
	}
	switch cli {
	case wslsupply.CLIClaude:
		return &wslsupply.ResourceLayout{
			HostRoot:     filepath.Join(home, ".claude"),
			Files:        []string{"CLAUDE.md"},
			DirUnits:     []string{"commands", "agents", "skills"},
			TargetPrefix: ".claude/",
		}
	case wslsupply.CLICodex:
		return &wslsupply.ResourceLayout{
			HostRoot: filepath.Join(home, ".codex"),
			Files:    []string{"AGENTS.md"},
			DirUnits: []string{"skills"},
		}
	default:
		return nil
	}
}

// registerS4Adapters 注册 S4 单元与资源作用域（newWSLSupplyCoordinator 调用）。
func registerS4Adapters(coord *wslsupply.Coordinator, app *App) {
	facts := wslTransformFactsSource{}
	claudeSrc := &wslClaudeConfigSource{app: app}
	codexSrc := &wslCodexConfigSource{app: app}
	// MCP/路径转换需要 wslpath 实测映射：来源内 args/env 的独立盘符路径并入。
	claudeFacts := cliUserConfigFacts{base: facts, readClaude: claudeSrc}
	codexFacts := cliUserConfigFacts{base: facts, readCodex: codexSrc}
	coord.RegisterProjection(wslsupply.NewClaudeMCPAdapter(claudeSrc, claudeFacts))
	coord.RegisterProjection(wslsupply.NewClaudeSettingsAdapter(claudeSrc, claudeFacts))
	coord.RegisterProjection(wslsupply.NewCodexConfigAdapter(codexSrc, codexFacts))
	for _, cli := range []wslsupply.CLI{wslsupply.CLIClaude, wslsupply.CLICodex} {
		if layout := wslCliResourceLayout(cli); layout != nil {
			coord.RegisterResources(wslsupply.NewResourceScope(cli, *layout, wslsupply.DefaultSkillsScanLimits()))
		}
	}
}

// cliUserConfigFacts 在基础事实之上并入该 CLI 来源内待验证路径的 wslpath
// 实测映射（与 pi 的 piConfigFactsWithPaths 同型）。
type cliUserConfigFacts struct {
	base       wslTransformFactsSource
	readClaude *wslClaudeConfigSource
	readCodex  *wslCodexConfigSource
}

func (f cliUserConfigFacts) TransformFacts(ctx context.Context, target wslsupply.TargetRef) (wslsupply.TransformFacts, error) {
	facts, err := f.base.TransformFacts(ctx, target)
	if err != nil {
		return facts, err
	}
	entries := f.collectEntries(target.Distro)
	if len(entries) == 0 {
		return facts, nil
	}
	_, paths := wslsupply.CollectTransformNeeds(entries)
	if len(paths) == 0 {
		return facts, nil
	}
	for win, linux := range wslPathMappingProbeFn(target.Distro, paths...) {
		if facts.PathMappings == nil {
			facts.PathMappings = map[string]string{}
		}
		facts.PathMappings[win] = linux
	}
	return facts, nil
}

func (f cliUserConfigFacts) collectEntries(distro string) []map[string]any {
	if f.readClaude != nil {
		if raw, err := f.readClaude.ClaudeJSON(); err == nil && len(raw) > 0 {
			var root map[string]any
			if json.Unmarshal(raw, &root) == nil {
				if servers, ok := root["mcpServers"].(map[string]any); ok {
					entries := make([]map[string]any, 0, len(servers))
					for _, raw := range servers {
						if entry, ok := raw.(map[string]any); ok {
							entries = append(entries, entry)
						}
					}
					if len(entries) > 0 {
						return entries
					}
				}
			}
		}
	}
	if f.readCodex != nil {
		if raw, err := f.readCodex.ConfigTOML(); err == nil && len(raw) > 0 {
			if entries, cerr := wslsupply.CodexMCPServerEntries(raw); cerr == nil && len(entries) > 0 {
				return entries
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 能力切片与状态面
// ---------------------------------------------------------------------------

// wslCliSupplyCoordinator 是 S4 面消费的协调器能力（nil 表示未接线）。
type wslCliSupplyCoordinator interface {
	PreviewUnits(ctx context.Context, distro string, cli wslsupply.CLI, units []wslsupply.UnitID) ([]wslsupply.UnitPreview, error)
	DropUnitRegistration(distro string, cli wslsupply.CLI, unit wslsupply.UnitID) error
	TakeOverUnit(ctx context.Context, distro string, cli wslsupply.CLI, unit wslsupply.UnitID, expectedTargetHash string) (wslsupply.UnitResult, error)
	GrantUnitConsent(ctx context.Context, distro string, cli wslsupply.CLI, unit wslsupply.UnitID) error
	SetUnitSelection(distro string, cli wslsupply.CLI, unit wslsupply.UnitID, keys []string) error
	StartCliResourcesScan(cli wslsupply.CLI) bool
	CliResourcesSnapshot(cli wslsupply.CLI) wslsupply.SkillsScanSnapshot
}

func (a *App) wslCliSupplyCoordinator() wslCliSupplyCoordinator {
	if a == nil || a.WSLSupply == nil {
		return nil
	}
	coord, _ := a.WSLSupply.(wslCliSupplyCoordinator)
	return coord
}

// wslCliFromName 校验前端 CLI 名（仅 claude/codex 开放 S4 面）。
func wslCliFromName(name string) (wslsupply.CLI, error) {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "claude":
		return wslsupply.CLIClaude, nil
	case "codex":
		return wslsupply.CLICodex, nil
	default:
		return "", fmt.Errorf("未知 CLI（S4 用户配置导入仅支持 claude/codex）：%s", name)
	}
}

// cliConfigUnits 是每 CLI 的键级单元（固定顺序）。
var cliConfigUnits = map[wslsupply.CLI][]wslsupply.UnitID{
	wslsupply.CLIClaude: {wslsupply.UnitClaudeMCP, wslsupply.UnitClaudeSettings},
	wslsupply.CLICodex:  {wslsupply.UnitCodexConfig},
}

var cliConfigUnitMeta = map[wslsupply.UnitID]struct{ relPath, label string }{
	wslsupply.UnitClaudeMCP:      {relPath: "~/.claude.json", label: "MCP 服务器（mcpServers，仅选择项）"},
	wslsupply.UnitClaudeSettings: {relPath: "~/.claude/settings.json", label: "设置偏好（白名单字段）"},
	wslsupply.UnitCodexConfig:    {relPath: "~/.codex/config.toml", label: "MCP 服务器与 profile（TOML 节）"},
}

// WSLCliSupplyStatus 是一个 CLI 的 S4 导入面状态。
type WSLCliSupplyStatus struct {
	Distro    string                `json:"distro"`
	Available bool                  `json:"available"`
	Reason    string                `json:"reason,omitempty"`
	Units     []WSLConfigUnitDetail `json:"units"`
	Resources WSLSkillsSupplyStatus `json:"resources"`
}

// GetWSLCliSupplyStatus 返回该 CLI 的导入面状态（读缓存；资源扫描单飞启动，
// 不阻塞）。
func (a *App) GetWSLCliSupplyStatus(cliName string) WSLCliSupplyStatus {
	out := WSLCliSupplyStatus{Units: []WSLConfigUnitDetail{}, Resources: WSLSkillsSupplyStatus{Skills: []WSLSkillSummary{}}}
	cli, err := wslCliFromName(cliName)
	if err != nil {
		out.Reason = err.Error()
		return out
	}
	if a == nil || a.WSLSupply == nil {
		out.Reason = "WSL 配置供给未启用"
		return out
	}
	distro := a.wslDefaultDistro()
	out.Distro = distro
	if distro == "" {
		out.Reason = "未找到可用的 WSL 发行版"
		return out
	}
	out.Available = true
	out.Units = cliConfigUnitDetails(a.wslSupplyStatus(distro, cli), cli, false)
	if coord := a.wslCliSupplyCoordinator(); coord != nil {
		coord.StartCliResourcesScan(cli)
		out.Resources = wslCliResourcesStatus(coord, distro, cli, a.wslSupplyStatus(distro, cli))
	}
	return out
}

// wslCliResourcesStatus 从资源扫描缓存与登记派生资源面状态（与技能面同构，
// 复用 WSLSkillSummary DTO）。
func wslCliResourcesStatus(coord wslCliSupplyCoordinator, distro string, cli wslsupply.CLI, st wslsupply.TargetStatus) WSLSkillsSupplyStatus {
	snap := coord.CliResourcesSnapshot(cli)
	out := WSLSkillsSupplyStatus{
		Distro: distro, Available: true, RootMissing: snap.RootMissing,
		ScanStarted: snap.Started, ScanRunning: snap.Running,
		ScanScanned: snap.Progress.Scanned, ScanTotal: snap.Progress.Total,
		ScanIncomplete: snap.Incomplete(), Skills: []WSLSkillSummary{},
	}
	units := map[string]wslsupply.UnitStatus{}
	for _, u := range st.Units {
		if _, rel, ok := wslsupply.ParseResourceUnit(u.Unit); ok {
			units[rel] = u
		}
	}
	for _, m := range snap.Skills {
		entry, hasEntry := units[m.Name]
		out.Skills = append(out.Skills, wslSkillSummary(m, entry, hasEntry))
	}
	seen := map[string]bool{}
	for _, m := range snap.Skills {
		seen[m.Name] = true
	}
	for rel := range units {
		if !seen[rel] {
			out.Skills = append(out.Skills, WSLSkillSummary{
				Name: rel, State: "source-deleted",
				Detail:  "Windows 侧该资源已删除；目标删除需确认（不自动删除）",
				Actions: []string{"keep-local"},
			})
		}
	}
	return out
}

// cliConfigUnitDetails 从状态清单派生键级单元详情（与 piConfigUnitDetails 同构；
// S4 单元的入口动作含选择导入）。
func cliConfigUnitDetails(st wslsupply.TargetStatus, cli wslsupply.CLI, pendingPush bool) []WSLConfigUnitDetail {
	out := []WSLConfigUnitDetail{}
	for _, unit := range cliConfigUnits[cli] {
		meta, ok := cliConfigUnitMeta[unit]
		if !ok {
			continue
		}
		detail := WSLConfigUnitDetail{
			Unit: string(unit), RelPath: meta.relPath, Policy: string(wslsupply.PolicyTrackedProjection),
			State: "unchecked", Detail: "尚未检查", Actions: []string{"preview"},
		}
		var entry *wslsupply.UnitStatus
		for i := range st.Units {
			if st.Units[i].Unit == unit {
				entry = &st.Units[i]
				break
			}
		}
		if entry == nil {
			detail.Detail = "Windows 侧来源就绪后预览可选导入项"
			out = append(out, detail)
			continue
		}
		switch {
		case entry.Detached:
			detail.State = "detached"
			detail.Detail = "已保留 WSL 本地（停跟随）；确认接管可重新启用"
			detail.Actions = []string{"preview", "takeover"}
		case entry.LastOutcome == "awaiting-consent" && !entry.Consented:
			detail.State = "awaiting-consent"
			detail.Detail = "尚未选择导入项；预览后勾选导入"
			detail.Actions = []string{"preview"}
		case entry.Frozen:
			detail.State = "frozen"
			detail.Detail = wslFrozenReasonText(entry.FrozenReason)
			detail.Actions = []string{"preview", "keep-local", "takeover"}
		case entry.LastErrorCode != "":
			detail.State = "failed"
			detail.Detail = "上次同步失败：" + entry.LastErrorCode
			detail.Actions = []string{"preview", "apply"}
		case entry.LastOutcome == "partial":
			detail.State = "partial"
			detail.Detail = "部分条目保持 WSL 现状（冻结/未托管/转换受阻）；预览查看逐项"
			detail.Actions = []string{"preview", "apply", "keep-local", "takeover"}
		case entry.LastOutcome == "written" || entry.LastOutcome == "takeover-written":
			if pendingPush {
				detail.State = "stale"
				detail.Detail = "有保存后的变更待推送"
			} else {
				detail.State = "synced"
				detail.Detail = "已同步（未手改的所选条目跟随 Windows 侧）"
			}
			detail.Actions = []string{"preview", "apply"}
		default:
			detail.Actions = []string{"preview", "apply"}
		}
		out = append(out, detail)
	}
	return out
}

// ---------------------------------------------------------------------------
// 五动作面（检查/预览即 PreviewUnits；应用安全更新/导入所选/保留本地/接管）
// ---------------------------------------------------------------------------

// PreviewWSLCliConfigSupply 执行「预览」：逐键动作 + 转换前→转换后对照 +
// 选择状态与接管 CAS 令牌。不写任何内容。
func (a *App) PreviewWSLCliConfigSupply(cliName string) []WSLConfigUnitPreview {
	out := []WSLConfigUnitPreview{}
	cli, err := wslCliFromName(cliName)
	if err != nil {
		return out
	}
	coord := a.wslCliSupplyCoordinator()
	if coord == nil {
		return out
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return out
	}
	units := wslSupplyUnitsFiltered(a.WSLSupply, cliConfigUnits[cli]...)
	previews, perr := coord.PreviewUnits(context.Background(), distro, cli, units)
	if perr != nil {
		a.Log.Warn("wslsupply", "CLI 用户配置预览失败", perr.Error())
		return out
	}
	for _, pv := range previews {
		row := WSLConfigUnitPreview{
			Unit: string(pv.Unit), RelPath: pv.RelPath, Policy: string(pv.Policy),
			Action: piPreviewActionName(pv.Action), Blocked: pv.BlockedReason,
			Frozen: pv.Freeze, TargetHash: pv.TargetHash,
			Notes: append([]string(nil), pv.Notes...), Keys: []WSLConfigUnitKeyPreview{},
		}
		for _, k := range pv.Keys {
			row.Keys = append(row.Keys, WSLConfigUnitKeyPreview{Key: k.Key, Action: k.Action, Reason: k.Reason, Before: k.Before, After: k.After, Selected: k.Selected, Importable: k.Importable})
		}
		out = append(out, row)
	}
	return out
}

// ImportWSLCliConfigKeys 执行「导入所选」：设置键级选择集 → 授予 per-target
// 同意 → 立即运行安全更新（CAS/冻结保护全程生效）。
func (a *App) ImportWSLCliConfigKeys(cliName, unit string, keys []string) []WSLConfigUnitOutcome {
	out := []WSLConfigUnitOutcome{}
	cli, err := wslCliFromName(cliName)
	if err != nil {
		return append(out, WSLConfigUnitOutcome{Unit: unit, Outcome: "blocked", Detail: err.Error()})
	}
	coord := a.wslCliSupplyCoordinator()
	if coord == nil {
		return append(out, WSLConfigUnitOutcome{Unit: unit, Outcome: "blocked", Detail: "WSL 配置供给未启用"})
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return append(out, WSLConfigUnitOutcome{Unit: unit, Outcome: "blocked", Detail: "未找到可用的 WSL 发行版"})
	}
	if !isCliConfigUnit(cli, wslsupply.UnitID(unit)) {
		return append(out, WSLConfigUnitOutcome{Unit: unit, Outcome: "blocked", Detail: "未知配置单元：" + unit})
	}
	if err := coord.SetUnitSelection(distro, cli, wslsupply.UnitID(unit), keys); err != nil {
		return append(out, WSLConfigUnitOutcome{Unit: unit, Outcome: "failed", Detail: "选择保存失败：" + err.Error()})
	}
	if err := coord.GrantUnitConsent(context.Background(), distro, cli, wslsupply.UnitID(unit)); err != nil {
		return append(out, WSLConfigUnitOutcome{Unit: unit, Outcome: "failed", Detail: "授权失败：" + err.Error()})
	}
	// 仅运行被导入的单元（同 CLI 其它未启用单元不受牵连）。
	res, err := a.WSLSupply.RunUnits(context.Background(), distro, cli, wslsupply.ReasonManual, []wslsupply.UnitID{wslsupply.UnitID(unit)})
	if err != nil {
		a.Log.Warn("wslsupply", "CLI 用户配置导入失败", err.Error())
		return append(out, WSLConfigUnitOutcome{Unit: unit, Outcome: "failed", Detail: err.Error()})
	}
	for _, r := range res.Results {
		if r.Unit != wslsupply.UnitID(unit) {
			continue
		}
		out = append(out, WSLConfigUnitOutcome{Unit: string(r.Unit), Outcome: r.Outcome.String(), Detail: r.Detail})
	}
	return out
}

// ApplyWSLCliConfigSupply 执行「应用安全更新」：按当前选择集运行（冻结/未
// 托管/转换受阻条目天然保持现状）。
func (a *App) ApplyWSLCliConfigSupply(cliName string) []WSLConfigUnitOutcome {
	out := []WSLConfigUnitOutcome{}
	cli, err := wslCliFromName(cliName)
	if err != nil {
		return out
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return out
	}
	return a.applyCliConfigUnits(distro, cli)
}

func (a *App) applyCliConfigUnits(distro string, cli wslsupply.CLI) []WSLConfigUnitOutcome {
	out := []WSLConfigUnitOutcome{}
	units := wslSupplyUnitsFiltered(a.WSLSupply, cliConfigUnits[cli]...)
	if len(units) == 0 {
		return out
	}
	res, err := a.WSLSupply.RunUnits(context.Background(), distro, cli, wslsupply.ReasonManual, units)
	if err != nil {
		a.Log.Warn("wslsupply", "CLI 用户配置导入失败", err.Error())
		for _, u := range units {
			out = append(out, WSLConfigUnitOutcome{Unit: string(u), Outcome: "failed", Detail: err.Error()})
		}
		return out
	}
	for _, r := range res.Results {
		if !isCliConfigUnit(cli, r.Unit) {
			continue
		}
		out = append(out, WSLConfigUnitOutcome{Unit: string(r.Unit), Outcome: r.Outcome.String(), Detail: r.Detail})
	}
	return out
}

func isCliConfigUnit(cli wslsupply.CLI, unit wslsupply.UnitID) bool {
	for _, u := range cliConfigUnits[cli] {
		if u == unit {
			return true
		}
	}
	return false
}

// KeepWSLCliConfigUnitLocal 执行键级单元的「保留本地（停跟随）」。
func (a *App) KeepWSLCliConfigUnitLocal(cliName, unit string) error {
	cli, err := wslCliFromName(cliName)
	if err != nil {
		return err
	}
	coord := a.wslCliSupplyCoordinator()
	if coord == nil {
		return fmt.Errorf("WSL 配置供给未启用")
	}
	if !isCliConfigUnit(cli, wslsupply.UnitID(unit)) {
		return fmt.Errorf("未知配置单元：%s", unit)
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return fmt.Errorf("未找到可用的 WSL 发行版")
	}
	if err := coord.DropUnitRegistration(distro, cli, wslsupply.UnitID(unit)); err != nil {
		return fmt.Errorf("停跟随失败：%v", err)
	}
	a.Log.Info("wslsupply", "已保留 WSL 本地 CLI 用户配置（停跟随）", "unit="+unit)
	return nil
}

// TakeOverWSLCliConfigUnit 执行键级单元的「确认接管（预览后）」。
func (a *App) TakeOverWSLCliConfigUnit(cliName, unit, expectedTargetHash string) error {
	cli, err := wslCliFromName(cliName)
	if err != nil {
		return err
	}
	coord := a.wslCliSupplyCoordinator()
	if coord == nil {
		return fmt.Errorf("WSL 配置供给未启用")
	}
	if !isCliConfigUnit(cli, wslsupply.UnitID(unit)) {
		return fmt.Errorf("未知配置单元：%s", unit)
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return fmt.Errorf("未找到可用的 WSL 发行版")
	}
	res, err := coord.TakeOverUnit(context.Background(), distro, cli, wslsupply.UnitID(unit), expectedTargetHash)
	if err != nil {
		return fmt.Errorf("接管失败：%v", err)
	}
	switch res.Outcome {
	case wslsupply.UnitApplied:
		a.Log.Info("wslsupply", "已按预览确认接管 WSL CLI 用户配置", fmt.Sprintf("unit=%s", unit))
		return nil
	case wslsupply.UnitBlocked:
		if res.Detail == "awaiting-consent" {
			return fmt.Errorf("尚未选择导入项（未授权）；先勾选导入再接管")
		}
		return fmt.Errorf("预览已过期（WSL 侧文件已变化，旧预览不覆盖新编辑）；请重新检查后再接管")
	default:
		return fmt.Errorf("接管失败：%s", res.Detail)
	}
}

// ---------------------------------------------------------------------------
// 资源目录单元（CLAUDE.md/commands/agents/skills 与 AGENTS.md/skills）
// ---------------------------------------------------------------------------

// CheckWSLCliResourcesSupply 执行资源面「检查」（触发后台重扫，返回当前状态）。
func (a *App) CheckWSLCliResourcesSupply(cliName string) WSLSkillsSupplyStatus {
	out := WSLSkillsSupplyStatus{Skills: []WSLSkillSummary{}}
	cli, err := wslCliFromName(cliName)
	if err != nil {
		out.Reason = err.Error()
		return out
	}
	coord := a.wslCliSupplyCoordinator()
	distro := a.wslDefaultDistro()
	out.Distro = distro
	if distro == "" {
		out.Reason = "未找到可用的 WSL 发行版"
		return out
	}
	out.Available = true
	if coord == nil {
		out.Reason = "资源供给未启用"
		return out
	}
	coord.StartCliResourcesScan(cli)
	return wslCliResourcesStatus(coord, distro, cli, a.wslSupplyStatus(distro, cli))
}

// PreviewWSLCliResourcesSupply 执行资源面「预览」。
func (a *App) PreviewWSLCliResourcesSupply(cliName string) []WSLSkillPreview {
	out := []WSLSkillPreview{}
	cli, err := wslCliFromName(cliName)
	if err != nil {
		return out
	}
	coord := a.wslCliSupplyCoordinator()
	if coord == nil {
		return out
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return out
	}
	snap := coord.CliResourcesSnapshot(cli)
	manifests := map[string]*wslsupply.SkillManifest{}
	for _, m := range snap.Skills {
		manifests[m.Name] = m
	}
	previews, perr := coord.PreviewUnits(context.Background(), distro, cli, cliResourceUnits(cli, snap))
	if perr != nil {
		a.Log.Warn("wslsupply", "资源预览失败", perr.Error())
		return out
	}
	for _, pv := range previews {
		rel, ok := parseCliResourceRel(pv.Unit)
		if !ok {
			continue
		}
		row := WSLSkillPreview{
			Name: rel, Action: piPreviewActionName(pv.Action), Blocked: pv.BlockedReason,
			Frozen: pv.Freeze, TargetHash: pv.TargetHash,
			Notes: append([]string(nil), pv.Notes...),
		}
		if m := manifests[rel]; m != nil {
			row.Portable = m.Portable()
			row.Excluded = len(m.Excluded)
			for _, it := range m.Incompatible {
				row.Incompatible = append(row.Incompatible, it.RelPath+"："+it.Reason)
			}
		}
		out = append(out, row)
	}
	return out
}

func cliResourceUnits(cli wslsupply.CLI, snap wslsupply.SkillsScanSnapshot) []wslsupply.UnitID {
	units := make([]wslsupply.UnitID, 0, len(snap.Skills))
	for _, m := range snap.Skills {
		units = append(units, wslsupply.ResourceUnitID(cli, m.Name))
	}
	return units
}

func parseCliResourceRel(unit wslsupply.UnitID) (string, bool) {
	_, rel, ok := wslsupply.ParseResourceUnit(unit)
	return rel, ok
}

// ApplyWSLCliResourcesSupply 执行资源「预览勾选后导入」：登记 per-target 同意
// 后一次 RunUnits 供给。
func (a *App) ApplyWSLCliResourcesSupply(cliName string, rels []string) []WSLConfigUnitOutcome {
	out := []WSLConfigUnitOutcome{}
	cli, err := wslCliFromName(cliName)
	if err != nil {
		return out
	}
	coord := a.wslCliSupplyCoordinator()
	if coord == nil {
		return out
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return out
	}
	snap := coord.CliResourcesSnapshot(cli)
	manifests := map[string]*wslsupply.SkillManifest{}
	for _, m := range snap.Skills {
		manifests[m.Name] = m
	}
	var units []wslsupply.UnitID
	for _, rel := range rels {
		m := manifests[rel]
		if m == nil {
			out = append(out, WSLConfigUnitOutcome{Unit: string(wslsupply.ResourceUnitID(cli, rel)), Outcome: "blocked", Detail: "扫描缓存中不存在该资源（未扫描或已删除）"})
			continue
		}
		if reason := m.NotPortableReason(); reason != "" {
			out = append(out, WSLConfigUnitOutcome{Unit: string(wslsupply.ResourceUnitID(cli, rel)), Outcome: "blocked", Detail: "该资源当前不可移植：" + reason})
			continue
		}
		unit := wslsupply.ResourceUnitID(cli, rel)
		if err := coord.GrantUnitConsent(context.Background(), distro, cli, unit); err != nil {
			out = append(out, WSLConfigUnitOutcome{Unit: string(unit), Outcome: "failed", Detail: "授权失败：" + err.Error()})
			continue
		}
		units = append(units, unit)
	}
	if len(units) == 0 {
		return out
	}
	res, err := a.WSLSupply.RunUnits(context.Background(), distro, cli, wslsupply.ReasonManual, units)
	if err != nil {
		a.Log.Warn("wslsupply", "资源导入失败", err.Error())
		for _, u := range units {
			out = append(out, WSLConfigUnitOutcome{Unit: string(u), Outcome: "failed", Detail: err.Error()})
		}
		return out
	}
	for _, r := range res.Results {
		if _, ok := parseCliResourceRel(r.Unit); !ok {
			continue
		}
		out = append(out, WSLConfigUnitOutcome{Unit: string(r.Unit), Outcome: r.Outcome.String(), Detail: r.Detail})
	}
	return out
}

// KeepWSLCliResourceLocal 执行资源的「保留本地（停跟随）」。
func (a *App) KeepWSLCliResourceLocal(cliName, rel string) error {
	cli, err := wslCliFromName(cliName)
	if err != nil {
		return err
	}
	coord := a.wslCliSupplyCoordinator()
	if coord == nil {
		return fmt.Errorf("WSL 配置供给未启用")
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return fmt.Errorf("未找到可用的 WSL 发行版")
	}
	if err := coord.DropUnitRegistration(distro, cli, wslsupply.ResourceUnitID(cli, rel)); err != nil {
		return fmt.Errorf("停跟随失败：%v", err)
	}
	a.Log.Info("wslsupply", "已保留 WSL 本地资源（停跟随）", "rel="+rel)
	return nil
}

// TakeOverWSLCliResource 执行资源的「确认接管（预览后）」。
func (a *App) TakeOverWSLCliResource(cliName, rel, expectedTargetHash string) error {
	cli, err := wslCliFromName(cliName)
	if err != nil {
		return err
	}
	coord := a.wslCliSupplyCoordinator()
	if coord == nil {
		return fmt.Errorf("WSL 配置供给未启用")
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return fmt.Errorf("未找到可用的 WSL 发行版")
	}
	res, err := coord.TakeOverUnit(context.Background(), distro, cli, wslsupply.ResourceUnitID(cli, rel), expectedTargetHash)
	if err != nil {
		return fmt.Errorf("接管失败：%v", err)
	}
	switch res.Outcome {
	case wslsupply.UnitApplied:
		a.Log.Info("wslsupply", "已按预览确认接管 WSL 资源", "rel="+rel)
		return nil
	case wslsupply.UnitBlocked:
		if res.Detail == "awaiting-consent" {
			return fmt.Errorf("尚未选择导入该资源（未授权）；先勾选导入再接管")
		}
		return fmt.Errorf("预览已过期（WSL 侧资源已变化，旧预览不覆盖新编辑）；请重新检查后再接管")
	default:
		return fmt.Errorf("接管失败：%s", res.Detail)
	}
}

// ---------------------------------------------------------------------------
// OpenCode 附加层 MCP 选择（全局同名冲突的显式覆盖）
// ---------------------------------------------------------------------------

// wslOpenCodeMCPSelectionFile 是选择存储文件名（CodeBox 配置目录）。
const wslOpenCodeMCPSelectionFile = "wsl-opencode-mcp.json"

// wslOpenCodeMCPSelectionEntry 是单 distro 的选择（全局冲突覆盖名单）。
type wslOpenCodeMCPSelectionEntry struct {
	// OverrideGlobal：与 distro 全局 opencode.json 同名时仍写入附加层的
	// server 名（默认保留本地——S1 语义不变，勾选才覆盖）。
	OverrideGlobal []string `json:"overrideGlobal,omitempty"`
	// SavedAt：上次保存时间（展示用）。
	SavedAt time.Time `json:"savedAt,omitempty"`
}

type wslOpenCodeMCPSelectionFileData struct {
	SchemaVersion int                                     `json:"schemaVersion"`
	Distros       map[string]wslOpenCodeMCPSelectionEntry `json:"distros"`
}

// loadWSLOpenCodeMCPSelection 读取选择存储（损坏/缺失回零值——选择面回到默认
// 保留本地，不因状态库失败破坏启动）。
func (a *App) loadWSLOpenCodeMCPSelection() wslOpenCodeMCPSelectionFileData {
	out := wslOpenCodeMCPSelectionFileData{SchemaVersion: 1, Distros: map[string]wslOpenCodeMCPSelectionEntry{}}
	if a == nil || a.configDir == "" {
		return out
	}
	data, err := os.ReadFile(filepath.Join(a.configDir, wslOpenCodeMCPSelectionFile))
	if err != nil {
		return out
	}
	var parsed wslOpenCodeMCPSelectionFileData
	if json.Unmarshal(data, &parsed) != nil || parsed.Distros == nil {
		return out
	}
	return parsed
}

// saveWSLOpenCodeMCPSelection 原子保存选择存储（tmp 0600 + rename）。
func (a *App) saveWSLOpenCodeMCPSelection(sel wslOpenCodeMCPSelectionFileData) error {
	sel.SchemaVersion = 1
	if sel.Distros == nil {
		sel.Distros = map[string]wslOpenCodeMCPSelectionEntry{}
	}
	b, err := json.MarshalIndent(sel, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	path := filepath.Join(a.configDir, wslOpenCodeMCPSelectionFile)
	if err := os.MkdirAll(a.configDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(a.configDir, ".wsl-opencode-mcp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// WSLOpenCodeMCPEntry 是选择面上单个 preset MCP server 的脱敏条目。
type WSLOpenCodeMCPEntry struct {
	Name           string `json:"name"`
	Remote         bool   `json:"remote"`
	NativeCommand  string `json:"nativeCommand"`
	NeedsAdapt     bool   `json:"needsAdapt"`
	Reason         string `json:"reason"`
	GlobalConflict bool   `json:"globalConflict"`
	// OverrideGlobal：当前是否已勾选覆盖（同名冲突时才生效）。
	OverrideGlobal bool `json:"overrideGlobal"`
}

// GetWSLOpenCodeMCPPreview 返回全部 OpenCode preset 的 MCP 条目分类预览
// （合并去重；形态检查与全局同名冲突只读判定，不写任何内容）。
func (a *App) GetWSLOpenCodeMCPPreview() []WSLOpenCodeMCPEntry {
	out := []WSLOpenCodeMCPEntry{}
	if a == nil || a.Config == nil {
		return out
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return out
	}
	merged := map[string]map[string]any{}
	for _, preset := range a.Config.GetOpenCodePresets() {
		if len(preset.Config) == 0 {
			continue
		}
		var cfg map[string]any
		if json.Unmarshal(preset.Config, &cfg) != nil {
			continue
		}
		servers, ok := cfg["mcp"].(map[string]any)
		if !ok {
			continue
		}
		for name, raw := range servers {
			if _, seen := merged[name]; seen {
				continue
			}
			if entry, ok := raw.(map[string]any); ok {
				merged[name] = entry
			}
		}
	}
	if len(merged) == 0 {
		return out
	}
	cfg := map[string]any{"mcp": merged}
	entries := launcher.PreviewWSLOpenCodeMCP(distro, cfg)
	override := map[string]bool{}
	if entry, ok := a.loadWSLOpenCodeMCPSelection().Distros[distro]; ok {
		for _, name := range entry.OverrideGlobal {
			override[name] = true
		}
	}
	for _, e := range entries {
		out = append(out, WSLOpenCodeMCPEntry{
			Name: e.Name, Remote: e.Remote, NativeCommand: e.NativeCommand,
			NeedsAdapt: e.NeedsAdapt, Reason: e.Reason,
			GlobalConflict: e.GlobalConflict, OverrideGlobal: override[e.Name],
		})
	}
	return out
}

// SetWSLOpenCodeMCPSelection 保存当前 distro 的全局同名冲突覆盖选择（显式
// 覆盖名单；默认保留本地的项不在名单内）。
func (a *App) SetWSLOpenCodeMCPSelection(overrideGlobal []string) error {
	if a == nil || a.configDir == "" {
		return fmt.Errorf("WSL 配置供给未启用")
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return fmt.Errorf("未找到可用的 WSL 发行版")
	}
	sel := a.loadWSLOpenCodeMCPSelection()
	names := make([]string, 0, len(overrideGlobal))
	for _, name := range overrideGlobal {
		name = strings.TrimSpace(name)
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	entry := sel.Distros[distro]
	entry.OverrideGlobal = names
	entry.SavedAt = time.Now()
	sel.Distros[distro] = entry
	if err := a.saveWSLOpenCodeMCPSelection(sel); err != nil {
		return fmt.Errorf("保存选择失败：%v", err)
	}
	a.Log.Info("opencode-wsl", "已保存 OpenCode 附加层 MCP 覆盖选择",
		fmt.Sprintf("distro=%s override=%d 项", distro, len(names)))
	return nil
}

// wslOpenCodeMCPOverrideSet 返回 distro 的覆盖名单（prepareWSLOpenCodeLayer 消费）。
func (a *App) wslOpenCodeMCPOverrideSet(distro string) map[string]bool {
	entry, ok := a.loadWSLOpenCodeMCPSelection().Distros[distro]
	if !ok || len(entry.OverrideGlobal) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, name := range entry.OverrideGlobal {
		out[name] = true
	}
	return out
}
