package plugin

// subitem_ops.go — 插件子项启停的「真实生效」操作层（2026-11 修复）。
//
// 背景：此前 SetSubItemEnabled 只写 CodeBox 私有记录
//（~/.amagi-codebox/plugin-subitems.json），Claude Code 不读该文件——
// 开关在界面上变化但对 CLI 完全不生效，且状态与文件系统真相脱钩。
//
// 模型：文件系统即真相。
//   - skill/command/agent 子项：禁用 = 把 installPath 下的目录/文件移入
//     stash（~/.amagi-codebox/plugin-subitems-stash/<pluginID>/<type>/<物理名>），
//     启用 = 移回；Claude 扫描不到被移走的路径，即真实不加载。
//   - mcp 子项：禁用 = 从 installPath/.mcp.json 的 mcpServers 移除该 server
//     条目并把定义存入 stash/mcp.json；启用 = 写回。Claude 读不到即不挂载。
//   - hook 子项：hooks.json 无稳定条目标识（Name 是展示用合成名），单独启停
//     暂不支持——SetSubItemEnabled 明确报错，UI 行不可切换（如实，不静默）。
//
// 展示名与物理路径的映射（F-01/F-06 修复）：skill/agent 的 frontmatter
// name 可与目录/文件名不同，子项操作传的是扫描展示名。stash 条目以
// 物理文件名存放，并伴写 sidecar（<物理名>.meta.json）记录展示名与相对
// installPath 的物理路径；启停与列表都经 sidecar 解析，展示名仅作展示。
// 恢复冲突（plugin update 重装恢复文件）策略：新文件优先，弃置 stash 副本
//（MCP 同策略：目标已存在时不覆盖新定义，仅清 stash）。
//
// 名称安全：pluginID（name@marketplace）与子项名经 validStashName 校验
//（单段、非隐藏、无路径分隔符/盘符冒号），杜绝 stash 路径穿越。
//
// 已知边界（如实）：validStashName 未覆盖 Windows 保留设备名（CON/NUL 等）
// 与结尾点/空格形态；stash 与 installPath 跨卷时 os.Rename 无回退（当前
// 两者都在用户主目录下，同卷成立）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// stash 子项类型与 installPath 内相对根的对应（hook 不参与搬移）。
var stashableRoots = map[SubItemType]string{
	SubItemTypeSkill:   "skills",
	SubItemTypeCommand: "commands",
	SubItemTypeAgent:   "agents",
}

// sidecarSuffix 是 stash 条目旁的元数据文件后缀（列表/恢复经它解析映射）。
const sidecarSuffix = ".meta.json"

// stashSidecar 记录「展示名 → 物理路径」映射（F-01）：
// skill 的 PhysicalPath 是目录相对路径（skills/foo）；command/agent 是文件
// 相对路径（commands/do-thing.md / agents/worker.md）。
type stashSidecar struct {
	Name         string `json:"name"`         // 子项展示名（扫描层 frontmatter name 等）
	PhysicalPath string `json:"physicalPath"` // 相对 installPath 的物理路径
}

// validStashName 校验用作 stash 路径段的名称：非空、非隐藏、单段、无路径
// 分隔符与 Windows 盘符冒号（pluginID 的 @ 合法保留）。
func validStashName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.HasPrefix(name, ".") &&
		filepath.Base(name) == name &&
		!strings.ContainsAny(name, `/\:`)
}

// stashRoot 返回 stash 根目录（应用配置目录下 plugin-subitems-stash）。
func (s *Service) stashRoot() string {
	return filepath.Join(s.configDirOrDefault(), "plugin-subitems-stash")
}

func (s *Service) stashPluginDir(pluginID, itemType string) string {
	return filepath.Join(s.stashRoot(), pluginID, string(itemType))
}

// readStashSidecar 读取主条目对应的 sidecar；不存在返回零值与 false。
func readStashSidecar(entryPath string) (stashSidecar, bool) {
	b, err := os.ReadFile(entryPath + sidecarSuffix)
	if err != nil {
		return stashSidecar{}, false
	}
	var sc stashSidecar
	if err := json.Unmarshal(b, &sc); err != nil || strings.TrimSpace(sc.Name) == "" || strings.TrimSpace(sc.PhysicalPath) == "" {
		return stashSidecar{}, false
	}
	// 物理路径防穿越：拒绝越出 installPath 的相对路径
	cleaned := filepath.Clean(sc.PhysicalPath)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || filepath.IsAbs(cleaned) {
		return stashSidecar{}, false
	}
	sc.PhysicalPath = cleaned
	return sc, true
}

