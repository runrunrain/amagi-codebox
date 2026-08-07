package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"amagi-codebox/internal/config"
	"gopkg.in/yaml.v3"
)

// OmpProviderID 返回 amagi 托管的 omp 自定义 provider 标识。
//
// omp 与 pi 同构：支持在 models.yml 里注册任意命名的自定义 provider，并用
// --model provider/model 或 --provider 引用。为避免与 omp 内置 provider 冲突
// 以及多个第三方 provider 互相覆盖，amagi 一律使用 "amagi-<providerName>" 作为
// omp 侧的 provider id（与 PiProviderID 同一隔离命名约定）。
func OmpProviderID(providerName string) string {
	return "amagi-" + providerName
}

// BuildOmpModelsConfig 依据 amagi Provider + Preset 参数生成 omp models.yml 配置（map 形式）。
//
// omp 的 models.yml 与 pi 的 models.json 同构（providers → baseUrl/api/apiKey/
// headers/authHeader/models[]），差异仅在于：
//   - omp 的 api 枚举更宽（openai-completions/openai-responses/anthropic-messages/
//     google-generative-ai/google-vertex/...），amagi 双格式映射与 pi 一致：
//     OpenAI 兼容 -> "openai-completions"（最通用）；否则 "anthropic-messages"。
//   - omp 的 models 为 YAML 数组（pi 为 JSON 数组），序列化由 WriteOmpAgentConfig 完成。
//
// 生成逻辑与 BuildPiModelsConfig 完全同构：
//  1. provider id = "amagi-<providerName>"（隔离命名，不碰 omp 内置 provider）
//  2. baseUrl/api/apiKey 从 Provider 双格式推导
//  3. models 注册当前选中的 model（来自 preset 或 provider.DefaultModel），
//     并透传 Parameters 中的 contextWindow/maxTokens/reasoning/thinkingLevelMap/compat
//
// 返回的 map 可直接 yaml.Marshal 后写入 ~/.omp/agent/models.yml。
func BuildOmpModelsConfig(
	providerName string,
	provider config.Provider,
	modelName string,
	apiKey string,
	params config.Parameters,
) (map[string]any, error) {
	ompID := OmpProviderID(providerName)
	format := "anthropic"
	if provider.IsOpenAICompatible() {
		format = "openai"
	}

	baseURL := strings.TrimSpace(provider.EffectiveBaseURL(format))
	if baseURL == "" {
		return nil, fmt.Errorf("provider %q has no baseURL, cannot build omp config", providerName)
	}

	entry := map[string]any{
		"baseUrl": baseURL,
		"api":     piAPIType(provider), // omp api 判定与 pi 同构，复用同一函数
	}
	if apiKey != "" {
		entry["apiKey"] = apiKey
	}
	// 可选透传（与 pi 一致）：
	//   - headers     ：provider 级自定义请求头（omp provider.headers），
	//     敏感值支持 `$ENV:VAR_NAME` / `${ENV:VAR_NAME}` 环境变量引用，启动时解析
	//     为实际值（resolveEnvHeaders 复用，未设环境变量时省略该项）。
	//   - authHeader  ：provider 级是否强制携带 Authorization: Bearer（omp authHeader）。
	if headers := provider.EffectiveHeaders(format); len(headers) > 0 {
		resolved := resolveEnvHeaders(headers)
		if len(resolved) > 0 {
			entry["headers"] = resolved
		}
	}
	if authHeader := provider.EffectiveAuthHeader(format); authHeader != nil {
		entry["authHeader"] = *authHeader
	}

	// 注册 model，使 omp 识别该模型并允许 --model 引用。
	model := strings.TrimSpace(modelName)
	if model == "" {
		model = strings.TrimSpace(provider.DefaultModel)
	}
	if model != "" {
		m := map[string]any{
			"id":   model,
			"name": model,
		}
		// 最大上下文窗口：amagi ContextWindow.ModelContextWindow -> omp contextWindow
		if params.ContextWindow != nil && params.ContextWindow.ModelContextWindow > 0 {
			m["contextWindow"] = params.ContextWindow.ModelContextWindow
		}
		// 最大输出 token
		if params.MaxTokens > 0 {
			m["maxTokens"] = params.MaxTokens
		}
		// 思考开关：amagi Thinking.Type=="enabled" -> omp reasoning=true
		// （思考强度级别通过 --thinking CLI flag 注入，见 app.go LaunchOmpSession）。
		// thinkingLevelMap.xhigh/max 恒开启，与 BuildPiModelsConfig 同一语义
		//（omp 与 pi 同源，clampThinkingLevel 仅在 map 显式声明时开放扩展级别）。
		if params.Thinking != nil && params.Thinking.Type == "enabled" {
			m["reasoning"] = true
			m["thinkingLevelMap"] = map[string]any{
				"xhigh": "xhigh",
				"max":   "max",
			}
		}
		// 可选透传 model 级 compat（supportsDeveloperRole/supportsReasoningEffort 等）。
		// supportsDeveloperRole 默认 false（与 pi 同因：amagi 托管的多为第三方
		// OpenAI 兼容服务商，内置探测无法覆盖，developer 角色会报 400）。
		compat := make(map[string]any, len(params.PiCompat)+1)
		for k, v := range params.PiCompat {
			compat[k] = v
		}
		if _, overridden := compat["supportsDeveloperRole"]; !overridden {
			compat["supportsDeveloperRole"] = false
		}
		m["compat"] = compat
		entry["models"] = []map[string]any{m}
	}

	return map[string]any{
		"providers": map[string]map[string]any{
			ompID: entry,
		},
	}, nil
}

