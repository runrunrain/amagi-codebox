// Package ompconfig 管理 Oh My Pi (omp) 的结构化配置文件：
// 读写 <agentDir>/config.yml（modelRoles / task.agentModelOverrides 等），
// 并从 <agentDir>/models.yml 抽取只读的 provider→model 目录，
// 供前端可视化配置界面的下拉关联使用。
//
// agentDir 解析：优先 $PI_CODING_AGENT_DIR，否则 ~/.omp/agent。
package ompconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	// Source 区分来源：custom = models.yml 注册表（可编辑），builtin = omp
	// 内置模型目录（`omp models ls --json` 拉取，如 openai-codex 等 OAuth
	// 登录提供商，凭据由 omp 自身管理）。custom 省略该字段。
	Source string `json:"source,omitempty"`
}

type ModelCatalog struct {
	Providers []ModelCatalogProvider `json:"providers"`
}

// GetOmpModelCatalog 读取 models.yml，抽取 provider→models 目录（只读，
// 不含 apiKey 等敏感信息），并追加 `omp models ls --json` 返回的内置目录
// 提供商（models.yml 已有的自定义条目优先）。文件缺失时返回仅内置的目录。
func (s *Service) GetOmpModelCatalog() (string, error) {
	catalog := ModelCatalog{Providers: []ModelCatalogProvider{}}

	data, err := os.ReadFile(modelsConfigPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read omp models config: %w", err)
	}

	names := []string{}
	providersRaw := map[string]any{}
	if err == nil {
		var root map[string]any
		if err := yaml.Unmarshal(data, &root); err != nil {
			return "", fmt.Errorf("parse omp models config: %w", err)
		}
		if raw, ok := root["providers"].(map[string]any); ok {
			providersRaw = raw
		}
		for name := range providersRaw {
			names = append(names, name)
		}
		sort.Strings(names)
	}

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

	// 追加 omp 内置目录（`omp models ls --json`）：补齐仅内置/OAuth 登录的
	// 提供商（如 openai-codex）。CLI 不可用或失败时静默降级为仅自定义目录。
	if builtin, err := loadOmpBuiltinCatalog(names); err == nil {
		catalog.Providers = append(catalog.Providers, builtin...)
	}

	out, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode model catalog: %w", err)
	}
	return string(out), nil
}

// loadOmpBuiltinCatalog 执行 `omp models ls --json` 并返回不在自定义注册表
// 中的内置 providers。omp 内置提供商凭据（OAuth 等）由 omp 自身管理，
// HasAuth 置 false 但 Source=builtin，前端以「内置」标注区分。
func loadOmpBuiltinCatalog(customNames []string) ([]ModelCatalogProvider, error) {
	output, err := runOmpModelsList()
	if err != nil {
		return nil, err
	}
	return parseOmpModelsList(output, customNames)
}

// runOmpModelsList 在 PATH 及常见安装路径中定位 omp 并执行
// `omp models ls --json`（5 秒超时）。
func runOmpModelsList() ([]byte, error) {
	candidates := []string{"omp", "/opt/homebrew/bin/omp", "/usr/local/bin/omp"}
	var bin string
	for _, c := range candidates {
		if c != "omp" {
			if _, err := os.Stat(c); err != nil {
				continue
			}
			bin = c
			break
		}
		if path, err := exec.LookPath(c); err == nil {
			bin = path
			break
		}
	}
	if bin == "" {
		return nil, errors.New("omp CLI not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "models", "ls", "--json", "--no-extensions")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run omp models ls: %w", err)
	}
	return out, nil
}

// parseOmpModelsList 解析 `omp models ls --json` 输出：
// { models: [ { provider, id, name, reasoning, contextWindow, thinking } ] }。
func parseOmpModelsList(output []byte, customNames []string) ([]ModelCatalogProvider, error) {
	var parsed struct {
		Models []struct {
			Provider      string `json:"provider"`
			ID            string `json:"id"`
			Name          string `json:"name"`
			Reasoning     bool   `json:"reasoning"`
			ContextWindow int    `json:"contextWindow"`
			Thinking      *struct {
				Levels []string `json:"levels"`
			} `json:"thinking"`
		} `json:"models"`
	}
	if err := json.Unmarshal(output, &parsed); err != nil {
		return nil, fmt.Errorf("parse omp models ls output: %w", err)
	}

	custom := map[string]bool{}
	for _, n := range customNames {
		custom[n] = true
	}

	byProvider := map[string]*ModelCatalogProvider{}
	var order []string
	for _, m := range parsed.Models {
		if m.Provider == "" || m.ID == "" || custom[m.Provider] {
			continue
		}
		entry, ok := byProvider[m.Provider]
		if !ok {
			entry = &ModelCatalogProvider{Name: m.Provider, Models: []ModelCatalogEntry{}, Source: "builtin"}
			byProvider[m.Provider] = entry
			order = append(order, m.Provider)
		}
		me := ModelCatalogEntry{ID: m.ID, Name: m.Name, Reasoning: m.Reasoning, ContextWindow: m.ContextWindow}
		if m.Thinking != nil && len(m.Thinking.Levels) > 0 {
			levels := append([]string(nil), m.Thinking.Levels...)
			sort.Strings(levels)
			me.ThinkingLevels = levels
		}
		entry.Models = append(entry.Models, me)
	}

	out := make([]ModelCatalogProvider, 0, len(order))
	sort.Strings(order)
	for _, name := range order {
		out = append(out, *byProvider[name])
	}
	return out, nil
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
