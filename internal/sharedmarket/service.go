// Package sharedmarket 管理 ~/.agents/marketplaces 下的共用插件市场：
// 把 Claude Code 插件市场（或任意 git 市场源）以引擎中立的形式纳入 CodeBox
// 统一管理（clone 到 ~/.agents/marketplaces/<name>，状态落 ~/.agents/marketplaces.json），
// 并支持把市场内插件的 skills 部署为 ~/.agents/skills 共用技能（跨 CLI 生效，
// 部署后的技能由「共用技能」子页统一启停管理）。
//
// 设计边界（与既有公共子页对齐）：
//   - 不 import internal/plugin：Claude 注册表只经 App 门面转换成本包的
//     「可 clone 源」（github repo / git url / 本地 installLocation 路径），
//     本包对来源引擎无感知；
//   - git 操作全部走 runGit 注入点（默认 exec git），单测以替身预置 fixture；
//   - 部署只覆盖 skills（commands/agents/hooks/MCP 是 Claude 插件系统语义，
//     无 ~/.agents 对应物——如实按「无技能」反馈，不静默丢内容）。
package sharedmarket

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/logging"
)

// cloneTimeout / pullTimeout 是 git 网络操作预算（官方市场等大仓库以浅克隆
// 控制；pull 走 --ff-only 不产生合并提交）。
const (
	cloneTimeout = 10 * time.Minute
	pullTimeout  = 2 * time.Minute
)

// Marketplace 是一个共用市场的视图（List 时附带完整目录扫描结果）。
type Marketplace struct {
	Name        string   `json:"name"`        // 标识 = clone 目录名
	Source      string   `json:"source"`      // git URL 或本地路径
	ClonePath   string   `json:"clonePath"`   // ~/.agents/marketplaces/<name>
	Description string   `json:"description"` // marketplace.json description
	AddedAt     string   `json:"addedAt"`
	LastUpdated string   `json:"lastUpdated"` // 最近一次成功 clone/pull（RFC3339）
	PluginCount int      `json:"pluginCount"`
	Plugins     []Plugin `json:"plugins"`
	// Missing 为 true 时 clone 目录或 marketplace.json 不可读（如实呈现，
	// Update 会尝试重新 clone 恢复）。
	Missing bool `json:"missing,omitempty"`
}

// Plugin 是市场目录里的一个插件条目（含可部署技能枚举）。
type Plugin struct {
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	Skills      []string `json:"skills"` // 可部署的技能目录名（本地 source 存在的条目）
	// DeployedSkills 是当前仍存在于 ~/.agents/skills（或 skills-disabled）且
	// 来源凭据（provenance 标记）指向本市场本插件的技能目录名。凭据制：同名
	// 但无凭据的目录不计入（不按名称碰撞推断）。
	DeployedSkills []string `json:"deployedSkills"`
	// ExternalSource 为 true 时插件内容不在市场 clone 内（如官方市场的
	// git-subdir 外部引用），无法本地枚举技能/部署。
	ExternalSource bool `json:"externalSource,omitempty"`
	Updatable      bool `json:"updatable"` // 市场目录中有可单独更新的本地来源条目
}

// DeployReport 是一次插件部署的结果（部署到 ~/.agents/skills）。
type DeployReport struct {
	Marketplace string   `json:"marketplace"`
	Plugin      string   `json:"plugin"`
	Deployed    []string `json:"deployed"` // 已部署的技能目录名
	Skipped     []string `json:"skipped"`  // 目标已存在而跳过的技能目录名
	TargetRoot  string   `json:"targetRoot"`
}

// Service 是共用插件市场的管理服务。
type Service struct {
	userHome string
	log      *logging.Service

	// runGit 是 git 执行注入点：默认 exec.CommandContext("git", ...)；
	// 测试替换为替身后，fixture 直接预置在目标 clone 路径。
	runGit func(ctx context.Context, args ...string) (string, error)

	// mu 串行化状态文件读-改-写与 clone 目录增删（Wails 绑定并发触发时
	// 防止交错写丢失/半删状态）。
	mu sync.Mutex
}

// New 创建服务。userHome 是用户主目录（~/.agents 所在）；log 允许 nil。
func New(userHome string, log *logging.Service) *Service {
	return &Service{userHome: userHome, log: log}
}

// Root 返回市场 clone 根目录 ~/.agents/marketplaces。
func (s *Service) Root() string {
	return filepath.Join(s.userHome, ".agents", "marketplaces")
}

