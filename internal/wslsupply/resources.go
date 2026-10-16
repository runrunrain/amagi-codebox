package wslsupply

// resources.go — S4 用户资源作用域：把 S3 的「目录单元」机制（有界扫描、静态
// 兼容检查、同意门、TrackedProjection 冻结/CAS/staging 提交/条件撤销）推广到
// 任意 CLI 的用户资源目录（design §四 Claude Code 行「CLAUDE.md、commands/、
// agents/、skills/：选中后导入；持续同步遵循 WSL 手改冻结」与 Codex 行
//「AGENTS.md、用户自有 skills：选中后导入」）。
//
// 单元粒度：单文件资源（CLAUDE.md/AGENTS.md）与目录资源（commands/<name>、
// agents/<name>、skills/<name>）各为一个最小登记单元，单元 id 形如
// "claude/res/commands/foo"、"codex/res/AGENTS.md"。语义与 shared/skill/<name>
// 完全一致（复用 SkillManifest/扫描器/提交原语/补偿），仅目标前缀不同
//（claude 目标根是 HOME，故目标相对路径带 .claude/ 前缀）。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ResourceUnitPrefix 是资源单元 id 前缀（"<cli>/res/"）。
const ResourceUnitPrefix = "/res/"

// ResourceUnitID 构造资源单元 id（cli 如 "claude"；rel 如 "commands/foo"）。
func ResourceUnitID(cli CLI, rel string) UnitID {
	return UnitID(string(cli) + ResourceUnitPrefix + filepath.ToSlash(rel))
}

// ParseResourceUnit 解析资源单元 id；rel 非法（空段/./..、反斜杠、NUL）返回
// false。cli 必须是注册过资源作用域的 CLI（存在性由协调器判定）。
func ParseResourceUnit(id UnitID) (cli CLI, rel string, ok bool) {
	s := string(id)
	i := strings.Index(s, ResourceUnitPrefix)
	if i <= 0 {
		return "", "", false
	}
	cli, rel = CLI(s[:i]), s[i+len(ResourceUnitPrefix):]
	switch cli {
	case CLIClaude, CLICodex:
	default:
		return "", "", false
	}
	if rel == "" || strings.Contains(rel, "\\") || strings.Contains(rel, "\x00") {
		return "", "", false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", "", false
		}
	}
	return cli, rel, true
}

// ResourceLayout 描述一个 CLI 的用户资源布局。
type ResourceLayout struct {
	// HostRoot：Windows 侧资源根（如 C:\Users\u\.claude）。
	HostRoot string
	// Files：单文件资源相对路径（如 "CLAUDE.md"、"AGENTS.md"）。
	Files []string
	// DirUnits：目录资源子目录名（其每个一级子目录是一个单元：commands/agents/skills）。
	DirUnits []string
	// TargetPrefix：目标相对路径前缀（claude 目标根为 HOME → ".claude/"；codex → ""）。
	TargetPrefix string
}

// ResourceScope 管理一个 CLI 资源布局的扫描缓存与后台扫描（并发安全；单飞）。
type ResourceScope struct {
	cli    CLI
	layout ResourceLayout
	limits SkillsScanLimits

	mu        sync.Mutex
	started   bool
	running   bool
	manifests map[string]*SkillManifest // rel → manifest
	progress  SkillsScanProgress
}

// NewResourceScope 创建资源作用域。
func NewResourceScope(cli CLI, layout ResourceLayout, limits SkillsScanLimits) *ResourceScope {
	return &ResourceScope{cli: cli, layout: layout, limits: limits.withDefaults(), manifests: map[string]*SkillManifest{}}
}

// CLI 返回作用域 CLI。
func (s *ResourceScope) CLI() CLI { return s.cli }

// Layout 返回布局（App 展示用）。
func (s *ResourceScope) Layout() ResourceLayout { return s.layout }

// Limits 返回生效的扫描上限。
func (s *ResourceScope) Limits() SkillsScanLimits { return s.limits }

// StartScanAsync 单飞启动后台扫描（幂等）。
func (s *ResourceScope) StartScanAsync() bool {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return false
	}
	s.running = true
	s.mu.Unlock()
	go func() {
		s.Scan()
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()
	return true
}

