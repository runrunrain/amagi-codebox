package envvars

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// EnvVar 单个自定义环境变量
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// GlobalEnvBackup 记录全局同步前某个 key 在 OS 中的原始状态
type GlobalEnvBackup struct {
	Existed bool   `json:"existed"`
	Value   string `json:"value,omitempty"`
}

// GlobalSyncStatus 全局同步状态，供前端展示
type GlobalSyncStatus struct {
	Supported    bool     `json:"supported"`
	Platform     string   `json:"platform"`
	Enabled      bool     `json:"enabled"`
	ManagedKeys  []string `json:"managedKeys"`
	ManagedCount int      `json:"managedCount"`
	Message      string   `json:"message,omitempty"`
}

// globalEnvPlatform 平台操作接口，用于可测试性
type globalEnvPlatform interface {
	readUserEnvVar(key string) (string, bool, error)
	writeUserEnvVar(key, value string) error
	deleteUserEnvVar(key string) error
	broadcastEnvChange() error
}

// envVarsFile envvars.json 文件结构
type envVarsFile struct {
	EnvVars               []EnvVar                   `json:"envVars"`
	GlobalSyncEnabled     bool                       `json:"globalSyncEnabled,omitempty"`
	GlobalSyncManagedKeys []string                   `json:"globalSyncManagedKeys,omitempty"`
	GlobalSyncBackups     map[string]GlobalEnvBackup `json:"globalSyncBackups,omitempty"`
}

// EnvVarsService 管理自定义环境变量，持久化到 envvars.json
type EnvVarsService struct {
	configPath            string
	envVars               []EnvVar
	globalSyncEnabled     bool
	globalSyncManagedKeys []string
	globalSyncBackups     map[string]GlobalEnvBackup
	mu                    sync.RWMutex
	platform              globalEnvPlatform
}

// NewEnvVarsService 创建新的 EnvVarsService 实例
func NewEnvVarsService(configDir string) *EnvVarsService {
	return &EnvVarsService{
		configPath:            filepath.Join(configDir, "envvars.json"),
		envVars:               []EnvVar{},
		globalSyncManagedKeys: []string{},
		globalSyncBackups:     map[string]GlobalEnvBackup{},
		platform:              newPlatformImpl(),
	}
}

// NewEnvVarsServiceWithPlatform 创建使用指定平台实现的服务实例（测试用）
func NewEnvVarsServiceWithPlatform(configDir string, p globalEnvPlatform) *EnvVarsService {
	return &EnvVarsService{
		configPath:            filepath.Join(configDir, "envvars.json"),
		envVars:               []EnvVar{},
		globalSyncManagedKeys: []string{},
		globalSyncBackups:     map[string]GlobalEnvBackup{},
		platform:              p,
	}
}

// Load 从磁盘加载环境变量配置
func (s *EnvVarsService) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.envVars = []EnvVar{}
			s.globalSyncEnabled = false
			s.globalSyncManagedKeys = []string{}
			s.globalSyncBackups = map[string]GlobalEnvBackup{}
			return nil
		}
		return fmt.Errorf("read envvars config: %w", err)
	}

	var f envVarsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("parse envvars json: %w", err)
	}
	if f.EnvVars == nil {
		f.EnvVars = []EnvVar{}
	}
	if f.GlobalSyncManagedKeys == nil {
		f.GlobalSyncManagedKeys = []string{}
	}
	if f.GlobalSyncBackups == nil {
		f.GlobalSyncBackups = map[string]GlobalEnvBackup{}
	}
	s.envVars = f.EnvVars
	s.globalSyncEnabled = f.GlobalSyncEnabled
	s.globalSyncManagedKeys = f.GlobalSyncManagedKeys
	s.globalSyncBackups = f.GlobalSyncBackups

	// 如果全局同步已开启，执行一次 reconcile 确保 OS 状态与配置一致
	if s.globalSyncEnabled {
		if err := s.reconcileGlobalEnvLocked(); err != nil {
			return fmt.Errorf("reconcile global env on load: %w", err)
		}
	}
	return nil
}

