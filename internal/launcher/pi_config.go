package launcher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"amagi-codebox/internal/config"
)

// PiProviderID 返回 amagi 托管的 pi provider 标识。
//
// pi 支持在 models.json 里注册任意命名的自定义 provider，并用 --provider 引用。
// 为避免与 pi 内置 provider（anthropic/openai/google/zai/kimi/...）冲突，
// 以及避免多个第三方 provider 互相覆盖，amagi 一律使用 "amagi-<providerName>" 作为
// pi 侧的 provider id。这样每个 amagi provider 在 pi 里都是独立条目，路由互不干扰。
func PiProviderID(providerName string) string {
	return "amagi-" + providerName
}

// piAPIType 把 amagi Provider 的 API 格式映射到 pi models.json 的 api 字段值。
//
// pi 支持四种 api：openai-completions / openai-responses / anthropic-messages /
// google-generative-ai。amagi 的双格式 Provider 映射：
//   - OpenAI 兼容   -> "openai-completions"（最通用）
//   - Anthropic 兼容 -> "anthropic-messages"
//
// 参照 opencode_config.go 的 isOpenAIType 判定逻辑。
func piAPIType(provider config.Provider) string {
	if provider.IsOpenAICompatible() {
		return "openai-completions"
	}
	return "anthropic-messages"
}

// BuildPiModelsConfig 依据 amagi Provider + Preset 参数生成 pi models.json 配置（map 形式）。
//
// 生成逻辑：
//  1. provider id = "amagi-<providerName>"（隔离命名，不碰 pi 内置 provider）
//  2. baseUrl/api/apiKey 从 Provider 双格式推导
//  3. models 注册当前选中的 model（来自 preset 或 provider.DefaultModel），
//     并透传 Parameters 中的 contextWindow/maxTokens/reasoning（pi Model Configuration 字段）
//
// 返回的 map 可直接 json.Marshal 后写入 PI_CODING_AGENT_DIR/models.json。
func BuildPiModelsConfig(
	providerName string,
	provider config.Provider,
	modelName string,
	apiKey string,
	params config.Parameters,
) (map[string]any, error) {
	piID := PiProviderID(providerName)
	format := "anthropic"
	if provider.IsOpenAICompatible() {
		format = "openai"
	}

	baseURL := strings.TrimSpace(provider.EffectiveBaseURL(format))
	if baseURL == "" {
		return nil, fmt.Errorf("provider %q has no baseURL, cannot build pi config", providerName)
	}

	entry := map[string]any{
		"baseUrl": baseURL,
		"api":     piAPIType(provider),
	}
	if apiKey != "" {
		entry["apiKey"] = apiKey
	}
	// 可选透传（仅配置存在时写入，不改变现有默认行为）：
	//   - headers     ：provider 级自定义请求头（pi provider.headers）
	//   - authHeader  ：provider 级是否强制携带 Authorization: Bearer（pi authHeader）
	//
	// 敏感值保护（P1-7）：header 值可写成 `$ENV:VAR_NAME` 或 `${ENV:VAR_NAME}`
	// 引用环境变量。amagi 的配置文件仅保存引用字面量（不含明文密钥，可安全导
	// 出/备份）；BuildPiModelsConfig 在启动时解析为 os.Getenv(VAR_NAME) 的实际值，
	// 只写入锁定的 ~/.pi/agent/models.json（0600，agent 目录 0700）。未设
	// 环境变量时解析为空字符串（该 header 被省略），避免落盘空占位。
	if headers := provider.EffectiveHeaders(format); len(headers) > 0 {
		resolved := resolveEnvHeaders(headers)
		if len(resolved) > 0 {
			entry["headers"] = resolved
		}
	}
	if authHeader := provider.EffectiveAuthHeader(format); authHeader != nil {
		entry["authHeader"] = *authHeader
	}

	// 注册 models 列表（v1.3.34 多模型修复）：托管条目注册该 provider 的
	// 全部预设模型——启动选中的模型排首位且参数以本次传入为准（权威），
	// 其余预设按各自 Parameters 注册（contextWindow/maxTokens/reasoning/
	// compat 独立生效），DefaultModel 未被覆盖时兑底裸注册。修复：用某一预设
	// 启动时，mergeProviderConfig 对同名托管条目 amagi-<name> 整体替换，
	// desired 只含单模型 → 同 provider 其他预设模型在 models.json 被覆盖丢失。
	if models := buildManagedModelEntries(provider, modelName, params); len(models) > 0 {
		entry["models"] = models
	}

	return map[string]any{
		"providers": map[string]map[string]any{
			piID: entry,
		},
	}, nil
}

