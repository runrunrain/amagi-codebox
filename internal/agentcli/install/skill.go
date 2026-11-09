package install

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SkillTarget 技能安装目标（各自独立的安装/更新/清理/状态查询）。
type SkillTarget string

const (
	SkillTargetAgents SkillTarget = "agents" // ~/.agents/skills/amagi-codebox（默认随应用安装）
	SkillTargetClaude SkillTarget = "claude" // ~/.claude/skills/amagi-codebox
	SkillTargetCodex  SkillTarget = "codex"  // ~/.codex/skills/amagi-codebox
)

// SkillTargets 全部技能目标（稳定顺序）。
var SkillTargets = []SkillTarget{SkillTargetAgents, SkillTargetClaude, SkillTargetCodex}

// skillName 技能目录名（同时是 SKILL.md frontmatter 的 name）。
const skillName = "amagi-codebox"

//go:embed skill/SKILL.md.tmpl
var skillTemplate string

// skillShimPlaceholder 模板中本机 shim 路径的占位符；安装时替换为 JSON
// 字符串形式（strconv.Quote：自带引号与转义，agent 可按字面引用）。
const skillShimPlaceholder = "{{CODEBOX_AGENT_SHIM}}"

func (m *Manager) skillSegments(t SkillTarget) ([]string, error) {
	switch t {
	case SkillTargetAgents:
		return []string{".agents", "skills", skillName}, nil
	case SkillTargetClaude:
		return []string{".claude", "skills", skillName}, nil
	case SkillTargetCodex:
		return []string{".codex", "skills", skillName}, nil
	}
	return nil, fmt.Errorf("未知技能目标：%s", t)
}

// SkillDir 返回目标技能目录的绝对路径。
func (m *Manager) SkillDir(t SkillTarget) (string, error) {
	segs, err := m.skillSegments(t)
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{m.userHome}, segs...)...), nil
}

// skillFiles 组装目标的两份托管文件：SKILL.md（模板渲染，注入本机 shim
// 路径）与 references/commands.md（由命令表生成，见 docgen.go）。
func (m *Manager) skillFiles(t SkillTarget) ([]managedFile, error) {
	dir, err := m.SkillDir(t)
	if err != nil {
		return nil, err
	}
	mark := func(hash string) string { return "<!-- amagi-codebox managed sha256:" + hash + " -->\n" }
	skill := strings.ReplaceAll(skillTemplate, skillShimPlaceholder, strconv.Quote(m.ShimPath()))
	skill = strings.TrimRight(strings.ReplaceAll(skill, "\r\n", "\n"), " \t\n") + "\n\n"
	commands := strings.ReplaceAll(GenerateCommandReference(m.table), "\r\n", "\n")
	return []managedFile{
		{path: filepath.Join(dir, "SKILL.md"), body: skill, mark: mark, mode: 0o644},
		{path: filepath.Join(dir, "references", "commands.md"), body: commands, mark: mark, mode: 0o644},
	}, nil
}

// skillPathBoundary 返回技能目录段与含 references 的逐级检查边界（评审
// D-12：状态/删除/安装共用同一检查面，references 层的符号链接不可透过）。
func (m *Manager) skillPathBoundary(t SkillTarget) (segs []string, withRefs []string, err error) {
	segs, err = m.skillSegments(t)
	if err != nil {
		return nil, nil, err
	}
	withRefs = append(append([]string{}, segs...), "references")
	return segs, withRefs, nil
}

// SkillStatus 查询目标技能状态（SKILL.md 与 references/commands.md 合并
// 判定：任一外部改动 → modified-by-user；任一旧版或缺席而另一份在 →
// outdated；全部在且一致 → up-to-date；全部缺席 → not-installed；路径
//（含 references 目录）含符号链接/非目录项 → invalid（评审 D-12））。
func (m *Manager) SkillStatus(t SkillTarget) Status {
	dir, err := m.SkillDir(t)
	if err != nil {
		return Status{Path: string(t), State: StateInvalid, Message: err.Error()}
	}
	_, withRefs, err := m.skillPathBoundary(t)
	if err != nil {
		return Status{Path: dir, State: StateInvalid, Message: err.Error()}
	}
	if err := ensureDirPath(m.userHome, withRefs, false); err != nil {
		return Status{Path: dir, State: StateInvalid, Message: err.Error()}
	}
	files, err := m.skillFiles(t)
	if err != nil {
		return Status{Path: dir, State: StateInvalid, Message: err.Error()}
	}
	sts := make([]Status, len(files))
	for i, f := range files {
		sts[i] = inspectManaged(f)
	}
	return combineStates(dir, sts)
}

