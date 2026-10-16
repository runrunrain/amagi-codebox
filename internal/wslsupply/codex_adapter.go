package wslsupply

// codex_adapter.go — Codex 用户配置导入单元 codex/config（P3-S4，design §四
// Codex 表逐行）。
//
// ~/.codex/config.toml 的受管 TOML 节投影：
//   - mcp_servers/<name>：server 级原子导入单元（复用 S2 mcp 转换器确定规则：
//     裸 shim 仅目标验证过原生命令才映射；Windows exe/ps1/盘符参数拒绝；远程
//     URL 保留）。仅用户选择的 server 进入候选（selectLeaf 语义）。
//   - profiles/<name>：选中的可移植 profile 节（标量/数组值；携带 Windows
//     路径形态的值剔除该节并报告；model_provider 引用不伪造——provider 由
//     既有 env/-c 管理，引用缺失只诊断不阻断）。
//   - trust / sandbox / approval 等权限决定、项目绝对路径、未知命令与一切
//     未知节 LocalOnly：不在受管作用域，字节不动（「无凭据迁移授权，不套用
//     A2」；auth.json/sessions/history/logs/cache 永不触碰——本单元只读写
//     config.toml 一个文件）。
//
// TOML 保真（验收 3）：候选经 toml_edit.go 行级编辑生成——非受管节、注释、
// 空行与行尾风格逐字节保留；受管段替换/删除/追加后自校验（重新解析 + 受管段
// 值 round-trip 相等才提交）。目标或来源的受管作用域出现不可解析形态 → 单元
// 受阻不写文件。
//
// 敏感度取舍：config.toml 携带大量用户自有内容——按普通单元处置（chmod 补偿
// 失败恢复前像，不整文件删除；新文件场景清理半提交），mcp env 中的密钥副本
// 不在宽松权限下新落盘。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CodexConfigFile 是单元目标文件（相对 distro ~/.codex）。
const CodexConfigFile = "config.toml"

const codexConfigTransform = "codex-config-v1"

// codexTOMLScopes 是受管 TOML 作用域（design §四 Codex 表推荐范围）。
var codexTOMLScopes = []string{"mcp_servers", "profiles"}

// CodexConfigSource 是 App 注入的 Windows 侧来源（真实文件读取；缺失返回
// nil,nil——键集为空，读失败才是 source-unavailable）。
type CodexConfigSource interface {
	// ConfigTOML 返回 Windows ~/.codex/config.toml 字节。
	ConfigTOML() ([]byte, error)
}

// NewCodexConfigAdapter 创建 codex/config 单元适配器（需 per-target 同意；
// 仅选择集内的 server/profile 导入）。
func NewCodexConfigAdapter(source CodexConfigSource, facts TransformFactsSource) *codexConfigAdapter {
	return &codexConfigAdapter{source: source, facts: facts}
}

type codexConfigAdapter struct {
	source CodexConfigSource
	facts  TransformFactsSource
}

func (a *codexConfigAdapter) Unit() UnitID          { return UnitCodexConfig }
func (a *codexConfigAdapter) Policy() Policy        { return PolicyTrackedProjection }
func (a *codexConfigAdapter) Sensitive() bool       { return false }
func (a *codexConfigAdapter) RequiresConsent() bool { return true }
func (a *codexConfigAdapter) Files() []string       { return []string{CodexConfigFile} }

// SelectionAware 报告接受键级导入选择（server/profile 叶名选择集）。
func (a *codexConfigAdapter) SelectionAware() bool { return true }

// codexAnalysis 是一次分析的完整产物（Plan/预览/候选共用形态）。
type codexAnalysis struct {
	desired    map[string]any
	importable map[string]any // 全量可导入段（预览勾选面；含未选择项）
	notes      []string
	decisions  []keyedDecision
	subs       map[string]SubHash
	targetDoc  *tomlManagedDoc
	targetHash string
	blocked    string
}

