package wslsupply

// skills.go — 技能资源单元（shared/skill/<name>）的作用域与计划逻辑（P3-S3）。
//
// SkillsScope 持有 Windows ~/.agents/skills 的有界扫描缓存与后台扫描状态：
// 扫描按 skill 目录分批推进（每批一个目录，天然受单 skill 上限约束），进度
// 对外可见；「扫描未完成」的 skill 不冒充完成、不可选入供给。启动链不触发
// 技能扫描（RunUnits 的启动单元清单不含技能单元——大目录绝不阻塞会话启动）。
//
// 单元语义（TrackedProjection，最小登记单元 = skill 目录）：
//   - 无记录 + 目标目录已存在 → unmanaged-existing 冻结（预览后接管）；
//   - 有记录 + 目标树 hash ≠ 上次写入 → WSL 手改，冻结该单元；
//   - 有记录 + 目标被删 → local-deletion-frozen（不自动补回用户删除）；
//   - 双侧同变 → both-changed-frozen（保留 WSL 现状，不以时间戳仲裁）；
//   - 来源清单未变且目标等于上次写入 → 跳过；来源变且目标未手改 → 更新；
//   - 来源（Windows 侧目录）已消失 → source-deleted（删除需确认，不自动删）。
// per-target 同意门：技能是用户资产跨界导入，未同意前恒 awaiting-consent
//（与 S1 视觉清单/S2 键级投影同一 GrantUnitConsent 机制与跨代语义）。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// isSkillUnitID 报告单元是否为技能单元。
func isSkillUnitID(id UnitID) bool { _, ok := ParseSkillUnit(id); return ok }

// SkillUnitPrefix 是技能单元 id 前缀（shared 作用域内动态单元）。
const SkillUnitPrefix = "shared/skill/"

// SkillUnitID 由 skill 目录名构造单元 id。
func SkillUnitID(name string) UnitID { return UnitID(SkillUnitPrefix + name) }

// ParseSkillUnit 解析技能单元 id；name 非法（空/含分隔符/点开头）返回 false。
func ParseSkillUnit(id UnitID) (string, bool) {
	s := string(id)
	if !strings.HasPrefix(s, SkillUnitPrefix) {
		return "", false
	}
	name := strings.TrimPrefix(s, SkillUnitPrefix)
	if name == "" || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") ||
		name == "." || name == ".." || strings.Contains(name, "\x00") {
		return "", false
	}
	return name, true
}

// SkillsScanProgress 是后台扫描进度（按目录计）。
type SkillsScanProgress struct {
	Scanned int // 已完成扫描的 skill 目录数
	Total   int // 发现的 skill 目录总数
}

// SkillsScanSnapshot 是扫描缓存的脱敏快照（供状态面/预览面）。
type SkillsScanSnapshot struct {
	Started     bool
	Running     bool
	Progress    SkillsScanProgress
	RootMissing bool // 宿主 skills 根不存在（无技能可共享，非错误）
	Skills      []*SkillManifest
}

// Incomplete 报告是否存在「扫描未完成」的 skill（状态面显示不冒充完成）。
func (s *SkillsScanSnapshot) Incomplete() bool {
	for _, m := range s.Skills {
		if m.Incomplete {
			return true
		}
	}
	return false
}

// SkillsScope 管理宿主技能根的扫描缓存与后台扫描。并发安全；扫描单飞。
type SkillsScope struct {
	root   string
	limits SkillsScanLimits

	mu       sync.Mutex
	started  bool
	running  bool
	skills   map[string]*SkillManifest
	progress SkillsScanProgress
}

// NewSkillsScope 创建技能作用域。root 是宿主侧 skills 根（Windows 路径）。
func NewSkillsScope(root string, limits SkillsScanLimits) *SkillsScope {
	return &SkillsScope{root: root, limits: limits.withDefaults(), skills: map[string]*SkillManifest{}}
}

// Root 返回宿主 skills 根（App 层展示与 e2e 使用）。
func (s *SkillsScope) Root() string { return s.root }

// Limits 返回生效的扫描上限。
func (s *SkillsScope) Limits() SkillsScanLimits { return s.limits }