// WriteOmpAgentConfig 将 omp models.yml 配置原子写入 agentDir/models.yml。
//
// agentDir 由调用方提供；CodeBox 使用 omp 的标准用户目录 ~/.omp/agent。
//
// 权限收紧（P1-7 同源策略）：agentDir 以 0700 创建，models.yml 以 0600 写入——
// 该文件可能携带解析后的敏感 header 值（见 BuildOmpModelsConfig 的 `$ENV:` 约定），
// 收紧权限避免本机其他账号读取。
//
// 写入采用全代码库统一的原子范式：MkdirAll -> yaml.Marshal -> tmp 文件 -> Rename。
// 每次启动覆盖写（幂等，无累积）。输出格式为 YAML：providers 是 map（yaml.v3
// 序列化，键自动排序），models 是数组——与 omp 官方 models.yml 结构对齐。
func WriteOmpAgentConfig(agentDir string, cfg map[string]any) error {
	if agentDir == "" {
		return fmt.Errorf("agentDir is required")
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return fmt.Errorf("mkdir omp agent dir: %w", err)
	}
	// MkdirAll 不会收紧已存在目录的权限（如旧版本创建的 0755），显式 Chmod 覆盖升级场景。
	if err := os.Chmod(agentDir, 0o700); err != nil {
		return fmt.Errorf("chmod omp agent dir: %w", err)
	}

	b, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal omp models.yml: %w", err)
	}

	modelsPath := filepath.Join(agentDir, "models.yml")
	tmp := modelsPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write omp models.yml tmp: %w", err)
	}
	// 覆盖残留的旧权限 tmp（WriteFile 不改变已存在文件的权限位）。
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod omp models.yml tmp: %w", err)
	}
	if err := os.Rename(tmp, modelsPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace omp models.yml: %w", err)
	}
	// Rename 后显式收紧（某些文件系统 Rename 不保留 tmp 权限位）。
	if err := os.Chmod(modelsPath, 0o600); err != nil {
		return fmt.Errorf("chmod omp models.yml: %w", err)
	}
	return nil
}

// MergeOmpModelsConfig 把 agentDir/models.yml 的现有内容并入待写入的 cfg。
// 它用于 CodeBox 直接共用 ~/.omp/agent 时保留用户已有的 providers 及其他顶层
// 字段（如 equivalence 等）；cfg 中的当次 amagi 配置优先（同名 provider 覆盖）。
func MergeOmpModelsConfig(cfg map[string]any, agentDir string) map[string]any {
	if strings.TrimSpace(agentDir) == "" {
		return cfg
	}
	existing := readOmpYAMLObject(filepath.Join(agentDir, "models.yml"))
	if len(existing) == 0 {
		return cfg
	}

	existingProviders, _ := existing["providers"].(map[string]any)
	managedProviders := piProviderEntries(cfg["providers"])
	providers := make(map[string]any, len(existingProviders)+len(managedProviders))
	for key, value := range existingProviders {
		providers[key] = value
	}
	for key, value := range managedProviders {
		providers[key] = value
	}

	merged := make(map[string]any, len(existing)+len(cfg))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range cfg {
		merged[key] = value
	}
	if len(providers) > 0 {
		merged["providers"] = providers
	}
	return merged
}

// readOmpYAMLObject 读取 YAML 配置为 map[string]any；文件缺失或解析失败按空
// 处理（与 readPiJSONObject 同一容错语义）。yaml.v3 解析嵌套 map 统一为
// map[string]any，与 JSON 路径的 provider 合并逻辑天然兼容（piProviderEntries 复用）。
//
// 边界：若 models.yml 含非字符串键（数字/布尔等），yaml.v3 unmarshal 到
// map[string]any 会失败，按空处理后在下次启动 Merge 时覆盖写入——但 omp 的
// provider 键恒为字符串（非字符串键对 omp 自身即非法配置），实际不可达，
// 与 readPiJSONObject 的 JSON 同语义容错一致。
func readOmpYAMLObject(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return map[string]any{}
	}
	var obj map[string]any
	if err := yaml.Unmarshal(data, &obj); err != nil {
		return map[string]any{}
	}
	return obj
}
