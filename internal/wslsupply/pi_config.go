package wslsupply

// pi_config.go — Pi 配置白名单投影的三个 TrackedProjection 单元（P3-S2，
// design §四 Pi 全景表 settings/amagi/mcp 行 + §三冲突表）。
//
//   - pi/settings：settings.json 字段级白名单投影（packages 排除——插件走 S5
//     重放；会话/机器字段与未知字段不导入；白名单内绝对路径值经映射转换或
//     剔除该字段，绝不做任意正文替换）。
//   - pi/amagi：amagi.json 的 profile/agents/compaction/concurrency/mcp 路由
//     投影（沿用 piconfig 既有验证器；引用的 provider/server 不存在时报告
//     未满足依赖，不伪造默认值）。
//   - pi/mcp：mcp.json server 级投影（一个 MCP server 是原子导入单元：转换
//     失败该单元不新增不更新，已有同名保持；MCP 形态经 transform.go 确定规则
//     转换，事实在 Apply 阶段经 FactsSource 实测）。
//
// 三者共用 keyed.go 的逐键冲突引擎与本文件的通用适配器骨架；来源是 Windows
// 侧真实文件（App 注入 PiConfigSource），目标候选只在内存存在。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// S2 单元 id 与转换版本。
const (
	UnitPiSettings UnitID = "pi/settings"
	UnitPiAmagi    UnitID = "pi/amagi"
	UnitPiMCP      UnitID = "pi/mcp"

	piSettingsTransform = "pi-settings-v1"
	piAmagiTransform    = "pi-amagi-v1"
	piMCPTransform      = "pi-mcp-v1"

	// PiSettingsFile / PiAmagiFile / PiMCPFile 是三个单元的目标文件（相对
	// distro 内 .pi/agent）。
	PiSettingsFile = "settings.json"
	PiAmagiFile    = "amagi.json"
	PiMCPFile      = "mcp.json"
)

// PiConfigSource 是 App 注入的 Windows 侧来源（真实文件读取；缺失返回
// nil,nil——来源缺失是确定的「键集为空」，读失败才是 source-unavailable）。
type PiConfigSource interface {
	SettingsJSON() ([]byte, error)
	AmagiJSON() ([]byte, error)
	MCPJSON() ([]byte, error)
	// KnownProviderIDs 返回 Windows 侧 provider 名全集（models.json 注册表 +
	// 内置目录）——amagi.json 引用诊断的比对宇宙。
	KnownProviderIDs() ([]string, error)
}

// keyedShape 描述一个键级投影单元的文件形态差异。
type keyedShape struct {
	relPath     string
	sensitive   bool
	alwaysFacts bool // mcp：任何写入都需事实（shim 验证）；settings 按值预判
	// selectLeaf：desired 需经用户选择集过滤（S4「仅用户选择的单元导入」）——
	// 叶名（最后一个 "/" 段）不在 state.SelectedKeys 内的键不进入候选；
	// 已导入后取消选择 = 来源删除语义（移除托管条目）。
	selectLeaf bool
	// desired 从来源对象抽取期望键集（扁平路径 → 值）与形态诊断。
	desired func(obj map[string]any) (map[string]any, []string)
	// convert 把一个期望值转换为目标形态；ok=false 表示该键不可转换（保持
	// 目标现状并记录 note）。
	convert func(key string, v any, facts TransformFacts) (any, bool, string)
	// validateTarget 校验目标文档形态（不符即 target-corrupt：拒绝不覆盖）。
	validateTarget func(obj map[string]any) error
	// validateCandidate 对最终候选做既有验证器校验（amagi：piconfig）。
	validateCandidate func(content []byte) error
	// summarizeEntry（S4 预览对照，可选）：键值的脱敏单行摘要（转换前/后）；
	// After 传空 facts 下不可确定性转换时返回转换受阻说明。不含 env 值/密钥。
	summarizeEntry func(key string, v any, facts TransformFacts) (before, after string)
}

// keyedProjectionAdapter 是键级投影单元的通用适配器（S2 Pi 三单元 + S4
// Claude 两单元）。
type keyedProjectionAdapter struct {
	unitID          UnitID
	facts           TransformFactsSource
	shape           keyedShape
	requiresConsent bool
	readSource      func() ([]byte, error)
	depNotes        func(desired map[string]any) []string
}

