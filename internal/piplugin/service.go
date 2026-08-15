// Package piplugin manages Pi coding agent packages (extensions/skills/prompts/themes).
//
// Pi 包 = npm 包或 git 仓库，安装记录写在 agent 目录的 settings.json 顶层 packages
// 数组；实体落 <agentDir>/npm/node_modules/<name>（npm 源）或
// <agentDir>/git/<host>/<user>/<project>（git 源）。CLI 入口：
//
//	pi install <source>   # 写 settings.json packages[] + 拉取实体
//	pi remove  <source>   # 从 packages[] 移除（实体保留以备重装）
//	pi list                # 读 settings.json 展示已装包
//	pi update  <source>    # 更新单个包
//
// 管理范围：CodeBox 装配时使用 Pi 的标准用户目录
// ~/.pi/agent。写操作（install/remove/update）在 fork pi CLI 时显式注入
// 该 agentDir，确保插件面板与普通 Pi/CodeBox Pi 会话共享同一份配置。
//
// 本服务设计对标 internal/opencodeplugin：读操作（list/details）优先解析
// settings.json + 扫描实体目录，避免 fork pi CLI；写操作（install/remove/update）
// 走 pi CLI。
package piplugin

import (
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/tidwall/gjson"
)

// piExecutable 是 pi CLI 的可执行名（与 internal/envcheck/checker_pi.go 一致）。
const piExecutable = "pi"

// Service manages Pi packages.
type Service struct {
	// agentDir 是 pi 的配置/包存储根（PI_CODING_AGENT_DIR 或 ~/.pi/agent）。
	// settings.json、npm/、git/ 均位于其下。
	agentDir string
	log      *logging.Service
	resolver platform.CLIResolver
	runner   platform.ProcessRunner
	mu       sync.Mutex
}

// NewService creates a pi plugin service. agentDir 为 pi 的配置/包存储根；CodeBox
// 装配处传入 ~/.pi/agent。传入空串时回退到
// $PI_CODING_AGENT_DIR → ~/.pi/agent
// （仅用于测试或外部直启场景）。
func NewService(agentDir string, log *logging.Service) *Service {
	return NewServiceWithDeps(agentDir, log,
		platform.NewCLIResolver(platform.CurrentCapabilities()),
		platform.NewProcessRunner())
}

// NewServiceWithDeps allows injecting a CLI resolver and process runner (tests).
func NewServiceWithDeps(agentDir string, log *logging.Service, resolver platform.CLIResolver, runner platform.ProcessRunner) *Service {
	agentDir = strings.TrimSpace(agentDir)
	if agentDir == "" {
		agentDir = defaultAgentDir()
	}
	return &Service{
		agentDir: filepath.Clean(agentDir),
		log:      log,
		resolver: resolver,
		runner:   runner,
	}
}

// defaultAgentDir 复刻 pi getAgentDir：优先 $PI_CODING_AGENT_DIR，否则 ~/.pi/agent。
func defaultAgentDir() string {
	if env := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".pi", "agent")
	}
	return filepath.Join(".", ".pi", "agent")
}

// settingsFilePath 返回 settings.json 路径。
func (s *Service) settingsFilePath() string {
	return filepath.Join(s.agentDir, "settings.json")
}

// cmdShellMetachars 是 Windows cmd.exe 的命令拼接元字符。pi CLI 在 Windows 上
// 通过 npm 全局安装，入口通常是 .cmd，CodeBox 会用 `cmd.exe /c pi.cmd ...` 执行
// （见 internal/platform/process_script_windows.go）。这些字符若出现在包源中，
// 会被 cmd.exe 解释为命令分隔/重定向，构成命令注入面（P1-1）。三类合法源
// （npm/git/local）的语法均不含这些字符，因此一律拒绝是安全的。
const cmdShellMetachars = `&|<>^%()`

// validateSource 校验源字符串，拒绝空/危险输入（对标 opencodeplugin validatePluginSpec）。
// 安全闸门（P1-1）：拒绝 cmd.exe 元字符，阻断 Windows .cmd wrapper 的命令注入路径。
func validateSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", errors.New("包源不能为空")
	}
	if len(source) > 2048 {
		return "", errors.New("包源地址过长")
	}
	if strings.HasPrefix(source, "-") || strings.ContainsAny(source, "\x00\r\n") {
		return "", errors.New("包源格式无效")
	}
	if strings.ContainsAny(source, cmdShellMetachars) {
		return "", errors.New("包源含命令行元字符，已拒绝（潜在的命令注入）")
	}
	return source, nil
}

