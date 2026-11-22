package piplugin

// filters.go — P4 pi 包启停与 skill 级过滤启停。
//
// 机制依据（pi 1.5.2 源码级探测，bundle chunks/chunk-VEJDBY3O.js）：
//   - pi 无包级 disable 概念：packages[] 中存在即加载（无 disabled 字段语义，
//     `pi config` TUI 只提供资源级开关）。包级禁用用 pi 原生资源过滤模式模拟：
//     四个过滤键（extensions/skills/prompts/themes）各追加 "!**"（glob 全排除，
//     minimatch 语义）——pi config TUI 中该包所有资源会如实显示为已停用。
//   - 资源级过滤条目四种形态（applyPatterns）：无前缀=白名单（glob）；"!p"=
//     glob 排除；"+p"=精确强制包含；"-p"=精确强制排除。`pi config` TUI 的
//     togglePackageResource 算法：移除同目标的既有条目（剥前缀比对）→ 追加
//     "+path"（启用）或 "-path"（禁用）→ 数组空则删键 → 四键全空则条目折叠回
//     字符串。本包 SetPackageSkillEnabled 与该算法逐字对齐，保证 pi config
//     TUI 与 CodeBox 两侧写入形态互通、互相可读。
//
// 本文件全部为 settings.json 的本地 JSON 手术（gjson/sjson），不经 pi CLI
// （pi CLI 无对应写入口；读操作已有 settings 直读先例）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// excludeAllPattern 是包级禁用的全排除标记（pi 原生 "!"+glob 语义，"**" 在
// minimatch 下匹配全部路径）。
const excludeAllPattern = "!**"

// filterKeys 是 packages[] 过滤对象的四个资源过滤键（与 pi schema 一致）。
var filterKeys = [4]string{"extensions", "skills", "prompts", "themes"}

func (s *Service) notifyChanged() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

// SetPackageEnabled 启用/禁用 pi 包（包级启停）。
//
// 禁用 = 在该 packages[] 条目的四个过滤键各追加 "!**"（保留用户既有过滤条目，
// 全排除叠加其上）；启用 = 移除四键中的全部 "!**" 条目（数组复原为用户原始
// 形态；空数组删键、四键全空时条目折叠回纯字符串——与 pi config TUI 的折叠
// 规则一致）。实体目录不动、packages[] 登记保留，pi list 仍可见该包。
//
// 判定（isPackageDisabledEntry）：四键均非空且均含 "!**" 且无 "+" 强制包含
// 条目（有 "+" 说明仍有资源被强制加载，不算整体禁用）。
func (s *Service) SetPackageEnabled(source string, enabled bool) (*CommandResult, error) {
	source, err := validateSource(source)
	if err != nil {
		return nil, err
	}
	if err := s.mutatePackageEntry(source, func(entry map[string]any) {
		for _, key := range filterKeys {
			current := stringSliceValue(entry[key])
			if enabled {
				next := make([]string, 0, len(current))
				for _, item := range current {
					if item != excludeAllPattern {
						next = append(next, item)
					}
				}
				assignFilterList(entry, key, next)
			} else if !containsExact(current, excludeAllPattern) {
				entry[key] = append(append([]string(nil), current...), excludeAllPattern)
			}
		}
	}); err != nil {
		return nil, err
	}
	s.notifyChanged()
	verb := "启用"
	if !enabled {
		verb = "禁用（已停止加载全部资源；重新启用可恢复）"
	}
	return &CommandResult{Success: true, Output: fmt.Sprintf("已%s pi 包 %s", verb, source)}, nil
}

// SetPackageSkillEnabled 启用/禁用包内单个 skill（skill 级启停，pi 原生机制）。
//
// 与 pi config TUI 的 togglePackageResource 算法对齐：对每个匹配 skillName 的
// skill 资源文件，把 skills[] 过滤数组中同目标条目替换为 "+rel"（启用）或
// "-rel"（禁用，精确强制排除）；数组空则删键、四键全空折叠回字符串。
// 与 extensions/prompts/themes 过滤器互不干扰（只动 skills 键）。
func (s *Service) SetPackageSkillEnabled(source, skillName string, enabled bool) (*CommandResult, error) {
	source, err := validateSource(source)
	if err != nil {
		return nil, err
	}
	skillName = strings.TrimSpace(skillName)
	if skillName == "" {
		return nil, errors.New("skill 名称不能为空")
	}
	patterns, err := s.skillPatternsByName(source, skillName)
	if err != nil {
		return nil, err
	}
	if len(patterns) == 0 {
		return nil, fmt.Errorf("包 %s 中未找到名为 %s 的 skill", source, skillName)
	}
	if err := s.mutatePackageEntry(source, func(entry map[string]any) {
		current := stringSliceValue(entry["skills"])
		for _, pattern := range patterns {
			current = replaceFilterTarget(current, pattern, filterPattern(pattern, enabled))
		}
		assignFilterList(entry, "skills", current)
	}); err != nil {
		return nil, err
	}
	s.notifyChanged()
	verb := "启用"
	if !enabled {
		verb = "禁用"
	}
	return &CommandResult{Success: true, Output: fmt.Sprintf("已%s pi 包 %s 的 skill %s", verb, source, skillName)}, nil
}

