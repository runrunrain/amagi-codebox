// provenance.go — 共用技能部署来源的真实记录（证据 = 目录 + 受限隐藏标记）。
//
// 背景：市场部署的技能此前与「共用技能」子页之间只能按目录名猜测归属，
// 同名手动条目无法区分。本文件改为凭据制：
//   - DeployPlugin 在自己的 staging stage 内为每个技能写入受限隐藏标记
//     （markerFileName，只读 0444），随 rename 发布——凭据只由成功部署产生，
//     旧版无凭据目录一律不认领；
//   - 标记跟随技能目录移动（skills ↔ skills-disabled 的启停 rename 不触碰
//     目录内容）；
//   - 读取标记只认普通文件、限额 markerMaxBytes、不跟随符号链接（Lstat 预检），
//     并校验版本、市场与技能名，以及技能目录、.agents 和所在技能根的
//     持久文件身份。复制目录不会保留文件身份，启停 rename 则会；
//   - 卸载（UninstallPlugin）只删除标记证据完全匹配的目录：同名手动条目、
//     无凭据目录、其他市场/插件的目录一律保留，且永不触碰市场 clone。
//
// 不扫描任何引擎；归属判定不做市场目录/技能名碰撞推断——只有标记里的
// marketplace/plugin 与查询目标相等才算匹配。
package sharedmarket

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// markerFileName 是技能目录内的来源凭据文件（隐藏点文件，共用技能扫描
	// 只列顶层非隐藏目录，点文件不参与呈现）。
	markerFileName = ".amagi-market-provenance.json"
	// markerVersion 是当前凭据格式版本；不匹配即视为无效（前向兼容留给未来）。
	markerVersion = 2
	// markerMaxBytes 限制凭据文件尺寸（读侧与写侧同限，防超大文件拖垮解析）。
	markerMaxBytes = 4 << 10 // 4KB
	// maxPluginNameLen 限制凭据内插件名长度（插件名是 catalog 透传的不透明
	// 标识，只做长度与空值约束，不做路径段校验）。
	maxPluginNameLen = 200
)

// provenanceMarker 是写入技能目录的凭据载荷。
type provenanceMarker struct {
	Version      int    `json:"version"`
	Marketplace  string `json:"marketplace"`
	Plugin       string `json:"plugin"`
	Skill        string `json:"skill"`
	DeployedAt   string `json:"deployedAt"`
	Instance     string `json:"instance"`
	Agents       string `json:"agents"`
	ActiveRoot   string `json:"activeRoot"`
	DisabledRoot string `json:"disabledRoot"`
}

// writeProvenanceMarker 仅在部署暂存目录内写入只读凭据，随目录 rename 发布。
func writeProvenanceMarker(dir *os.Root, m provenanceMarker) error {
	m.Version = markerVersion
	if !validMarker(m, m.Skill) || m.Instance == "" || m.Agents == "" || m.ActiveRoot == "" || m.DisabledRoot == "" {
		return fmt.Errorf("来源凭据身份无效")
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化来源凭据失败：%v", err)
	}
	if len(b) > markerMaxBytes {
		return fmt.Errorf("来源凭据超限（%d > %d 字节）", len(b), markerMaxBytes)
	}
	// Discard any shipped regular marker; refuse a directory at the reserved name.
	if fi, err := dir.Lstat(markerFileName); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("来源凭据路径不是普通文件")
		}
		if err := dir.Remove(markerFileName); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := dir.OpenFile(markerFileName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	return errors.Join(err, f.Close())
}

// validMarker 校验凭据载荷：版本匹配、市场名是合法单段名、插件名非空且
// 不超长、技能名与所在目录 basename 一致（目录 + 标记共同组成证据，两者
// 对不上即不认领）。
func validMarker(m provenanceMarker, skillName string) bool {
	if m.Version != markerVersion {
		return false
	}
	if !validName(m.Marketplace) || !validName(skillName) {
		return false
	}
	if m.Skill != skillName {
		return false
	}
	plugin := strings.TrimSpace(m.Plugin)
	if plugin == "" || len(plugin) > maxPluginNameLen {
		return false
	}
	return true
}