// configuredPackage 是 settings.json packages[] 一个元素的解析结果。
type configuredPackage struct {
	source     string
	extensions []string
	skills     []string
	prompts    []string
	themes     []string
	// raw 是元素在数组中的原始 JSON（便于去重/定位）。
	raw string
}

// readConfiguredPackages 读取 settings.json 的 packages[] 数组，去重后返回。
// 元素可为字符串源或 {source, extensions[], skills[], prompts[], themes[]} 过滤对象。
func (s *Service) readConfiguredPackages() ([]configuredPackage, error) {
	path := s.settingsFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []configuredPackage{}, nil
		}
		return nil, fmt.Errorf("读取 pi settings.json 失败: %w", err)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("pi settings.json 不是有效 JSON: %s", path)
	}
	items := gjson.GetBytes(data, "packages").Array()
	result := make([]configuredPackage, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		pkg, ok := packageEntryFromJSON(item)
		if !ok || pkg.source == "" {
			continue
		}
		// 去重依据：源字符串（pi 的去重按 identity，此处按字符串足够满足展示需求）。
		if _, dup := seen[pkg.source]; dup {
			continue
		}
		seen[pkg.source] = struct{}{}
		pkg.raw = item.Raw
		result = append(result, pkg)
	}
	return result, nil
}

// packageEntryFromJSON 把一个 packages[] 元素解析为 configuredPackage。
func packageEntryFromJSON(item gjson.Result) (configuredPackage, bool) {
	if item.Type == gjson.String {
		src := strings.TrimSpace(item.String())
		return configuredPackage{source: src}, src != ""
	}
	if item.IsObject() {
		src := strings.TrimSpace(item.Get("source").String())
		if src == "" {
			return configuredPackage{}, false
		}
		return configuredPackage{
			source:     src,
			extensions: toStringSlice(item.Get("extensions")),
			skills:     toStringSlice(item.Get("skills")),
			prompts:    toStringSlice(item.Get("prompts")),
			themes:     toStringSlice(item.Get("themes")),
		}, true
	}
	return configuredPackage{}, false
}

