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
	// Source 区分来源：custom = models.json 注册表（可编辑），builtin =
	// 内置模型目录（models-store.json 缓存，如 openai-codex 等 OAuth 登录
	// 提供商）。custom 省略该字段。
	Source string `json:"source,omitempty"`
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

	var root map[string]any
	data, err := os.ReadFile(modelsConfigPath())
	if err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return "", fmt.Errorf("parse pi models config: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read pi models config: %w", err)
	}
	// models.json 缺失不提前返回（实战修复 2026-09-05）：否则 builtin 目录
	// （models-store.json，OAuth provider 如 openai-codex）与前线契约合并全部被
	// 跳过——纯 OAuth 用户（无 models.json）在下拉里看不到任何内置 provider。
	if root == nil {
		root = map[string]any{}
	}
	providersRaw, _ := root["providers"].(map[string]any)
	// 无 providers 键（含 models.json 缺失）不提前返回：builtin 目录与前线契约仍需合并。

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
					if me, ok := extractModelEntry(model); ok {
						entry.Models = append(entry.Models, me)
					}
				}
			}
		}
		catalog.Providers = append(catalog.Providers, entry)
	}

	// 追加内置目录（models-store.json，根层为 providers 映射）：补齐仅存在于
	// 内置目录/仅通过 OAuth 登录的提供商（如 openai-codex）。models.json 中
	// 已有的自定义条目优先，不被内置版本覆盖。
	if builtin, err := loadBuiltinCatalog(names, authNames); err == nil {
		catalog.Providers = append(catalog.Providers, builtin...)
	}

	// 合并 amagi-pi 前线契约文件（~/.pi/agent/amagi/data/codex-frontline.json）：
	// amagi-pi 在 pi.dev 官方目录未收录时运行时注入的新模型（如 gpt-6-astra）不落盘
	// models-store.json，静态目录看不到；契约文件由 amagi-pi refreshModels 生效时落盘、
	// 官方收录后退位删除——展示侧只需宽容合并（同 id 已存在则跳过，官方优先）。
	mergeFrontlineContract(&catalog)

	out, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode model catalog: %w", err)
	}
	return string(out), nil
}