// StartScanAsync 单飞启动后台扫描（已在跑/已启动过幂等返回）。扫描按目录分批：
// 每批一个 skill 目录，批间释放锁让 Snapshot 可见中间进度。
func (s *SkillsScope) StartScanAsync() bool {
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

// Scan 同步执行一轮全量重扫（后台包装与测试使用；有界）。已启动标记置位。
func (s *SkillsScope) Scan() {
	dirs, err := listSkillDirs(s.root)
	if err != nil {
		// 根不存在/不可读：清空缓存并标记已启动（空快照，RootMissing 由
		// Snapshot 报告）——读错误不冒充零技能完成态。
		s.mu.Lock()
		s.started = true
		s.skills = map[string]*SkillManifest{}
		s.progress = SkillsScanProgress{}
		s.mu.Unlock()
		return
	}
	budget := 0
	for i, name := range dirs {
		manifest := scanSkillDir(s.root, name, s.limits, &budget)
		if budget > s.limits.MaxTotalFiles {
			manifest.markIncomplete("total-file-limit")
		}
		s.mu.Lock()
		s.started = true
		s.skills[name] = manifest
		s.progress = SkillsScanProgress{Scanned: i + 1, Total: len(dirs)}
		s.mu.Unlock()
	}
	s.mu.Lock()
	// 删除本轮已消失的缓存项（增量基线跟随删除）。
	for name := range s.skills {
		found := false
		for _, d := range dirs {
			if d == name {
				found = true
				break
			}
		}
		if !found {
			delete(s.skills, name)
		}
	}
	s.mu.Unlock()
}

// Snapshot 返回扫描缓存快照（只读，不触发扫描）。
func (s *SkillsScope) Snapshot() SkillsScanSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := SkillsScanSnapshot{
		Started: s.started, Running: s.running, Progress: s.progress,
		Skills: make([]*SkillManifest, 0, len(s.skills)),
	}
	if s.root != "" {
		if _, err := os.Stat(s.root); err != nil && os.IsNotExist(err) {
			snap.RootMissing = true
		}
	}
	names := make([]string, 0, len(s.skills))
	for name := range s.skills {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		snap.Skills = append(snap.Skills, s.skills[name])
	}
	return snap
}

// manifest 返回指定 skill 的扫描缓存（未扫描返回 nil）。
func (s *SkillsScope) manifest(name string) *SkillManifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skills[name]
}

// ---------------------------------------------------------------------------
// 计划（目标侧有界只读探测 + TrackedProjection 判定）
// ---------------------------------------------------------------------------

// SkillTargetState 是目标侧 skill 目录的有界扫描结果。TreeHash 与来源清单
// hash 同构（同一 skillTreeHash），同步状态 ⇔ 两侧相等。
type SkillTargetState struct {
	Exists       bool
	TreeHash     string
	FileCount    int
	Unverifiable bool
	Reason       string
}

// scanTargetSkillDir 有界只读扫描目标 skill 目录（UNC 视图）。符号链接按
// 「目标被外部修改」参与 hash（链接目标串入 hash）；读失败/超限 → 不可核验
// （不猜语义）。
func scanTargetSkillDir(uncSkillDir string, limits SkillsScanLimits) SkillTargetState {
	limits = limits.withDefaults()
	if _, err := os.Lstat(uncSkillDir); err != nil {
		if os.IsNotExist(err) {
			return SkillTargetState{}
		}
		return SkillTargetState{Unverifiable: true, Reason: "target-unreadable"}
	}
	var files []SkillFileEntry
	stop := false
	var walk func(dir, relPrefix string, depth int) bool
	walk = func(dir, relPrefix string, depth int) bool {
		if stop {
			return false
		}
		des, err := os.ReadDir(dir)
		if err != nil {
			stop = true
			return false
		}
		for _, e := range des {
			rel := relPrefix + e.Name()
			switch {
			case e.Type()&os.ModeSymlink != 0:
				// F1：符号链接判定先于 IsDir（Windows 目录形态链接同时携带
				// ModeDir|ModeSymlink；按链接条目入 hash，绝不递归下降）。
				target, err := os.Readlink(filepath.Join(dir, e.Name()))
				if err != nil {
					stop = true
					return false
				}
				files = append(files, SkillFileEntry{RelPath: rel, Hash: hexHash([]byte("link:" + target)), LinkTarget: target})
			case e.IsDir():
				if depth+1 > limits.MaxDepth || len(files) > limits.MaxFilesPerSkill {
					stop = true
					return false
				}
				if !walk(filepath.Join(dir, e.Name()), rel+"/", depth+1) {
					return false
				}
			case e.Type().IsRegular():
				data, err := readFileBounded(filepath.Join(dir, e.Name()), limits.MaxFileBytes)
				if err != nil {
					stop = true
					return false
				}
				files = append(files, SkillFileEntry{RelPath: rel, Size: int64(len(data)), Hash: hexHash(data)})
			default:
				files = append(files, SkillFileEntry{RelPath: rel, Hash: hexHash([]byte("nonregular:" + e.Name()))})
			}
			if len(files) > limits.MaxFilesPerSkill {
				stop = true
				return false
			}
		}
		return true
	}
	if !walk(uncSkillDir, "", 0) {
		return SkillTargetState{Exists: true, Unverifiable: true, Reason: "target-scan-incomplete"}
	}
	return SkillTargetState{Exists: true, TreeHash: skillTreeHash(files), FileCount: len(files)}
}

// planSkill 是技能单元的纯判定（目标侧经有界只读 UNC 探测，不写、不 spawn、
// 不执行任何脚本）。consent 门由协调器 Plan 统一前置。
func (c *Coordinator) planSkill(target TargetRef, name string, entry *StateEntry) PlannedUnit {
	unit := PlannedUnit{Unit: SkillUnitID(name), Policy: PolicyTrackedProjection, RelPath: skillRelDir(name)}
	if entry != nil && entry.Detached {
		unit.Action = ActionBlocked
		unit.BlockedReason = "detached"
		return unit
	}
	if c.skills == nil {
		unit.Action = ActionBlocked
		unit.BlockedReason = "skills-scope-unavailable"
		return unit
	}
	snap := c.skills.Snapshot()
	if !snap.Started {
		unit.Action = ActionBlocked
		unit.BlockedReason = "scan-not-ready"
		return unit
	}
	m := c.skills.manifest(name)
	if m == nil {
		// 来源已删除：不自动删除目标（design §三「skills 的删除需确认」）。
		unit.Action = ActionBlocked
		unit.BlockedReason = "source-deleted"
		return unit
	}
	return planTreeUnitCore(target, unit, m, c.skills.Limits(), entry, skillRelDir(name), scanTargetSkillDir)
}