func (s *Service) statePath() string {
	return filepath.Join(s.userHome, ".agents", "marketplaces.json")
}

func (s *Service) clonePath(name string) string {
	return filepath.Join(s.Root(), name)
}

// gitRunner 默认实现：执行 git 并返回合并输出（stdout+stderr）。
func defaultRunGit(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *Service) git(ctx context.Context, args ...string) (string, error) {
	if s.runGit == nil {
		return defaultRunGit(ctx, args...)
	}
	return s.runGit(ctx, args...)
}

// ---- 状态文件 ----

type stateFile struct {
	Version      int          `json:"version"`
	Marketplaces []stateEntry `json:"marketplaces"`
}

type stateEntry struct {
	Name            string                    `json:"name"`
	Source          string                    `json:"source"`
	AddedAt         string                    `json:"addedAt"`
	LastUpdated     string                    `json:"lastUpdated"`
	PluginSnapshots map[string]pluginSnapshot `json:"pluginSnapshots,omitempty"`
}

func (s stateFile) marketIndex(name string) int {
	for i, entry := range s.Marketplaces {
		if entry.Name == name {
			return i
		}
	}
	return -1
}

// readState 原子读状态文件；不存在 → 空态（首次使用即空态，不报错）。
func (s *Service) readState() (stateFile, error) {
	state := stateFile{Version: 1}
	b, err := os.ReadFile(s.statePath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return state, nil
		}
		return state, fmt.Errorf("读取市场状态文件失败：%s（%v）", s.statePath(), err)
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return stateFile{}, fmt.Errorf("解析市场状态文件失败：%s（%v）", s.statePath(), err)
	}
	if state.Version < 1 {
		state.Version = 1
	}
	return state, nil
}

// writeState 原子写状态文件（tmp+rename，模式对齐 sharedmcp.atomic）。
func (s *Service) writeState(state stateFile) error {
	if state.Version < 1 {
		state.Version = 1
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化市场状态失败：%v", err)
	}
	dir := filepath.Dir(s.statePath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 ~/.agents 失败：%s（%v）", dir, err)
	}
	tmp := s.statePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("写市场状态临时文件失败：%s（%v）", tmp, err)
	}
	if err := os.Rename(tmp, s.statePath()); err != nil {
		// rename 失败时尽力清理残留 .tmp（失败仅记日志，不掩盖原错误）
		if rmErr := os.Remove(tmp); rmErr != nil && s.log != nil {
			s.log.Debug("sharedmarket", "清理状态临时文件失败（忽略）", fmt.Sprintf("file=%s err=%v", tmp, rmErr))
		}
		return fmt.Errorf("落盘市场状态失败：%s（%v）", s.statePath(), err)
	}
	return nil
}

// ---- 名称与来源解析 ----

// validName 校验市场名（对齐 sharedskills 的边界规则：非空、非隐藏、单段、
// 防路径穿越）。
func validName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.HasPrefix(name, ".") &&
		filepath.Base(name) == name &&
		!strings.ContainsAny(name, `/\:`)
}

// nameFromSource 从 git URL / 本地路径提取市场名：basename 去 .git 后缀。
func nameFromSource(source string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(source), "/")
	if i := strings.LastIndexAny(trimmed, `/\\`); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	trimmed = strings.TrimSuffix(trimmed, ".git")
	if !validName(trimmed) {
		return ""
	}
	return trimmed
}

// ---- 目录扫描（宽容解析，与 internal/plugin catalog 同族规则） ----

type catalogFile struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Plugins     []catalogPlugin `json:"plugins"`
}

