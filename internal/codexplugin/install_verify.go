package codexplugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxCodexAgentFileBytes = 1024 * 1024

var safeCodexAgentNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type pluginInstallVerification struct {
	Plugin             CodexPlugin
	Manifest           CodexPluginManifest
	ManifestPath       string
	SyncedCustomAgents []string
}

func (s *Service) verifyPluginInstall(ctx context.Context, pluginID string) (*pluginInstallVerification, error) {
	if err := validatePluginID(pluginID); err != nil {
		return nil, err
	}
	name, marketplace := splitPluginID(pluginID)
	states, err := s.readPluginStates()
	if err != nil {
		return nil, fmt.Errorf("读取安装后的 Codex config.toml 失败: %w", err)
	}
	enabled, ok := states[pluginID]
	if !ok {
		return nil, fmt.Errorf("config.toml 缺少插件启用记录 %s", pluginID)
	}
	if !enabled {
		return nil, fmt.Errorf("config.toml 中插件 %s 未启用", pluginID)
	}

	plugins, err := s.listPlugins(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("读取安装后的 Codex 插件列表失败: %w", err)
	}
	installed, ok := findInstalledPluginForVerification(plugins, pluginID, name, marketplace)
	if !ok {
		if root, manifestPath := s.resolvePluginRoot("", "", name, marketplace); root != "" {
			installed = CodexPlugin{
				ID:           pluginID,
				Name:         name,
				Marketplace:  marketplace,
				Enabled:      true,
				InstallPath:  root,
				ManifestPath: manifestPath,
				Source:       "cacheVerification",
			}
			ok = true
		}
	}
	if !ok {
		return nil, fmt.Errorf("安装后未在 Codex 插件列表或缓存目录中找到 %s", pluginID)
	}
	if !installed.Enabled {
		return nil, fmt.Errorf("安装后插件 %s 仍处于禁用状态", pluginID)
	}

	root, resolvedManifestPath := s.resolvePluginRoot(installed.InstallPath, installed.ManifestPath, installed.Name, installed.Marketplace)
	if root == "" {
		return nil, fmt.Errorf("无法定位插件 %s 的安装根目录", pluginID)
	}
	installed.InstallPath = root
	installed.ManifestPath = resolvedManifestPath

	manifest, manifestPath, err := s.readPluginManifest(root)
	if err != nil {
		return nil, fmt.Errorf("读取插件 manifest 失败: %w", err)
	}
	if strings.TrimSpace(manifest.Name) != "" && !strings.EqualFold(strings.TrimSpace(manifest.Name), name) {
		return nil, fmt.Errorf("manifest name=%q 与插件 ID %s 不一致", manifest.Name, pluginID)
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return nil, fmt.Errorf("manifest 缺少 version，无法保证插件版本管理正确")
	}
	if installed.Version != "" && !samePluginVersion(installed.Version, manifest.Version) {
		return nil, fmt.Errorf("Codex 列表版本 %q 与 manifest version %q 不一致", installed.Version, manifest.Version)
	}

	syncedAgents, err := s.syncCodexCustomAgents(root)
	if err != nil {
		return nil, err
	}
	return &pluginInstallVerification{
		Plugin:             installed,
		Manifest:           manifest,
		ManifestPath:       firstNonEmpty(manifestPath, resolvedManifestPath),
		SyncedCustomAgents: syncedAgents,
	}, nil
}

func findInstalledPluginForVerification(plugins []CodexPlugin, pluginID, name, marketplace string) (CodexPlugin, bool) {
	for _, plugin := range plugins {
		if strings.EqualFold(strings.TrimSpace(plugin.ID), pluginID) {
			return plugin, true
		}
	}
	for _, plugin := range plugins {
		if strings.EqualFold(strings.TrimSpace(plugin.Name), name) && strings.EqualFold(strings.TrimSpace(plugin.Marketplace), marketplace) {
			return plugin, true
		}
	}
	return CodexPlugin{}, false
}

func samePluginVersion(a, b string) bool {
	return normalizePluginVersion(a) == normalizePluginVersion(b)
}

func normalizePluginVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(strings.ToLower(version)), "v")
}