// buildManagedModelEntry 构建单个托管模型条目（pi 与 omp 同构，v1.3.34 抽取共享）。
func buildManagedModelEntry(model string, params config.Parameters) map[string]any {
	m := map[string]any{
		"id":   model,
		"name": model,
	}
	// 最大上下文窗口：amagi ContextWindow.ModelContextWindow -> pi/omp contextWindow
	if params.ContextWindow != nil && params.ContextWindow.ModelContextWindow > 0 {
		m["contextWindow"] = params.ContextWindow.ModelContextWindow
	}
	// 最大输出 token
	if params.MaxTokens > 0 {
		m["maxTokens"] = params.MaxTokens
	}
	// 思考开关：amagi Thinking.Type=="enabled" 或 ReasoningEffort 非空 -> reasoning=true
	//（思考强度级别经 --thinking CLI flag 注入，见 app.go Launch*Session）。
	// v1.3.23 修复：reasoning_effort 单独出现（无 thinking.type）也必须开启 reasoning——
	// pi/omp 侧 clampThinkingLevel 对未声明 reasoning 的模型把任何 --thinking 值钳回
	// off，导致预设 reasoning_effort=max 静默失效（实战：glm/codecode 预设长期零推理）。
	hasReasoningEffort := strings.TrimSpace(params.ReasoningEffort) != ""
	if (params.Thinking != nil && params.Thinking.Type == "enabled") || hasReasoningEffort {
		m["reasoning"] = true
		// 开放扩展思考级别 xhigh/max：仅当 thinkingLevelMap 显式声明该级别时才被
		// 视为支持（getSupportedThinkingLevels：xhigh/max 要求 map 值非 undefined，
		// 否则 clampThinkingLevel 钳回 high）。amagi 的 ReasoningEffort 值域含
		// xhigh/max 且直接作为 --thinking 级别透传，故 identity 声明恒开启；标准
		// 级别（off..high）走默认映射，不声明。
		m["thinkingLevelMap"] = map[string]any{
			"xhigh": "xhigh",
			"max":   "max",
		}
	}
	// 可选透传 model 级 compat；supportsDeveloperRole 默认 false：reasoning=true 的
	// 模型默认以 developer 角色发 system prompt，内置探测仅覆盖 moonshot/zai 等
	// 非标服务商，amagi 托管的第三方 OpenAI 兼容服务商会被 400 "role 'developer'
	// is not allowed"；system 角色对所有服务商都安全，故默认关闭（可经 pi_compat
	// 显式覆写，显式值优先）。
	compat := make(map[string]any, len(params.PiCompat)+1)
	for k, v := range params.PiCompat {
		compat[k] = v
	}
	if _, overridden := compat["supportsDeveloperRole"]; !overridden {
		compat["supportsDeveloperRole"] = false
	}
	m["compat"] = compat
	return m
}

// appendManagedModelEntry 按模型 id 去重追加（空 id / 已注册跳过）。去重保证
// 同 provider 多预设引用同一模型 id 时只注册一次，且先注册者（启动选中模型/
// 排序靠前的预设）参数优先，输出确定。
func appendManagedModelEntry(models []map[string]any, seen map[string]bool, model string, params config.Parameters) []map[string]any {
	model = strings.TrimSpace(model)
	if model == "" || seen[model] {
		return models
	}
	seen[model] = true
	return append(models, buildManagedModelEntry(model, params))
}

// buildManagedModelEntries 生成托管 provider 的 models 注册列表（v1.3.34）。
// 顺序与权威性：启动选中的模型（缺省回落 DefaultModel）排首位且参数以本次传入
// 为准；其余预设按键序注册、各带自己的 Parameters；DefaultModel 未被前两者
// 覆盖时以零参数兑底注册。preset 键排序保证输出确定（models.json/yml 幂等可比）。
func buildManagedModelEntries(provider config.Provider, modelName string, params config.Parameters) []map[string]any {
	launched := strings.TrimSpace(modelName)
	if launched == "" {
		launched = strings.TrimSpace(provider.DefaultModel)
	}
	// 2026-08-19 修复：裸参数启动（default_model 直启 / 请求未带 parameters）时，
	// 启动模型以零参数优先注册（appendManagedModelEntry 同 id 去重先到先得），会把
	// 同 id 预设的 contextWindow/maxTokens/reasoning 全部剥掉——实战 glm-5.3 被裸
	// 注册后 reasoning 丢失、maxTokens 缺省回落服务端 16384，推理吃光输出预算导致
	// stopReason=length 零正文截断。零值参数时回退继承同 Model 预设的 Parameters
	// （preset 键序保证挑选确定）；显式传入的参数仍优先。
	if isZeroParameters(params) {
		for _, key := range sortedPresetKeys(provider.Presets) {
			if strings.TrimSpace(provider.Presets[key].Model) == launched {
				params = provider.Presets[key].Parameters
				break
			}
		}
	}
	models := make([]map[string]any, 0, len(provider.Presets)+1)
	seen := make(map[string]bool, len(provider.Presets)+1)
	models = appendManagedModelEntry(models, seen, launched, params)
	keys := sortedPresetKeys(provider.Presets)
	for _, key := range keys {
		models = appendManagedModelEntry(models, seen, provider.Presets[key].Model, provider.Presets[key].Parameters)
	}
	models = appendManagedModelEntry(models, seen, provider.DefaultModel, config.Parameters{})
	return models
}