func writeStashSidecar(entryPath string, sc stashSidecar) error {
	b, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(entryPath+sidecarSuffix, b, 0o644)
}

// listStashedSubItems 枚举某插件 stash 中的全部子项（文件搬移类 + mcp 条目）。
// 展示名优先取 sidecar（F-01：与扫描层展示名一致）；无 sidecar 的条目回退
// 物理名（去 .md 仅限 command/agent，F-06：skill 目录名原样保留）。
func (s *Service) listStashedSubItems(pluginID string) []SubItem {
	out := make([]SubItem, 0)
	if !validStashName(pluginID) {
		return out
	}
	for itemType := range stashableRoots {
		dir := s.stashPluginDir(pluginID, string(itemType))
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), sidecarSuffix) {
				continue // sidecar 不是子项
			}
			entryPath := filepath.Join(dir, entry.Name())
			name := entry.Name()
			if sc, ok := readStashSidecar(entryPath); ok {
				name = sc.Name
			} else if itemType == SubItemTypeCommand || itemType == SubItemTypeAgent {
				name = strings.TrimSuffix(entry.Name(), ".md")
			}
			if !validStashName(name) {
				continue
			}
			out = append(out, SubItem{
				Type:       itemType,
				Name:       name,
				Path:       entryPath,
				Enabled:    false,
				Selectable: true,
			})
		}
	}
	// mcp stash 条目
	for _, name := range s.stashedMCPServerNames(pluginID) {
		out = append(out, SubItem{
			Type:       SubItemTypeMCP,
			Name:       name,
			Path:       ".mcp.json（已移出）",
			Enabled:    false,
			Selectable: true,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type == out[j].Type {
			return out[i].Name < out[j].Name
		}
		return subItemTypeRank(out[i].Type) < subItemTypeRank(out[j].Type)
	})
	return out
}

// physicalRelPath 把子项展示条目的相对 Path 归一为可搬移的物理相对路径：
// skill 的 Path 指向 SKILL.md 文件 → 取其目录；command/agent 的 Path 即文件。
func physicalRelPath(itemType SubItemType, relPath string) string {
	rel := filepath.Clean(strings.TrimSpace(relPath))
	if rel == "" || rel == "." {
		return ""
	}
	if itemType == SubItemTypeSkill {
		return filepath.Dir(rel)
	}
	return rel
}

// moveItemToStash 把 installPath 内的子项目录/文件移入 stash（禁用）。
// relPath 为相对 installPath 的物理路径（来自详情扫描的 SubItem.Path），
// displayName 为子项展示名（写入 sidecar 供列表与恢复解析）。
func (s *Service) moveItemToStash(installPath, pluginID string, ref SubItemRef, relPath, displayName string) error {
	root, ok := stashableRoots[ref.Type]
	if !ok {
		return fmt.Errorf("子项类型 %q 不支持文件搬移式启停", ref.Type)
	}
	physical := physicalRelPath(ref.Type, relPath)
	if physical == "" || !validStashName(pluginID) {
		return fmt.Errorf("子项 %s 缺少可定位的物理路径，已拒绝操作（拒绝路径穿越）", ref.Key())
	}

	src := filepath.Join(installPath, physical)
	if _, err := os.Lstat(src); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// 物理路径不在：可能已被外部处理；如实报错而不是假装禁用成功
			//（F-01：源缺失不能一概推断已经禁用）
			return fmt.Errorf("子项 %s 的源路径不存在：%s（可能已被移动或删除，请刷新详情）", ref.Key(), src)
		}
		return fmt.Errorf("检查子项路径失败：%s（%v）", src, err)
	}
	if filepath.Base(filepath.Dir(src)) != root {
		return fmt.Errorf("子项 %s 的物理路径 %s 不在预期的 %s/ 根下，已拒绝操作", ref.Key(), physical, root)
	}

	dstDir := s.stashPluginDir(pluginID, string(ref.Type))
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("创建 stash 目录失败：%s（%v）", dstDir, err)
	}
	dst := filepath.Join(dstDir, filepath.Base(src))
	if _, err := os.Lstat(dst); err == nil {
		// stash 已有同名（异常残留）：弃置旧 stash 副本，以当前文件为准
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("清理 stash 旧副本失败：%s（%v）", dst, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("检查 stash 目标失败：%s（%v）", dst, err)
	}
	if err := writeStashSidecar(dst, stashSidecar{Name: displayName, PhysicalPath: physical}); err != nil {
		return fmt.Errorf("写 stash 元数据失败：%s（%v）", dst+sidecarSuffix, err)
	}
	if err := os.Rename(src, dst); err != nil {
		_ = os.Remove(dst + sidecarSuffix)
		return fmt.Errorf("移入 stash 失败：%s → %s（%v）", src, dst, err)
	}
	return nil
}

