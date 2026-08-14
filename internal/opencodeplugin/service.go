// Package opencodeplugin manages globally configured OpenCode plugins.
//
// OpenCode currently exposes one official plugin command:
//
//	opencode plugin <module> --global [--force]
//
// Installation and updates therefore go through the CLI. Listing and details
// are derived from the global config and OpenCode's package cache. Uninstall is
// a surgical removal from the global plugin array because the CLI has no
// uninstall subcommand.
package opencodeplugin

import (
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/pretty"
	"github.com/tidwall/sjson"
)

type Service struct {
	configDir string
	cacheDir  string
	log       *logging.Service
	resolver  platform.CLIResolver
	runner    platform.ProcessRunner
	http      httpDoer
	mu        sync.Mutex
}

func NewService(configDir string, cacheDir string, log *logging.Service) *Service {
	return NewServiceWithDeps(
		configDir,
		cacheDir,
		log,
		platform.NewCLIResolver(platform.CurrentCapabilities()),
		platform.NewProcessRunner(),
	)
}

func NewServiceWithDeps(
	configDir string,
	cacheDir string,
	log *logging.Service,
	resolver platform.CLIResolver,
	runner platform.ProcessRunner,
) *Service {
	if strings.TrimSpace(configDir) == "" {
		configDir = defaultOpenCodeConfigDir()
	}
	if strings.TrimSpace(cacheDir) == "" {
		cacheDir = defaultOpenCodeCacheDir()
	}
	return &Service{
		configDir: filepath.Clean(configDir),
		cacheDir:  filepath.Clean(cacheDir),
		log:       log,
		resolver:  resolver,
		runner:    runner,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

func defaultOpenCodeConfigDir() string {
	if configured := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")); configured != "" {
		return configured
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".", ".config", "opencode")
	}
	return filepath.Join(home, ".config", "opencode")
}

func defaultOpenCodeCacheDir() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".", ".cache", "opencode")
	}
	return filepath.Join(home, ".cache", "opencode")
}