// readProvenanceMarker 读取并校验 dir 内的凭据：必须是普通文件（拒绝符号
// 链接，Lstat 预检不跟随）、尺寸不超过 markerMaxBytes、内容解析后通过
// validMarker。任一环节失败都按「无凭据」处理（不认领、不报错——坏凭据
// 只意味着证据缺失，不是用户可修复的异常态）。
func readProvenanceMarker(root *os.Root, skillName, agentsID, rootID string, enabled bool) (provenanceMarker, bool) {
	var zero provenanceMarker
	if !validName(skillName) {
		return zero, false
	}
	dir, instance, err := openPlainDir(root, skillName)
	if err != nil {
		return zero, false
	}
	defer func() { _ = dir.Close() }()
	fi, err := dir.Lstat(markerFileName)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > markerMaxBytes {
		return zero, false
	}
	f, err := dir.Open(markerFileName)
	if err != nil {
		return zero, false
	}
	defer f.Close()
	if opened, err := f.Stat(); err != nil || !opened.Mode().IsRegular() || !os.SameFile(fi, opened) {
		return zero, false
	}
	b, err := io.ReadAll(io.LimitReader(f, markerMaxBytes+1))
	if err != nil || int64(len(b)) > markerMaxBytes {
		return zero, false
	}
	var m provenanceMarker
	if err := json.Unmarshal(b, &m); err != nil {
		return zero, false
	}
	wantRoot := m.DisabledRoot
	if enabled {
		wantRoot = m.ActiveRoot
	}
	if !validMarker(m, skillName) || m.Instance != instance || m.Agents != agentsID || wantRoot != rootID {
		return zero, false
	}
	return m, true
}

// SkillOrigin 是一个共用技能目录的部署来源视图（凭据有效才有值）。
type SkillOrigin struct {
	Skill       string `json:"skill"`       // 技能目录名（= 凭据 skill，已校验一致）
	Marketplace string `json:"marketplace"` // 部署来源市场名
	Plugin      string `json:"plugin"`      // 部署来源插件名（catalog 透传标识）
	DeployedAt  string `json:"deployedAt"`  // 部署时间（RFC3339，凭据写入时刻）
	Enabled     bool   `json:"enabled"`     // 位于 skills/ → true；skills-disabled/ → false
	Path        string `json:"path"`        // 技能目录绝对路径
}

// OriginForSkill inspects the exact skill directory selected by the listing.
func OriginForSkill(dir string, enabled bool) *SkillOrigin {
	dir = filepath.Clean(dir)
	rootPath := filepath.Dir(dir)
	agentsPath := filepath.Dir(rootPath)
	if filepath.Base(rootPath) != skillsDir(enabled) || filepath.Base(agentsPath) != agentsDir {
		return nil
	}
	agents, agentsID, err := openAgents(filepath.Dir(agentsPath))
	if err != nil {
		return nil
	}
	defer func() { _ = agents.Close() }()
	root, rootID, err := openPlainDir(agents, skillsDir(enabled))
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	return originForEntry(root, filepath.Base(dir), agentsID, rootID, enabled)
}

func originForEntry(root *os.Root, name, agentsID, rootID string, enabled bool) *SkillOrigin {
	m, ok := readProvenanceMarker(root, name, agentsID, rootID, enabled)
	if !ok {
		return nil
	}
	return &SkillOrigin{Skill: name, Marketplace: m.Marketplace, Plugin: m.Plugin,
		DeployedAt: m.DeployedAt, Enabled: enabled, Path: filepath.Join(root.Name(), name)}
}

// disabledSkillsRoot 返回停用态技能根 ~/.agents/skills-disabled（与
// sharedskills 同根同语义）。
func (s *Service) disabledSkillsRoot() string {
	return filepath.Join(s.userHome, ".agents", "skills-disabled")
}

// skillRootCandidates 是启停两个技能根的遍历顺序（启用根优先，与
// sharedskills.List 的同名冲突取舍一致）。
func (s *Service) skillRootCandidates() []struct {
	root    string
	enabled bool
} {
	return []struct {
		root    string
		enabled bool
	}{{s.skillsRoot(), true}, {s.disabledSkillsRoot(), false}}
}