// skillPatternsByName 返回包内与 skillName 同名的全部 skill 资源的过滤模式串
// （相对包根的 POSIX 路径，与 pi config TUI 写入形态一致）。同名多文件时全部
// 返回（逐文件写模式，保证全部生效）。
func (s *Service) skillPatternsByName(source, skillName string) ([]string, error) {
	configured, err := s.readConfiguredPackages()
	if err != nil {
		return nil, err
	}
	registered := s.findRegistered(source, configured)
	if registered == "" {
		return nil, fmt.Errorf("pi 包未在 settings.json 中登记: %s", source)
	}
	var match configuredPackage
	for _, cfg := range configured {
		if cfg.source == registered {
			match = cfg
			break
		}
	}
	root, _ := s.resolveInstallPath(match.source)
	if root == "" {
		return nil, fmt.Errorf("无法定位 pi 包 %s 的实体目录", match.source)
	}
	baseDir := root
	if info, statErr := os.Stat(root); statErr == nil && !info.IsDir() {
		baseDir = filepath.Dir(root)
	} else if statErr != nil {
		return nil, fmt.Errorf("pi 包 %s 的实体目录不存在（%s）", match.source, root)
	}
	declared := map[string][]string{}
	if data, readErr := os.ReadFile(filepath.Join(baseDir, "package.json")); readErr == nil {
		var pkg map[string]any
		if json.Unmarshal(data, &pkg) == nil {
			declared = piManifestPaths(pkg)
		}
	}
	patterns := make([]string, 0, 2)
	for _, res := range scanPiResources(baseDir, declared) {
		if res.Type != resourceSkill || res.Name != skillName {
			continue
		}
		rel, relErr := filepath.Rel(baseDir, res.FilePath)
		if relErr != nil {
			continue
		}
		patterns = append(patterns, filepath.ToSlash(rel))
	}
	sort.Strings(patterns)
	return patterns, nil
}

// mutatePackageEntry 定位 settings.json packages[] 中 source 对应条目（归一化
// 匹配，与 findRegistered 同规则），以 mutate 修改其对象形态（字符串条目先升格
// 为 {source} 对象），sjson 原位替换后原子写回。其余键与数组顺序保持不变。
func (s *Service) mutatePackageEntry(source string, mutate func(entry map[string]any)) error {
	path := s.settingsFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("pi settings.json 不存在（尚未安装任何包）: %s", path)
		}
		return fmt.Errorf("读取 pi settings.json 失败: %w", err)
	}
	if !json.Valid(data) {
		return fmt.Errorf("pi settings.json 不是有效 JSON，拒绝修改: %s", path)
	}
	items := gjson.GetBytes(data, "packages").Array()
	index := -1
	for i, item := range items {
		pkg, ok := packageEntryFromJSON(item)
		if !ok {
			continue
		}
		if s.sourceMatchesRegistered(pkg.source, source) {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("pi 包未在 settings.json 中登记: %s", source)
	}
	entry := map[string]any{}
	// 保留既有对象键（含未知自定义键）；字符串条目从 {source} 起步。
	if items[index].IsObject() {
		if err := json.Unmarshal([]byte(items[index].Raw), &entry); err != nil {
			return fmt.Errorf("解析 packages[%d] 失败: %w", index, err)
		}
	}
	if src, _ := entry["source"].(string); strings.TrimSpace(src) == "" {
		pkg, _ := packageEntryFromJSON(items[index])
		entry["source"] = pkg.source
	}
	mutate(entry)
	// pi config TUI 折叠规则：四键全空 → 条目退回纯字符串源。
	if !anyFilterKeyPresent(entry) {
		if src, _ := entry["source"].(string); src != "" && len(entry) == 1 {
			return s.writePackagesEntry(path, data, index, src)
		}
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("编码 packages[%d] 失败: %w", index, err)
	}
	return s.writePackagesEntry(path, data, index, json.RawMessage(encoded))
}

