package envvars

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// EnvVar 单个自定义环境变量
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// envVarsFile envvars.json 文件结构
type envVarsFile struct {
	EnvVars []EnvVar `json:"envVars"`
}

// EnvVarsService 管理自定义环境变量，持久化到 envvars.json
type EnvVarsService struct {
	configPath string
	envVars    []EnvVar
	mu         sync.RWMutex
}

// NewEnvVarsService 创建新的 EnvVarsService 实例
func NewEnvVarsService(configDir string) *EnvVarsService {
	return &EnvVarsService{
		configPath: filepath.Join(configDir, "envvars.json"),
		envVars:    []EnvVar{},
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
	s.envVars = f.EnvVars
	return nil
}

// save 持久化到磁盘（调用方必须持有写锁）
func (s *EnvVarsService) save() error {
	if err := os.MkdirAll(filepath.Dir(s.configPath), 0o755); err != nil {
		return fmt.Errorf("mkdir envvars dir: %w", err)
	}

	f := envVarsFile{EnvVars: s.envVars}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ev := range s.envVars {
		if ev.Key == key {
			s.envVars[i].Value = value
			return s.save()
		}
	}
	s.envVars = append(s.envVars, EnvVar{Key: key, Value: value})
	return s.save()
}

// Delete 删除指定 key 的环境变量，并持久化
func (s *EnvVarsService) Delete(key string) error {
	if key == "" {
		return errors.New("env var key is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ev := range s.envVars {
		if ev.Key == key {
			s.envVars = append(s.envVars[:i], s.envVars[i+1:]...)
			return s.save()
		}
	}
	return fmt.Errorf("env var not found: %s", key)
}

// BatchSet 批量设置环境变量（全量替换），并持久化
func (s *EnvVarsService) BatchSet(vars []EnvVar) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vars == nil {
		vars = []EnvVar{}
	}
	s.envVars = vars
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.envVars = f.EnvVars
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
func (s *EnvVarsService) MergeWithSystem() []string {
	s.mu.RLock()
	customVars := make([]EnvVar, len(s.envVars))
	copy(customVars, s.envVars)
	s.mu.RUnlock()

	// 以系统环境变量为基础
	base := os.Environ()

	// 构建覆盖 map（自定义变量）
	overrides := make(map[string]string, len(customVars))
	for _, ev := range customVars {
		if ev.Key != "" {
			overrides[ev.Key] = ev.Value
		}
	}

	if len(overrides) == 0 {
		return base
	}

	// 构建结果：保留系统变量，覆盖或追加自定义变量
	result := make([]string, 0, len(base)+len(overrides))
	overriddenKeys := make(map[string]struct{}, len(overrides))

	for _, kv := range base {
		// 找到 = 的位置分割 key
		eq := -1
		for i, c := range kv {
			if c == '=' {
				eq = i
				break
			}
		}
		if eq < 0 {
			result = append(result, kv)
			continue
		}
		k := kv[:eq]
		if newVal, ok := overrides[k]; ok {
			result = append(result, k+"="+newVal)
			overriddenKeys[k] = struct{}{}
		} else {
			result = append(result, kv)
		}
	}

	// 追加系统中不存在的自定义变量
	for _, ev := range customVars {
		if ev.Key == "" {
			continue
		}
		if _, alreadySet := overriddenKeys[ev.Key]; !alreadySet {
			result = append(result, ev.Key+"="+ev.Value)
		}
	}

	return result
}