// save 持久化到磁盘（调用方必须持有写锁）
func (s *EnvVarsService) save() error {
	if err := os.MkdirAll(filepath.Dir(s.configPath), 0o755); err != nil {
		return fmt.Errorf("mkdir envvars dir: %w", err)
	}

	f := envVarsFile{
		EnvVars:               s.envVars,
		GlobalSyncEnabled:     s.globalSyncEnabled,
		GlobalSyncManagedKeys:  s.globalSyncManagedKeys,
		GlobalSyncBackups:      s.globalSyncBackups,
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal envvars: %w", err)
	}
	b = append(b, '\n')

	tmp := s.configPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write temp envvars: %w", err)
	}
	if err := os.Rename(tmp, s.configPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace envvars: %w", err)
	}
	return nil
}

// GetAll 返回所有自定义环境变量的副本
func (s *EnvVarsService) GetAll() []EnvVar {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]EnvVar, len(s.envVars))
	copy(out, s.envVars)
	return out
}

// Get 返回指定 key 的值，找不到时返回空字符串和 false
func (s *EnvVarsService) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ev := range s.envVars {
		if ev.Key == key {
			return ev.Value, true
		}
	}
	return "", false
}

// Set 设置单个环境变量（key 存在则更新，不存在则追加），并持久化
func (s *EnvVarsService) Set(key, value string) error {
	if key == "" {
		return errors.New("env var key is required")
	}
	if err := validateKey(key); err != nil {
		return fmt.Errorf("invalid env var key: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// 保存完整快照以便回滚
	oldVars := make([]EnvVar, len(s.envVars))
	copy(oldVars, s.envVars)
	oldManagedKeys := make([]string, len(s.globalSyncManagedKeys))
	copy(oldManagedKeys, s.globalSyncManagedKeys)
	oldBackups := make(map[string]GlobalEnvBackup, len(s.globalSyncBackups))
	for k, v := range s.globalSyncBackups {
		oldBackups[k] = v
	}

	found := false
	for i, ev := range s.envVars {
		if keysEqual(ev.Key, key) {
			s.envVars[i].Value = value
			found = true
			break
		}
	}
	if !found {
		s.envVars = append(s.envVars, EnvVar{Key: key, Value: value})
	}
	if s.globalSyncEnabled {
		if err := s.syncSingleKeyLocked(key, value); err != nil {
			// 同步失败，回滚完整内存状态
			s.envVars = oldVars
			s.globalSyncManagedKeys = oldManagedKeys
			s.globalSyncBackups = oldBackups
			return fmt.Errorf("sync global env for key %q: %w", key, err)
		}
	}
	return s.save()
}

// Delete 删除指定 key 的环境变量，并持久化
func (s *EnvVarsService) Delete(key string) error {
	if key == "" {
		return errors.New("env var key is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, ev := range s.envVars {
		if keysEqual(ev.Key, key) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("env var not found: %s", key)
	}

	// 保存完整快照以便回滚
	oldVars := make([]EnvVar, len(s.envVars))
	copy(oldVars, s.envVars)
	oldManagedKeys := make([]string, len(s.globalSyncManagedKeys))
	copy(oldManagedKeys, s.globalSyncManagedKeys)
	oldBackups := make(map[string]GlobalEnvBackup, len(s.globalSyncBackups))
	for k, v := range s.globalSyncBackups {
		oldBackups[k] = v
	}

	s.envVars = append(s.envVars[:idx], s.envVars[idx+1:]...)
	if s.globalSyncEnabled {
		if err := s.removeSyncKeyLocked(key); err != nil {
			// 同步失败，回滚完整内存状态
			s.envVars = oldVars
			s.globalSyncManagedKeys = oldManagedKeys
			s.globalSyncBackups = oldBackups
			return fmt.Errorf("remove global env for key %q: %w", key, err)
		}
	}
	return s.save()
}

// BatchSet 批量设置环境变量（全量替换），并持久化
func (s *EnvVarsService) BatchSet(vars []EnvVar) error {
	if vars == nil {
		vars = []EnvVar{}
	}
	// 统一 key 校验和重复检测
	if err := validateEnvVars(vars); err != nil {
		return fmt.Errorf("batch set validation: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 保存快照以便回滚
	oldVars := make([]EnvVar, len(s.envVars))
	copy(oldVars, s.envVars)
	oldManagedKeys := make([]string, len(s.globalSyncManagedKeys))
	copy(oldManagedKeys, s.globalSyncManagedKeys)
	oldBackups := make(map[string]GlobalEnvBackup, len(s.globalSyncBackups))
	for k, v := range s.globalSyncBackups {
		oldBackups[k] = v
	}

	s.envVars = vars
	if s.globalSyncEnabled {
		if err := s.reconcileGlobalEnvLocked(); err != nil {
			// 同步失败，回滚内存状态
			s.envVars = oldVars
			s.globalSyncManagedKeys = oldManagedKeys
			s.globalSyncBackups = oldBackups
			return fmt.Errorf("reconcile global env after batch set: %w", err)
		}
	}
	return s.save()
}

// SetBatch 是 BatchSet 的别名，提供语义上更清晰的接口
func (s *EnvVarsService) SetBatch(vars []EnvVar) error {
	return s.BatchSet(vars)
}

// Import 从 JSON 字符串导入环境变量（全量替换），并持久化
func (s *EnvVarsService) Import(jsonStr string) error {
	var f envVarsFile
	if err := json.Unmarshal([]byte(jsonStr), &f); err != nil {
		return fmt.Errorf("parse import json: %w", err)
	}
	if f.EnvVars == nil {
		f.EnvVars = []EnvVar{}
	}
	// 统一 key 校验和重复检测
	if err := validateEnvVars(f.EnvVars); err != nil {
		return fmt.Errorf("import validation: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 保存快照以便回滚
	oldVars := make([]EnvVar, len(s.envVars))
	copy(oldVars, s.envVars)
	oldManagedKeys := make([]string, len(s.globalSyncManagedKeys))
	copy(oldManagedKeys, s.globalSyncManagedKeys)
	oldBackups := make(map[string]GlobalEnvBackup, len(s.globalSyncBackups))
	for k, v := range s.globalSyncBackups {
		oldBackups[k] = v
	}

	s.envVars = f.EnvVars
	if s.globalSyncEnabled {
		if err := s.reconcileGlobalEnvLocked(); err != nil {
			// 同步失败，回滚内存状态
			s.envVars = oldVars
			s.globalSyncManagedKeys = oldManagedKeys
			s.globalSyncBackups = oldBackups
			return fmt.Errorf("reconcile global env after import: %w", err)
		}
	}
	return s.save()
}

// Export 导出为 JSON 字符串
func (s *EnvVarsService) Export() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f := envVarsFile{EnvVars: s.envVars}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal export: %w", err)
	}
	return string(b), nil
}

// GetJSON 获取所有环境变量的 JSON 格式（供 JSON 编辑器使用）
func (s *EnvVarsService) GetJSON() (string, error) {
	return s.Export()
}

// SaveJSON 从 JSON 字符串保存（供 JSON 编辑器使用，等同于 Import）
func (s *EnvVarsService) SaveJSON(jsonStr string) error {
	return s.Import(jsonStr)
}

// MergeWithSystem 返回合并后的环境变量列表（自定义变量覆盖系统变量）。
// 返回格式为 []string，每项格式为 "KEY=VALUE"，可直接传给 os/exec 或 ConPTY。
// 优先级：自定义 > 系统全局（os.Environ()）
// 注意：无论全局同步是否开启，此方法行为不变，始终为应用内终端提供进程级合并。
func (s *EnvVarsService) MergeWithSystem() []string {
	s.mu.RLock()
	customVars := make([]EnvVar, len(s.envVars))
	copy(customVars, s.envVars)
	s.mu.RUnlock()

	return mergeEnv(os.Environ(), customVars)
}

// GetGlobalSyncStatus 返回当前全局同步状态
func (s *EnvVarsService) GetGlobalSyncStatus() GlobalSyncStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	managed := make([]string, len(s.globalSyncManagedKeys))
	copy(managed, s.globalSyncManagedKeys)

	status := GlobalSyncStatus{
		Platform:     runtime.GOOS,
		Enabled:      s.globalSyncEnabled,
		ManagedKeys:  managed,
		ManagedCount: len(s.globalSyncManagedKeys),
	}

	if !isPlatformSupported() {
		status.Supported = false
		status.Message = "global env sync is only supported on Windows"
	} else {
		status.Supported = true
	}
	return status
}

// SetGlobalSyncEnabled 开启或关闭全局环境变量同步
func (s *EnvVarsService) SetGlobalSyncEnabled(enabled bool) (GlobalSyncStatus, error) {
	if enabled && !isPlatformSupported() {
		return s.GetGlobalSyncStatus(), fmt.Errorf("global env sync is only supported on Windows, current platform: %s", runtime.GOOS)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if enabled == s.globalSyncEnabled {
		// 状态未变化，直接返回
		status := s.buildStatusLocked()
		return status, nil
	}

	if enabled {
		if err := s.enableGlobalSyncLocked(); err != nil {
			// 开启失败，回滚所有被 reconcile 修改的状态
			s.globalSyncEnabled = false
			s.globalSyncManagedKeys = []string{}
			s.globalSyncBackups = map[string]GlobalEnvBackup{}
			return s.buildStatusLocked(), fmt.Errorf("enable global sync: %w", err)
		}
	} else {
		if err := s.disableGlobalSyncLocked(); err != nil {
			// 部分失败时返回错误，但保留可重试状态
			return s.buildStatusLocked(), fmt.Errorf("disable global sync: %w", err)
		}
	}

	if err := s.save(); err != nil {
		return s.buildStatusLocked(), fmt.Errorf("save after sync toggle: %w", err)
	}
	return s.buildStatusLocked(), nil
}

// buildStatusLocked 在持有锁时构建状态（调用方必须持有读锁或写锁）
func (s *EnvVarsService) buildStatusLocked() GlobalSyncStatus {
	managed := make([]string, len(s.globalSyncManagedKeys))
	copy(managed, s.globalSyncManagedKeys)

	status := GlobalSyncStatus{
		Platform:     runtime.GOOS,
		Enabled:      s.globalSyncEnabled,
		ManagedKeys:  managed,
		ManagedCount: len(s.globalSyncManagedKeys),
	}
	if !isPlatformSupported() {
		status.Supported = false
		status.Message = "global env sync is only supported on Windows"
	} else {
		status.Supported = true
	}
	return status
}

// enableGlobalSyncLocked 开启全局同步（调用方必须持有写锁）
// 先执行 reconcile，成功后再设置 enabled=true，避免失败时伪成功状态
func (s *EnvVarsService) enableGlobalSyncLocked() error {
	// 先执行 reconcile，不修改 enabled 状态
	if err := s.reconcileGlobalEnvLocked(); err != nil {
		// reconcile 失败，不改变任何状态，保持 enabled=false
		return err
	}
	// reconcile 成功，才设置 enabled
	s.globalSyncEnabled = true
	return nil
}

// disableGlobalSyncLocked 关闭全局同步（调用方必须持有写锁）
// 遍历 managedKeys，按 backup 恢复/删除 OS 值
func (s *EnvVarsService) disableGlobalSyncLocked() error {
	var firstErr error
	var restoredKeys []string

	for _, nk := range s.globalSyncManagedKeys {
		backup, hasBackup := s.globalSyncBackups[nk]
		if hasBackup && backup.Existed {
			// 恢复原始值
			if err := s.platform.writeUserEnvVar(nk, backup.Value); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("restore global env var %q: %w", nk, err)
				}
				continue
			}
		} else {
			// 删除本应用创建的变量
			if err := s.platform.deleteUserEnvVar(nk); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("delete global env var %q: %w", nk, err)
				}
				continue
			}
		}
		restoredKeys = append(restoredKeys, nk)
	}

	// 从 managedKeys 和 backups 中移除已成功恢复的 key
	if len(restoredKeys) > 0 {
		restoredSet := make(map[string]struct{}, len(restoredKeys))
		for _, k := range restoredKeys {
			restoredSet[k] = struct{}{}
		}
		newManaged := make([]string, 0, len(s.globalSyncManagedKeys))
		for _, k := range s.globalSyncManagedKeys {
			if _, ok := restoredSet[k]; !ok {
				newManaged = append(newManaged, k)
			}
		}
		s.globalSyncManagedKeys = newManaged
		for _, k := range restoredKeys {
			delete(s.globalSyncBackups, k)
		}
	}

	// 广播一次环境变更（即使部分失败也尝试广播）
	_ = s.platform.broadcastEnvChange()

	// 只有全部成功才清除 enabled 标记
	if firstErr == nil {
		s.globalSyncEnabled = false
		s.globalSyncManagedKeys = []string{}
		s.globalSyncBackups = map[string]GlobalEnvBackup{}
	}

	return firstErr
}

// reconcileGlobalEnvLocked 将当前 envVars 与全局环境同步（调用方必须持有写锁）
// desired = 当前 envVars key 集合
// managed = 当前 globalSyncManagedKeys 集合
// toWrite = desired 中的 key
// toRestoreOrDelete = managed - desired 中的 key
func (s *EnvVarsService) reconcileGlobalEnvLocked() error {
	// 构建 desired key -> value 映射（使用规范化 key）
	desiredMap := make(map[string]string) // normalizedKey -> value
	for _, ev := range s.envVars {
		if ev.Key == "" {
			continue
		}
		nk := normalizeKey(ev.Key)
		desiredMap[nk] = ev.Value
	}

	// 构建 managed key 集合（规范化）
	managedSet := make(map[string]struct{}, len(s.globalSyncManagedKeys))
	for _, k := range s.globalSyncManagedKeys {
		managedSet[k] = struct{}{}
	}

	// 写入 desired 中的所有 key
	for nk, val := range desiredMap {
		// 首次托管：备份 OS 原始值
		if _, hasBackup := s.globalSyncBackups[nk]; !hasBackup {
			osVal, osExisted, err := s.platform.readUserEnvVar(nk)
			if err != nil {
				return fmt.Errorf("read global env var %q for backup: %w", nk, err)
			}
			s.globalSyncBackups[nk] = GlobalEnvBackup{
				Existed: osExisted,
				Value:   osVal,
			}
		}
		// 写入新值
		if err := s.platform.writeUserEnvVar(nk, val); err != nil {
			return fmt.Errorf("write global env var %q: %w", nk, err)
		}
		// 确保在 managed 集合中
		if _, ok := managedSet[nk]; !ok {
			s.globalSyncManagedKeys = append(s.globalSyncManagedKeys, nk)
			managedSet[nk] = struct{}{}
		}
	}

	// 恢复/删除不在 desired 中但仍在 managed 中的 key
	var restoredKeys []string
	for _, nk := range s.globalSyncManagedKeys {
		if _, inDesired := desiredMap[nk]; inDesired {
			continue
		}
		backup, hasBackup := s.globalSyncBackups[nk]
		if hasBackup && backup.Existed {
			if err := s.platform.writeUserEnvVar(nk, backup.Value); err != nil {
				return fmt.Errorf("restore global env var %q on reconcile: %w", nk, err)
			}
		} else {
			if err := s.platform.deleteUserEnvVar(nk); err != nil {
				return fmt.Errorf("delete global env var %q on reconcile: %w", nk, err)
			}
		}
		restoredKeys = append(restoredKeys, nk)
	}

	// 清理已恢复/删除的 key
	if len(restoredKeys) > 0 {
		restoredSet := make(map[string]struct{}, len(restoredKeys))
		for _, k := range restoredKeys {
			restoredSet[k] = struct{}{}
		}
		newManaged := make([]string, 0, len(s.globalSyncManagedKeys))
		for _, k := range s.globalSyncManagedKeys {
			if _, ok := restoredSet[k]; !ok {
				newManaged = append(newManaged, k)
			}
		}
		s.globalSyncManagedKeys = newManaged
		for _, k := range restoredKeys {
			delete(s.globalSyncBackups, k)
		}
	}

	// 批量广播一次
	// 广播失败不阻断流程；外部桌面端可能需重启才能感知环境变更
	_ = s.platform.broadcastEnvChange()
	return nil
}

// syncSingleKeyLocked 同步单个 key 到全局环境（调用方必须持有写锁）
func (s *EnvVarsService) syncSingleKeyLocked(key, value string) error {
	nk := normalizeKey(key)
	// 首次托管：备份
	if _, hasBackup := s.globalSyncBackups[nk]; !hasBackup {
		osVal, osExisted, err := s.platform.readUserEnvVar(nk)
		if err != nil {
			return fmt.Errorf("read global env var for backup: %w", err)
		}
		s.globalSyncBackups[nk] = GlobalEnvBackup{
			Existed: osExisted,
			Value:   osVal,
		}
	}
	// 写入
	if err := s.platform.writeUserEnvVar(nk, value); err != nil {
		return fmt.Errorf("write global env var: %w", err)
	}
	// 确保在 managed 集合中
	found := false
	for _, mk := range s.globalSyncManagedKeys {
		if mk == nk {
			found = true
			break
		}
	}
	if !found {
		s.globalSyncManagedKeys = append(s.globalSyncManagedKeys, nk)
	}
	// 广播失败不阻断；外部桌面端可能需重启才能感知环境变更
	_ = s.platform.broadcastEnvChange()
	return nil
}

// removeSyncKeyLocked 从全局环境中移除指定 key（调用方必须持有写锁）
// 按 backup 恢复/删除 OS 中的值
func (s *EnvVarsService) removeSyncKeyLocked(key string) error {
	nk := normalizeKey(key)
	// 只处理在 managed 集合中的 key
	inManaged := false
	for _, mk := range s.globalSyncManagedKeys {
		if mk == nk {
			inManaged = true
			break
		}
	}
	if !inManaged {
		return nil
	}

	backup, hasBackup := s.globalSyncBackups[nk]
	if hasBackup && backup.Existed {
		if err := s.platform.writeUserEnvVar(nk, backup.Value); err != nil {
			return fmt.Errorf("restore global env var: %w", err)
		}
	} else {
		if err := s.platform.deleteUserEnvVar(nk); err != nil {
			return fmt.Errorf("delete global env var: %w", err)
		}
	}

	// 从 managed 和 backups 中移除
	newManaged := make([]string, 0, len(s.globalSyncManagedKeys))
	for _, mk := range s.globalSyncManagedKeys {
		if mk != nk {
			newManaged = append(newManaged, mk)
		}
	}
	s.globalSyncManagedKeys = newManaged
	delete(s.globalSyncBackups, nk)

	// 广播失败不阻断；外部桌面端可能需重启才能感知环境变更
	_ = s.platform.broadcastEnvChange()
	return nil
}

// validateKey 校验环境变量 key 是否合法
func validateKey(key string) error {
	if strings.ContainsRune(key, '=') {
		return fmt.Errorf("key must not contain '='")
	}
	if strings.ContainsRune(key, 0) {
		return fmt.Errorf("key must not contain NUL")
	}
	if strings.ContainsRune(key, '\n') || strings.ContainsRune(key, '\r') {
		return fmt.Errorf("key must not contain newline or carriage return")
	}
	return nil
}

// validateEnvVars 批量校验环境变量列表，拒绝空 key、非法字符、以及 normalize 后的重复 key
func validateEnvVars(vars []EnvVar) error {
	seen := make(map[string]string, len(vars)) // normalizedKey -> originalKey
	for i, ev := range vars {
		if ev.Key == "" {
			return fmt.Errorf("env var key at index %d is empty", i)
		}
		if err := validateKey(ev.Key); err != nil {
			return fmt.Errorf("invalid env var key %q: %w", ev.Key, err)
		}
		nk := normalizeKey(ev.Key)
		if orig, exists := seen[nk]; exists {
			return fmt.Errorf("duplicate env var key (case-insensitive): %q and %q", orig, ev.Key)
		}
		seen[nk] = ev.Key
	}
	return nil
}

// normalizeKey 返回用于内部比较的规范化 key
func normalizeKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

// keysEqual 比较两个 key 是否相等（Windows 下大小写不敏感）
func keysEqual(a, b string) bool {
	return normalizeKey(a) == normalizeKey(b)
}

// isPlatformSupported 返回当前平台是否支持全局环境变量同步
func isPlatformSupported() bool {
	return runtime.GOOS == "windows"
}

func mergeEnv(base []string, customVars []EnvVar) []string {
	values := make(map[string]string, len(base)+len(customVars))
	order := make([]string, 0, len(base)+len(customVars))
	seen := make(map[string]struct{}, len(base)+len(customVars))
	keyMap := make(map[string]string, len(base)+len(customVars))

	for _, kv := range base {
		k, v := splitEnvKV(kv)
		if k == "" {
			continue
		}
		nk := normalizeKey(k)
		actualKey, ok := keyMap[nk]
		if !ok {
			actualKey = k
			keyMap[nk] = actualKey
		}
		values[actualKey] = v
		if _, ok := seen[actualKey]; !ok {
			seen[actualKey] = struct{}{}
			order = append(order, actualKey)
		}
	}

	for _, ev := range customVars {
		if ev.Key == "" {
			continue
		}
		nk := normalizeKey(ev.Key)
		actualKey, ok := keyMap[nk]
		if !ok {
			actualKey = ev.Key
			keyMap[nk] = actualKey
		}
		values[actualKey] = ev.Value
		if _, ok := seen[actualKey]; !ok {
			seen[actualKey] = struct{}{}
			order = append(order, actualKey)
		}
	}

	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+values[k])
	}
	return out
}

func splitEnvKV(kv string) (key string, val string) {
	i := strings.IndexByte(kv, '=')
	if i <= 0 {
		return "", ""
	}
	return kv[:i], kv[i+1:]
}
