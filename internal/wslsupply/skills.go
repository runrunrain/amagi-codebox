package wslsupply

// skills.go — 树单元（目录/单文件）的共享计划与目标侧探测原语。
//
// 演进：本文件原是 P3-S3「共享技能」作用域（SkillsScope + shared/skill/<name>
// 同意制投影）。1.3.130 起 WSL 内 ~/.agents/skills 改为受管符号链接直连
// Windows 技能根（见 platform.WSLEnsureSharedAgentsSkillsLink 与
// app_wsl_supply.go 的 EnsureWSLSharedSkillsRoot），同一棵树不再保留两套写入
// 通道——作用域与 shared/skill 单元全部退役（旧登记经 pruneRetiredUnits 剔除）。
//
// 保留下来的只有 S4 资源单元（claude/codex 用户资源目录）仍共用的原语：
//   - SkillsScanProgress / SkillsScanSnapshot：树扫描的进度与快照类型；
//   - SkillTargetState / scanTargetSkillDir：目标侧有界只读探测；
//   - planTreeUnitCore：TrackedProjection 树单元的共享判定核心。

import (
	"os"
	"path/filepath"
)

// SkillsScanProgress 是后台扫描进度（按目录计）。
type SkillsScanProgress struct {
	Scanned int // 已完成扫描的目录数
	Total   int // 发现的目录总数
}

// SkillsScanSnapshot 是扫描缓存的脱敏快照（供状态面/预览面）。
type SkillsScanSnapshot struct {
	Started     bool
	Running     bool
	Progress    SkillsScanProgress
	RootMissing bool // 宿主根不存在（无可共享内容，非错误）
	Skills      []*SkillManifest
}

// Incomplete 报告是否存在「扫描未完成」的目录（状态面显示不冒充完成）。
func (s *SkillsScanSnapshot) Incomplete() bool {
	for _, m := range s.Skills {
		if m.Incomplete {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 计划（目标侧有界只读探测 + TrackedProjection 判定）
// ---------------------------------------------------------------------------

// SkillTargetState 是目标侧目录的有界扫描结果。TreeHash 与来源清单 hash
// 同构（同一 skillTreeHash），同步状态 ⇔ 两侧相等。
type SkillTargetState struct {
	Exists       bool
	TreeHash     string
	FileCount    int
	Unverifiable bool
	Reason       string
}

// scanTargetSkillDir 有界只读扫描目标目录（UNC 视图）。符号链接按
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

// targetStateFn 是树单元（目录/单文件）的目标侧有界探测函数。
type targetStateFn func(uncPath string, limits SkillsScanLimits) SkillTargetState

// planTreeUnitCore 是树单元（S4 用户资源目录/文件）的共享纯判定核心
//（consent 门由协调器 Plan 统一前置）。
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

// skillsTransformVersion 是树单元供给的转换版本（实体化规则 v1）。
const skillsTransformVersion = "skills-materialize-v1"
