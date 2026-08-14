// Package piconfig 管理 pi（含 amagi-pi 插件）的结构化配置文件：
// 读写 <agentDir>/amagi.json（profile / 角色 model / MCP 路由），
// 并从 <agentDir>/models.json 抽取只读的 provider→model 目录，
// 供前端可视化配置界面的下拉关联使用。
//
// agentDir 解析复刻 pi getAgentDir：优先 $PI_CODING_AGENT_DIR，否则 ~/.pi/agent。
package piconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// defaultAmagiConfig 是 amagi.json 缺失时返回的默认骨架，
// 与 amagi-pi 首次模型解析时自动创建的内容保持一致。
const defaultAmagiConfig = "{\n  \"$schema\": \"https://raw.githubusercontent.com/runrunrain/amagi-pi/main/schemas/amagi-config.json\",\n  \"profile\": \"tiered\",\n  \"agents\": {},\n  \"mcp\": {\n    \"default\": [],\n    \"agents\": {}\n  }\n}\n"

// Service 提供对 pi amagi 配置与模型目录的读写访问。
// 无状态：每次调用时解析 agentDir，保证路径始终最新。
type Service struct{}

// NewService 创建 pi 配置服务。
func NewService() *Service {
	return &Service{}
}

// agentDir 返回 pi 的配置根目录（复刻 pi getAgentDir 语义）。
func agentDir() string {
	if env := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".pi", "agent")
	}
	return filepath.Join(".", ".pi", "agent")
}

func amagiConfigPath() string {
	return filepath.Join(agentDir(), "amagi.json")
}

func modelsConfigPath() string {
	return filepath.Join(agentDir(), "models.json")
}

func authConfigPath() string {
	return filepath.Join(agentDir(), "auth.json")
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

// GetAmagiConfig 读取 amagi.json 内容。文件缺失时返回默认骨架；
// JSON 非法或根不是对象时原样返回内容，供用户在源码模式修复。
func (s *Service) GetAmagiConfig() (string, error) {
	data, err := os.ReadFile(amagiConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultAmagiConfig, nil
		}
		return "", fmt.Errorf("read amagi config: %w", err)
	}
	if !json.Valid(data) {
		return string(data), nil
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return string(data), nil
	}
	formatted, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return "", fmt.Errorf("format amagi config: %w", err)
	}
	return string(formatted) + "\n", nil
}

// SaveAmagiConfig 校验并保存 amagi.json：必须是合法 JSON 且根为对象。
// 通过临时文件原子写入（0600，文件包含模型配置但不含密钥，仍按私有对待）。
func (s *Service) SaveAmagiConfig(content string) error {
	if !json.Valid([]byte(content)) {
		return fmt.Errorf("invalid JSON: content is not valid JSON")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(content), &obj); err != nil || obj == nil {
		return errors.New("invalid config: root must be a JSON object")
	}
	formatted, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("format JSON: %w", err)
	}
	formatted = append(formatted, '\n')

	path := amagiConfigPath()
	if err := ensureDir(path); err != nil {
		return err
	}
	return writePrivateFile(path, formatted)
}

// GetAmagiConfigPath 返回 amagi.json 的绝对路径（供前端展示/复制）。
func (s *Service) GetAmagiConfigPath() (string, error) {
	return amagiConfigPath(), nil
}

// writePrivateFile 临时文件 + rename 原子写入，权限 0600；
// rename 失败（如 Windows 文件被占用）时回退直接覆盖。
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

// GetModelsConfig 读取 models.json（provider 注册表）内容。
// 文件缺失时返回只含空 providers 的骨架；JSON 非法或根不是对象时
// 原样返回内容，供用户在源码模式修复。
func (s *Service) GetModelsConfig() (string, error) {
	data, err := os.ReadFile(modelsConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "{\n  \"providers\": {}\n}\n", nil
		}
		return "", fmt.Errorf("read pi models config: %w", err)
	}
	if !json.Valid(data) {
		return string(data), nil
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return string(data), nil
	}
	formatted, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return "", fmt.Errorf("format models config: %w", err)
	}
	return string(formatted) + "\n", nil
}

// SaveModelsConfig 校验并保存 models.json：必须是合法 JSON 且根为对象。
// 文件含 apiKey 等敏感信息，通过临时文件原子写入（0600）。
func (s *Service) SaveModelsConfig(content string) error {
	if !json.Valid([]byte(content)) {
		return fmt.Errorf("invalid JSON: content is not valid JSON")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(content), &obj); err != nil || obj == nil {
		return errors.New("invalid config: root must be a JSON object")
	}
	formatted, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("format JSON: %w", err)
	}
	formatted = append(formatted, '\n')

	path := modelsConfigPath()
	if err := ensureDir(path); err != nil {
		return err
	}
	return writePrivateFile(path, formatted)
}

// GetModelsConfigPath 返回 models.json 的绝对路径，供前端展示。
func (s *Service) GetModelsConfigPath() (string, error) {
	return modelsConfigPath(), nil
}