// Scan 同步执行一轮全量重扫（有界；测试与后台包装共用）。
func (s *ResourceScope) Scan() {
	budget := 0
	next := map[string]*SkillManifest{}
	scanned, total := 0, 0
	for _, f := range s.layout.Files {
		total++
		if m := scanResourceFile(s.layout.HostRoot, f, s.limits, &budget); m != nil {
			next[f] = m
		}
		scanned++
	}
	for _, sub := range s.layout.DirUnits {
		dirs, err := listSkillDirs(filepath.Join(s.layout.HostRoot, sub))
		if err != nil {
			continue // 子目录不存在：该类无单元（非错误）
		}
		for _, name := range dirs {
			rel := sub + "/" + name
			total++
			m := scanSkillDir(s.layout.HostRoot, rel, s.limits, &budget)
			next[rel] = m
			scanned++
		}
	}
	s.mu.Lock()
	s.started = true
	s.manifests = next
	s.progress = SkillsScanProgress{Scanned: scanned, Total: total}
	s.mu.Unlock()
}

// Snapshot 返回扫描缓存快照（只读，不触发扫描；RootMissing 由布局根探测）。
func (s *ResourceScope) Snapshot() SkillsScanSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := SkillsScanSnapshot{
		Started: s.started, Running: s.running, Progress: s.progress,
		Skills: make([]*SkillManifest, 0, len(s.manifests)),
	}
	if s.layout.HostRoot != "" {
		if _, err := os.Stat(s.layout.HostRoot); err != nil && os.IsNotExist(err) {
			snap.RootMissing = true
		}
	}
	names := make([]string, 0, len(s.manifests))
	for name := range s.manifests {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		snap.Skills = append(snap.Skills, s.manifests[name])
	}
	return snap
}

// manifest 返回指定 rel 的扫描缓存（未扫描/不存在返回 nil）。
func (s *ResourceScope) manifest(rel string) *SkillManifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manifests[rel]
}

// targetRel 返回单元的目标相对路径（TargetPrefix + rel）。
func (s *ResourceScope) targetRel(rel string) string {
	return s.layout.TargetPrefix + rel
}

// isFileUnit 报告 rel 是否为布局声明的单文件单元。
func (s *ResourceScope) isFileUnit(rel string) bool {
	for _, f := range s.layout.Files {
		if f == rel {
			return true
		}
	}
	return false
}

// scanResourceFile 扫描一个单文件资源（缺失返回 nil；形态不兼容产出标记
// incompatible 的 manifest，整单元不入供给——与 S3「不产半残」一致）。
func scanResourceFile(root, rel string, limits SkillsScanLimits, budget *int) *SkillManifest {
	limits = limits.withDefaults()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	m := &SkillManifest{Name: rel}
	if *budget++; *budget > limits.MaxTotalFiles {
		return nil // 超总预算：本轮不登记（下次重扫收敛）
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil // 文件缺失：无单元（source-deleted 由登记侧呈现）
	}
	if info.Mode()&os.ModeSymlink != 0 {
		m.Incompatible = append(m.Incompatible, SkillIncompat{RelPath: rel, Code: SkillIncompatAbsoluteLink, Reason: skillIncompatReasonText(SkillIncompatAbsoluteLink)})
		return m
	}
	if !info.Mode().IsRegular() {
		m.Incompatible = append(m.Incompatible, SkillIncompat{RelPath: rel, Code: SkillIncompatNonRegularFile, Reason: skillIncompatReasonText(SkillIncompatNonRegularFile)})
		return m
	}
	m.scanRegularFile(abs, rel, limits)
	m.SourceHash = skillTreeHash(m.Files)
	return m
}

// ---------------------------------------------------------------------------
// 协调器接线（与 shared/skill 同构：同意门 → 计划 → Apply → 预览/接管/停跟随）
// ---------------------------------------------------------------------------

// RegisterResources 注册一个 CLI 的资源作用域（S4 装配）。
func (c *Coordinator) RegisterResources(scope *ResourceScope) {
	if scope == nil {
		return
	}
	if c.resources == nil {
		c.resources = map[CLI]*ResourceScope{}
	}
	c.resources[scope.cli] = scope
}

// resourceScope 返回 CLI 的资源作用域（未注册返回 nil）。
func (c *Coordinator) resourceScope(cli CLI) *ResourceScope { return c.resources[cli] }

// StartCliResourcesScan 单飞启动指定 CLI 的资源后台扫描。
func (c *Coordinator) StartCliResourcesScan(cli CLI) bool {
	if s := c.resourceScope(cli); s != nil {
		return s.StartScanAsync()
	}
	return false
}

// CliResourcesSnapshot 返回指定 CLI 的资源扫描快照。
func (c *Coordinator) CliResourcesSnapshot(cli CLI) SkillsScanSnapshot {
	if s := c.resourceScope(cli); s != nil {
		return s.Snapshot()
	}
	return SkillsScanSnapshot{}
}