// restoreItemFromStash 把 stash 中的子项移回 installPath（启用）。
// 优先经 sidecar（展示名 → 物理路径）解析；目标已存在（plugin update 重装
// 恢复）时弃置 stash 副本，保新文件（新文件优先，F-02 同策略）。
func (s *Service) restoreItemFromStash(installPath, pluginID string, ref SubItemRef) error {
	if _, ok := stashableRoots[ref.Type]; !ok {
		return fmt.Errorf("子项类型 %q 不支持文件搬移式启停", ref.Type)
	}
	if !validStashName(ref.Name) || !validStashName(pluginID) {
		return fmt.Errorf("无效的子项名 %q 或插件标识（拒绝路径穿越）", ref.Name)
	}

	src, physical, found := s.locateStashedEntry(pluginID, ref)
	if !found {
		return nil // stash 无该条目：文件本就在位（或从未禁用），幂等成功
	}

	dst := filepath.Join(installPath, physical)
	if _, err := os.Lstat(dst); err == nil {
		// 目标已存在（重装/更新恢复）：新文件优先，弃置 stash 副本
		if err := os.RemoveAll(src); err != nil {
			return fmt.Errorf("清理 stash 副本失败：%s（%v）", src, err)
		}
		_ = os.Remove(src + sidecarSuffix)
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("检查启用目标失败：%s（%v）", dst, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("创建插件目录失败：%s（%v）", filepath.Dir(dst), err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("从 stash 恢复失败：%s → %s（%v）", src, dst, err)
	}
	_ = os.Remove(src + sidecarSuffix)
	return nil
}

// locateStashedEntry 定位 stash 中展示名匹配的主条目：返回主条目绝对路径、
// 相对 installPath 的物理路径。匹配优先级：sidecar.Name == ref.Name →
// 兜底物理名（ref.Name / ref.Name.md，面向无 sidecar 的异常条目）。
func (s *Service) locateStashedEntry(pluginID string, ref SubItemRef) (string, string, bool) {
	dir := s.stashPluginDir(pluginID, string(ref.Type))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", false
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), sidecarSuffix) {
			continue
		}
		entryPath := filepath.Join(dir, entry.Name())
		if sc, ok := readStashSidecar(entryPath); ok && sc.Name == ref.Name {
			return entryPath, sc.PhysicalPath, true
		}
	}
	// 兜底：无 sidecar 的条目按物理名匹配（command/agent 尝试 .md 形态）
	for _, candidate := range []string{ref.Name, ref.Name + ".md"} {
		entryPath := filepath.Join(dir, candidate)
		if _, err := os.Lstat(entryPath); err == nil {
			root := stashableRoots[ref.Type]
			return entryPath, filepath.Join(root, candidate), true
		}
	}
	return "", "", false
}

// ---- MCP 条目级启停（.mcp.json 编辑 + stash/mcp.json 条目存取） ----

func (s *Service) mcpStashPath(pluginID string) string {
	return filepath.Join(s.stashRoot(), pluginID, "mcp.json")
}

// stashedMCPServerNames 读取 mcp stash 的 server 名列表（null/损坏 → nil）。
func (s *Service) stashedMCPServerNames(pluginID string) []string {
	if !validStashName(pluginID) {
		return nil
	}
	raw, ok := readMCPServersMap(s.mcpStashPath(pluginID))
	if !ok {
		return nil
	}
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// readMCPServersMap 读取 JSON 文件并返回顶层对象 map。F-03/F-07 语义：
//   - 文件不存在 → (nil, true) 视为空；
//   - 合法 null → (nil, true) 按空对象策略处理（不 panic）；
//   - 顶层非对象或解析失败 → (nil, false)，调用方须显式报错（不静默覆盖）。
func readMCPServersMap(path string) (map[string]interface{}, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, true
		}
		return nil, false
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, false
	}
	return doc, true // doc == nil 表示 JSON null → 按空对象处理
}