// targetStateFn 是树单元（目录/单文件）的目标侧有界探测函数。
type targetStateFn func(uncPath string, limits SkillsScanLimits) SkillTargetState

// planTreeUnitCore 是树单元（S3 技能目录 + S4 用户资源）的共享纯判定核心。
func planTreeUnitCore(target TargetRef, unit PlannedUnit, m *SkillManifest, limits SkillsScanLimits, entry *StateEntry, targetRel string, probe targetStateFn) PlannedUnit {
	unit.TransformVersion = skillsTransformVersion
	if m.Incomplete {
		unit.Action = ActionBlocked
		unit.BlockedReason = "scan-incomplete"
		return unit
	}
	if reason := m.NotPortableReason(); reason != "" {
		unit.Action = ActionBlocked
		unit.BlockedReason = reason // incompatible-items / empty
		return unit
	}
	if m.TotalBytes > skillCandidateCap {
		// F5：总量超限（候选读取预算与前像封顶对齐）→ 计划期先行阻断并报因，
		// 不静默截断也不无界读入。
		unit.Action = ActionBlocked
		unit.BlockedReason = "oversize-skill"
		return unit
	}
	tgt := probe(filepath.Join(target.UNCRoot, filepath.FromSlash(targetRel)), limits)
	if tgt.Unverifiable {
		unit.Action = ActionBlocked
		unit.BlockedReason = tgt.Reason
		return unit
	}
	recorded := entry != nil && entry.LastWrittenHash != ""
	sourceChanged := recorded && entry.SourceHash != m.SourceHash
	switch {
	case !recorded && tgt.Exists:
		unit.Action = ActionBlocked
		unit.BlockedReason = "unmanaged-existing"
		unit.Freeze = true
	case recorded && !tgt.Exists:
		unit.Action = ActionBlocked
		unit.BlockedReason = "local-deletion-frozen"
		unit.Freeze = true
	case recorded && tgt.TreeHash != entry.LastWrittenHash:
		unit.Action = ActionBlocked
		unit.BlockedReason = "local-modification-frozen"
		if sourceChanged {
			unit.BlockedReason = "both-changed-frozen"
		}
		unit.Freeze = true
	case recorded && !sourceChanged:
		unit.Action = ActionNone
	default: // 首次供给或来源变化且目标未被手改
		unit.Action = ActionWriteProjection
		unit.ExpectedTargetHash = tgt.TreeHash
		unit.CandidateSize = m.TotalBytes
	}
	return unit
}

// skillRelDir 返回 skill 的目标相对目录（skills/<name>）。
func skillRelDir(name string) string { return "skills/" + name }

// skillsTransformVersion 是技能供给的转换版本（实体化规则 v1）。
const skillsTransformVersion = "skills-materialize-v1"

// RegisterSkills 注册技能作用域（S3 装配；生产由 newWSLSupplyCoordinator 注入）。
func (c *Coordinator) RegisterSkills(scope *SkillsScope) {
	if scope != nil {
		c.skills = scope
	}
}

// StartSkillsScan 单飞启动后台技能扫描（已在跑则幂等）。
func (c *Coordinator) StartSkillsScan() bool {
	if c.skills == nil {
		return false
	}
	return c.skills.StartScanAsync()
}

// SkillsSnapshot 返回技能扫描快照（只读缓存，不触发扫描）。
func (c *Coordinator) SkillsSnapshot() SkillsScanSnapshot {
	if c.skills == nil {
		return SkillsScanSnapshot{}
	}
	return c.skills.Snapshot()
}

// SkillsHostRoot 返回宿主技能根（App/e2e 使用）。
func (c *Coordinator) SkillsHostRoot() string {
	if c.skills == nil {
		return ""
	}
	return c.skills.Root()
}

// applySkillPlanned 分发技能单元的 Apply 动作（none/blocked/write），与投影
// 单元同一套冻结/待同意登记语义。
func (c *Coordinator) applySkillPlanned(ctx context.Context, target TargetRef, unit PlannedUnit) UnitResult {
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
		return c.applySkillUnit(ctx, target, unit, false)
	default:
		return UnitResult{Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitFailed,
			Detail: "planned-action-mismatch",
			Err:    fmt.Errorf("unsupported skill action: %d", unit.Action)}
	}
}

// skillIncompatDetail 把不兼容项拼为脱敏明细行（相对路径 + 原因）。
func skillIncompatDetail(items []SkillIncompat) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, fmt.Sprintf("%s：%s", it.RelPath, it.Reason))
	}
	return out
}