// NewPiSettingsAdapter 创建 settings.json 字段级白名单投影单元（需 per-target
// 同意后才开始投影，见 keyedProjectionAdapter.requiresConsent）。
func NewPiSettingsAdapter(source PiConfigSource, facts TransformFactsSource) *keyedProjectionAdapter {
	return &keyedProjectionAdapter{
		unitID: UnitPiSettings, facts: facts,
		shape:           piSettingsShape,
		requiresConsent: true,
		readSource:      source.SettingsJSON,
	}
}

// NewPiAmagiAdapter 创建 amagi.json 投影单元（需 per-target 同意）。
func NewPiAmagiAdapter(source PiConfigSource, facts TransformFactsSource) *keyedProjectionAdapter {
	return &keyedProjectionAdapter{
		unitID: UnitPiAmagi, facts: facts,
		shape:           piAmagiShape,
		requiresConsent: true,
		readSource:      source.AmagiJSON,
		depNotes: func(desired map[string]any) []string {
			if source == nil {
				return nil
			}
			providers := map[string]bool{}
			if ids, err := source.KnownProviderIDs(); err == nil {
				for _, id := range ids {
					providers[id] = true
				}
			}
			servers := map[string]bool{}
			if mcpSrc, err := source.MCPJSON(); err == nil && len(mcpSrc) > 0 {
				var root map[string]any
				if json.Unmarshal(mcpSrc, &root) == nil {
					if mcpServers, ok := root["mcpServers"].(map[string]any); ok {
						for name := range mcpServers {
							servers[name] = true
						}
					}
				}
			}
			return amagiDepNotesFrom(desired, providers, servers)
		},
	}
}

// NewPiMCPAdapter 创建 mcp.json server 级投影单元（需 per-target 同意）。
func NewPiMCPAdapter(source PiConfigSource, facts TransformFactsSource) *keyedProjectionAdapter {
	return &keyedProjectionAdapter{
		unitID: UnitPiMCP, facts: facts,
		shape:           piMCPShape,
		requiresConsent: true,
		readSource:      source.MCPJSON,
	}
}

func (a *keyedProjectionAdapter) Unit() UnitID          { return a.unitID }
func (a *keyedProjectionAdapter) Policy() Policy        { return PolicyTrackedProjection }
func (a *keyedProjectionAdapter) Sensitive() bool       { return a.shape.sensitive }
func (a *keyedProjectionAdapter) RequiresConsent() bool { return a.requiresConsent }
func (a *keyedProjectionAdapter) Files() []string       { return []string{a.shape.relPath} }

// SelectionAware 报告本单元是否接受键级导入选择（S4 selectLeaf 单元；
// SetUnitSelection 的能力面）。
func (a *keyedProjectionAdapter) SelectionAware() bool { return a.shape.selectLeaf }

// ---------------------------------------------------------------------------
// 读取与判定
// ---------------------------------------------------------------------------

// keyedAnalysis 是一次键级分析的完整产物（Plan/预览共用）。
type keyedAnalysis struct {
	desired    map[string]any
	importable map[string]any // selectLeaf 单元的全量可导入键集（预览勾选面；其余为 nil）
	depNotes   []string
	decisions  []keyedDecision
	subs       map[string]SubHash
	targetDoc  map[string]any
	targetHash string // 目标文件当前字节 hash（CAS 锚；空 = 目标缺失）
	blocked    string // source-unavailable / source-corrupt / target-corrupt
}

// analyze 读取来源与目标并完成逐键判定（Plan 阶段：只读 UNC，不 spawn）。
func (a *keyedProjectionAdapter) analyze(snap *Snapshot, state *StateEntry) keyedAnalysis {
	ana := keyedAnalysis{desired: map[string]any{}, subs: map[string]SubHash{}}
	if state != nil && state.Detached {
		// 已选择保留本地（停跟随）：零动作；分析结果为空集。
		ana.blocked = "detached"
		return ana
	}
	src, err := a.readSourceSafe()
	if err != nil {
		ana.blocked = "source-unavailable"
		return ana
	}
	desired, notes, corrupt := a.desiredFrom(src)
	if corrupt {
		ana.blocked = "source-corrupt"
		return ana
	}
	if a.shape.selectLeaf {
		// S4 选择门：仅用户选择的条目进入候选（叶名不在选择集 → 不导入）；
		// 已登记条目被取消选择会走「来源删除」路径移除托管副本。全量集保留在
		// importable 供预览勾选面展示。
		ana.importable = desired
		desired = a.applySelection(desired, state)
	}
	ana.desired = desired
	ana.depNotes = notes
	if state != nil {
		ana.subs = state.Subs
		if ana.subs == nil {
			ana.subs = map[string]SubHash{}
		}
	}
	doc, hash, terr := a.readTargetDoc(snap)
	if terr != "" {
		ana.blocked = terr // target-corrupt
		return ana
	}
	ana.targetDoc = doc
	ana.targetHash = hash
	ana.decisions = resolveKeyed(desired, func(key string) (any, bool) {
		return keyedGet(doc, key)
	}, ana.subs)
	return ana
}