// planResourceUnit 是资源单元的纯判定（consent 门由协调器 Plan 前置）。
func (c *Coordinator) planResourceUnit(target TargetRef, unit UnitID, cli CLI, rel string, entry *StateEntry) PlannedUnit {
	scope := c.resourceScope(cli)
	unit2 := PlannedUnit{Unit: unit, Policy: PolicyTrackedProjection}
	if entry != nil && entry.Detached {
		unit2.Action = ActionBlocked
		unit2.BlockedReason = "detached"
		return unit2
	}
	if scope == nil {
		unit2.Action = ActionBlocked
		unit2.BlockedReason = "resources-scope-unavailable"
		return unit2
	}
	unit2.RelPath = scope.targetRel(rel)
	snap := scope.Snapshot()
	if !snap.Started {
		unit2.Action = ActionBlocked
		unit2.BlockedReason = "scan-not-ready"
		return unit2
	}
	m := scope.manifest(rel)
	if m == nil {
		unit2.Action = ActionBlocked
		unit2.BlockedReason = "source-deleted"
		return unit2
	}
	if m.Incomplete {
		unit2.Action = ActionBlocked
		unit2.BlockedReason = "scan-incomplete"
		return unit2
	}
	if reason := m.NotPortableReason(); reason != "" {
		unit2.Action = ActionBlocked
		unit2.BlockedReason = reason
		return unit2
	}
	if m.TotalBytes > skillCandidateCap {
		unit2.Action = ActionBlocked
		unit2.BlockedReason = "oversize-skill"
		return unit2
	}
	if scope.isFileUnit(rel) {
		return planTreeUnitCore(target, unit2, m, scope.Limits(), entry, scope.targetRel(rel), scanTargetFileState)
	}
	return planTreeUnitCore(target, unit2, m, scope.Limits(), entry, scope.targetRel(rel), scanTargetSkillDir)
}

// scanTargetFileState 探测目标侧单文件（hash 基准与树 hash 同构：单条目
// skillTreeHash）。读失败/超限 → 不可核验（不猜语义）。
func scanTargetFileState(uncPath string, limits SkillsScanLimits) SkillTargetState {
	limits = limits.withDefaults()
	info, err := os.Lstat(uncPath)
	if err != nil {
		if os.IsNotExist(err) {
			return SkillTargetState{}
		}
		return SkillTargetState{Unverifiable: true, Reason: "target-unreadable"}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// 用户手改可能留下的链接：按链接条目参与 hash（与 S3 目标扫描一致）。
		tgt, lerr := os.Readlink(uncPath)
		if lerr != nil {
			return SkillTargetState{Exists: true, Unverifiable: true, Reason: "target-unreadable"}
		}
		entry := SkillFileEntry{RelPath: filepath.Base(uncPath), Hash: hexHash([]byte("link:" + tgt))}
		return SkillTargetState{Exists: true, TreeHash: skillTreeHash([]SkillFileEntry{entry}), FileCount: 1}
	}
	if !info.Mode().IsRegular() {
		return SkillTargetState{Exists: true, Unverifiable: true, Reason: "non-regular-target"}
	}
	data, rerr := readFileBounded(uncPath, limits.MaxFileBytes)
	if rerr != nil {
		return SkillTargetState{Exists: true, Unverifiable: true, Reason: "target-unreadable"}
	}
	entry := SkillFileEntry{RelPath: filepath.Base(uncPath), Size: int64(len(data)), Hash: hexHash(data)}
	return SkillTargetState{Exists: true, TreeHash: skillTreeHash([]SkillFileEntry{entry}), FileCount: 1}
}

// applyResourcePlanned 分发资源单元的 Apply 动作。
func (c *Coordinator) applyResourcePlanned(ctx context.Context, target TargetRef, unit PlannedUnit, cli CLI, rel string) UnitResult {
	switch unit.Action {
	case ActionNone:
		return UnitResult{Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitFresh, Detail: "no-change"}
	case ActionBlocked:
		if unit.Freeze {
			c.recordProjectionFreeze(target, unit.Unit, PolicyTrackedProjection, unit.BlockedReason)
		} else if unit.BlockedReason == "awaiting-consent" {
			c.recordProjectionPendingConsent(target, unit.Unit, PolicyTrackedProjection)
		}
		return UnitResult{Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitBlocked, Detail: unit.BlockedReason}
	case ActionWriteProjection:
		return c.applyResourceUnit(ctx, target, unit, cli, rel, false)
	default:
		return UnitResult{Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitFailed,
			Detail: "planned-action-mismatch",
			Err:    fmt.Errorf("unsupported resource action: %d", unit.Action)}
	}
}