type catalogPlugin struct {
	Name        string            `json:"name"`
	Source      json.RawMessage   `json:"source"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Author      map[string]string `json:"author"`
}

func (c catalogFile) findPlugin(name string) (catalogPlugin, bool) {
	for _, plugin := range c.Plugins {
		if plugin.Name == name {
			return plugin, true
		}
	}
	return catalogPlugin{}, false
}

// sourcePath 提取条目 source 的本地相对路径：字符串 source（"./plugins/x"）
// 返回净化后的相对路径；对象 source（git-subdir 外部引用）返回空（外部仓库，
// 本地 clone 内不存在）。
func (p catalogPlugin) sourcePath() string {
	var text string
	if err := json.Unmarshal(p.Source, &text); err != nil {
		return ""
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	cleaned := filepath.Clean(text)
	if filepath.IsAbs(cleaned) {
		return ""
	}
	// 防穿越：拒绝回到 clone 根之上
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return ""
	}
	return cleaned
}

// readCatalog 解析 clone 根下的 .claude-plugin/marketplace.json。
func readCatalog(cloneDir string) (catalogFile, error) {
	b, err := os.ReadFile(filepath.Join(cloneDir, catalogPath))
	if err != nil {
		return catalogFile{}, err
	}
	var catalog catalogFile
	if err := json.Unmarshal(b, &catalog); err != nil {
		return catalogFile{}, fmt.Errorf("解析 marketplace.json 失败（%v）", err)
	}
	return catalog, nil
}

// pluginDir 解析插件在 clone 内的目录：source 相对路径 → clone/<source>；
// 无本地 source 的条目返回空串（外部引用）。
func pluginDir(cloneDir string, entry catalogPlugin) string {
	rel := entry.sourcePath()
	if rel == "" {
		return ""
	}
	return filepath.Join(cloneDir, rel)
}

// withinRoot 验证 target 解析符号链接后仍位于 root 内（含祖先链接穿越防护：
// 仓库里 source 指向符号链接祖先时，解析后的真实路径可能落到 clone 之外）。
// 任一路径 EvalSymlinks 失败（不存在/不可达）即视为越界。
func withinRoot(root, target string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// isRegularFile 判定 path 是普通文件（Lstat 不跟随符号链接：符号链接的
// SKILL.md 不得视为可部署内容）。
func isRegularFile(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode().IsRegular()
}

// skillDirs 枚举插件目录内可部署的技能：插件根有 SKILL.md → 整个插件目录
// 即一个技能（名为插件名）；否则取 skills/ 下每个含 SKILL.md 的子目录。
// 安全规则（F01/F08）：名称段（插件名/子目录名）必须通过 validName（拒绝
// "../x"、隐藏名、多段名）；目录必须经 withinRoot 验证仍在 clone 内
// （防符号链接祖先穿越）；SKILL.md 必须是普通文件（拒绝链接形态）。
func skillDirs(pluginName, dir, cloneRoot string) []deployableSkill {
	if dir == "" || !validName(pluginName) {
		return nil
	}
	if !withinRoot(cloneRoot, dir) {
		return nil
	}
	var out []deployableSkill
	if isRegularFile(filepath.Join(dir, "SKILL.md")) {
		return []deployableSkill{{Name: pluginName, Dir: dir}}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "skills"))
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || !validName(e.Name()) {
			continue
		}
		sub := filepath.Join(dir, "skills", e.Name())
		if !withinRoot(cloneRoot, sub) {
			continue
		}
		if isRegularFile(filepath.Join(sub, "SKILL.md")) {
			out = append(out, deployableSkill{Name: e.Name(), Dir: sub})
		}
	}
	return out
}

// deployableSkill 是一个可部署技能（名称段已验证 + 目录已验证在 clone 内）。
type deployableSkill struct {
	Name string
	Dir  string
}

// scanPlugins 扫描 clone 目录，返回市场视图。dirMissing 时置 Missing。
// origins 是两个技能根的来源凭据快照（scanSkillOrigins），用于组装每个
// 插件的 DeployedSkills（凭据匹配才算已部署，供卸载入口呈现）。
func (s *Service) scanPlugins(entry stateEntry, origins map[string]SkillOrigin) Marketplace {
	market := Marketplace{
		Name:        entry.Name,
		Source:      entry.Source,
		ClonePath:   s.clonePath(entry.Name),
		AddedAt:     entry.AddedAt,
		LastUpdated: entry.LastUpdated,
		Plugins:     []Plugin{},
	}

	catalog, err := readCatalog(market.ClonePath)
	if err != nil {
		market.Missing = true
		if s.log != nil {
			s.log.Warn("sharedmarket", "市场 clone 不可读，标记 Missing", fmt.Sprintf("%s: %v", entry.Name, err))
		}
		catalog = catalogFile{}
	}
	market.Description = catalog.Description
	deployed := map[string][]string{}
	for _, origin := range origins {
		if origin.Marketplace == entry.Name {
			deployed[origin.Plugin] = append(deployed[origin.Plugin], origin.Skill)
		}
	}

	for _, cp := range catalog.Plugins {
		if strings.TrimSpace(cp.Name) == "" {
			continue
		}
		plugin := s.scanPlugin(entry, cp, deployed[cp.Name])
		delete(deployed, cp.Name)
		market.Plugins = append(market.Plugins, plugin)
	}
	// A catalog refresh can remove a plugin; retain an uninstall entry for its deployments.
	remaining := make([]string, 0, len(deployed))
	for name := range deployed {
		remaining = append(remaining, name)
	}
	sort.Strings(remaining)
	for _, name := range remaining {
		sort.Strings(deployed[name])
		market.Plugins = append(market.Plugins, Plugin{Name: name, Skills: []string{},
			Description: "当前市场目录不含此插件，仅可卸载已部署技能", DeployedSkills: deployed[name]})
	}
	market.PluginCount = len(market.Plugins)
	return market
}

func (s *Service) scanPlugin(market stateEntry, entry catalogPlugin, deployed []string) Plugin {
	entry, dir, root := s.pluginContent(market, entry)
	plugin := Plugin{
		Name:           entry.Name,
		Version:        entry.Version,
		Description:    entry.Description,
		Author:         strings.TrimSpace(entry.Author["name"]),
		Skills:         []string{},
		DeployedSkills: append([]string{}, deployed...),
		ExternalSource: dir == "",
		Updatable:      dir != "" && validName(entry.Name),
	}
	for _, skill := range skillDirs(entry.Name, dir, root) {
		plugin.Skills = append(plugin.Skills, skill.Name)
	}
	sort.Strings(plugin.Skills)
	sort.Strings(plugin.DeployedSkills)
	return plugin
}

// ---- 对外方法 ----

// List 返回全部共用市场（状态 + 实时目录扫描，按名称排序）。
func (s *Service) List() ([]Marketplace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState()
	if err != nil {
		return nil, err
	}
	out := make([]Marketplace, 0, len(state.Marketplaces))
	origins := s.scanSkillOrigins() // 单次扫描两个技能根，供全部市场复用
	for _, entry := range state.Marketplaces {
		out = append(out, s.scanPlugins(entry, origins))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Add 从 git 源（URL 或本地路径）新增共用市场：浅 clone 到
// ~/.agents/marketplaces/<name>，校验 marketplace.json 后落状态。
// nameHint 非空时优先用作市场名（从 Claude 注册表导入时保持同名）；
// 缺省从源地址 basename 推导（去 .git）。同名已存在 → 明确拒绝。
func (s *Service) Add(source, nameHint string) (Marketplace, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return Marketplace{}, fmt.Errorf("市场源不能为空")
	}
	name := strings.TrimSpace(nameHint)
	if name == "" {
		name = nameFromSource(source)
	}
	if !validName(name) {
		return Marketplace{}, fmt.Errorf("无效的市场名 %q：不允许为空、隐藏目录或包含路径分隔符", name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState()
	if err != nil {
		return Marketplace{}, err
	}
	for _, entry := range state.Marketplaces {
		if entry.Name == name {
			return Marketplace{}, fmt.Errorf("共用市场 %q 已存在（源 %s）；如需拉取最新请使用「更新」", name, entry.Source)
		}
	}

	dst := s.clonePath(name)
	if _, err := os.Lstat(dst); err == nil {
		return Marketplace{}, fmt.Errorf("目标目录已存在：%s（状态中无此市场，疑似残留；请手动处理后再试）", dst)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Marketplace{}, fmt.Errorf("检查目录失败：%s（%v）", dst, err)
	}
	if err := os.MkdirAll(s.Root(), 0o755); err != nil {
		return Marketplace{}, fmt.Errorf("创建市场根目录失败：%s（%v）", s.Root(), err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cloneTimeout)
	defer cancel()
	out, err := s.git(ctx, "clone", "--depth", "1", source, dst)
	if err != nil {
		// 失败清理：仅删除本次新建的 clone 目录，不动状态
		if rmErr := os.RemoveAll(dst); rmErr != nil && s.log != nil {
			s.log.Warn("sharedmarket", "clone 失败后清理残留目录失败", fmt.Sprintf("dir=%s err=%v", dst, rmErr))
		}
		if s.log != nil {
			s.log.Error("sharedmarket", "git clone 失败", fmt.Sprintf("source=%s out=%s err=%v", source, out, err))
		}
		return Marketplace{}, fmt.Errorf("clone 市场失败（%v）：\n%s", err, strings.TrimSpace(out))
	}

	catalog, err := readCatalog(dst)
	if err != nil || len(catalog.Plugins) == 0 {
		if rmErr := os.RemoveAll(dst); rmErr != nil && s.log != nil {
			s.log.Warn("sharedmarket", "校验失败后清理残留目录失败", fmt.Sprintf("dir=%s err=%v", dst, rmErr))
		}
		if err != nil {
			return Marketplace{}, fmt.Errorf("%s 不是有效的插件市场（缺少 .claude-plugin/marketplace.json，%v），已回滚", source, err)
		}
		return Marketplace{}, fmt.Errorf("%s 的 marketplace.json 不含任何插件条目，已回滚", source)
	}

	now := time.Now().Format(time.RFC3339)
	state.Marketplaces = append(state.Marketplaces, stateEntry{
		Name: name, Source: source, AddedAt: now, LastUpdated: now,
	})
	if err := s.writeState(state); err != nil {
		// 状态提交失败回滚（F04）：清理本轮新建的 clone，否则市场不可见且无法
		// 重加（重试会撞「目标目录已存在」）；清理失败仅记日志不掩盖原错误。
		if rmErr := os.RemoveAll(dst); rmErr != nil && s.log != nil {
			s.log.Warn("sharedmarket", "状态落盘失败后回滚清理 clone 目录失败", fmt.Sprintf("dir=%s err=%v", dst, rmErr))
		}
		return Marketplace{}, err
	}
	if s.log != nil {
		s.log.Info("sharedmarket", "新增共用市场", fmt.Sprintf("name=%s source=%s plugins=%d", name, source, len(catalog.Plugins)))
	}
	return s.scanPlugins(stateEntry{Name: name, Source: source, AddedAt: now, LastUpdated: now}, s.scanSkillOrigins()), nil
}

// Update 拉取市场最新内容：clone 目录缺失时重新 clone；否则 git pull --ff-only。
// 成功后更新 lastUpdated 并返回最新视图。pull 冲突/网络失败如实报错。
func (s *Service) Update(name string) (Marketplace, error) {
	if !validName(name) {
		return Marketplace{}, fmt.Errorf("无效的市场名 %q", name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState()
	if err != nil {
		return Marketplace{}, err
	}
	idx := state.marketIndex(name)
	if idx < 0 {
		return Marketplace{}, fmt.Errorf("共用市场 %q 不存在", name)
	}
	entry := state.Marketplaces[idx]
	dst := s.clonePath(name)

	if _, statErr := os.Lstat(dst); errors.Is(statErr, fs.ErrNotExist) {
		// Missing 恢复：重新浅 clone
		ctx, cancel := context.WithTimeout(context.Background(), cloneTimeout)
		out, gitErr := s.git(ctx, "clone", "--depth", "1", entry.Source, dst)
		cancel()
		if gitErr != nil {
			if rmErr := os.RemoveAll(dst); rmErr != nil && s.log != nil {
				s.log.Warn("sharedmarket", "重新 clone 失败后清理残留目录失败", fmt.Sprintf("dir=%s err=%v", dst, rmErr))
			}
			return Marketplace{}, fmt.Errorf("重新 clone 失败（%v）：\n%s", gitErr, strings.TrimSpace(out))
		}
	} else if statErr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
		out, gitErr := s.git(ctx, "-C", dst, "pull", "--ff-only")
		cancel()
		if gitErr != nil {
			if s.log != nil {
				s.log.Warn("sharedmarket", "git pull 失败", fmt.Sprintf("name=%s out=%s err=%v", name, out, gitErr))
			}
			return Marketplace{}, fmt.Errorf("更新市场失败（%v）：\n%s", gitErr, strings.TrimSpace(out))
		}
	} else {
		return Marketplace{}, fmt.Errorf("检查目录失败：%s（%v）", dst, statErr)
	}

	if _, err := readCatalog(dst); err != nil {
		return Marketplace{}, fmt.Errorf("更新后的 marketplace.json 不可读（%v）", err)
	}

	entry.LastUpdated = time.Now().Format(time.RFC3339)
	previousSnapshots := entry.PluginSnapshots
	entry.PluginSnapshots = nil
	state.Marketplaces[idx] = entry
	if err := s.writeState(state); err != nil {
		return Marketplace{}, err
	}
	for _, snapshot := range previousSnapshots {
		s.removePluginSnapshot(snapshot)
	}
	// 注：Update（git pull）不重写已部署技能的凭据——重新部署对已存在目标
	// 保持跳过语义，凭据仍是最初部署时刻的证据（deployedAt 不代表技能内容
	// 版本）。
	return s.scanPlugins(entry, s.scanSkillOrigins()), nil
}

// Remove 删除共用市场：移除 clone 目录与状态条目。已部署到 ~/.agents/skills
// 的技能不受影响（由「共用技能」子页管理）。
func (s *Service) Remove(name string) error {
	if !validName(name) {
		return fmt.Errorf("无效的市场名 %q", name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState()
	if err != nil {
		return err
	}
	idx := state.marketIndex(name)
	if idx < 0 {
		return fmt.Errorf("共用市场 %q 不存在", name)
	}
	// Fail closed: an unreadable/redirected root is not an empty deployment list.
	agents, _, err := openAgents(s.userHome)
	if err != nil {
		return err
	}
	defer func() { _ = agents.Close() }()
	for _, rootName := range []string{activeSkillsDir, disabledSkillsDir} {
		root, _, err := openPlainDir(agents, rootName)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		_ = root.Close()
	}
	for _, origin := range s.scanSkillOrigins() {
		if origin.Marketplace == name {
			return fmt.Errorf("请先卸载「%s」中已部署的插件技能，再删除市场", name)
		}
	}

	if err := os.RemoveAll(s.clonePath(name)); err != nil {
		return fmt.Errorf("删除市场目录失败：%s（%v）", s.clonePath(name), err)
	}
	previousSnapshots := state.Marketplaces[idx].PluginSnapshots
	state.Marketplaces = append(state.Marketplaces[:idx], state.Marketplaces[idx+1:]...)
	if err := s.writeState(state); err != nil {
		return err
	}
	for _, snapshot := range previousSnapshots {
		s.removePluginSnapshot(snapshot)
	}
	if s.log != nil {
		s.log.Info("sharedmarket", "移除共用市场", name)
	}
	return nil
}

// skillsRoot 返回共用技能部署目标 ~/.agents/skills（与 sharedskills 同根）。
func (s *Service) skillsRoot() string {
	return filepath.Join(s.userHome, ".agents", "skills")
}

// DeployPlugin 把市场内插件的可部署技能复制到 ~/.agents/skills/<技能名>：
// 插件根有 SKILL.md → 整目录部署为同名技能；否则逐个复制 skills/ 子目录。
// 目标已存在的技能跳过并记入报告（不覆盖，防丢用户改动）。无可部署技能 →
// 明确报错（不静默）。
func (s *Service) DeployPlugin(name, pluginName string) (*DeployReport, error) {
	if !validName(name) {
		return nil, fmt.Errorf("无效的市场名 %q", name)
	}
	if strings.TrimSpace(pluginName) == "" {
		return nil, fmt.Errorf("插件名不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	agents, agentsID, err := openAgents(s.userHome)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("共用市场 %q 不存在", name)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = agents.Close() }()

	state, err := s.readState()
	if err != nil {
		return nil, err
	}
	idx := state.marketIndex(name)
	if idx < 0 {
		return nil, fmt.Errorf("共用市场 %q 不存在", name)
	}

	cloneDir := s.clonePath(name)
	catalog, err := readCatalog(cloneDir)
	if err != nil {
		return nil, fmt.Errorf("市场 %q 不可读（%v）；请先「更新」恢复", name, err)
	}
	entry, found := catalog.findPlugin(pluginName)
	if !found {
		return nil, fmt.Errorf("市场 %q 中不存在插件 %q", name, pluginName)
	}
	_, dir, root := s.pluginContent(state.Marketplaces[idx], entry)
	if dir == "" {
		return nil, fmt.Errorf("插件 %q 的内容不在市场仓库内（外部 git 引用），暂不支持部署为共用技能", pluginName)
	}
	skills := skillDirs(pluginName, dir, root)
	if len(skills) == 0 {
		return nil, fmt.Errorf("插件 %q 没有可部署的技能（未发现 SKILL.md，或名称/路径未通过安全校验）", pluginName)
	}

	// Bind both roots now so the first enable/disable rename preserves ownership.
	for _, name := range []string{activeSkillsDir, disabledSkillsDir} {
		if err := mkdirIfMissing(agents, name); err != nil {
			return nil, err
		}
	}
	active, activeID, err := openPlainDir(agents, activeSkillsDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = active.Close() }()
	disabled, disabledID, err := openPlainDir(agents, disabledSkillsDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = disabled.Close() }()

	report := &DeployReport{Marketplace: name, Plugin: pluginName, TargetRoot: s.skillsRoot(), Deployed: []string{}, Skipped: []string{}}
	stagingName := ".deploy-staging-" + rand.Text()
	if err := active.Mkdir(stagingName, 0o700); err != nil {
		return nil, fmt.Errorf("创建部署暂存失败：%w", err)
	}
	defer func() {
		if err := active.RemoveAll(stagingName); err != nil && s.log != nil {
			s.log.Warn("sharedmarket", "清理部署暂存目录失败", fmt.Sprintf("dir=%s err=%v", stagingName, err))
		}
	}()
	staging, _, err := openPlainDir(active, stagingName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = staging.Close() }()

	deployedAt := time.Now().Format(time.RFC3339)
	for _, skill := range skills {
		if _, err := disabled.Lstat(skill.Name); err == nil {
			report.Skipped = append(report.Skipped, skill.Name)
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("检查停用技能失败：%w", err)
		}
		if _, err := active.Lstat(skill.Name); err == nil {
			report.Skipped = append(report.Skipped, skill.Name)
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("检查目标目录失败：%w", err)
		}

		if err := staging.Mkdir(skill.Name, 0o755); err != nil {
			return nil, s.deployPartialError(report, skill.Name, err)
		}
		stage, instance, err := openPlainDir(staging, skill.Name)
		if err != nil {
			return nil, s.deployPartialError(report, skill.Name, err)
		}
		if err := copyDir(skill.Dir, stage); err != nil {
			_ = stage.Close()
			return nil, s.deployPartialError(report, skill.Name, fmt.Errorf("复制技能失败：%w", err))
		}
		err = writeProvenanceMarker(stage, provenanceMarker{
			Marketplace: name, Plugin: pluginName, Skill: skill.Name, DeployedAt: deployedAt,
			Instance: instance, Agents: agentsID, ActiveRoot: activeID, DisabledRoot: disabledID,
		})
		_ = stage.Close()
		if err != nil {
			return nil, s.deployPartialError(report, skill.Name, fmt.Errorf("写入来源凭据失败：%w", err))
		}
		if err := active.Rename(filepath.Join(stagingName, skill.Name), skill.Name); err != nil {
			return nil, s.deployPartialError(report, skill.Name, fmt.Errorf("发布技能失败：%w", err))
		}
		report.Deployed = append(report.Deployed, skill.Name)
	}
	if s.log != nil {
		s.log.Info("sharedmarket", "部署插件技能到共用技能", fmt.Sprintf("market=%s plugin=%s deployed=%d skipped=%d", name, pluginName, len(report.Deployed), len(report.Skipped)))
	}
	return report, nil
}

// deployPartialError 构造部分成功部署的错误信息（F03）：已成功的技能不因
// 后续失败而沉默——错误文案披露已部署清单，便于用户知晓现场。
func (s *Service) deployPartialError(report *DeployReport, failedSkill string, cause error) error {
	if len(report.Deployed) == 0 {
		return fmt.Errorf("部署技能 %q 失败（暂存已清理，可重试）：%w", failedSkill, cause)
	}
	return fmt.Errorf("部署技能 %q 失败（暂存已清理，可重试；此前已成功部署：%s）：%w",
		failedSkill, strings.Join(report.Deployed, "、"), cause)
}

// MarketDir 返回市场 clone 绝对路径（供「打开目录」门面校验后打开）。
func (s *Service) MarketDir(name string) (string, error) {
	if !validName(name) {
		return "", fmt.Errorf("无效的市场名 %q", name)
	}
	dir := s.clonePath(name)
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("市场目录不存在：%s", dir)
		}
		return "", fmt.Errorf("检查目录失败：%s（%v）", dir, err)
	}
	return dir, nil
}

// copyDir 递归复制目录（普通文件 + 子目录；跳过 .git 与符号链接——
// 符号链接不跟随复制，防穿越与断链）。
func copyDir(src string, dst *os.Root) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" && rel == ".git" {
				return fs.SkipDir
			}
			return dst.Mkdir(rel, 0o755)
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return nil // 跳过链接与其他非普通文件
		}
		return copyFile(path, dst, rel, info.Mode())
	})
}

func copyFile(src string, dst *os.Root, rel string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := dst.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}
