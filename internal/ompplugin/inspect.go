package ompplugin

// inspect.go — P4 omp 插件 skills 扫描与详情。
//
// omp（Oh My Pi）插件体系与 Claude Code 同构（binary 内嵌字符串证据：
// `~/.omp/plugins/installed_plugins.json`、"/marketplace add
// anthropics/claude-plugins-official"、claude-plugins 加载器）：npm 插件实体
// 在 ~/.omp/plugins（CLI list 的 path 字段给出实体路径），marketplace 插件由
// installed_plugins.json 登记。skills 采用 skills/<name>/SKILL.md 约定目录
// （binary 中 SKILL.md→baseDir 归一逻辑与 Claude/pi 同源）。
//
// 注意：marketplace 条目的 installed_plugins.json 字段形态无本机实样可验
// （编写时本机无 marketplace 插件安装），解析按 Claude Code 同构 schema 做
// 字段级容错（installPath/install_path/path 三候选），未命中时 skills 诚实
// 为空、不报错。

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

// ResourceInfo describes one discoverable skill shipped in an omp plugin.
type ResourceInfo struct {
	Name     string `json:"name"`
	FilePath string `json:"filePath"`
}

// PluginDetail contains plugin metadata plus the discoverable skills.
type PluginDetail struct {
	Plugin
	Skills []ResourceInfo `json:"skills"`
}

// GetPluginDetails 返回单个 omp 插件的详情（含 skills 扫描）。
// 通过 omp CLI list 定位插件（npm 实体路径 / marketplace 登记），再本地扫描。
func (s *Service) GetPluginDetails(name string) (*PluginDetail, error) {
	name, err := validatePluginSpec(name)
	if err != nil {
		return nil, err
	}
	plugins, _, err := s.listPluginsUnlocked()
	if err != nil {
		return nil, err
	}
	for i := range plugins {
		if plugins[i].ID == name || plugins[i].Name == name {
			detail := s.InspectPlugin(plugins[i])
			return detail, nil
		}
	}
	return nil, fmt.Errorf("未找到 omp 插件：%s", name)
}

// InspectPlugin 对一个已列表的 omp 插件做本地详情扫描（不再调 omp CLI）：
// npm 插件用 CLI 给出的实体路径；marketplace 插件回退查 installed_plugins.json。
// 供列表后批量取 skills（共用中心聚合 provider），避免 N+1 次 CLI fork。
func (s *Service) InspectPlugin(plugin Plugin) *PluginDetail {
	detail := &PluginDetail{Plugin: plugin, Skills: []ResourceInfo{}}
	root := strings.TrimSpace(plugin.InstallPath)
	if root == "" {
		root = s.marketplaceInstallPath(plugin.ID)
	}
	if root == "" {
		return detail
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return detail
	}
	detail.InstallPath = root
	detail.Skills = scanPluginSkills(root)
	return detail
}

// marketplaceInstallPath 从 ~/.omp/plugins/installed_plugins.json 解析
// marketplace 插件的实体路径（Claude Code 同构 schema；entries[0] 为当前生效
// 版本，与 list_parse 的 entries.0 约定一致）。未登记/字段缺失返回空串。
func (s *Service) marketplaceInstallPath(pluginID string) string {
	root := s.pluginsRoot
	if strings.TrimSpace(root) == "" {
		root = defaultOmpPluginsRoot()
	}
	pluginsFile := filepath.Join(root, "installed_plugins.json")
	data, err := os.ReadFile(pluginsFile)
	if err != nil {
		return ""
	}
	entry := gjson.GetBytes(data, "plugins."+gjsonEscapeKey(pluginID)+".0")
	if !entry.IsObject() {
		return ""
	}
	for _, key := range []string{"installPath", "install_path", "path"} {
		if value := strings.TrimSpace(entry.Get(key).String()); value != "" {
			return value
		}
	}
	return ""
}

// gjsonEscapeKey 把插件 ID（name@marketplace 形态）转为 gjson 路径键
// （"@"、"." 等在 gjson 路径语法中有含义，需转义）。
func gjsonEscapeKey(key string) string {
	replacer := strings.NewReplacer(".", `\.`, "#", `\#`, "|", `\|`, "@", `\@`)
	return replacer.Replace(key)
}

// defaultOmpPluginsRoot 返回 omp 插件根目录（~/.omp/plugins；omp CLI 自带
// 目录语义，无官方环境变量覆盖——见 executor.go 头部注释）。
func defaultOmpPluginsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".", ".omp", "plugins")
	}
	return filepath.Join(home, ".omp", "plugins")
}

// scanPluginSkills 扫描插件根目录下的 skills 约定目录：
//   - skills/<name>/SKILL.md → 一个 skill（目录名为 skill 名，Claude/pi 同源约定）；
//   - skills/<name>.md      → 顶层 .md 也视为一个 skill（pi scanConventionSkills 同款）。
//
// skills 目录不存在时返回空（多数 omp 插件只带 extension，无 skills）。
func scanPluginSkills(root string) []ResourceInfo {
	skillsDir := filepath.Join(root, "skills")
	result := make([]ResourceInfo, 0)
	_ = filepath.WalkDir(skillsDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil // 目录缺失/不可读：诚实降级为空
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
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if strings.EqualFold(entry.Name(), "SKILL.md") {
			name = filepath.Base(filepath.Dir(path))
		}
		result = append(result, ResourceInfo{Name: name, FilePath: path})
		return nil
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