func (a *keyedProjectionAdapter) readSourceSafe() ([]byte, error) {
	if a.readSource == nil {
		return nil, fmt.Errorf("keyed projection source unavailable")
	}
	return a.readSource()
}

// applySelection 按用户选择集过滤期望键集（叶名匹配；无登记 = 全不选）。
func (a *keyedProjectionAdapter) applySelection(desired map[string]any, state *StateEntry) map[string]any {
	if !a.shape.selectLeaf {
		return desired
	}
	selected := map[string]bool{}
	if state != nil {
		for k := range state.SelectedKeys {
			selected[k] = true
		}
	}
	filtered := map[string]any{}
	for k, v := range desired {
		if selected[keyLeafName(k)] {
			filtered[k] = v
		}
	}
	return filtered
}

// keyLeafName 返回键路径的叶名（最后一个 "/" 段；无分隔符即整串）。
func keyLeafName(key string) string {
	if i := strings.LastIndexByte(key, '/'); i >= 0 {
		return key[i+1:]
	}
	return key
}

// desiredFrom 解析来源字节并抽取期望键集；corrupt=true 表示来源形态不可解析。
func (a *keyedProjectionAdapter) desiredFrom(src []byte) (map[string]any, []string, bool) {
	if len(src) == 0 {
		return map[string]any{}, nil, false
	}
	var obj map[string]any
	if json.Unmarshal(src, &obj) != nil || obj == nil {
		return nil, nil, true
	}
	desired, notes := a.shape.desired(obj)
	if a.depNotes != nil {
		notes = append(notes, a.depNotes(desired)...)
	}
	return desired, notes, false
}

// readTargetDoc 读取并解析目标文件（缺失 → 空文档 + 空 hash）。
func (a *keyedProjectionAdapter) readTargetDoc(snap *Snapshot) (map[string]any, string, string) {
	target := TargetRef{}
	if snap != nil {
		target = snap.Target
	}
	if target.UNCRoot == "" {
		return nil, "", "target-unresolved"
	}
	data, err := os.ReadFile(filepath.Join(target.UNCRoot, filepath.FromSlash(a.shape.relPath)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, "", ""
		}
		return nil, "", "target-unreadable"
	}
	hash := hexHash(data)
	if len(strings.TrimSpace(string(data))) == 0 {
		// 空文件：视作空文档（首次投影可安全建立）。
		return map[string]any{}, hash, ""
	}
	var obj map[string]any
	if json.Unmarshal(data, &obj) != nil || obj == nil {
		return nil, hash, "target-corrupt"
	}
	if a.shape.validateTarget != nil {
		if err := a.shape.validateTarget(obj); err != nil {
			return nil, hash, "target-corrupt"
		}
	}
	return obj, hash, ""
}

// Plan 是纯计算：按 §三冲突表聚合单键判定。可写键（新增/更新/移除）存在 →
// 写入（CAS 锚 = 目标当前字节 hash）；无可写且有冻结/未托管 → 阻断 + 冻结
// 登记；全部无变化 → 跳过。
func (a *keyedProjectionAdapter) Plan(_ SupplyRequest, snap *Snapshot, state *StateEntry) (PlannedUnit, error) {
	ana := a.analyze(snap, state)
	return planFromKeyedDecisions(ana.blocked, ana.decisions, a.shape.relPath, a.transformVersion(), ana.targetHash), nil
}