// sortedPresetKeys 返回按键排序的 preset 键列表（nil 安全）。
func sortedPresetKeys(presets map[string]config.Preset) []string {
	keys := make([]string, 0, len(presets))
	for key := range presets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// isZeroParameters 判断 Parameters 是否为零值（调用方未显式传参）。
// 含指针与 map 字段，逐字段判零易漏，DeepEqual 与零值整体比较最稳。
func isZeroParameters(p config.Parameters) bool {
	return reflect.DeepEqual(p, config.Parameters{})
}

// WritePiAgentConfig 将 pi models.json 配置原子写入 agentDir/models.json。
//
// agentDir 由调用方提供；CodeBox 使用 Pi 的标准用户目录
// ~/.pi/agent，不再另建 <configDir>/pi-runtime 副本。
//
// 权限收紧（P1-7）：agentDir 以 0700 创建，models.json 以 0600 写入——该文件可能
// 携带解析后的敏感 header 值（见 BuildPiModelsConfig 的 `$ENV:` 约定），收紧权限
// 避免本机其他账号读取。
//
// 写入采用全代码库统一的原子范式：MkdirAll -> MarshalIndent -> tmp 文件 -> Rename。
// 每次启动覆盖写（幂等，无累积）。
func WritePiAgentConfig(agentDir string, cfg map[string]any) error {
	if agentDir == "" {
		return fmt.Errorf("agentDir is required")
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return fmt.Errorf("mkdir pi agent dir: %w", err)
	}
	// MkdirAll 不会收紧已存在目录的权限（如旧版本创建的 0755），显式 Chmod 覆盖升级场景。
	if err := os.Chmod(agentDir, 0o700); err != nil {
		return fmt.Errorf("chmod pi agent dir: %w", err)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pi models.json: %w", err)
	}
	b = append(b, '\n')

	modelsPath := filepath.Join(agentDir, "models.json")
	tmp := modelsPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write pi models.json tmp: %w", err)
	}
	// 覆盖残留的旧权限 tmp（WriteFile 不改变已存在文件的权限位，R3 复审 Minor-2）。
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod pi models.json tmp: %w", err)
	}
	if err := os.Rename(tmp, modelsPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace pi models.json: %w", err)
	}
	// Rename 后显式收紧（某些文件系统 Rename 不保留 tmp 权限位）。
	if err := os.Chmod(modelsPath, 0o600); err != nil {
		return fmt.Errorf("chmod pi models.json: %w", err)
	}
	return nil
}

// MergePiAgentConfig 把 agentDir/models.json 的现有内容并入待写入的 cfg。
// 它用于 CodeBox 直接共用 ~/.pi/agent 时保留用户已有的 providers
// 及其他顶层字段；cfg 中的当次 amagi 配置优先。
func MergePiAgentConfig(cfg map[string]any, agentDir string) map[string]any {
	if strings.TrimSpace(agentDir) == "" {
		return cfg
	}
	existing := readPiJSONObject(filepath.Join(agentDir, "models.json"))
	if len(existing) == 0 {
		return cfg
	}

	return mergeProviderConfig(existing, cfg, false)
}

// piProviderEntries 把 cfg["providers"] 归一化为 map[string]any。
// BuildPiModelsConfig 产出 map[string]map[string]any，JSON 反序列化则产出
// map[string]any，合并时两种形态都要保留。
func piProviderEntries(value any) map[string]any {
	out := make(map[string]any)
	switch providers := value.(type) {
	case map[string]any:
		for key, entry := range providers {
			out[key] = entry
		}
	case map[string]map[string]any:
		for key, entry := range providers {
			out[key] = entry
		}
	}
	return out
}

func readPiJSONObject(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return map[string]any{}
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return map[string]any{}
	}
	return obj
}

// envHeaderRefPattern 匹配 header 值中对环境变量的引用：`$ENV:VAR_NAME` 或
// `${ENV:VAR_NAME}`。VAR_NAME 允许字母/数字/下划线。整个值必须就是该引用（不支持
// 部分插值），以避免把明文密钥与引用混在同一值里。
var envHeaderRefPattern = regexp.MustCompile(`^\$\{ENV:([A-Za-z_][A-Za-z0-9_]*)\}$|^\$ENV:([A-Za-z_][A-Za-z0-9_]*)$`)

// resolveEnvHeaderValue 解析单个 header 值中的 `$ENV:VAR` / `${ENV:VAR}` 引用。
// 返回 (解析值, 是否为引用)。非引用值原样返回；引用但环境变量未设时返回
// ("", true)（调用方据此省略该 header，避免落盘空占位）。
func resolveEnvHeaderValue(value string) (string, bool) {
	m := envHeaderRefPattern.FindStringSubmatch(value)
	if m == nil {
		return value, false
	}
	name := m[1]
	if name == "" {
		name = m[2]
	}
	return os.Getenv(name), true
}

// resolveEnvHeaders 对一组 header 值逐项解析 `$ENV:VAR` 引用；解析后为空的项（含
// 未设环境变量的引用）被省略，避免在运行时配置里落盘空占位。
func resolveEnvHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		resolved, _ := resolveEnvHeaderValue(v)
		if strings.TrimSpace(resolved) == "" {
			continue
		}
		out[k] = resolved
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