// scanSkillOrigins 单次扫描两个技能根的顶层非隐藏目录，返回「技能绝对路径 →
// 有效来源凭据」。只读顶层（技能根下没有嵌套部署形态）；ReadDir 失败（根
// 不存在等）按空处理并记日志，不阻断市场列表。
func (s *Service) scanSkillOrigins() map[string]SkillOrigin {
	out := make(map[string]SkillOrigin)
	agents, agentsID, err := openAgents(s.userHome)
	if err != nil {
		return out
	}
	defer func() { _ = agents.Close() }()
	for _, cand := range s.skillRootCandidates() {
		root, rootID, err := openPlainDir(agents, skillsDir(cand.enabled))
		if err != nil {
			continue
		}
		entries, err := fs.ReadDir(root.FS(), ".")
		if err != nil {
			_ = root.Close()
			if !errors.Is(err, fs.ErrNotExist) && s.log != nil {
				s.log.Warn("sharedmarket", "读取共用技能根失败，本轮跳过来源枚举", fmt.Sprintf("root=%s err=%v", cand.root, err))
			}
			continue
		}
		for _, e := range entries {
			// 只认普通目录（ReadDir 的 DirEntry 对符号链接报 symlink 类型，
			// IsDir 为 false）；隐藏目录（.deploy-staging 等）不参与。
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if origin := originForEntry(root, e.Name(), agentsID, rootID, cand.enabled); origin != nil {
				out[origin.Path] = *origin
			}
		}
		_ = root.Close()
	}
	return out
}

// ListSkillOrigins 枚举两个技能根下全部带有效来源凭据的共用技能（按技能名
// 排序，同名时按路径）。无凭据目录不出现在结果里——这是「共用技能页消除按名猜测」的批量
// 数据源；单条查询用 SharedSkillOrigin。
func (s *Service) ListSkillOrigins() ([]SkillOrigin, error) {
	origins := s.scanSkillOrigins()
	out := make([]SkillOrigin, 0, len(origins))
	for _, o := range origins {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Skill == out[j].Skill {
			return out[i].Path < out[j].Path
		}
		return out[i].Skill < out[j].Skill
	})
	return out, nil
}

// SharedSkillOrigin 读取单个共用技能目录的来源凭据：
//   - 目录不存在于任何技能根 → 明确 error；
//   - 目录存在但无有效凭据 → (nil, nil)（不认领：可能是手动条目或旧版部署）；
//   - 目录是符号链接/非目录 → error（与 sharedskills.locate 同口径拒绝）。
func (s *Service) SharedSkillOrigin(skillName string) (*SkillOrigin, error) {
	if !validName(skillName) {
		return nil, fmt.Errorf("无效的技能名 %q：不允许为空、隐藏目录或包含路径分隔符", skillName)
	}
	agents, agentsID, err := openAgents(s.userHome)
	if err != nil {
		return nil, err
	}
	defer func() { _ = agents.Close() }()
	for _, cand := range s.skillRootCandidates() {
		root, id, err := openPlainDir(agents, skillsDir(cand.enabled))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		defer func() { _ = root.Close() }()
		fi, err := root.Lstat(skillName)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return nil, fmt.Errorf("%s 不是普通目录（可能是符号链接），已拒绝读取来源", skillName)
		}
		return originForEntry(root, skillName, agentsID, id, cand.enabled), nil
	}
	return nil, fmt.Errorf("技能 %q 不存在（已同时检查 ~/.agents/skills 与 ~/.agents/skills-disabled）", skillName)
}

// UninstallReport 是一次市场插件卸载的结果。
type UninstallReport struct {
	Marketplace string `json:"marketplace"`
	Plugin      string `json:"plugin"`
	// Removed 是已删除的技能目录名（凭据证据完全匹配的，含停用态目录）。
	Removed []string `json:"removed"`
}