// planFromKeyedDecisions 把逐键判定聚合为单元计划（S2/S4 键级适配器共用）。
func planFromKeyedDecisions(blocked string, decisions []keyedDecision, relPath, transformVersion, targetHash string) PlannedUnit {
	unit := PlannedUnit{RelPath: relPath, TransformVersion: transformVersion}
	switch blocked {
	case "detached", "":
	case "source-unavailable", "source-corrupt", "target-unresolved", "target-unreadable", "target-corrupt":
		unit.Action = ActionBlocked
		unit.BlockedReason = blocked
		if blocked == "target-corrupt" {
			unit.Freeze = true
		}
		return unit
	}
	writable, frozenKind := 0, ""
	for _, d := range decisions {
		if d.Kind.writable() {
			writable++
			continue
		}
		if d.Kind == keyFrozenLocalMod || d.Kind == keyFrozenLocalDel || d.Kind == keyFrozenBothChanged {
			if frozenKind == "" || frozenKind == "unmanaged-existing" {
				frozenKind = keyedFrozenCode(d.Kind)
			}
		} else if d.Kind == keyUnmanaged && frozenKind == "" {
			frozenKind = "unmanaged-existing"
		}
	}
	switch {
	case writable > 0:
		unit.Action = ActionWriteProjection
		unit.ExpectedTargetHash = targetHash
	case frozenKind != "":
		unit.Action = ActionBlocked
		unit.BlockedReason = frozenKind
		unit.Freeze = true
	default:
		unit.Action = ActionNone
	}
	return unit
}

func (a *keyedProjectionAdapter) transformVersion() string {
	switch a.unitID {
	case UnitPiSettings:
		return piSettingsTransform
	case UnitPiAmagi:
		return piAmagiTransform
	default:
		return piMCPTransform
	}
}

func keyedFrozenCode(kind keyedDecisionKind) string {
	switch kind {
	case keyFrozenLocalDel:
		return "local-deletion-frozen"
	case keyFrozenBothChanged:
		return "both-changed-frozen"
	default:
		return "local-modification-frozen"
	}
}

// PreviewKeys 返回逐键预览（检查/预览动作的详情面；脱敏，仅方向与原因）。
// S4：selectLeaf 单元附选择状态；summarizeEntry 单元附转换前→转换后对照
// （事实经显式预览动作实测——允许唤醒当前目标；探测失败时 After 给出「待实测」）。
func (a *keyedProjectionAdapter) PreviewKeys(snap *Snapshot, state *StateEntry) ([]KeyPreview, string) {
	ana := a.analyze(snap, state)
	if ana.blocked != "" && ana.blocked != "detached" {
		return nil, ana.blocked
	}
	if ana.blocked == "detached" {
		return nil, "detached"
	}
	selected := map[string]bool{}
	if state != nil {
		for k := range state.SelectedKeys {
			selected[k] = true
		}
	}
	var facts TransformFacts
	factsOK := false
	if a.shape.summarizeEntry != nil && a.facts != nil && snap != nil {
		if got, err := a.facts.TransformFacts(context.Background(), snap.Target); err == nil {
			facts, factsOK = got, true
		}
	}
	out := make([]KeyPreview, 0, len(ana.decisions))
	for _, d := range ana.decisions {
		kp := KeyPreview{Key: d.Key, Action: d.Kind.String(), Reason: d.Reason}
		if a.shape.selectLeaf {
			kp.Selected = selected[keyLeafName(d.Key)]
			_, srcOK := ana.desired[d.Key]
			kp.Importable = srcOK && d.Kind != keyFrozenBothChanged
		}
		if a.shape.summarizeEntry != nil {
			if srcVal, ok := ana.desired[d.Key]; ok {
				before, after := a.shape.summarizeEntry(d.Key, srcVal, facts)
				kp.Before = before
				if !factsOK && after == "" {
					kp.After = "转换需实测验证（应用时执行）"
				} else {
					kp.After = after
				}
			}
		}
		out = append(out, kp)
	}
	// 选择面：未选择且不在判定集内的来源条目以「可勾选」呈现（selectLeaf）。
	if a.shape.selectLeaf && len(ana.importable) > 0 {
		seen := map[string]bool{}
		for _, kp := range out {
			seen[kp.Key] = true
		}
		for _, key := range sortedDesiredKeys(ana.importable) {
			if seen[key] {
				continue
			}
			kp := KeyPreview{Key: key, Action: "add", Reason: "未选择导入；勾选后随导入写入", Importable: true}
			if a.shape.summarizeEntry != nil {
				before, after := a.shape.summarizeEntry(key, ana.importable[key], facts)
				kp.Before = before
				if !factsOK && after == "" {
					kp.After = "转换需实测验证（应用时执行）"
				} else {
					kp.After = after
				}
			}
			out = append(out, kp)
		}
	}
	return out, ""
}