// readSourceDoc 读取并解析 Windows 侧 config.toml（缺失 → 空文档）。
func (a *codexConfigAdapter) readSourceDoc() (*tomlManagedDoc, map[string]*tomlSectionRange, error) {
	if a.source == nil {
		return nil, nil, errors.New("codex config source unavailable")
	}
	src, err := a.source.ConfigTOML()
	if err != nil {
		return nil, nil, fmt.Errorf("codex config source unavailable: %w", err)
	}
	if len(strings.TrimSpace(string(src))) == 0 {
		return nil, nil, nil // 来源缺失：键集为空
	}
	return parseTOMLManaged(src, codexTOMLScopes)
}

// newEmptyTOMLDoc 返回初始化好的空文档（目标文件缺失时的候选基底）。
func newEmptyTOMLDoc() *tomlManagedDoc {
	return &tomlManagedDoc{
		sections:      map[string]bool{},
		sectionRanges: map[string]*tomlSectionRange{},
		deleted:       map[int]bool{},
	}
}

// readTargetDoc 读取并解析目标 config.toml（缺失 → 空文档 + 空 hash）。
func (a *codexConfigAdapter) readTargetDoc(target TargetRef) (*tomlManagedDoc, map[string]*tomlSectionRange, string, error) {
	data, err := os.ReadFile(filepath.Join(target.UNCRoot, filepath.FromSlash(CodexConfigFile)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newEmptyTOMLDoc(), map[string]*tomlSectionRange{}, "", nil
		}
		return nil, nil, "", fmt.Errorf("read distro config.toml: %w", err)
	}
	doc, ranges, perr := parseTOMLManaged(data, codexTOMLScopes)
	if perr != nil {
		return nil, nil, hexHash(data), fmt.Errorf("%w: distro config.toml: %v", ErrTargetCorrupt, perr)
	}
	return doc, ranges, hexHash(data), nil
}

// desiredFrom 从来源文档抽取期望键集（选择集过滤 + 引用诊断）。
func (a *codexConfigAdapter) desiredFrom(doc *tomlManagedDoc, selected map[string]bool) (map[string]any, []string) {
	out := map[string]any{}
	var notes []string
	if doc == nil {
		return out, nil
	}
	for _, tomlKey := range doc.Keys() {
		root, name, ok := splitCodexSectionKey(tomlKey)
		if !ok || strings.Contains(name, "/") {
			notes = append(notes, fmt.Sprintf("TOML 节名 %q 无法按路径投影（含分隔符/非受管），跳过", tomlKey))
			continue
		}
		if selected != nil && !selected[name] {
			continue // 未选择导入：不进候选（预览可见全集）
		}
		val, has := doc.SectionValue(tomlKey)
		if !has {
			continue
		}
		out[root+"/"+name] = val
	}
	return out, notes
}

// splitCodexSectionKey 拆 "mcp_servers.foo" → ("mcp_servers","foo", true)。
func splitCodexSectionKey(tomlKey string) (root, name string, ok bool) {
	i := strings.IndexByte(tomlKey, '.')
	if i <= 0 || i == len(tomlKey)-1 {
		return "", "", false
	}
	root, name = tomlKey[:i], tomlKey[i+1:]
	if root != "mcp_servers" && root != "profiles" {
		return "", "", false
	}
	return root, name, true
}

func codexKeyToSectionKey(key string) string {
	return strings.Replace(key, "/", ".", 1)
}

func (a *codexConfigAdapter) analyze(target TargetRef, state *StateEntry) codexAnalysis {
	ana := codexAnalysis{desired: map[string]any{}, subs: map[string]SubHash{}}
	if state != nil && state.Detached {
		ana.blocked = "detached"
		return ana
	}
	selected := map[string]bool{} // 空 = 全不选（nil 会被当作不过滤，禁止）
	if state != nil {
		for k := range state.SelectedKeys {
			selected[k] = true
		}
		if state.Subs != nil {
			ana.subs = state.Subs
		}
	}
	srcDoc, _, err := a.readSourceDoc()
	if err != nil {
		ana.blocked = "source-unavailable"
		if errors.Is(err, errTomlParse) || errors.Is(err, errTomlUnsupportedForm) {
			ana.blocked = "source-corrupt"
		}
		return ana
	}
	ana.desired, ana.notes = a.desiredFrom(srcDoc, selected)
	if srcDoc != nil {
		ana.importable, _ = a.desiredFrom(srcDoc, nil) // 全量集：预览勾选面
	}
	tgtDoc, _, tgtHash, terr := a.readTargetDoc(target)
	if terr != nil {
		if errors.Is(terr, ErrTargetCorrupt) {
			ana.blocked = "target-corrupt"
			return ana
		}
		ana.blocked = "target-unreadable"
		return ana
	}
	ana.targetDoc = tgtDoc
	ana.targetHash = tgtHash
	ana.decisions = resolveKeyed(ana.desired, func(key string) (any, bool) {
		v, ok := tgtDoc.SectionValue(codexKeyToSectionKey(key))
		return v, ok
	}, ana.subs)
	return ana
}