func (s *Service) configFilePath() string {
	for _, name := range []string{"opencode.json", "opencode.jsonc", "config.json"} {
		candidate := filepath.Join(s.configDir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return filepath.Join(s.configDir, "opencode.json")
}

func validatePluginSpec(spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", errors.New("插件模块不能为空")
	}
	if len(spec) > 2048 {
		return "", errors.New("插件模块地址过长")
	}
	if strings.HasPrefix(spec, "-") || strings.ContainsAny(spec, "\x00\r\n") {
		return "", errors.New("插件模块格式无效")
	}
	return spec, nil
}

func (s *Service) ListInstalledPlugins() ([]Plugin, error) {
	specs, err := s.readConfiguredSpecs()
	if err != nil {
		return nil, err
	}
	plugins := make([]Plugin, 0, len(specs))
	for _, spec := range specs {
		plugins = append(plugins, s.inspectPlugin(spec, false).Plugin)
	}
	sort.Slice(plugins, func(i, j int) bool {
		return strings.ToLower(plugins[i].Name) < strings.ToLower(plugins[j].Name)
	})
	return plugins, nil
}

func (s *Service) RefreshPlugins() (*PluginsData, error) {
	plugins, err := s.ListInstalledPlugins()
	if err != nil {
		return nil, err
	}
	warnings := make([]string, 0)
	for _, plugin := range plugins {
		if plugin.InstallPath == "" && plugin.Source != "file" {
			warnings = append(warnings, fmt.Sprintf("%s 已配置，但未在 OpenCode 缓存中找到安装包", plugin.Spec))
		}
	}
	return &PluginsData{Installed: plugins, Warnings: warnings}, nil
}

func (s *Service) GetPluginDetails(spec string) (*PluginDetail, error) {
	spec, err := validatePluginSpec(spec)
	if err != nil {
		return nil, err
	}
	configured, err := s.readConfiguredSpecs()
	if err != nil {
		return nil, err
	}
	found := false
	for _, item := range configured {
		if item == spec {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("OpenCode 插件未在全局配置中启用: %s", spec)
	}
	detail := s.inspectPlugin(spec, true)
	return &detail, nil
}

func (s *Service) InstallPlugin(spec string) (*CommandResult, error) {
	spec, err := validatePluginSpec(spec)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.executeOpenCodeCommand(context.TODO(), "plugin", spec, "--global")
	if err != nil {
		// CLI 失败兜底：opencode 1.18.10 对 github: spec 会「假失败」——包已完整
		// 装入 cache（inspectPlugin 能定位 InstallPath），仅入口点解析报错且不写
		// 配置。兜底严格收窄到已证实的 github 假失败条件，避免陈旧或无效 cache 被
		// 误报为安装成功（Major-1 签名守卫 + Major-2 三重校验）：
		//  0. 仅当 CLI 失败签名命中 isKnownFalseInstallFailure（NpmInstallFailedError）；
		//     超时/权限/参数错误等无关失败原样返回，不写配置、不发起 ls-remote；
		//  1. 仅 pluginSource(spec)=="github"（npm/file spec CLI 失败原样报错；
		//     npm 路径实证正常，不兜底）；
		//  2. git ls-remote 能解析出 latest tag（resolveUpdateTarget 成功）；
		//  3. cache 中包版本 == latest tag 版本（防陈旧 cache 被当作本次产物）。
		// 写入原 spec（而非 target.Spec 的归一化形式）：Install 语义是装用户要的
		// spec，moving ref（#main）保持其语义，由后续 Update 迁移到 immutable tag。
		if !isKnownFalseInstallFailure(result, err) {
			return result, err
		}
		if pluginSource(spec) == "github" {
			if target, resolveErr := s.resolveUpdateTarget(spec); resolveErr == nil {
				cached := s.inspectPlugin(spec, false)
				if cached.InstallPath != "" && cached.Version == target.Version {
					if cfgErr := s.ensurePluginSpecInConfig("", spec); cfgErr != nil {
						return result, err
					}
					return &CommandResult{
						Success: true,
						Output:  fmt.Sprintf("OpenCode CLI 安装报错，但缓存已就绪（版本 %s 匹配最新 tag %s），已通过本地兜底将 %s 写入全局配置（CLI 错误: %s）", cached.Version, target.Version, spec, result.Error),
					}, nil
				}
			}
		}
		return result, err
	}
	return result, nil
}

func (s *Service) UpdatePlugin(spec string) (*CommandResult, error) {
	spec, err := validatePluginSpec(spec)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	target, err := s.resolveUpdateTarget(spec)
	if err != nil {
		return nil, err
	}
	current := s.inspectPlugin(spec, false)
	if result := alreadyCurrentResult(spec, current, target); result != nil {
		return result, nil
	}

	result, err := s.executeOpenCodeCommand(context.TODO(), "plugin", target.Spec, "--global", "--force")
	if err != nil {
		// CLI 失败兜底（同 InstallPlugin）。Major-1 签名守卫：仅当 CLI 失败命中
		// isKnownFalseInstallFailure（NpmInstallFailedError）才进入；超时/权限/参数
		// 错误等无关失败原样返回，不写配置、不做 cache 校验。注意 Update 的
		// resolveUpdateTarget 已在 CLI 之前执行（Update 语义前置，用于确定 target），
		// 故签名不匹配仅能阻止 fallback 内的 inspectPlugin/配置写入/verify，无法
		// 省去前置 ls-remote。随后 Major-2 收窄：仅 github spec 生效；npm/file spec
		// CLI 失败原样报错（npm 路径实证正常，不兜底）。先确认 cache 已装好
		// target.Spec 且版本匹配 target.Version，再做配置切换（旧 spec→target.Spec），
		// 最后用 verifyUpdatedPlugin 收口；任一环节不达标则返回原始 CLI 错误，兼容
		// 未来修好的 CLI（修好即不进此分支）。
		if !isKnownFalseInstallFailure(result, err) {
			return result, err
		}
		if pluginSource(spec) == "github" {
			cached := s.inspectPlugin(target.Spec, false)
			if cached.InstallPath != "" && cached.Version == target.Version {
				if cfgErr := s.ensurePluginSpecInConfig(spec, target.Spec); cfgErr != nil {
					return result, err
				}
				if verifyErr := s.verifyUpdatedPlugin(spec, target); verifyErr != nil {
					if s.log != nil {
						s.log.Info("opencodeplugin", "兜底校验未通过，回退原始 CLI 错误", verifyErr.Error())
					}
					return result, err
				}
				return &CommandResult{
					Success: true,
					Output:  fmt.Sprintf("OpenCode CLI 更新报错，但缓存已就绪，已通过本地兜底将 %s 切换为 %s（版本 %s）；CLI 错误: %s", spec, target.Spec, target.Version, result.Error),
				}, nil
			}
		}
		return result, err
	}
	if err := s.verifyUpdatedPlugin(spec, target); err != nil {
		result.Success = false
		result.Error = err.Error()
		return result, err
	}
	result.Output = fmt.Sprintf("已将 %s 更新为 %s（版本 %s）", spec, target.Spec, target.Version)
	return result, nil
}

func (s *Service) UninstallPlugin(spec string) (*CommandResult, error) {
	spec, err := validatePluginSpec(spec)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.configFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 OpenCode 配置失败: %w", err)
	}
	if !json.Valid(data) {
		if json.Valid(pretty.Spec(data)) {
			return nil, fmt.Errorf("OpenCode 全局配置包含 JSONC 注释或尾随逗号，当前不能在不破坏格式的情况下自动卸载；请从 %s 的 plugin 数组移除该项", path)
		}
		return nil, fmt.Errorf("OpenCode 配置不是有效 JSON/JSONC: %s", path)
	}
	items := gjson.GetBytes(data, "plugin").Array()
	if len(items) == 0 {
		return nil, fmt.Errorf("OpenCode 全局配置没有已启用插件: %s", path)
	}
	next := make([]any, 0, len(items))
	removed := false
	for _, item := range items {
		itemSpec := pluginSpecFromJSON(item)
		if itemSpec == spec {
			removed = true
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(item.Raw), &value); err != nil {
			return nil, fmt.Errorf("解析 OpenCode plugin 配置失败: %w", err)
		}
		next = append(next, value)
	}
	if !removed {
		return nil, fmt.Errorf("OpenCode 插件未在全局配置中启用: %s", spec)
	}

	var updated []byte
	if len(next) == 0 {
		updated, err = sjson.DeleteBytes(data, "plugin")
	} else {
		updated, err = sjson.SetBytes(data, "plugin", next)
	}
	if err != nil {
		return nil, fmt.Errorf("更新 OpenCode plugin 配置失败: %w", err)
	}
	if err := writeAtomic(path, appendNewline(updated)); err != nil {
		return nil, err
	}
	if s.log != nil {
		s.log.Info("opencodeplugin", "已从 OpenCode 全局配置移除插件", spec)
	}
	return &CommandResult{
		Success: true,
		Output:  fmt.Sprintf("已从 %s 移除 %s；OpenCode 缓存保留以供后续重装", path, spec),
	}, nil
}

// ensurePluginSpecInConfig 把 newSpec 写入 OpenCode 全局配置的 plugin 数组；若
// oldSpec 非空且已存在则移除 oldSpec（oldSpec==newSpec 时仅保证不重复添加）。
//
// 用于 opencode CLI 对 github: spec「假失败」时的本地兜底：CLI 已把包装入 cache
// （inspectPlugin 能定位 InstallPath），但入口点解析报错且不写 opencode.json 配置
// （opencode 1.18.10 实证），此时由本服务直接补做配置写入使插件真正生效。配置文件
// 不存在时按 {} 起步创建。
//
// JSON/JSONC 校验 + gjson/sjson + writeAtomic 的口径与 UninstallPlugin 保持一致；
// JSONC 含注释无法安全编辑时返回错误，调用方应回退为返回原始 CLI 错误。
func (s *Service) ensurePluginSpecInConfig(oldSpec, newSpec string) error {
	path := s.configFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			data = []byte("{}")
		} else {
			return fmt.Errorf("读取 OpenCode 配置失败: %w", err)
		}
	}
	if !json.Valid(data) {
		if json.Valid(pretty.Spec(data)) {
			return fmt.Errorf("OpenCode 全局配置包含 JSONC 注释或尾随逗号，当前不能在不破坏格式的情况下自动修改；请手工编辑 %s 的 plugin 数组", path)
		}
		return fmt.Errorf("OpenCode 配置不是有效 JSON/JSONC: %s", path)
	}

	items := gjson.GetBytes(data, "plugin").Array()
	next := make([]any, 0, len(items)+1)
	hasNew := false
	for _, item := range items {
		itemSpec := pluginSpecFromJSON(item)
		if itemSpec == newSpec {
			hasNew = true
		}
		// 移除旧引用：oldSpec 非空、与 newSpec 不同、且命中时跳过
		if oldSpec != "" && oldSpec != newSpec && itemSpec == oldSpec {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(item.Raw), &value); err != nil {
			return fmt.Errorf("解析 OpenCode plugin 配置失败: %w", err)
		}
		next = append(next, value)
	}
	if !hasNew {
		next = append(next, newSpec)
	}

	var updated []byte
	if len(next) == 0 {
		updated, err = sjson.DeleteBytes(data, "plugin")
	} else {
		updated, err = sjson.SetBytes(data, "plugin", next)
	}
	if err != nil {
		return fmt.Errorf("更新 OpenCode plugin 配置失败: %w", err)
	}
	if err := writeAtomic(path, appendNewline(updated)); err != nil {
		return err
	}
	if s.log != nil {
		s.log.Info("opencodeplugin", "本地兜底写入 OpenCode 全局配置", newSpec)
	}
	return nil
}