// ---------------------------------------------------------------------------
// Candidate / AdoptCandidate
// ---------------------------------------------------------------------------

// Candidate 在 Apply 阶段生成合并候选：重新读取目标现状（fresh），按判定执行
// 逐键合并——转换失败的键保持目标现状并记录诊断；冻结/未托管键原样保留。
// 返回的 Subs 是成功合并后的完整键级基准（写入侧统一登记）。
func (a *keyedProjectionAdapter) Candidate(ctx context.Context, target TargetRef, state *StateEntry) (ProjectionCandidate, error) {
	return a.buildCandidate(ctx, target, state, false)
}

// AdoptCandidate 是「预览后确认接管」的候选：托管域内键以当前来源覆写（转换
// 失败的键保持目标现状 + 诊断），域外内容原样保留；来源已删除且目标未手改的
// 旧托管键一并移除。CAS 由协调器在调用前完成。
func (a *keyedProjectionAdapter) AdoptCandidate(ctx context.Context, target TargetRef, state *StateEntry) (ProjectionCandidate, error) {
	return a.buildCandidate(ctx, target, state, true)
}

func (a *keyedProjectionAdapter) buildCandidate(ctx context.Context, target TargetRef, state *StateEntry, adopt bool) (ProjectionCandidate, error) {
	src, err := a.readSourceSafe()
	if err != nil {
		return ProjectionCandidate{}, fmt.Errorf("pi config source unavailable: %w", err)
	}
	desired, notes, corrupt := a.desiredFrom(src)
	if corrupt {
		return ProjectionCandidate{}, fmt.Errorf("windows-side %s is not valid JSON; fix the source file first", a.shape.relPath)
	}
	if a.shape.selectLeaf {
		desired = a.applySelection(desired, state)
	}
	doc, _, terr := a.readTargetDoc(&Snapshot{Target: target})
	switch terr {
	case "":
	case "target-corrupt":
		return ProjectionCandidate{}, fmt.Errorf("%w: distro %s failed shape validation", ErrTargetCorrupt, a.shape.relPath)
	default:
		return ProjectionCandidate{}, fmt.Errorf("read distro %s: %s", a.shape.relPath, terr)
	}
	if doc == nil {
		doc = map[string]any{}
	}

	subs := map[string]SubHash{}
	var adoptNotes []string
	var decisions []keyedDecision
	if adopt {
		// 接管：托管域内全部键按来源覆写（重建基准，不继承旧冻结）。
		keys := make([]string, 0, len(desired))
		for k := range desired {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			kind := keyUpdate
			if _, exists := keyedGet(doc, k); !exists {
				kind = keyAdd
			}
			decisions = append(decisions, keyedDecision{Key: k, Kind: kind})
		}
		// 来源已删除的旧托管键：目标仍等于上次写入 → 移除；被手改 → 保留+诊断。
		if state != nil && len(state.Subs) > 0 {
			stale := make([]string, 0)
			for k := range state.Subs {
				if _, inDesired := desired[k]; !inDesired {
					stale = append(stale, k)
				}
			}
			sort.Strings(stale)
			for _, k := range stale {
				sub := state.Subs[k]
				if v, ok := keyedGet(doc, k); ok && keyedValueHash(v) == sub.Written {
					decisions = append(decisions, keyedDecision{Key: k, Kind: keyRemove})
				} else {
					adoptNotes = append(adoptNotes, fmt.Sprintf("键 %s：来源已删除但 WSL 侧已被修改，接管时保留本地", k))
				}
			}
		}
		notes = append(notes, adoptNotes...)
	} else {
		if state != nil && state.Subs != nil {
			for k, v := range state.Subs {
				subs[k] = v
			}
		}
		if state != nil && state.Detached {
			return ProjectionCandidate{}, fmt.Errorf("unit is detached (keep-local); takeover required to re-enable")
		}
		decisions = resolveKeyed(desired, func(key string) (any, bool) {
			return keyedGet(doc, key)
		}, subs)
	}

	// 事实收集：仅存在可写键且形态需要时（mcp 恒需——shim 验证；settings 按
	// 值预判；amagi 纯传递无需）。探测失败按「不可确认」处理：空事实 + 诊断
	//（转换自然保守拒绝，不生成未验证条目）。
	facts := TransformFacts{}
	needFacts := false
	for _, d := range decisions {
		if d.Kind.writable() {
			needFacts = true
			break
		}
	}
	wantFacts := a.shape.alwaysFacts || valuesContainWindowsPath(desired)
	if (needFacts || adopt) && wantFacts && a.facts != nil {
		got, factsErr := a.facts.TransformFacts(ctx, target)
		if factsErr != nil {
			notes = append(notes, "目标形态事实探测失败，本次未做命令/路径转换（不生成未验证条目）")
		} else {
			facts = got
		}
	}

	wroteAny, blockedAny := false, false
	for _, d := range decisions {
		srcVal, srcOK := desired[d.Key]
		switch d.Kind {
		case keyAdd, keyUpdate:
			converted, ok, note := a.shape.convert(d.Key, srcVal, facts)
			if !ok {
				notes = append(notes, fmt.Sprintf("键 %s：%s（保持 WSL 现状）", d.Key, note))
				blockedAny = true
				continue
			}
			if !keyedSet(doc, d.Key, converted) {
				notes = append(notes, fmt.Sprintf("键 %s：目标侧路径被非对象结构占据，无法合并（保持现状）", d.Key))
				blockedAny = true
				continue
			}
			subs[d.Key] = SubHash{Source: keyedValueHash(srcVal), Written: keyedValueHash(converted)}
			wroteAny = true
		case keyRemove:
			keyedDelete(doc, d.Key)
			delete(subs, d.Key)
			wroteAny = true
		case keyNone:
			if prev, ok := subs[d.Key]; ok {
				if srcOK {
					prev.Source = keyedValueHash(srcVal)
				}
				subs[d.Key] = prev
			}
		case keyFrozenLocalMod, keyFrozenLocalDel, keyFrozenBothChanged, keyUnmanaged:
			blockedAny = true
			notes = append(notes, fmt.Sprintf("键 %s：%s", d.Key, d.Reason))
		}
	}

	content, mErr := marshalKeyedDoc(doc)
	if mErr != nil {
		return ProjectionCandidate{}, fmt.Errorf("marshal merged %s: %w", a.shape.relPath, mErr)
	}
	if a.shape.validateCandidate != nil {
		if vErr := a.shape.validateCandidate(content); vErr != nil {
			return ProjectionCandidate{}, fmt.Errorf("merged %s failed validation (target kept): %w", a.shape.relPath, vErr)
		}
	}
	if !wroteAny && blockedAny {
		// 全部可判定键均保持现状：不产生写入（调用方按受阻处理，非写失败）。
		return ProjectionCandidate{}, errKeyedNoWrite{notes: notes, reason: a.noWriteReason(decisions)}
	}
	outcome := "written"
	if blockedAny {
		outcome = "partial"
	}
	return ProjectionCandidate{
		Content:          content,
		SourceHash:       keyedAggregateHash(desired),
		TransformVersion: a.transformVersion(),
		Notes:            notes,
		Subs:             subs,
		Outcome:          outcome,
	}, nil
}