// toStringSlice 把 gjson 数组转为 []string（非数组返回 nil）。
func toStringSlice(result gjson.Result) []string {
	if !result.IsArray() {
		return nil
	}
	arr := result.Array()
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if v.Type == gjson.String {
			if s := strings.TrimSpace(v.String()); s != "" {
				out = append(out, s)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveInstallPath 依据源类型定位实体目录：
//   - npm:  <agentDir>/npm/node_modules/<name>
//   - git:  <agentDir>/git/<host>/<user>/<project>
//   - local: 配置路径（相对路径按 agentDir 解析）
func (s *Service) resolveInstallPath(source string) (string, parsedSource) {
	parsed := parseSource(source)
	switch parsed.sourceType {
	case sourceNPM:
		if parsed.name == "" {
			return "", parsed
		}
		return filepath.Join(s.agentDir, "npm", "node_modules", filepath.FromSlash(parsed.name)), parsed
	case sourceGit:
		if parsed.host == "" || parsed.path == "" {
			return "", parsed
		}
		// path 形如 "user/project"，FromSlash 处理平台分隔符。
		return filepath.Join(s.agentDir, "git", filepath.FromSlash(parsed.host), filepath.FromSlash(parsed.path)), parsed
	default:
		// local：相对路径按 settings.json 所在目录（agentDir）解析，与 pi 一致。
		p := parsed.localPath
		if !filepath.IsAbs(p) {
			p = filepath.Join(s.agentDir, p)
		}
		return p, parsed
	}
}

// ListInstalledPackages 列出 settings.json 中登记的所有包，附实体元数据。
func (s *Service) ListInstalledPackages() ([]Package, error) {
	configured, err := s.readConfiguredPackages()
	if err != nil {
		return nil, err
	}
	packages := make([]Package, 0, len(configured))
	for _, cfg := range configured {
		packages = append(packages, s.inspectPackage(cfg, false).Package)
	}
	sort.Slice(packages, func(i, j int) bool {
		return strings.ToLower(packages[i].Name) < strings.ToLower(packages[j].Name)
	})
	return packages, nil
}

// RefreshPackages 刷新并返回聚合数据（含"已配置但实体缺失"告警）。
func (s *Service) RefreshPackages() (*PackagesData, error) {
	packages, err := s.ListInstalledPackages()
	if err != nil {
		return nil, err
	}
	warnings := make([]string, 0)
	for _, p := range packages {
		if p.InstallPath == "" {
			continue
		}
		if p.SourceType == sourceLocal {
			continue
		}
		if info, statErr := os.Stat(p.InstallPath); statErr != nil || !info.IsDir() {
			warnings = append(warnings, fmt.Sprintf("%s 已在 settings.json 登记，但实体目录未找到（%s）", p.Source, p.InstallPath))
		}
	}
	return &PackagesData{Installed: packages, Warnings: warnings}, nil
}

// GetPackageDetails 返回单个包的详情（含扫描到的子资源）。
func (s *Service) GetPackageDetails(source string) (*PackageDetail, error) {
	source, err := validateSource(source)
	if err != nil {
		return nil, err
	}
	configured, err := s.readConfiguredPackages()
	if err != nil {
		return nil, err
	}
	found := false
	var match configuredPackage
	for _, cfg := range configured {
		if cfg.source == source {
			found = true
			match = cfg
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("pi 包未在 settings.json 中登记: %s", source)
	}
	detail := s.inspectPackage(match, true)
	return &detail, nil
}

// InstallPackage 通过 pi CLI 安装包（写 settings.json + 拉取实体）。
func (s *Service) InstallPackage(source string) (*CommandResult, error) {
	source, err := validateSource(source)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executePiCommand(context.TODO(), "install", source)
}

// RemovePackage 通过 pi CLI 移除包（从 settings.json packages[] 删除；实体保留）。
func (s *Service) RemovePackage(source string) (*CommandResult, error) {
	source, err := validateSource(source)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executePiCommand(context.TODO(), "remove", source)
}

// UpdatePackage 通过 pi CLI 更新单个包。
// 仅允许更新已登记的包（未登记返回明确错误，避免 CLI 静默无操作）。
func (s *Service) UpdatePackage(source string) (*CommandResult, error) {
	source, err := validateSource(source)
	if err != nil {
		return nil, err
	}
	configured, err := s.readConfiguredPackages()
	if err != nil {
		return nil, err
	}
	found := false
	for _, cfg := range configured {
		if cfg.source == source {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("pi 包未在 settings.json 中登记，无法更新: %s", source)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executePiCommand(context.TODO(), "update", source)
}

// inspectPackage 扫描实体目录补全包元数据；resources=true 时进一步枚举子资源。
func (s *Service) inspectPackage(cfg configuredPackage, resources bool) PackageDetail {
	parsed := parseSource(cfg.source)
	detail := PackageDetail{
		Package: Package{
			ID:         cfg.source,
			Source:     cfg.source,
			SourceType: parsed.sourceType,
			Name:       fallbackName(cfg.source),
			Scope:      "user",
			Enabled:    true,
			Extensions: cfg.extensions,
			Skills:     cfg.skills,
			Prompts:    cfg.prompts,
			Themes:     cfg.themes,
		},
		Resources: []ResourceInfo{},
	}
	// pinned：npm 仅精确语义化版本视为 pinned（pi 官方语义，P2-4）；
	// git 任意非空 ref（commit/tag/branch）均锁定。
	if parsed.sourceType == sourceNPM && isExactSemver(parsed.ref) {
		detail.Pinned = true
	}
	if parsed.sourceType == sourceGit && parsed.ref != "" {
		detail.Pinned = true
	}

	root, _ := s.resolveInstallPath(cfg.source)
	if root == "" {
		return detail
	}
	// InstallPath 始终记录计算路径（npm/git 实体目录或本地路径），
	// 即使实体缺失也保留——供 RefreshPackages 产生“已登记但未安装”告警。
	detail.InstallPath = root
	// 实体探测：npm/git 需目录存在；local 可能是文件或目录。
	manifestDir := root
	if info, err := os.Stat(root); err == nil {
		if !info.IsDir() {
			manifestDir = filepath.Dir(root)
		}
	} else {
		// 实体缺失：保留 InstallPath（供告警），跳过元数据/资源读取。
		return detail
	}

	manifest := filepath.Join(manifestDir, "package.json")
	detail.ManifestPath = manifest
	if info, err := os.Stat(manifest); err == nil {
		detail.LastUpdated = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
	}
	declared := map[string][]string{}
	if data, err := os.ReadFile(manifest); err == nil {
		var pkg map[string]any
		if json.Unmarshal(data, &pkg) == nil {
			detail.Name = stringField(pkg, "name", detail.Name)
			detail.Version = stringField(pkg, "version", "")
			detail.Description = stringField(pkg, "description", "")
			detail.Author = authorField(pkg["author"])
			detail.Repository = repositoryField(pkg["repository"])
			declared = piManifestPaths(pkg)
			detail.ManifestDeclared = len(declared) > 0
		}
	}
	if !resources {
		return detail
	}
	detail.Resources = scanPiResources(manifestDir, declared)
	return detail
}

// piManifestPaths 读取 package.json 的 pi 键声明的资源路径（extensions/skills/prompts/themes）。
// 值为字符串或字符串数组（glob 模式此处仅作路径记录，不展开求值）。
func piManifestPaths(pkg map[string]any) map[string][]string {
	piNode, ok := pkg["pi"].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string][]string, 4)
	for key := range map[string]struct{}{"extensions": {}, "skills": {}, "prompts": {}, "themes": {}} {
		if v, ok := piNode[key]; ok {
			out[key] = anyToStringSlice(v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// anyToStringSlice 把 string / []any 统一为 []string。
func anyToStringSlice(value any) []string {
	switch v := value.(type) {
	case string:
		if t := strings.TrimSpace(v); t != "" {
			return []string{t}
		}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if t := strings.TrimSpace(s); t != "" {
					out = append(out, t)
				}
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// scanPiResources 扫描包目录的可发现资源。
// 优先采用 package.json pi manifest 声明的路径；声明缺失时按约定目录自动发现：
//   - extensions/ → .ts/.js
//   - skills/     → SKILL.md 所在目录 + 顶层 .md
//   - prompts/    → .md
//   - themes/     → .json
func scanPiResources(root string, declared map[string][]string) []ResourceInfo {
	result := make([]ResourceInfo, 0)
	add := func(typ, absPath string) {
		if absPath == "" {
			return
		}
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(root, absPath)
		}
		if info, err := os.Stat(absPath); err != nil || info.IsDir() {
			// 声明路径可能是 glob，逐项 stat 失败时跳过；约定目录扫描在下方补充。
			return
		}
		result = append(result, ResourceInfo{Name: resourceDisplayName(root, absPath, typ), FilePath: absPath, Type: typ})
	}
	// manifest 声明路径（逐项记录；glob 不展开，保持信息忠实）。
	for _, typ := range []string{"extensions", "skills", "prompts", "themes"} {
		for _, p := range declared[typ] {
			add(piResourceType(typ), p)
		}
	}
	// 约定目录自动发现（manifest 缺失或补充发现）。
	result = append(result, scanConventionDir(filepath.Join(root, "extensions"), resourceExtension, isExtensionScript)...)
	result = append(result, scanConventionSkills(filepath.Join(root, "skills"))...)
	result = append(result, scanConventionDir(filepath.Join(root, "prompts"), resourcePrompt, isMarkdown)...)
	result = append(result, scanConventionDir(filepath.Join(root, "themes"), resourceTheme, isThemeJSON)...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type != result[j].Type {
			return result[i].Type < result[j].Type
		}
		return result[i].Name < result[j].Name
	})
	return result
}

// scanConventionDir 在约定目录下递归收集匹配的文件。
func scanConventionDir(root, typ string, match fileMatcher) []ResourceInfo {
	result := make([]ResourceInfo, 0)
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == "node_modules" || entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !match(entry.Name()) {
			return nil
		}
		result = append(result, ResourceInfo{
			Name:     resourceDisplayName(root, path, typ),
			FilePath: path,
			Type:     typ,
		})
		return nil
	})
	return result
}

// scanConventionSkills 扫描 skills 目录：SKILL.md 所在目录视为一个 skill，
// 顶层 .md 文件也作为 skill。
func scanConventionSkills(root string) []ResourceInfo {
	result := make([]ResourceInfo, 0)
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == "node_modules" || entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(entry.Name(), "SKILL.md") && !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		// SKILL.md 以其所在目录名作为 skill 名。
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if strings.EqualFold(entry.Name(), "SKILL.md") {
			name = filepath.Base(filepath.Dir(path))
		}
		result = append(result, ResourceInfo{Name: name, FilePath: path, Type: resourceSkill})
		return nil
	})
	return result
}

// fileMatcher 决定一个文件名是否属于某类约定资源。
type fileMatcher func(name string) bool

// isExtensionScript 匹配 pi extension 脚本：.ts / .js / .mjs。
func isExtensionScript(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ts", ".js", ".mjs":
		return true
	}
	return false
}

// isMarkdown 匹配 .md 文件（prompts）。
func isMarkdown(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".md")
}

// isThemeJSON 匹配 .json 文件（themes）。
func isThemeJSON(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".json")
}

// piResourceType 把 manifest 键名映射为资源类型常量。
func piResourceType(key string) string {
	switch key {
	case "extensions":
		return resourceExtension
	case "skills":
		return resourceSkill
	case "prompts":
		return resourcePrompt
	case "themes":
		return resourceTheme
	}
	return key
}

// resourceDisplayName 生成资源的展示名（相对 root 的去后缀路径，skills 用目录名）。
func resourceDisplayName(root, absPath, typ string) string {
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return filepath.Base(absPath)
	}
	name := strings.TrimSuffix(filepath.ToSlash(rel), filepath.Ext(rel))
	if typ == resourceSkill && strings.HasSuffix(strings.ToLower(filepath.Base(absPath)), "skill.md") {
		name = filepath.Base(filepath.Dir(absPath))
	}
	return name
}

