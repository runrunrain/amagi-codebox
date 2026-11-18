package sharedmarket

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	catalogPath          = ".claude-plugin/marketplace.json"
	pluginSnapshotPrefix = ".plugin-update-"
)

// Each snapshot pairs a catalog entry with the plugin directory from the same checkout.
type pluginSnapshot struct {
	Dir   string        `json:"dir"`
	Entry catalogPlugin `json:"entry"`
}

func (s *Service) pluginSnapshotPath(snapshot pluginSnapshot) string {
	suffix, found := strings.CutPrefix(snapshot.Dir, pluginSnapshotPrefix)
	if !found || suffix == "" || strings.Trim(suffix, "0123456789") != "" {
		return ""
	}
	return filepath.Join(s.Root(), snapshot.Dir)
}

func (s *Service) removePluginSnapshot(snapshot pluginSnapshot) {
	if path := s.pluginSnapshotPath(snapshot); path != "" {
		if err := os.RemoveAll(path); err != nil && s.log != nil {
			s.log.Warn("sharedmarket", "清理插件更新缓存失败", fmt.Sprintf("dir=%s err=%v", path, err))
		}
	}
}

func (s *Service) pluginContent(market stateEntry, entry catalogPlugin) (catalogPlugin, string, string) {
	if snapshot, ok := market.PluginSnapshots[entry.Name]; ok && snapshot.Entry.Name == entry.Name {
		if path := s.pluginSnapshotPath(snapshot); path != "" {
			dir := filepath.Join(path, "plugin")
			info, err := os.Lstat(dir)
			if err == nil && info.IsDir() && withinRoot(s.Root(), dir) && withinRoot(path, dir) {
				return snapshot.Entry, dir, path
			}
		}
	}
	root := s.clonePath(market.Name)
	return entry, pluginDir(root, entry), root
}

// UpdatePlugin refreshes a single catalog entry and its files from the registered source.
// Keep the market checkout as the baseline so other entries keep their current versions.
func (s *Service) UpdatePlugin(name, pluginName string) (Plugin, error) {
	if !validName(name) || !validName(pluginName) {
		return Plugin{}, fmt.Errorf("无效的市场或插件名称 %q / %q", name, pluginName)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState()
	if err != nil {
		return Plugin{}, err
	}
	idx := state.marketIndex(name)
	if idx < 0 {
		return Plugin{}, fmt.Errorf("共用市场 %q 不存在", name)
	}
	market := &state.Marketplaces[idx]
	catalog, err := readCatalog(s.clonePath(name))
	if err != nil {
		return Plugin{}, fmt.Errorf("请先更新市场 %q：%w", name, err)
	}
	entry, found := catalog.findPlugin(pluginName)
	if !found || entry.sourcePath() == "" {
		return Plugin{}, fmt.Errorf("市场 %q 中的插件 %q 没有可更新的本地来源", name, pluginName)
	}

	snapshot, err := s.fetchPluginSnapshot(market.Source, pluginName)
	if err != nil {
		return Plugin{}, err
	}
	previous := market.PluginSnapshots[pluginName]
	if market.PluginSnapshots == nil {
		market.PluginSnapshots = make(map[string]pluginSnapshot)
	}
	market.PluginSnapshots[pluginName] = snapshot
	if err := s.writeState(state); err != nil {
		s.removePluginSnapshot(snapshot)
		return Plugin{}, err
	}
	s.removePluginSnapshot(previous)

	var deployed []string
	for _, origin := range s.scanSkillOrigins() {
		if origin.Marketplace == name && origin.Plugin == pluginName {
			deployed = append(deployed, origin.Skill)
		}
	}
	return s.scanPlugin(*market, entry, deployed), nil
}

func (s *Service) fetchPluginSnapshot(source, pluginName string) (pluginSnapshot, error) {
	tmp, err := os.MkdirTemp("", "amagi-market-update-")
	if err != nil {
		return pluginSnapshot{}, fmt.Errorf("创建市场更新暂存目录失败：%w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	checkout := filepath.Join(tmp, "market")
	ctx, cancel := context.WithTimeout(context.Background(), cloneTimeout)
	defer cancel()
	if output, err := s.git(ctx, "clone", "--depth", "1", "--no-local", "--", source, checkout); err != nil {
		return pluginSnapshot{}, fmt.Errorf("获取市场源失败（%w）：\n%s", err, strings.TrimSpace(output))
	}
	if path := filepath.Join(checkout, catalogPath); !withinRoot(checkout, path) || !isRegularFile(path) {
		return pluginSnapshot{}, fmt.Errorf("市场源缺少有效的 marketplace.json")
	}
	catalog, err := readCatalog(checkout)
	if err != nil {
		return pluginSnapshot{}, err
	}
	entry, found := catalog.findPlugin(pluginName)
	if !found {
		return pluginSnapshot{}, fmt.Errorf("市场最新目录中找不到插件 %q", pluginName)
	}
	sourceDir := pluginDir(checkout, entry)
	if sourceDir == "" || !withinRoot(checkout, sourceDir) {
		return pluginSnapshot{}, fmt.Errorf("插件 %q 的目录不可用或位于市场仓库之外", pluginName)
	}
	resolved, err := filepath.EvalSymlinks(sourceDir)
	if err != nil {
		return pluginSnapshot{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return pluginSnapshot{}, fmt.Errorf("插件 %q 的来源不是可读目录", pluginName)
	}

	path, err := os.MkdirTemp(s.Root(), pluginSnapshotPrefix)
	if err != nil {
		return pluginSnapshot{}, fmt.Errorf("创建插件更新目录失败：%w", err)
	}
	dir := filepath.Join(path, "plugin")
	if err := copyPluginSnapshot(resolved, dir); err != nil {
		_ = os.RemoveAll(path)
		return pluginSnapshot{}, fmt.Errorf("复制插件 %q 失败：%w", pluginName, err)
	}
	return pluginSnapshot{Dir: filepath.Base(path), Entry: entry}, nil
}

func copyPluginSnapshot(source, path string) error {
	if err := os.Mkdir(path, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	return errors.Join(copyDir(source, root), root.Close())
}