// errKeyedNoWrite 表示候选阶段发现无可写键（全部冻结/未托管/转换受阻）——
// 协调器据此把结果归类为受阻而非写失败；reason 是主导原因码（与 Plan 的
// 冻结码同词汇 + conversion-blocked）。
type errKeyedNoWrite struct {
	notes  []string
	reason string
}

func (e errKeyedNoWrite) Error() string { return "keyed projection has no writable keys" }

// noWriteReason 归纳无可写时的主导原因：冻结类优先于未托管，转换受阻单独成码。
func (a *keyedProjectionAdapter) noWriteReason(decisions []keyedDecision) string {
	unmanaged := false
	for _, d := range decisions {
		switch d.Kind {
		case keyFrozenBothChanged:
			return keyedFrozenCode(keyFrozenBothChanged)
		case keyFrozenLocalMod:
			return keyedFrozenCode(keyFrozenLocalMod)
		case keyFrozenLocalDel:
			return keyedFrozenCode(keyFrozenLocalDel)
		case keyUnmanaged:
			unmanaged = true
		}
	}
	if unmanaged {
		return "unmanaged-existing"
	}
	return "conversion-blocked"
}

// marshalKeyedDoc 序列化合并文档（缩进 2 + 换行，与 pi 生态文件形态一致）。
func marshalKeyedDoc(doc map[string]any) ([]byte, error) {
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// keyedAggregateHash 计算期望键集的规范化 hash（登记可观测性用）。
func keyedAggregateHash(desired map[string]any) string {
	keys := make([]string, 0, len(desired))
	for k := range desired {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString("\x00")
		sb.WriteString(keyedValueHash(desired[k]))
		sb.WriteString("\n")
	}
	return hexHash([]byte(sb.String()))
}

// valuesContainWindowsPath 报告期望键集中是否存在携带 Windows 路径形态的值
// （settings 惰性事实收集的预判）。
func valuesContainWindowsPath(desired map[string]any) bool {
	for _, v := range desired {
		if anyContainsWindowsPath(v) {
			return true
		}
	}
	return false
}

func anyContainsWindowsPath(v any) bool {
	switch t := v.(type) {
	case string:
		return ContainsWindowsDriveRef(t)
	case map[string]any:
		for _, child := range t {
			if anyContainsWindowsPath(child) {
				return true
			}
		}
	case []any:
		for _, child := range t {
			if anyContainsWindowsPath(child) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// amagi 引用依赖诊断（design §四 amagi 行：不伪造默认值，报告未满足依赖）
// ---------------------------------------------------------------------------

// amagiDepNotesFrom 从已确认的 provider/server 宇宙推导引用依赖诊断。
func amagiDepNotesFrom(desired map[string]any, providers, servers map[string]bool) []string {
	var notes []string
	for _, key := range sortedDesiredKeys(desired) {
		if !strings.HasPrefix(key, "agents/") {
			continue
		}
		role := strings.TrimPrefix(key, "agents/")
		entry, _ := desired[key].(map[string]any)
		model, _ := entry["model"].(string)
		if provider, ok := modelProviderRef(model); ok && !providers[provider] {
			notes = append(notes, fmt.Sprintf("角色 %s 引用的 provider %q 未在 Windows 侧模型目录中找到（未满足依赖，不伪造默认值）", role, provider))
		}
	}
	if compaction, ok := desired["compaction"].(map[string]any); ok {
		if model, _ := compaction["model"].(string); model != "" {
			if provider, ok := modelProviderRef(model); ok && !providers[provider] {
				notes = append(notes, fmt.Sprintf("压缩模型引用的 provider %q 未在 Windows 侧模型目录中找到", provider))
			}
		}
	}
	if concurrency, ok := desired["concurrency"].(map[string]any); ok {
		if perProvider, ok := concurrency["providers"].(map[string]any); ok {
			names := make([]string, 0, len(perProvider))
			for name := range perProvider {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if !providers[name] {
					notes = append(notes, fmt.Sprintf("并发配置引用的 provider %q 未在 Windows 侧模型目录中找到", name))
				}
			}
		}
	}
	referenced := map[string]bool{}
	for _, key := range sortedDesiredKeys(desired) {
		if !strings.HasPrefix(key, "mcp/") {
			continue
		}
		appendServerRefList(desired[key], referenced)
	}
	names := make([]string, 0, len(referenced))
	for name := range referenced {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !servers[name] {
			notes = append(notes, fmt.Sprintf("MCP 路由引用的 server %q 未在 mcp.json 中找到（若由插件提供，插件服务器校验属后续批次）", name))
		}
	}
	return notes
}

// sortedDesiredKeys 返回期望键集的确定性排序视图。
func sortedDesiredKeys(desired map[string]any) []string {
	keys := make([]string, 0, len(desired))
	for k := range desired {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// collectServerRefs 把一个扁平 mcp 路由键（default / agents/<role> / 其它
// 列表键）的值并入引用名集合。

func appendServerRefList(v any, out map[string]bool) {
	list, ok := v.([]any)
	if !ok {
		return
	}
	for _, item := range list {
		if s, ok := item.(string); ok {
			out[s] = true
		}
	}
}

// modelProviderRef 从 "provider/model[:thinking]" 提取 provider 段。
func modelProviderRef(model string) (string, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", false
	}
	idx := strings.IndexByte(model, '/')
	if idx <= 0 || idx == len(model)-1 {
		return "", false
	}
	return model[:idx], true
}