// UninstallPlugin 显式卸载市场插件的已部署共用技能：扫描两个技能根，只删除
// 「目录为普通目录 + 凭据校验通过 + marketplace/plugin 均与参数相等」的目录。
//   - 同名手动条目 / 无凭据目录（含旧版部署）/ 其他市场或插件的目录：一律
//     保留（证据不匹配即不是本插件的部署产物，不做名称碰撞推断）；
//   - 不触碰市场 clone（~/.agents/marketplaces 不在扫描范围内）；
//   - 不要求市场仍在状态文件中登记（凭据自证归属；市场已删除后凭据仍可
//     读，但常规入口在市场详情页）；
//   - 删除失败即中止并披露已移除清单（部分成功不沉默）。
func (s *Service) UninstallPlugin(name, pluginName string) (*UninstallReport, error) {
	if !validName(name) {
		return nil, fmt.Errorf("无效的市场名 %q", name)
	}
	if strings.TrimSpace(pluginName) == "" {
		return nil, fmt.Errorf("插件名不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	report := &UninstallReport{Marketplace: name, Plugin: pluginName, Removed: []string{}}
	agents, agentsID, err := openAgents(s.userHome)
	if errors.Is(err, fs.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = agents.Close() }()
	type openedRoot struct {
		root    *os.Root
		id      string
		enabled bool
	}
	var roots []openedRoot
	for _, cand := range s.skillRootCandidates() {
		root, id, err := openPlainDir(agents, skillsDir(cand.enabled))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		defer func() { _ = root.Close() }()
		roots = append(roots, openedRoot{root, id, cand.enabled})
	}
	for _, cand := range roots {
		entries, err := fs.ReadDir(cand.root.FS(), ".")
		if err != nil {
			return nil, fmt.Errorf("读取技能根目录失败：%w；此前已移除：%s", err, strings.Join(report.Removed, "、"))
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			m, ok := readProvenanceMarker(cand.root, e.Name(), agentsID, cand.id, cand.enabled)
			if !ok || m.Marketplace != name || m.Plugin != pluginName {
				continue // 无凭据或证据不匹配：保留，不按名称推断
			}
			if err := uninstallMatchedSkill(cand.root, agentsID, cand.id, cand.enabled, m); err != nil {
				if len(report.Removed) == 0 {
					return nil, fmt.Errorf("卸载技能 %q 失败：%w", e.Name(), err)
				}
				return nil, fmt.Errorf("卸载技能 %q 失败：%w；此前已移除：%s", e.Name(), err, strings.Join(report.Removed, "、"))
			}
			report.Removed = append(report.Removed, e.Name())
		}
	}
	if s.log != nil {
		s.log.Info("sharedmarket", "卸载市场插件的已部署技能", fmt.Sprintf("market=%s plugin=%s removed=%d", name, pluginName, len(report.Removed)))
	}
	return report, nil
}

// Move the live entry into a private, same-volume directory before committing
// to deletion. Another process can replace the live name after the scan, but
// only the exact deployment inspected at that point may be deleted here.
func uninstallMatchedSkill(root *os.Root, agentsID, rootID string, enabled bool, want provenanceMarker) error {
	name := ".uninstall-" + rand.Text()
	if err := root.Mkdir(name, 0o700); err != nil {
		return fmt.Errorf("创建卸载隔离目录失败：%w", err)
	}
	// Remove only an empty directory. Never recursively clean up a quarantine
	// that might still hold a replacement or a partially removed skill.
	defer func() { _ = root.Remove(name) }()
	quarantine, _, err := openPlainDir(root, name)
	if err != nil {
		return fmt.Errorf("打开卸载隔离目录失败：%w", err)
	}
	defer func() { _ = quarantine.Close() }()
	isolated := filepath.Join(name, want.Skill)
	if err := root.Rename(want.Skill, isolated); err != nil {
		return fmt.Errorf("隔离技能失败：%w", err)
	}
	if got, ok := readProvenanceMarker(quarantine, want.Skill, agentsID, rootID, enabled); !ok || got != want {
		return fmt.Errorf("技能目录或凭据在卸载期间发生变化，未删除；请手动检查并恢复：%s", filepath.Join(root.Name(), isolated))
	}
	if err := quarantine.RemoveAll(want.Skill); err != nil {
		return fmt.Errorf("删除失败，请检查隔离目录 %s：%w", filepath.Join(root.Name(), isolated), err)
	}
	return nil
}