// Plan 纯计算：复用键级聚合（S2/S4 共用语义）。
func (a *codexConfigAdapter) Plan(_ SupplyRequest, snap *Snapshot, state *StateEntry) (PlannedUnit, error) {
	target := TargetRef{}
	if snap != nil {
		target = snap.Target
	}
	ana := a.analyze(target, state)
	return planFromKeyedDecisions(ana.blocked, ana.decisions, CodexConfigFile, codexConfigTransform, ana.targetHash), nil
}

// PreviewKeys 逐键预览（含转换前→转换后对照与选择状态）。
func (a *codexConfigAdapter) PreviewKeys(snap *Snapshot, state *StateEntry) ([]KeyPreview, string) {
	target := TargetRef{}
	if snap != nil {
		target = snap.Target
	}
	ana := a.analyze(target, state)
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
	if a.facts != nil {
		if got, err := a.facts.TransformFacts(context.Background(), target); err == nil {
			facts, factsOK = got, true
		}
	}
	out := make([]KeyPreview, 0, len(ana.decisions))
	for _, d := range ana.decisions {
		kp := KeyPreview{Key: d.Key, Action: d.Kind.String(), Reason: d.Reason}
		kp.Selected = selected[keyLeafName(d.Key)]
		if srcVal, ok := ana.desired[d.Key]; ok {
			kp.Importable = d.Kind != keyFrozenBothChanged
			before, after := summarizeMCPServerEntry(d.Key, srcVal, facts)
			if strings.HasPrefix(d.Key, "mcp_servers/") {
				kp.Before, kp.After = before, after
				if !factsOK && after == "" {
					kp.After = "转换需实测验证（应用时执行）"
				}
			} else {
				kp.Before = "profile（" + strconvItoa(len(toMapKeys(srcVal))) + " 个键）"
			}
		}
		out = append(out, kp)
	}
	// 选择面：未选择且不在判定集内的来源段以「可勾选」呈现。
	seen := map[string]bool{}
	for _, kp := range out {
		seen[kp.Key] = true
	}
	for _, key := range sortedDesiredKeys(ana.importable) {
		if seen[key] {
			continue
		}
		kp := KeyPreview{Key: key, Action: "add", Reason: "未选择导入；勾选后随导入写入", Importable: true}
		if strings.HasPrefix(key, "mcp_servers/") {
			before, after := summarizeMCPServerEntry(key, ana.importable[key], facts)
			kp.Before = before
			if !factsOK && after == "" {
				kp.After = "转换需实测验证（应用时执行）"
			} else {
				kp.After = after
			}
		} else {
			kp.Before = "profile（" + strconvItoa(len(toMapKeys(ana.importable[key]))) + " 个键）"
		}
		out = append(out, kp)
	}
	return out, ""
}