// applyResourceUnit 执行资源单元写入（文件单元走 submitFile 原语；目录单元
// 走 submitDirUnit staging 原语）。
func (c *Coordinator) applyResourceUnit(_ context.Context, target TargetRef, planned PlannedUnit, cli CLI, rel string, adopt bool) UnitResult {
	scope := c.resourceScope(cli)
	if scope == nil {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitBlocked,
			Detail: "resources-scope-unavailable", Err: fmt.Errorf("resources scope unavailable")}
	}
	m := scope.manifest(rel)
	if m == nil {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitBlocked, Detail: "source-deleted"}
	}
	if reason := m.NotPortableReason(); reason != "" {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitBlocked, Detail: reason}
	}
	files, err := readSkillCandidateFiles(m, scope.Limits())
	if err != nil {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: "candidate-unavailable", Err: err}
	}
	written := make([]SkillFileEntry, 0, len(files))
	for _, f := range files {
		written = append(written, SkillFileEntry{RelPath: f.RelPath, Size: int64(len(f.Bytes)), Hash: hexHash(f.Bytes)})
	}
	writtenTreeHash := skillTreeHash(written)
	targetRel := scope.targetRel(rel)
	uncTarget := filepath.Join(target.UNCRoot, filepath.FromSlash(targetRel))
	var tgt SkillTargetState
	if scope.isFileUnit(rel) {
		tgt = scanTargetFileState(uncTarget, scope.Limits())
	} else {
		tgt = scanTargetSkillDir(uncTarget, scope.Limits())
	}
	if tgt.Unverifiable {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: tgt.Reason, Err: fmt.Errorf("cannot verify target resource before write")}
	}
	if tgt.TreeHash != planned.ExpectedTargetHash {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: "target-changed-since-planning",
			Err:    fmt.Errorf("target resource changed since planning (external edit retained)")}
	}
	var undo *CompensationHandle
	if scope.isFileUnit(rel) {
		// 单文件：私有 staging 提交原语（0600；普通单元 chmod 失败恢复前像）。
		undo, err = c.submitFile(target, targetRel, files[0].Bytes, false)
	} else {
		undo, err = c.submitDirUnit(target, targetRel, files, writtenTreeHash)
	}
	if err != nil {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: "write-failed", Err: err}
	}
	undo.unit = planned.Unit
	outcome := "written"
	if adopt {
		outcome = "takeover-written"
	}
	notes := skillApplyNotes(m)
	c.recordProjectionState(target, planned.Unit, PolicyTrackedProjection, m.SourceHash, writtenTreeHash, skillsTransformVersion, nil, outcome)
	detail := outcome
	if len(notes) > 0 {
		detail += "; " + strings.Join(notes, "; ")
	}
	return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitApplied, Detail: detail, Undo: undo}
}

// previewResourceUnit 构造资源单元预览（脱敏）。
func (c *Coordinator) previewResourceUnit(target TargetRef, unit UnitID, cli CLI, rel string, entry *StateEntry) UnitPreview {
	scope := c.resourceScope(cli)
	prev := UnitPreview{Unit: unit, Policy: PolicyTrackedProjection, Action: ActionNone}
	if scope == nil {
		prev.BlockedReason = "resources-scope-unavailable"
		return prev
	}
	prev.RelPath = scope.targetRel(rel)
	uncTarget := filepath.Join(target.UNCRoot, filepath.FromSlash(prev.RelPath))
	var tgt SkillTargetState
	if scope.isFileUnit(rel) {
		tgt = scanTargetFileState(uncTarget, scope.Limits())
	} else {
		tgt = scanTargetSkillDir(uncTarget, scope.Limits())
	}
	if !tgt.Unverifiable {
		prev.TargetHash = tgt.TreeHash
	}
	if m := scope.manifest(rel); m != nil {
		for _, it := range m.Incompatible {
			prev.Notes = append(prev.Notes, fmt.Sprintf("不兼容：%s：%s", it.RelPath, it.Reason))
		}
		if len(m.Excluded) > 0 {
			prev.Notes = append(prev.Notes, fmt.Sprintf("将排除 %d 项（.git/node_modules/缓存/凭据/会话产物）", len(m.Excluded)))
		}
		if m.Incomplete {
			prev.Notes = append(prev.Notes, "扫描未完成（"+m.IncompleteReason+"），不可供给")
		}
	}
	if entry != nil && entry.Detached {
		prev.BlockedReason = "detached"
		return prev
	}
	if entry == nil || !entry.Consented {
		prev.Action = ActionBlocked
		prev.BlockedReason = "awaiting-consent"
		prev.Notes = append(prev.Notes, "尚未选择导入（未授权）；勾选导入授权后才会写入")
		if tgt.Exists {
			prev.Notes = append(prev.Notes, "目标已存在内容（授权后将判定为未托管，需预览后确认接管）")
		}
		return prev
	}
	planned := c.planResourceUnit(target, unit, cli, rel, entry)
	prev.Action = planned.Action
	prev.BlockedReason = planned.BlockedReason
	prev.Freeze = planned.Freeze
	return prev
}