// extractModelEntry 从单个 model 映射抽取目录条目（id 为空时 ok=false）。
func extractModelEntry(model map[string]any) (ModelCatalogEntry, bool) {
	me := ModelCatalogEntry{}
	if id, ok := model["id"].(string); ok {
		me.ID = id
	}
	if me.ID == "" {
		return me, false
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
	me.ThinkingLevels = extractThinkingLevels(me.Reasoning, model["thinkingLevelMap"])
	if me.ThinkingLevels == nil {
		if thinking, ok := model["thinking"].(map[string]any); ok {
			me.ThinkingLevels = extractThinkingLevels(me.Reasoning, thinking["levels"])
		}
	}
	return me, true
}

// loadBuiltinCatalog 读取 models-store.json（pi 内置模型目录缓存）并返回
// 不在自定义注册表中的 providers。authNames 用于标注 OAuth/api_key 登录状态。
func loadBuiltinCatalog(customNames []string, authNames map[string]bool) ([]ModelCatalogProvider, error) {
	storePath := filepath.Join(agentDir(), "models-store.json")
	data, err := os.ReadFile(storePath)
	if err != nil {
		return nil, err
	}
	var store map[string]any
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}

	custom := map[string]bool{}
	for _, n := range customNames {
		custom[n] = true
	}

	names := make([]string, 0, len(store))
	for name := range store {
		if custom[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	var out []ModelCatalogProvider
	for _, name := range names {
		entry := ModelCatalogProvider{
			Name:    name,
			Models:  []ModelCatalogEntry{},
			Source:  "builtin",
			HasAuth: authNames[name],
		}
		provider, _ := store[name].(map[string]any)
		if provider != nil {
			if models, ok := provider["models"].([]any); ok {
				for _, m := range models {
					model, ok := m.(map[string]any)
					if !ok {
						continue
					}
					if me, ok := extractModelEntry(model); ok {
						entry.Models = append(entry.Models, me)
					}
				}
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// mergeFrontlineContract 把 amagi-pi 前线契约文件的追加模型与档位补丁合并进目录对应 provider。
// 宽容语义：文件缺失/坏 JSON/字段形态意外 → 静默跳过（展示辅助，非正确性依赖）。
func mergeFrontlineContract(catalog *ModelCatalog) {
	contractPath := filepath.Join(agentDir(), "amagi", "data", "codex-frontline.json")
	data, err := os.ReadFile(contractPath)
	if err != nil {
		return
	}
	var contract struct {
		Provider     string                `json:"provider"`
		Models       []map[string]any      `json:"models"`
		ModelPatches map[string]map[string]any `json:"modelPatches"`
	}
	if json.Unmarshal(data, &contract) != nil || contract.Provider == "" ||
		(len(contract.Models) == 0 && len(contract.ModelPatches) == 0) {
		return
	}
	// 目录中已有同 id 模型（官方已收录）则跳过——与 amagi-pi 退位语义一致，官方优先。
	// modelPatches 是 amagi-pi 对基础模型的档位补全（如 gpt-5.6 补 max 档）：整体替换
	// thinkingLevelMap 后重算档位集（展示与 pi 运行时一致）。
	existing := map[string]bool{}
	for i := range catalog.Providers {
		p := &catalog.Providers[i] // 索引取址：range 值副本修改不回写（实战踩坑）
		if p.Name != contract.Provider {
			continue
		}
		for j := range p.Models {
			m := &p.Models[j]
			existing[m.ID] = true
			if patch, ok := contract.ModelPatches[m.ID]; ok {
				if raw, ok := patch["thinkingLevelMap"]; ok {
					m.ThinkingLevels = extractThinkingLevels(m.Reasoning, raw)
				}
			}
		}
		for _, raw := range contract.Models {
			if me, ok := extractModelEntry(raw); ok && !existing[me.ID] {
				p.Models = append(p.Models, me)
				existing[me.ID] = true
			}
		}
		return // 只合并首个同名 provider（目录内 provider 名唯一）
	}
	// provider 不在目录（models-store 尚无该 OAuth provider）：作为 builtin 条目补入
	entry := ModelCatalogProvider{
		Name:   contract.Provider,
		Models: []ModelCatalogEntry{},
		Source: "builtin",
	}
	for _, raw := range contract.Models {
		if me, ok := extractModelEntry(raw); ok {
			entry.Models = append(entry.Models, me)
		}
	}
	if len(entry.Models) > 0 {
		catalog.Providers = append(catalog.Providers, entry)
	}
}

// EXTENDED_THINKING_LEVELS 与 pi pi-ai EXTENDED_THINKING_LEVELS 间序一致。
var extendedThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// standardThinkingLevels 为 pi 语义下的「标准档」：map 缺键时默认支持
//（provider 默认映射，透传档名）；xhigh/max 是扩展档，必须 map 显式映射才支持。
var standardThinkingLevels = map[string]bool{
	"off": true, "minimal": true, "low": true, "medium": true, "high": true,
}

// extractThinkingLevels 复刻 pi getSupportedThinkingLevels 语义（实战修复 2026-09-05）：
//   - reasoning=false → 仅 off；
//   - reasoning=true + thinkingLevelMap：map[k]===null → 不支持；标准档(≤high)缺键 → 默认支持；
//     xhigh/max 缺键 → 不支持（需显式声明）；
//   - reasoning=true + 无 map：标准档全支持，xhigh/max 不支持；
//   - omp thinking: {levels: []} 数组形态：原样抽取。
// 原实现只取 map 键：gpt-5.6 系列 map 只有 xhigh/minimal 两键 → 下拉只显示两档，
// 与 pi 运行时真实支持集（六档，标准档默认支持）脱节。
func extractThinkingLevels(reasoning bool, v any) []string {
	switch t := v.(type) {
	case []any:
		levels := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				levels = append(levels, s)
			}
		}
		sort.Strings(levels)
		return levels
	case map[string]any:
		if !reasoning {
			return []string{"off"}
		}
		levels := make([]string, 0, len(extendedThinkingLevels))
		for _, lvl := range extendedThinkingLevels {
			mapped, exists := t[lvl]
			if exists && mapped == nil {
				continue // 显式 null：不支持
			}
			if !exists && !standardThinkingLevels[lvl] {
				continue // xhigh/max 缺键：不支持
			}
			levels = append(levels, lvl)
		}
		return levels
	default:
		if !reasoning {
			return []string{"off"}
		}
		levels := make([]string, 0)
		for _, lvl := range extendedThinkingLevels {
			if standardThinkingLevels[lvl] {
				levels = append(levels, lvl)
			}
		}
		return levels
	}
}