// sourceMatchesRegistered 复用 findRegistered 的归一化匹配判定（字面相等或
// local 源绝对/相对形态互认）。
func (s *Service) sourceMatchesRegistered(registered, source string) bool {
	if registered == source {
		return true
	}
	if parseSource(registered).sourceType == sourceLocal && parseSource(source).sourceType == sourceLocal {
		return s.absolutizeLocalSource(registered) == s.absolutizeLocalSource(source)
	}
	return false
}

// writePackagesEntry 把 packages[index] 替换为 value（对象或字符串）并原子写回。
func (s *Service) writePackagesEntry(path string, data []byte, index int, value any) error {
	updated, err := sjson.SetBytes(data, fmt.Sprintf("packages.%d", index), value)
	if err != nil {
		return fmt.Errorf("更新 pi settings.json 失败: %w", err)
	}
	return writeSettingsAtomic(path, appendSettingsNewline(updated))
}

// writeSettingsAtomic 原子写 settings.json（临时文件+rename；保留既有权限，
// 缺省 0600——与 pi 自身写入一致）。
func writeSettingsAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建 pi agent 目录失败: %w", err)
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings.json.*.tmp")
	if err != nil {
		return fmt.Errorf("创建 pi settings 临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("设置 pi settings 临时文件权限失败: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写入 pi settings 临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭 pi settings 临时文件失败: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("替换 pi settings.json 失败: %w", err)
	}
	cleanup = false
	return nil
}

func appendSettingsNewline(data []byte) []byte {
	trimmed := strings.TrimRight(string(data), "\r\n")
	if trimmed == "" {
		return []byte(data)
	}
	return append([]byte(trimmed), '\n')
}

// assignFilterList 按过滤键写数组：空列表删键（pi 语义：键省略=加载全部），
// 非空写列表。
func assignFilterList(entry map[string]any, key string, list []string) {
	if len(list) == 0 {
		delete(entry, key)
		return
	}
	entry[key] = list
}

func anyFilterKeyPresent(entry map[string]any) bool {
	for _, key := range filterKeys {
		if _, ok := entry[key]; ok {
			return true
		}
	}
	return false
}

