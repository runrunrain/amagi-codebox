// Package ompconfig 管理 Oh My Pi (omp) 的结构化配置文件：
// 读写 <agentDir>/config.yml（modelRoles / task.agentModelOverrides 等），
// 并从 <agentDir>/models.yml 抽取只读的 provider→model 目录，
// 供前端可视化配置界面的下拉关联使用。
//
// agentDir 解析：优先 $PI_CODING_AGENT_DIR，否则 ~/.omp/agent。
package ompconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Service 提供对 omp 配置与模型目录的读写访问。无状态。
type Service struct{}

// NewService 创建 omp 配置服务。
func NewService() *Service {
	return &Service{}
}

// agentDir 返回 omp 的配置根目录。
func agentDir() string {
	if env := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".omp", "agent")
	}
	return filepath.Join(".", ".omp", "agent")
}

func configPath() string {
	return filepath.Join(agentDir(), "config.yml")
}

func modelsConfigPath() string {
	return filepath.Join(agentDir(), "models.yml")
}

// GetOmpConfig 读取 config.yml 内容。文件缺失时返回最小骨架；
// YAML 非法或根不是映射时原样返回内容，供用户在源码模式修复。
func (s *Service) GetOmpConfig() (string, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "modelRoles: {}\n", nil
		}
		return "", fmt.Errorf("read omp config: %w", err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil || root == nil {
		return string(data), nil
	}
	formatted, err := marshalYAML(root)
	if err != nil {
		return "", fmt.Errorf("format omp config: %w", err)
	}
	return formatted, nil
}

// SaveOmpConfig 校验并保存 config.yml：必须是合法 YAML 且根为映射。
// 通过临时文件原子写入（0600）。
func (s *Service) SaveOmpConfig(content string) error {
	var root map[string]any
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}
	if root == nil {
		return errors.New("invalid config: root must be a YAML mapping")
	}
	formatted, err := marshalYAML(root)
	if err != nil {
		return fmt.Errorf("format YAML: %w", err)
	}

	path := configPath()
	if err := ensureDir(path); err != nil {
		return err
	}
	return writePrivateFile(path, []byte(formatted))
}

// GetOmpConfigPath 返回 config.yml 的绝对路径（供前端展示/复制）。
func (s *Service) GetOmpConfigPath() (string, error) {
	return configPath(), nil
}

// marshalYAML 以 2 空格缩进序列化，保持稳定的输出格式。
func marshalYAML(root map[string]any) (string, error) {
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return "", err
	}
	_ = enc.Close()
	return buf.String(), nil
}

// ensureDir creates the parent directory of the given path if it does not exist.
func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	} else if err != nil {
		return fmt.Errorf("stat directory %s: %w", dir, err)
	}
	return nil
}

// writePrivateFile 临时文件 + rename 原子写入，权限 0600。
func writePrivateFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp file %s: %w", tmp, err)
	}
	defer func() { _ = os.Remove(tmp) }()
	_ = os.Chmod(tmp, 0o600)

	if err := os.Rename(tmp, path); err == nil {
		_ = os.Chmod(path, 0o600)
		return nil
	} else if overwriteErr := os.WriteFile(path, data, 0o600); overwriteErr == nil {
		_ = os.Chmod(path, 0o600)
		return nil
	} else {
		return fmt.Errorf("replace config file %s: %w", path, err)
	}
}

// GetModelsConfig 读取 models.yml（provider 注册表）内容。
// 文件缺失时返回只含空 providers 的骨架；YAML 非法或根不是映射时
// 原样返回内容，供用户在源码模式修复。
func (s *Service) GetModelsConfig() (string, error) {
	data, err := os.ReadFile(modelsConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "providers: {}\n", nil
		}
		return "", fmt.Errorf("read omp models config: %w", err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil || root == nil {
		return string(data), nil
	}
	formatted, err := marshalYAML(root)
	if err != nil {
		return "", fmt.Errorf("format models config: %w", err)
	}
	return formatted, nil
}

// SaveModelsConfig 校验并保存 models.yml：必须是合法 YAML 且根为映射。
// 文件含 apiKey 等敏感信息，通过临时文件原子写入（0600）。
func (s *Service) SaveModelsConfig(content string) error {
	var root map[string]any
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}
	if root == nil {
		return errors.New("invalid config: root must be a YAML mapping")
	}
	formatted, err := marshalYAML(root)
	if err != nil {
		return fmt.Errorf("format YAML: %w", err)
	}

	path := modelsConfigPath()
	if err := ensureDir(path); err != nil {
		return err
	}
	return writePrivateFile(path, []byte(formatted))
}