// SwitchPackageSource 在同一逻辑插件的不同源类型之间切换（git ⇄ npm ⇄ 本地）。
//
// 背景（2026-08-15 实战）：开发期需要 git（发布版）与本地路径（工作区直载）之间
// 往返——此前只能手动 pi remove + pi install + 手改 settings.json（曾因双引用并存
// 导致双载冲突）。本方法把该流程原子化：
//
//	oldSource 在 packages[] 登记 → remove（实体保留）→ install newSource → 失败回滚重装 old。
//	校验 oldSource 已登记；newSource 合法性走 validateSource；同源直通（no-op 成功）。
func (s *Service) SwitchPackageSource(oldSource, newSource string) (*CommandResult, error) {
	oldSource, err := validateSource(oldSource)
	if err != nil {
		return nil, err
	}
	newSource, err2 := validateSource(newSource)
	if err2 != nil {
		return nil, err2
	}
	if oldSource == newSource {
		return &CommandResult{Success: true, Output: "新旧源相同，无需切换"}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	configured, err := s.readConfiguredPackages()
	if err != nil {
		return nil, err
	}
	oldFound := false
	for _, cfg := range configured {
		if cfg.source == oldSource {
			oldFound = true
			break
		}
	}
	if !oldFound {
		return nil, fmt.Errorf("旧源未在 settings.json 中登记，无法切换: %s", oldSource)
	}
	// 新源已登记时拒绝（pi install 同名包会双引用——实战踩坑）。
	for _, cfg := range configured {
		if cfg.source == newSource {
			return nil, fmt.Errorf("新源已在 settings.json 中登记（会形成双引用冲突），请先移除: %s", newSource)
		}
	}

	if s.log != nil {
		s.log.Info("piplugin", "切换包源", fmt.Sprintf("%s -> %s", oldSource, newSource))
	}

	// ① remove 旧源（packages[] 移除；实体保留备回滚）
	if _, err := s.executePiCommand(context.TODO(), "remove", oldSource); err != nil {
		return nil, fmt.Errorf("移除旧源失败（配置未变）: %w", err)
	}
	// ② install 新源
	installRes, err := s.executePiCommand(context.TODO(), "install", newSource)
	if err != nil || (installRes != nil && !installRes.Success) {
		// ③ 回滚：重装旧源，恢复切换前状态
		var rollbackErr error
		if _, rollbackErr = s.executePiCommand(context.TODO(), "install", oldSource); rollbackErr != nil && s.log != nil {
			s.log.Error("piplugin", "切换失败且回滚重装旧源失败（需手动 pi install 恢复）",
				fmt.Sprintf("old=%s err=%v", oldSource, rollbackErr))
		}
		if err != nil {
			return nil, fmt.Errorf("安装新源失败（已回滚旧源）: %w", err)
		}
		return &CommandResult{
			Success: false,
			Error:   fmt.Sprintf("安装新源失败（已回滚旧源）: %s", installRes.Error),
			Output:  installRes.Output,
		}, nil
	}
	installRes.Output = fmt.Sprintf("已从 %s 切换到 %s\n%s", oldSource, newSource, installRes.Output)
	return installRes, nil
}