// stringSliceValue 把 any（[]any 或 []string）规整为 []string。
func stringSliceValue(value any) []string {
	switch list := value.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func containsExact(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

// filterPattern 返回目标 pattern 的启用/禁用形态（pi config TUI 同款前缀）。
func filterPattern(pattern string, enabled bool) string {
	if enabled {
		return "+" + pattern
	}
	return "-" + pattern
}

// replaceFilterTarget 移除同目标的既有条目（剥 "!"/"+"/"-" 前缀后比对，TUI
// 同款）后追加 replacement。
func replaceFilterTarget(items []string, target, replacement string) []string {
	out := make([]string, 0, len(items)+1)
	for _, item := range items {
		if stripFilterPrefix(item) == target {
			continue
		}
		out = append(out, item)
	}
	return append(out, replacement)
}

func stripFilterPrefix(item string) string {
	if strings.HasPrefix(item, "!") || strings.HasPrefix(item, "+") || strings.HasPrefix(item, "-") {
		return item[1:]
	}
	return item
}

// isPackageDisabledEntry 判定 packages[] 条目是否处于我们的包级禁用形态：
// 四键均存在、均含 "!**" 且无 "+" 强制包含条目。
func isPackageDisabledEntry(cfg configuredPackage) bool {
	lists := [][]string{cfg.extensions, cfg.skills, cfg.prompts, cfg.themes}
	hasForceInclude := false
	for _, list := range lists {
		if list == nil {
			return false
		}
		if !containsExact(list, excludeAllPattern) {
			return false
		}
		for _, item := range list {
			if strings.HasPrefix(item, "+") {
				hasForceInclude = true
			}
		}
	}
	return !hasForceInclude
}

// skillEnabledByFilters 按 pi applyPatterns 语义评估一个 skill 文件在过滤数组
// 下的启停态。patterns 为 nil（键省略）= 加载全部。求值顺序与 pi 一致：
// 白名单（无前缀 glob）→ "!" glob 排除 → "+" 精确强制包含 → "-" 精确强制排除。
// baseDir 为包根；filePath 为 skill 文件绝对路径。
func skillEnabledByFilters(baseDir, filePath string, patterns []string) bool {
	if patterns == nil {
		// 键省略 = 加载全部（pi 三态语义）；显式空数组 [] = 一项都不加载。
		return true
	}
	if len(patterns) == 0 {
		return false
	}
	candidates := patternMatchCandidates(baseDir, filePath)
	plain := make([]string, 0, len(patterns))
	var excludes, forceIncludes, forceExcludes []string
	for _, item := range patterns {
		switch {
		case strings.HasPrefix(item, "+"):
			forceIncludes = append(forceIncludes, stripFilterPrefix(item))
		case strings.HasPrefix(item, "-"):
			forceExcludes = append(forceExcludes, stripFilterPrefix(item))
		case strings.HasPrefix(item, "!"):
			excludes = append(excludes, stripFilterPrefix(item))
		default:
			plain = append(plain, item)
		}
	}
	matchAny := func(list []string, exact bool) bool {
		for _, pattern := range list {
			for _, candidate := range candidates {
				if exact {
					if pattern == candidate {
						return true
					}
				} else if filterGlobMatch(pattern, candidate) {
					return true
				}
			}
		}
		return false
	}
	enabled := true
	if len(plain) > 0 && !matchAny(plain, false) {
		enabled = false
	}
	if len(excludes) > 0 && matchAny(excludes, false) {
		enabled = false
	}
	if len(forceIncludes) > 0 && matchAny(forceIncludes, true) {
		enabled = true
	}
	if len(forceExcludes) > 0 && matchAny(forceExcludes, true) {
		enabled = false
	}
	return enabled
}

// patternMatchCandidates 生成该文件的候选匹配串（镜像 pi minimatch 的多基准
// 尝试：相对路径、文件名；SKILL.md 额外认父目录相对路径与父目录名）。
func patternMatchCandidates(baseDir, filePath string) []string {
	rel, err := filepath.Rel(baseDir, filePath)
	if err != nil {
		rel = filePath
	}
	relPosix := filepath.ToSlash(rel)
	name := filepath.Base(filePath)
	candidates := []string{relPosix, name, filepath.ToSlash(filePath)}
	if strings.EqualFold(name, "SKILL.md") {
		parent := filepath.Dir(filePath)
		parentRel, err := filepath.Rel(baseDir, parent)
		if err == nil {
			candidates = append(candidates, filepath.ToSlash(parentRel))
		}
		candidates = append(candidates, filepath.Base(parent), filepath.ToSlash(parent))
	}
	return candidates
}

// filterGlobMatch 是 pi minimatch 语义的轻量近似：支持 "**"（跨目录任意段）与
// "*"（单段内任意）；"*" 不跨 "/"（minimatch 默认行为）。CodeBox 与 pi config
// TUI 写入的都是精确路径（不含 glob），本函数只服务用户手写 glob 的评估。
func filterGlobMatch(pattern, path string) bool {
	if pattern == "**" {
		return true
	}
	return globSegmentsMatch(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func globSegmentsMatch(pattern, path []string) bool {
	if len(pattern) == 0 {
		return len(path) == 0
	}
	if pattern[0] == "**" {
		// "**" 可吞任意数量段：递归尝试剩余 path 的所有切分。
		for skip := 0; skip <= len(path); skip++ {
			if globSegmentsMatch(pattern[1:], path[skip:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 {
		return false
	}
	if !segmentMatch(pattern[0], path[0]) {
		return false
	}
	return globSegmentsMatch(pattern[1:], path[1:])
}

func segmentMatch(pattern, segment string) bool {
	// 仅支持 "*" 通配的简单段匹配（无字符类/范围——过滤模式实际使用面之外）。
	if !strings.Contains(pattern, "*") {
		return pattern == segment
	}
	return starMatch(pattern, segment)
}

func starMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	// 自后向前锚定最后一段，再对中段做顺序贪心匹配。
	last := parts[len(parts)-1]
	if !strings.HasSuffix(s, last) {
		return false
	}
	s = s[:len(s)-len(last)]
	middle := parts[1 : len(parts)-1]
	for _, part := range middle {
		idx := strings.Index(s, part)
		if idx < 0 {
			return false
		}
		s = s[idx+len(part):]
	}
	return true
}