// GetModelsConfigPath 返回 models.yml 的绝对路径，供前端展示。
func (s *Service) GetModelsConfigPath() (string, error) {
	return modelsConfigPath(), nil
}

// ModelCatalogEntry / ModelCatalogProvider / ModelCatalog 与 piconfig 保持
// 相同的 JSON 形态，前端可共用同一套目录解析逻辑。
type ModelCatalogEntry struct {
	ID             string   `json:"id"`
	Name           string   `json:"name,omitempty"`
	Reasoning      bool     `json:"reasoning"`
	ThinkingLevels []string `json:"thinkingLevels,omitempty"`
	ContextWindow  int      `json:"contextWindow,omitempty"`
}

type ModelCatalogProvider struct {
	Name   string              `json:"name"`
	API    string              `json:"api,omitempty"`
	Models []ModelCatalogEntry `json:"models"`
	// HasAuth 表示该 provider 已有可用凭据（omp 凭据内联在 models.yml：
	// apiKey / auth / authHeader），供前端下拉标注。
	HasAuth bool `json:"hasAuth"`
}

type ModelCatalog struct {
	Providers []ModelCatalogProvider `json:"providers"`
}

// GetOmpModelCatalog 读取 models.yml，抽取 provider→models 目录（只读，
// 不含 apiKey 等敏感信息），序列化为 JSON 返回。文件缺失时返回空目录。
func (s *Service) GetOmpModelCatalog() (string, error) {
	catalog := ModelCatalog{Providers: []ModelCatalogProvider{}}

	data, err := os.ReadFile(modelsConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			out, _ := json.Marshal(catalog)
			return string(out), nil
		}
		return "", fmt.Errorf("read omp models config: %w", err)
	}

	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return "", fmt.Errorf("parse omp models config: %w", err)
	}
	providersRaw, ok := root["providers"].(map[string]any)
	if !ok {
		out, _ := json.Marshal(catalog)
		return string(out), nil
	}

	names := make([]string, 0, len(providersRaw))
	for name := range providersRaw {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entry := ModelCatalogProvider{Name: name, Models: []ModelCatalogEntry{}}
		provider, _ := providersRaw[name].(map[string]any)
		if provider != nil {
			if api, ok := provider["api"].(string); ok {
				entry.API = api
			}
			if key, ok := provider["apiKey"].(string); ok && key != "" {
				entry.HasAuth = true
			}
			if _, ok := provider["auth"]; ok {
				entry.HasAuth = true
			}
			if header, ok := provider["authHeader"].(bool); ok && header {
				entry.HasAuth = true
			}
			if models, ok := provider["models"].([]any); ok {
				for _, m := range models {
					model, ok := m.(map[string]any)
					if !ok {
						continue
					}
					me := ModelCatalogEntry{}
					if id, ok := model["id"].(string); ok {
						me.ID = id
					}
					if me.ID == "" {
						continue
					}
					if n, ok := model["name"].(string); ok {
						me.Name = n
					}
					if r, ok := model["reasoning"].(bool); ok {
						me.Reasoning = r
					}
					if cw, ok := toFloat(model["contextWindow"]); ok {
						me.ContextWindow = int(cw)
					}
					if tlm, ok := model["thinkingLevelMap"].(map[string]any); ok {
						me.ThinkingLevels = mapKeysSorted(tlm)
					} else if thinking, ok := model["thinking"].(map[string]any); ok {
						if levels, ok := thinking["levels"].([]any); ok {
							me.ThinkingLevels = stringListSorted(levels)
						}
					}
					entry.Models = append(entry.Models, me)
				}
			}
		}
		catalog.Providers = append(catalog.Providers, entry)
	}

	out, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode model catalog: %w", err)
	}
	return string(out), nil
}

// yaml.v3 会把整数解码为 int、浮点解码为 float64，toFloat 统一数值转换。
func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case float64:
		return t, true
	default:
		return 0, false
	}
}

func mapKeysSorted(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func stringListSorted(items []any) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