func appendInstallVerificationOutput(result *CommandResult, verification *pluginInstallVerification) {
	if result == nil || verification == nil {
		return
	}
	lines := make([]string, 0, 3)
	if output := strings.TrimSpace(result.Output); output != "" {
		lines = append(lines, output)
	}
	lines = append(lines, fmt.Sprintf("安装后校验通过：%s version=%s path=%s", verification.Plugin.ID, verification.Manifest.Version, verification.Plugin.InstallPath))
	if len(verification.SyncedCustomAgents) > 0 {
		lines = append(lines, "已同步 Codex custom agents: "+strings.Join(verification.SyncedCustomAgents, ", "))
	}
	result.Success = true
	result.Output = strings.Join(lines, "\n")
}

func (s *Service) syncCodexCustomAgents(pluginRoot string) ([]string, error) {
	sourceDir := filepath.Join(pluginRoot, "codex-agents")
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("读取 Codex custom agents 目录失败: %w", err)
	}
	targetDir := filepath.Join(s.codexDir, "agents")
	synced := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".toml") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("拒绝同步符号链接 custom agent: %s", entry.Name())
		}
		if err := validateCodexAgentFileName(entry.Name()); err != nil {
			return nil, err
		}
		sourcePath := filepath.Join(sourceDir, entry.Name())
		content, err := os.ReadFile(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("读取 custom agent %s 失败: %w", sourcePath, err)
		}
		if err := validateCodexCustomAgentToml(entry.Name(), content); err != nil {
			return nil, err
		}
		targetPath := filepath.Join(targetDir, entry.Name())
		if err := writeFileAtomicallyWithBackup(targetPath, content, 0600); err != nil {
			return nil, fmt.Errorf("写入 Codex custom agent %s 失败: %w", targetPath, err)
		}
		written, err := os.ReadFile(targetPath)
		if err != nil {
			return nil, fmt.Errorf("复核 Codex custom agent %s 失败: %w", targetPath, err)
		}
		if !bytes.Equal(written, content) {
			return nil, fmt.Errorf("复核 Codex custom agent %s 失败: 写入内容与插件源不一致", targetPath)
		}
		synced = append(synced, entry.Name())
	}
	sort.Strings(synced)
	return synced, nil
}

func validateCodexAgentFileName(fileName string) error {
	if fileName != filepath.Base(fileName) {
		return fmt.Errorf("custom agent 文件名不安全: %s", fileName)
	}
	if !strings.EqualFold(filepath.Ext(fileName), ".toml") {
		return fmt.Errorf("custom agent 文件必须是 .toml: %s", fileName)
	}
	base := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if base == "" || !safeCodexAgentNamePattern.MatchString(base) {
		return fmt.Errorf("custom agent 文件名只能包含字母、数字、点、下划线和连字符: %s", fileName)
	}
	return nil
}

func validateCodexCustomAgentToml(fileName string, content []byte) error {
	if len(bytes.TrimSpace(content)) == 0 {
		return fmt.Errorf("custom agent %s 内容为空", fileName)
	}
	if len(content) > maxCodexAgentFileBytes {
		return fmt.Errorf("custom agent %s 超过 %d bytes", fileName, maxCodexAgentFileBytes)
	}
	fields := parseSimpleTomlStringFields(string(content))
	for _, required := range []string{"name", "description", "developer_instructions"} {
		if strings.TrimSpace(fields[required]) == "" {
			return fmt.Errorf("custom agent %s 缺少必需字段 %s", fileName, required)
		}
	}
	if effort := strings.TrimSpace(fields["model_reasoning_effort"]); effort != "" && !isSupportedReasoningEffort(effort) {
		return fmt.Errorf("custom agent %s 的 model_reasoning_effort=%q 不受支持", fileName, effort)
	}
	return nil
}

func isSupportedReasoningEffort(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none", "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
}

func writeFileAtomicallyWithBackup(path string, next []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	previous, readErr := os.ReadFile(path)
	if readErr == nil {
		if bytes.Equal(previous, next) {
			return nil
		}
		backupPath := fmt.Sprintf("%s.bak.%s", path, time.Now().Format("20060102150405"))
		if err := os.WriteFile(backupPath, previous, mode); err != nil {
			return err
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(next); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