// takeOverResource 资源单元的预览后接管（CAS；未同意仍拦截）。
func (c *Coordinator) takeOverResource(ctx context.Context, distro string, unit UnitID, cli CLI, rel, expectedTargetHash string) (UnitResult, error) {
	scope := c.resourceScope(cli)
	if scope == nil {
		return UnitResult{}, fmt.Errorf("resources scope unavailable")
	}
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	target, err := c.ResolveTarget(ctx, distro, cli)
	if err != nil {
		return UnitResult{}, fmt.Errorf("resolve wsl supply target: %w", err)
	}
	st := c.state.load()
	entry := st.entryFor(unit, target.Generation)
	if !entry.Consented {
		return UnitResult{Unit: unit, Policy: PolicyTrackedProjection, Outcome: UnitBlocked,
			Detail: "awaiting-consent",
			Err:    fmt.Errorf("resource is not consented for this target; import it first")}, nil
	}
	uncTarget := filepath.Join(target.UNCRoot, filepath.FromSlash(scope.targetRel(rel)))
	var tgt SkillTargetState
	if scope.isFileUnit(rel) {
		tgt = scanTargetFileState(uncTarget, scope.Limits())
	} else {
		tgt = scanTargetSkillDir(uncTarget, scope.Limits())
	}
	if tgt.Unverifiable {
		return UnitResult{Unit: unit, Policy: PolicyTrackedProjection, Outcome: UnitFailed,
			Detail: tgt.Reason, Err: fmt.Errorf("cannot verify target resource")}, nil
	}
	if tgt.TreeHash != expectedTargetHash {
		return UnitResult{Unit: unit, Policy: PolicyTrackedProjection, Outcome: UnitBlocked,
			Detail: "preview-expired",
			Err:    fmt.Errorf("target resource changed since preview; re-check before takeover")}, nil
	}
	planned := PlannedUnit{Unit: unit, Policy: PolicyTrackedProjection, RelPath: scope.targetRel(rel),
		ExpectedTargetHash: expectedTargetHash, Action: ActionWriteProjection, TransformVersion: skillsTransformVersion}
	return c.applyResourceUnit(ctx, target, planned, cli, rel, true), nil
}

// SetUnitSelection 设置一个键级单元的导入选择集（S4「仅用户选择的单元导入」：
// 选择以目标 generation 为生命周期；空集 = 全不导入，已导入条目走来源删除
// 语义移除）。仅 selectLeaf 单元接受本操作。
func (c *Coordinator) SetUnitSelection(distro string, cli CLI, unit UnitID, keys []string) error {
	adapter, ok := c.projections[unit]
	if !ok {
		// 技能/资源单元不接受键级选择（它们按单元粒度同意）。
		return fmt.Errorf("wsl supply unit %q does not accept key selection", string(unit))
	}
	shapeAware, ok := adapter.(interface{ SelectionAware() bool })
	if !ok || !shapeAware.SelectionAware() {
		return fmt.Errorf("wsl supply unit %q does not accept key selection", string(unit))
	}
	target, err := c.ResolveTarget(context.Background(), distro, cli)
	if err != nil {
		return fmt.Errorf("resolve wsl supply target: %w", err)
	}
	selected := map[string]bool{}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" {
			selected[k] = true
		}
	}
	return c.state.mutate(func(st *StateFile) {
		prev, _ := st.entry(unit)
		next := StateEntry{
			Unit: unit, Policy: adapter.Policy(), Generation: target.Generation,
			SelectedKeys: selected, Consented: prev.Consented,
			SourceHash: prev.SourceHash, LastWrittenHash: prev.LastWrittenHash,
			TransformVersion: prev.TransformVersion, Subs: prev.Subs, Detached: prev.Detached,
			Frozen: prev.Frozen, FrozenReason: prev.FrozenReason,
			LastSuccess: prev.LastSuccess, LastOutcome: prev.LastOutcome,
		}
		if prev.Generation == target.Generation || prev.Generation == "" {
			st.upsertEntry(next, target.Generation)
		} else {
			st.upsertEntry(next, target.Generation)
		}
		st.Distro = target.Distro
	})
}