// combineStates 把多份托管文件的状态合并为一个安装面状态。
func combineStates(path string, sts []Status) Status {
	allMissing, allCurrent := true, true
	for _, st := range sts {
		switch st.State {
		case StateNotInstalled:
			allCurrent = false
		case StateUpToDate:
			allMissing = false
		case StateOutdated:
			allMissing = false
			allCurrent = false
		case StateModifiedByUser:
			return Status{Path: path, State: StateModifiedByUser}
		case StateInvalid:
			return Status{Path: path, State: StateInvalid, Message: st.Message}
		}
	}
	switch {
	case allMissing:
		return Status{Path: path, State: StateNotInstalled}
	case allCurrent:
		return Status{Path: path, State: StateUpToDate}
	default:
		return Status{Path: path, State: StateOutdated}
	}
}

// InstallSkill 幂等安装/更新目标技能；任一文件被外部改动则整体保留并返回
// *ConflictedError。显式安装成功会清除该目标的用户清理记录。
func (m *Manager) InstallSkill(t SkillTarget) error {
	_, withRefs, err := m.skillPathBoundary(t)
	if err != nil {
		return err
	}
	if err := ensureDirPath(m.userHome, withRefs, true); err != nil {
		return err
	}
	files, err := m.skillFiles(t)
	if err != nil {
		return err
	}
	sts := make([]Status, len(files))
	for i, f := range files {
		sts[i] = inspectManaged(f)
	}
	if err := assertManageable(sts); err != nil {
		return err
	}
	for i, f := range files {
		if sts[i].State != StateUpToDate {
			if err := atomicWrite(f.path, f.body+f.mark(sha256Hex(f.body)), f.mode); err != nil {
				return err
			}
		}
	}
	return m.setDeclined(string(t), false)
}

// RemoveSkill 清理目标技能（外部改动的文件保留），尽量删除空目录，并记录
// 用户显式清理的选择（应用启动默认安装不再装回）。references 层与安装同
// 边界逐级检查符号链接（评审 D-12）：发现链接一律拒绝，绝不删除（防跨目标
// 误删链接目标里的文件）。
func (m *Manager) RemoveSkill(t SkillTarget) error {
	_, withRefs, err := m.skillPathBoundary(t)
	if err != nil {
		return err
	}
	if err := ensureDirPath(m.userHome, withRefs, false); err != nil {
		return err
	}
	files, err := m.skillFiles(t)
	if err != nil {
		return err
	}
	sts := make([]Status, len(files))
	for i, f := range files {
		sts[i] = inspectManaged(f)
	}
	if err := assertManageable(sts); err != nil {
		return err
	}
	for i, f := range files {
		if sts[i].State != StateNotInstalled {
			if err := os.Remove(f.path); err != nil {
				return err
			}
		}
	}
	if dir, dirErr := m.SkillDir(t); dirErr == nil {
		_ = os.Remove(filepath.Join(dir, "references")) // 仅空目录可删，失败忽略
		_ = os.Remove(dir)
	}
	return m.setDeclined(string(t), true)
}

// InstallDefaults 应用启动时的默认安装：shim 与 agents 技能（对齐
// feishu-sync installDefaults）。用户显式清理过的面不再自动装回；偏好文件
// 损坏时如实经 onError 上报且不安装。单项失败不打断其余项。
func (m *Manager) InstallDefaults(onError func(error)) {
	declined, err := m.declinedDefaults()
	if err != nil {
		if onError != nil {
			onError(err)
		}
		return
	}
	if !declined[prefShim] {
		if err := m.InstallShim(); err != nil && onError != nil {
			onError(err)
		}
	}
	if !declined[prefAgents] {
		if err := m.InstallSkill(SkillTargetAgents); err != nil && onError != nil {
			onError(err)
		}
	}
}