func (s *Service) readConfiguredSpecs() ([]string, error) {
	path := s.configFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("读取 OpenCode 配置失败: %w", err)
	}
	normalized := data
	if !json.Valid(normalized) {
		normalized = pretty.Spec(data)
	}
	if !json.Valid(normalized) {
		return nil, fmt.Errorf("OpenCode 全局配置不是有效 JSON/JSONC: %s", path)
	}
	items := gjson.GetBytes(normalized, "plugin").Array()
	result := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		spec := pluginSpecFromJSON(item)
		if spec == "" {
			continue
		}
		if _, ok := seen[spec]; ok {
			continue
		}
		seen[spec] = struct{}{}
		result = append(result, spec)
	}
	return result, nil
}

func pluginSpecFromJSON(item gjson.Result) string {
	if item.Type == gjson.String {
		return strings.TrimSpace(item.String())
	}
	if item.IsArray() {
		values := item.Array()
		if len(values) > 0 && values[0].Type == gjson.String {
			return strings.TrimSpace(values[0].String())
		}
	}
	return ""
}

func (s *Service) inspectPlugin(spec string, resources bool) PluginDetail {
	detail := PluginDetail{
		Plugin: Plugin{
			ID:      spec,
			Spec:    spec,
			Name:    fallbackPluginName(spec),
			Source:  pluginSource(spec),
			Scope:   "global",
			Enabled: true,
			Targets: []string{},
		},
		Skills:   []ResourceInfo{},
		Agents:   []ResourceInfo{},
		Commands: []ResourceInfo{},
		Hooks:    []ResourceInfo{},
	}
	root := s.resolveInstallRoot(spec)
	if root == "" {
		return detail
	}
	detail.InstallPath = root
	manifest := filepath.Join(root, "package.json")
	detail.ManifestPath = manifest
	if info, err := os.Stat(manifest); err == nil {
		detail.LastUpdated = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
	}
	if data, err := os.ReadFile(manifest); err == nil {
		var pkg map[string]any
		if json.Unmarshal(data, &pkg) == nil {
			detail.Name = stringField(pkg, "name", detail.Name)
			detail.Version = stringField(pkg, "version", "")
			detail.Description = stringField(pkg, "description", "")
			detail.Author = authorField(pkg["author"])
			detail.Repository = repositoryField(pkg["repository"])
			detail.Targets = packageTargets(pkg)
		}
	}
	if !resources {
		return detail
	}
	detail.Skills = scanResources(filepath.Join(root, "skills"), "SKILL.md")
	detail.Commands = scanResources(filepath.Join(root, "commands"), ".md")
	detail.Agents = scanResources(filepath.Join(root, "agents"), ".md")
	if len(detail.Agents) == 0 {
		detail.Agents = scanResources(filepath.Join(root, "prompts"), ".md")
	}
	detail.Hooks = scanResources(filepath.Join(root, "hooks"), "")
	for _, candidate := range []string{
		filepath.Join(root, "mcp", "servers.json"),
		filepath.Join(root, ".mcp.json"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			detail.HasMCP = true
			break
		}
	}
	return detail
}