// GetAuthConfig 读取 auth.json（提供商凭据）内容。文件缺失时返回空对象骨架；
// JSON 非法或根不是对象时原样返回内容，供用户在源码模式修复。
// 文件含明文凭据，仅本地读取展示，写回走 SaveAuthConfig。
func (s *Service) GetAuthConfig() (string, error) {
	data, err := os.ReadFile(authConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "{\n}\n", nil
		}
		return "", fmt.Errorf("read pi auth config: %w", err)
	}
	if !json.Valid(data) {
		return string(data), nil
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return string(data), nil
	}
	formatted, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return "", fmt.Errorf("format auth config: %w", err)
	}
	if string(formatted) == "{}" {
		formatted = []byte("{\n}")
	}
	return string(formatted) + "\n", nil
}

// SaveAuthConfig 校验并保存 auth.json：必须是合法 JSON 且根为对象。
// 含明文凭据，通过临时文件原子写入（0600）。
func (s *Service) SaveAuthConfig(content string) error {
	if !json.Valid([]byte(content)) {
		return fmt.Errorf("invalid JSON: content is not valid JSON")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(content), &obj); err != nil || obj == nil {
		return errors.New("invalid config: root must be a JSON object")
	}
	formatted, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("format JSON: %w", err)
	}
	if string(formatted) == "{}" {
		formatted = []byte("{\n}")
	}
	formatted = append(formatted, '\n')

	path := authConfigPath()
	if err := ensureDir(path); err != nil {
		return err
	}
	return writePrivateFile(path, formatted)
}

// GetAuthConfigPath 返回 auth.json 的绝对路径，供前端展示。
func (s *Service) GetAuthConfigPath() (string, error) {
	return authConfigPath(), nil
}

// ModelCatalogEntry 是目录中的单个模型摘要（不含 cost/compat 等编辑无关字段）。
type ModelCatalogEntry struct {
	ID             string   `json:"id"`
	Name           string   `json:"name,omitempty"`
	Reasoning      bool     `json:"reasoning"`
	ThinkingLevels []string `json:"thinkingLevels,omitempty"`
	ContextWindow  int      `json:"contextWindow,omitempty"`
}

// ModelCatalogProvider 是单个 provider 及其模型列表。
type ModelCatalogProvider struct {
	Name   string              `json:"name"`
	API    string              `json:"api,omitempty"`
	Models []ModelCatalogEntry `json:"models"`
	// HasAuth 表示该 provider 已有可用凭据（auth.json 条目或 models.json 内联
	// apiKey），供前端下拉标注，避免选到无凭据模型。
	HasAuth bool `json:"hasAuth"`
}

// ModelCatalog 是下发到前端的 provider→model 目录。
// 只抽取下拉关联所需的字段，敏感字段（apiKey 等）一律不读取。
type ModelCatalog struct {
	Providers []ModelCatalogProvider `json:"providers"`
}

// GetPiModelCatalog 读取 models.json，抽取 provider→models 目录（只读，
// 不含 apiKey 等敏感信息），序列化为 JSON 返回。文件缺失时返回空目录。
func (s *Service) GetPiModelCatalog() (string, error) {
	catalog := ModelCatalog{Providers: []ModelCatalogProvider{}}

	data, err := os.ReadFile(modelsConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			out, _ := json.Marshal(catalog)
			return string(out), nil
		}
		return "", fmt.Errorf("read pi models config: %w", err)
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return "", fmt.Errorf("parse pi models config: %w", err)
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

	// auth.json 中的提供商名集合（不读取凭据内容，仅判断有无有效条目）
	authNames := map[string]bool{}
	if authData, err := os.ReadFile(authConfigPath()); err == nil {
		var authRoot map[string]any
		if json.Unmarshal(authData, &authRoot) == nil {
			for k, v := range authRoot {
				entry, ok := v.(map[string]any)
				if !ok {
					continue
				}
				switch t, _ := entry["type"].(string); t {
				case "api_key", "oauth":
					authNames[k] = true
				}
			}
		}
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
			entry.HasAuth = entry.HasAuth || authNames[name]
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
					if cw, ok := model["contextWindow"].(float64); ok {
						me.ContextWindow = int(cw)
					}
					me.ThinkingLevels = extractThinkingLevels(model["thinkingLevelMap"])
					if me.ThinkingLevels == nil {
						if thinking, ok := model["thinking"].(map[string]any); ok {
							me.ThinkingLevels = extractThinkingLevels(thinking["levels"])
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

// extractThinkingLevels 从 thinkingLevelMap（map[输入级别]输出级别）中
// 抽取输入级别键并排序；支持 omp 新版 thinking: {levels: []} 数组形态。
func extractThinkingLevels(v any) []string {
	switch t := v.(type) {
	case map[string]any:
		levels := make([]string, 0, len(t))
		for k := range t {
			levels = append(levels, k)
		}
		sort.Strings(levels)
		return levels
	case []any:
		levels := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				levels = append(levels, s)
			}
		}
		sort.Strings(levels)
		return levels
	default:
		return nil
	}
}