func toMapKeys(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func strconvItoa(n int) string { return fmt.Sprintf("%d", n) }

// Candidate 在 Apply 阶段生成合并候选（TOML 行级编辑；转换失败键保持目标现状）。
func (a *codexConfigAdapter) Candidate(ctx context.Context, target TargetRef, state *StateEntry) (ProjectionCandidate, error) {
	return a.buildCandidate(ctx, target, state, false)
}

// AdoptCandidate 预览后确认接管候选（托管域覆写，域外保留）。
func (a *codexConfigAdapter) AdoptCandidate(ctx context.Context, target TargetRef, state *StateEntry) (ProjectionCandidate, error) {
	return a.buildCandidate(ctx, target, state, true)
}

func (a *codexConfigAdapter) buildCandidate(ctx context.Context, target TargetRef, state *StateEntry, adopt bool) (ProjectionCandidate, error) {
	if state != nil && state.Detached {
		return ProjectionCandidate{}, errors.New("unit is detached (keep-local); takeover required to re-enable")
	}
	selected := map[string]bool{} // 空 = 全不选（nil 会被当作不过滤，禁止）
	if state != nil {
		for k := range state.SelectedKeys {
			selected[k] = true
		}
	}
	srcDoc, _, err := a.readSourceDoc()
	if err != nil {
		return ProjectionCandidate{}, err
	}
	desired, notes := a.desiredFrom(srcDoc, selected)
	tgtDoc, _, _, terr := a.readTargetDoc(target)
	if terr != nil {
		return ProjectionCandidate{}, terr
	}

	var decisions []keyedDecision
	subs := map[string]SubHash{}
	if state != nil && state.Subs != nil {
		for k, v := range state.Subs {
			subs[k] = v
		}
	}
	if adopt {
		keys := make([]string, 0, len(desired))
		for k := range desired {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			kind := keyUpdate
			if _, exists := tgtDoc.SectionValue(codexKeyToSectionKey(k)); !exists {
				kind = keyAdd
			}
			decisions = append(decisions, keyedDecision{Key: k, Kind: kind})
		}
		for k := range subs {
			if _, inDesired := desired[k]; !inDesired {
				sub := subs[k]
				if v, ok := tgtDoc.SectionValue(codexKeyToSectionKey(k)); ok && keyedValueHash(v) == sub.Written {
					decisions = append(decisions, keyedDecision{Key: k, Kind: keyRemove})
				} else {
					notes = append(notes, fmt.Sprintf("键 %s：来源已删除但 WSL 侧已被修改，接管时保留本地", k))
				}
			}
		}
	} else {
		decisions = resolveKeyed(desired, func(key string) (any, bool) {
			v, ok := tgtDoc.SectionValue(codexKeyToSectionKey(key))
			return v, ok
		}, subs)
	}

	// 事实收集：存在可写 mcp 键或携带路径形态的 profile 值时探测。
	needFacts := adopt
	for _, d := range decisions {
		if d.Kind.writable() && strings.HasPrefix(d.Key, "mcp_servers/") {
			needFacts = true
			break
		}
	}
	var facts TransformFacts
	if needFacts && a.facts != nil {
		if got, factsErr := a.facts.TransformFacts(ctx, target); factsErr == nil {
			facts = got
		} else {
			notes = append(notes, "目标形态事实探测失败，本次未做命令/路径转换（不生成未验证条目）")
		}
	}

	wroteAny, blockedAny := false, false
	writtenThisRound := map[string]SubHash{} // F2：仅本轮写入/更新的键参与自校验
	var removedThisRound []string
	for _, d := range decisions {
		srcVal, srcOK := desired[d.Key]
		switch d.Kind {
		case keyAdd, keyUpdate:
			converted, ok, note := a.convertEntry(d.Key, srcVal, facts)
			if !ok {
				notes = append(notes, fmt.Sprintf("键 %s：%s（保持 WSL 现状）", d.Key, note))
				blockedAny = true
				continue
			}
			if err := tgtDoc.UpsertSection(codexKeyToSectionKey(d.Key), toSectionMap(converted)); err != nil {
				notes = append(notes, fmt.Sprintf("键 %s：TOML 序列化受阻（%v），保持 WSL 现状", d.Key, err))
				blockedAny = true
				continue
			}
			subs[d.Key] = SubHash{Source: keyedValueHash(srcVal), Written: keyedValueHash(converted)}
			writtenThisRound[d.Key] = subs[d.Key]
			wroteAny = true
		case keyRemove:
			tgtDoc.RemoveSection(codexKeyToSectionKey(d.Key))
			delete(subs, d.Key)
			removedThisRound = append(removedThisRound, d.Key)
			wroteAny = true
		case keyNone:
			if prev, ok := subs[d.Key]; ok && srcOK {
				prev.Source = keyedValueHash(srcVal)
				subs[d.Key] = prev
			}
		default:
			blockedAny = true
			notes = append(notes, fmt.Sprintf("键 %s：%s", d.Key, d.Reason))
		}
	}
	if !wroteAny && blockedAny {
		return ProjectionCandidate{}, errKeyedNoWrite{notes: notes, reason: a.noWriteReason(decisions)}
	}
	content := tgtDoc.Bytes()
	if vErr := verifyTOMLCandidate(content, writtenThisRound, removedThisRound); vErr != nil {
		return ProjectionCandidate{}, fmt.Errorf("codex candidate self-check failed (target kept): %w", vErr)
	}
	outcome := "written"
	if blockedAny {
		outcome = "partial"
	}
	return ProjectionCandidate{
		Content:          content,
		SourceHash:       keyedAggregateHash(desired),
		TransformVersion: codexConfigTransform,
		Notes:            notes,
		Subs:             subs,
		Outcome:          outcome,
	}, nil
}

// convertEntry 转换一个期望键值：mcp server 走 §五确定规则；profile 子树做
// 路径可移植性检查（不可映射 → 该节保持现状）。附带 model_provider 引用诊断。
func (a *codexConfigAdapter) convertEntry(key string, v any, facts TransformFacts) (any, bool, string) {
	entry, ok := v.(map[string]any)
	if !ok {
		return nil, false, "节值不是表，需手动适配"
	}
	if strings.HasPrefix(key, "mcp_servers/") {
		result := TransformMCPServer(entry, facts)
		if result.Outcome == TransformRejected {
			return nil, false, result.Reason
		}
		return result.Entry, true, ""
	}
	converted, ok := TransformSettingValue(entry, facts)
	if !ok {
		return nil, false, "profile 携带不可映射的 Windows 路径值，本次剔除该节"
	}
	return converted, true, ""
}

// noWriteReason 归纳无可写键的主导原因（与 keyedProjectionAdapter 同词汇）。
func (a *codexConfigAdapter) noWriteReason(decisions []keyedDecision) string {
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

// verifyTOMLCandidate 候选自校验：重新解析必须成功；仅对本轮实际写入/更新
// （或基准刷新）的键断言段存在且值与写入基准一致，被移除的段断言不存在
// （round-trip 保真证明；失败则不提交）。
// 范围约束（S4 diting F2）：冻结/未托管/接管时保留本地的键携带陈旧基准
// （目标侧为用户手改值，本就不等于上次写入）——它们不在本轮编辑面内，
// 不得纳入校验；否则任一陈旧基准漂移会否决整候选，健康键更新无法落盘。
func verifyTOMLCandidate(content []byte, written map[string]SubHash, removed []string) error {
	doc, _, err := parseTOMLManaged(content, codexTOMLScopes)
	if err != nil {
		return fmt.Errorf("re-parse: %w", err)
	}
	for key, sub := range written {
		got, ok := doc.SectionValue(codexKeyToSectionKey(key))
		if !ok {
			return fmt.Errorf("section %q missing after edit", key)
		}
		if keyedValueHash(got) != sub.Written {
			return fmt.Errorf("section %q value drift after edit", key)
		}
	}
	for _, key := range removed {
		if got, ok := doc.SectionValue(codexKeyToSectionKey(key)); ok {
			return fmt.Errorf("section %q still present after removal (len=%d)", key, len(got))
		}
	}
	return nil
}

// toSectionMap 确保值是 map（TOML 段本体）。
func toSectionMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// CodexMCPServerEntries 提取 TOML 中全部 mcp_servers 段的解析值（App 层
// FactsSource 预扫描待验证路径用；解析失败返回错误，调用方按空集处理）。
func CodexMCPServerEntries(toml []byte) ([]map[string]any, error) {
	doc, _, err := parseTOMLManaged(toml, []string{"mcp_servers"})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0)
	for _, key := range doc.Keys() {
		if root, _, ok := splitCodexSectionKey(key); !ok || root != "mcp_servers" {
			continue
		}
		if val, has := doc.SectionValue(key); has && val != nil {
			out = append(out, val)
		}
	}
	return out, nil
}