func (s *Service) resolveInstallRoot(spec string) string {
	if strings.HasPrefix(spec, "file://") {
		candidate, err := fileURLPath(spec)
		if err != nil {
			return ""
		}
		if info, err := os.Stat(candidate); err == nil {
			if info.IsDir() {
				return candidate
			}
			return filepath.Dir(candidate)
		}
		return ""
	}
	if filepath.IsAbs(spec) {
		if info, err := os.Stat(spec); err == nil {
			if info.IsDir() {
				return spec
			}
			return filepath.Dir(spec)
		}
		return ""
	}
	packagesRoot := filepath.Join(s.cacheDir, "packages")
	wrapper := filepath.Join(packagesRoot, filepath.FromSlash(openCodeCacheKey(spec)))
	if !pathWithin(packagesRoot, wrapper) {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(wrapper, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil || len(pkg.Dependencies) == 0 {
		return ""
	}
	names := make([]string, 0, len(pkg.Dependencies))
	for name := range pkg.Dependencies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		candidate := filepath.Join(wrapper, "node_modules", filepath.FromSlash(name))
		if info, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func scanResources(root string, match string) []ResourceInfo {
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
		if match == "SKILL.md" && entry.Name() != match {
			return nil
		}
		if match == ".md" && !strings.EqualFold(filepath.Ext(entry.Name()), match) {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		name := strings.TrimSuffix(filepath.ToSlash(relative), filepath.Ext(relative))
		if match == "SKILL.md" {
			name = filepath.Base(filepath.Dir(path))
		}
		result = append(result, ResourceInfo{Name: name, FilePath: path})
		return nil
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func packageTargets(pkg map[string]any) []string {
	result := make([]string, 0, 2)
	exports, _ := pkg["exports"].(map[string]any)
	if exports != nil {
		if _, ok := exports["./server"]; ok {
			result = append(result, "server")
		}
		if _, ok := exports["./tui"]; ok {
			result = append(result, "tui")
		}
	}
	if len(result) == 0 && strings.TrimSpace(stringField(pkg, "main", "")) != "" {
		result = append(result, "server")
	}
	if _, ok := pkg["oc-themes"]; ok && !containsString(result, "tui") {
		result = append(result, "tui")
	}
	return result
}

func stringField(data map[string]any, key string, fallback string) string {
	value, _ := data[key].(string)
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func authorField(value any) string {
	switch hit := value.(type) {
	case string:
		return strings.TrimSpace(hit)
	case map[string]any:
		if name, ok := hit["name"].(string); ok {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

func repositoryField(value any) string {
	switch hit := value.(type) {
	case string:
		return strings.TrimSpace(hit)
	case map[string]any:
		if raw, ok := hit["url"].(string); ok {
			return strings.TrimSpace(raw)
		}
	}
	return ""
}

func fallbackPluginName(spec string) string {
	value := strings.TrimSuffix(spec, "/")
	if index := strings.LastIndex(value, "/"); index >= 0 {
		value = value[index+1:]
	}
	if index := strings.Index(value, "#"); index >= 0 {
		value = value[:index]
	}
	return value
}

func pluginSource(spec string) string {
	switch {
	case strings.HasPrefix(spec, "file://") || filepath.IsAbs(spec):
		return "file"
	case strings.HasPrefix(spec, "github:") || strings.Contains(spec, "github.com"):
		return "github"
	default:
		return "npm"
	}
}

// knownFalseInstallFailureSignature 是已证实的 opencode CLI「假失败」错误签名。
//
// 实证来源：opencode CLI 1.18.10（macOS）对 github: spec 安装/更新时，包已完整
// 装入 cache（inspectPlugin 能定位 InstallPath），但入口点解析报 NpmInstallFailedError
// 且非零退出、不写配置。本地兜底仅在此已知签名下进入，避免超时/权限/参数错误等无关
// 失败被误报为安装成功，并避免给无关失败附加最长 45s 的 git ls-remote 网络成本。
const knownFalseInstallFailureSignature = "NpmInstallFailedError"

// isKnownFalseInstallFailure 判定 CLI 失败是否属于已证实的「假失败」签名。
//
// 大小写不敏感子串匹配；同时检查 CommandResult.Error（CLI stderr 归集，executor
// 在 stderr 为空时回填 err.Error()）与返回的 Go error 文本，覆盖 runner 把签名放
// 在 stderr 或作为 error 返回的两种实现。无法可靠识别时返回 false，调用方应保守
// 返回原始 CLI 错误。
func isKnownFalseInstallFailure(result *CommandResult, err error) bool {
	signature := strings.ToLower(knownFalseInstallFailureSignature)
	if result != nil && strings.Contains(strings.ToLower(result.Error), signature) {
		return true
	}
	if err != nil && strings.Contains(strings.ToLower(err.Error()), signature) {
		return true
	}
	return false
}

func containsString(items []string, expected string) bool {
	for _, item := range items {
		if item == expected {
			return true
		}
	}
	return false
}

func pathWithin(parent string, child string) bool {
	relative, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func appendNewline(data []byte) []byte {
	data = []byte(strings.TrimRight(string(data), "\r\n"))
	return append(data, '\n')
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建 OpenCode 配置目录失败: %w", err)
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".opencode-plugin-*.tmp")
	if err != nil {
		return fmt.Errorf("创建 OpenCode 配置临时文件失败: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("设置 OpenCode 配置临时文件权限失败: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("写入 OpenCode 配置临时文件失败: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭 OpenCode 配置临时文件失败: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		// Windows does not allow Rename to replace an existing file. The
		// temporary-file path remains the normal atomic route on platforms
		// that support replacement; this fallback preserves functionality.
		if writeErr := os.WriteFile(path, data, mode); writeErr != nil {
			return fmt.Errorf("替换 OpenCode 配置失败: %v（回退写入失败: %w）", err, writeErr)
		}
	}
	return nil
}