// mcpServersOf 从顶层对象取 mcpServers；存在但非对象 → 明确错误（不静默、不 panic）。
func mcpServersOf(doc map[string]interface{}) (map[string]interface{}, error) {
	raw, exists := doc["mcpServers"]
	if !exists || raw == nil {
		return map[string]interface{}{}, nil
	}
	servers, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf(".mcp.json 的 mcpServers 结构异常（不是对象）")
	}
	return servers, nil
}

// disableMCPServer 从 installPath/.mcp.json 移除 server 条目并存入 stash。
// 说明：.mcp.json 为小文件且结构固定（mcpServers 扁平键），map 编辑会重排
// 键序但语义等价；保留未知顶层键原样（顶层整体 unmarshal 到 map）。
func (s *Service) disableMCPServer(installPath, pluginID, name string) error {
	if !validStashName(name) || !validStashName(pluginID) {
		return fmt.Errorf("无效的 MCP server 名 %q（拒绝路径穿越）", name)
	}
	configPath := filepath.Join(installPath, ".mcp.json")
	doc, ok := readMCPServersMap(configPath)
	if !ok {
		return fmt.Errorf("读取/解析 .mcp.json 失败：%s（内容损坏，请手动检查）", configPath)
	}
	if doc == nil {
		doc = map[string]interface{}{} // JSON null → 空对象（F-03）
	}
	servers, err := mcpServersOf(doc)
	if err != nil {
		return err
	}
	entry, exists := servers[name]
	if !exists {
		return nil // 条目已不在（重复禁用）：幂等成功
	}

	stashPath := s.mcpStashPath(pluginID)
	stash, ok := readMCPServersMap(stashPath)
	if !ok {
		// F-07：stash 损坏不静默覆盖——保留原文件、显式报错（人工恢复线索）
		return fmt.Errorf("MCP stash 损坏，拒绝继续禁用（请手动检查 %s）", stashPath)
	}
	if stash == nil {
		stash = map[string]interface{}{} // JSON null → 空对象（F-03）
	}
	stash[name] = entry
	if err := writeJSONFile(stashPath, stash); err != nil {
		return err
	}

	delete(servers, name)
	if len(servers) == 0 {
		delete(doc, "mcpServers")
	}
	return writeJSONFile(configPath, doc)
}

// enableMCPServer 把 stash 中的 server 条目写回 installPath/.mcp.json。
// 目标已存在新版定义（plugin update 重装恢复）时不覆盖——仅清 stash（F-02）。
func (s *Service) enableMCPServer(installPath, pluginID, name string) error {
	if !validStashName(name) || !validStashName(pluginID) {
		return fmt.Errorf("无效的 MCP server 名 %q（拒绝路径穿越）", name)
	}
	stashPath := s.mcpStashPath(pluginID)
	stash, ok := readMCPServersMap(stashPath)
	if !ok {
		return fmt.Errorf("MCP stash 损坏，拒绝继续启用（请手动检查 %s）", stashPath)
	}
	if stash == nil {
		stash = map[string]interface{}{}
	}
	entry, exists := stash[name]
	if !exists {
		return nil // 无 stash：条目本就在位（或从未禁用），幂等成功
	}

	configPath := filepath.Join(installPath, ".mcp.json")
	doc, ok := readMCPServersMap(configPath)
	if !ok {
		return fmt.Errorf("读取/解析 .mcp.json 失败：%s（内容损坏，请手动检查）", configPath)
	}
	if doc == nil {
		doc = map[string]interface{}{} // JSON null → 空对象（F-03）
	}
	servers, err := mcpServersOf(doc)
	if err != nil {
		return err
	}
	if _, exists := servers[name]; exists {
		// F-02：新版定义已在位——不覆盖，仅丢弃 stash 旧条目（新文件优先）
		delete(stash, name)
		if len(stash) == 0 {
			return os.Remove(stashPath)
		}
		return writeJSONFile(stashPath, stash)
	}
	servers[name] = entry
	doc["mcpServers"] = servers
	if err := writeJSONFile(configPath, doc); err != nil {
		return err
	}

	delete(stash, name)
	if len(stash) == 0 {
		return os.Remove(stashPath)
	}
	return writeJSONFile(stashPath, stash)
}

// writeJSONFile 原子写 JSON（tmp+rename，缩进两空格）。
func writeJSONFile(path string, doc map[string]interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建目录失败：%s（%v）", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 JSON 失败（%v）", err)
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("写临时文件失败：%s（%v）", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("落盘失败：%s（%v）", path, err)
	}
	return nil
}
