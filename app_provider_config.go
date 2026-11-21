package main

// app_provider_config.go — Provider CRUD/export/import, API key & Pi/OMP launch model
// config helpers, plugin sub-item toggling, URL/envvar/harness config, presets, work dirs,
// updater, and logging facades.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/updater"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// isOpenAIProvider reports whether the provider uses the OpenAI-compatible API.
// Delegates to the Provider's dual-format compatibility method.
func isOpenAIProvider(p config.Provider) bool {
	return p.IsOpenAICompatible()
}

func appendUniqueNonEmpty(values []string, candidates ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(candidates))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		seen[trimmed] = struct{}{}
	}
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(strings.ToLower(candidate))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		values = append(values, trimmed)
		seen[trimmed] = struct{}{}
	}
	return values
}

func legacyProviderAPIKeyCandidates(provider config.Provider) []string {
	candidates := []string{}
	preferred := provider.PreferredFormat()
	if preferred != "" {
		candidates = appendUniqueNonEmpty(candidates, preferred)
	}
	if provider.IsAnthropicCompatible() {
		candidates = appendUniqueNonEmpty(candidates, "anthropic")
	}
	if provider.IsOpenAICompatible() {
		candidates = appendUniqueNonEmpty(candidates, "openai")
	}
	return candidates
}

func legacyProviderAPIKeyCandidatesForFormat(format string) []string {
	candidates := []string{}
	switch strings.TrimSpace(strings.ToLower(format)) {
	case "anthropic":
		candidates = appendUniqueNonEmpty(candidates, "anthropic", "openai")
	case "openai":
		candidates = appendUniqueNonEmpty(candidates, "openai", "anthropic")
	default:
		candidates = appendUniqueNonEmpty(candidates, "anthropic", "openai")
	}
	return candidates
}

func (a *App) getProviderAPIKeyWithLegacyCandidates(providerName string, legacyCandidates []string) (string, string) {
	if key, source := a.Secrets.GetAPIKeyWithFallback(providerName); key != "" {
		return key, source
	}
	for _, format := range legacyCandidates {
		if key, err := a.Secrets.GetAPIKey(providerName + ":" + format); err == nil {
			trimmed := strings.TrimSpace(key)
			if trimmed != "" {
				return trimmed, "legacy:" + format
			}
		}
	}
	return "", ""
}

// getProviderAPIKey 读取指定 provider 的统一 API key。
// 新模型优先读取 providerName；若缺失，再兼容旧命名 providerName:anthropic / providerName:openai。
// 返回 (apiKey, source)。
func (a *App) getProviderAPIKey(providerName string, provider config.Provider) (string, string) {
	return a.getProviderAPIKeyWithLegacyCandidates(providerName, legacyProviderAPIKeyCandidates(provider))
}

func buildProviderFromExportProvider(ep config.ExportProvider) config.Provider {
	return ep.ToProvider()
}

func selectImportedProviderAPIKey(ep config.ExportProvider) string {
	return ep.UnifiedAPIKey()
}

func buildExportProvider(provider config.Provider, apiKey string) config.ExportProvider {
	return config.BuildExportProvider(provider, apiKey)
}

func (a *App) saveImportedProviderAPIKey(providerName, apiKey string) error {
	trimmed := strings.TrimSpace(apiKey)
	if trimmed == "" {
		return nil
	}
	if err := a.Secrets.SetAPIKey(providerName, trimmed); err != nil {
		return err
	}
	_ = a.Secrets.DeleteAPIKey(providerName + ":anthropic")
	_ = a.Secrets.DeleteAPIKey(providerName + ":openai")
	return nil
}

func normalizeCodexModelName(modelName string) string {
	trimmed := strings.TrimSpace(modelName)
	lower := strings.ToLower(trimmed)
	if strings.HasSuffix(lower, "[1m]") {
		trimmed = strings.TrimSpace(trimmed[:len(trimmed)-len("[1m]")])
	}
	return trimmed
}

func resolveCodexLaunchSettings(provider config.Provider, requestedModel string) codexLaunchSettings {
	normalizedModel := normalizeCodexModelName(requestedModel)
	settings := codexLaunchSettings{
		Model: normalizedModel,
	}
	// 接口协议选择透传：OpenAI 子块缺失或未设置时 EffectiveWireAPI 返回 ""，
	// 下游 codexWireAPI 兑底为 responses，维持 legacy 行为零变化。
	settings.WireAPI = provider.OpenAI.EffectiveWireAPI()

	if normalizedModel == "" {
		normalizedModel = normalizeCodexModelName(provider.DefaultModel)
		settings.Model = normalizedModel
	}

	var matchedPreset *config.Preset
	requestedRaw := strings.TrimSpace(requestedModel)
	for _, preset := range provider.Presets {
		presetModel := strings.TrimSpace(preset.Model)
		if requestedRaw != "" && presetModel == requestedRaw {
			presetCopy := preset
			matchedPreset = &presetCopy
			break
		}
	}
	if matchedPreset == nil && normalizedModel != "" {
		for _, preset := range provider.Presets {
			if normalizeCodexModelName(preset.Model) == normalizedModel {
				presetCopy := preset
				matchedPreset = &presetCopy
				break
			}
		}
	}

	if matchedPreset != nil {
		if normalizedPresetModel := normalizeCodexModelName(matchedPreset.Model); normalizedPresetModel != "" {
			settings.Model = normalizedPresetModel
		}
		applyCodexPresetParameters(&settings, matchedPreset.Parameters)
	}

	return settings
}

func applyCodexPresetParameters(settings *codexLaunchSettings, params config.Parameters) {
	if settings == nil {
		return
	}
	settings.ReasoningEffort = strings.TrimSpace(params.ReasoningEffort)
	if params.ContextWindow != nil {
		settings.ModelContextWindow = params.ContextWindow.ModelContextWindow
		settings.AutoCompactLimit = params.ContextWindow.AutoCompactTokenLimit
	}
}

// buildCodexCLIArgs maps CodeBox presets onto documented Codex per-process
// config overrides. Keeping these settings on the command line ensures one
// session's preset does not rewrite the user's persistent defaults.
func buildCodexCLIArgs(settings codexLaunchSettings) []string {
	args := make([]string, 0, 8)
	if settings.Model != "" {
		args = append(args, "-m", settings.Model)
	}
	if settings.ReasoningEffort != "" {
		args = append(args, "-c", "model_reasoning_effort="+strconv.Quote(settings.ReasoningEffort))
	}
	if settings.ModelContextWindow > 0 {
		args = append(args, "-c", "model_context_window="+strconv.Itoa(settings.ModelContextWindow))
	}
	if settings.AutoCompactLimit > 0 {
		args = append(args, "-c", "model_auto_compact_token_limit="+strconv.Itoa(settings.AutoCompactLimit))
	}
	if settings.ProviderBaseURL != "" {
		providerConfig := "{name=" + strconv.Quote(codexModelProviderName) +
			",base_url=" + strconv.Quote(settings.ProviderBaseURL) +
			",env_key=" + strconv.Quote("OPENAI_API_KEY") +
			",requires_openai_auth=false,wire_api=" + strconv.Quote(codexWireAPI(settings.WireAPI)) + "}"
		args = append(args,
			"-c", "model_provider="+strconv.Quote(codexModelProviderName),
			"-c", "forced_login_method="+strconv.Quote("api"),
			"-c", "model_providers."+codexModelProviderName+"="+providerConfig,
		)
	}
	return args
}

// writePiProviderModelsAtLaunch 承担 LaunchPiSession 的 providerSyncMu 临界区：
// 构建 + 合并写入 Windows 侧 models.json + API key env 兜底注入，返回可能更新的
// launchSettings。provider 不可读时原样返回（既有行为）；构建/写入失败回退
// piProviderMapping 兜底。
//
// 锁纪律（S1 diting F1）：本函数是临界区的唯一形态，defer Unlock 保证全部
// return 路径释放锁——修复前 WSL 构建失败分支在 Lock/Unlock 区间内直接
// return，providerSyncMu 永久持有，此后 provider 保存/Pi 启动/SaveOpenCodeConfig
// 全部挂死。
func (a *App) writePiProviderModelsAtLaunch(providerID string, settings piLaunchSettings, presetParams config.Parameters, envOverrides map[string]string) (piLaunchSettings, error) {
	a.providerSyncMu.Lock()
	defer a.providerSyncMu.Unlock()

	provider, err := a.Config.GetProvider(providerID)
	if err != nil {
		return settings, nil // provider 不可读：不改 settings、不注入 env（既有行为）
	}
	settings = resolvePiLaunchSettings(*provider, settings.Model, presetParams)
	apiKey, _ := a.getProviderAPIKey(providerID, *provider)

	// 合并写入 pi 标准 agent 目录的 models.json。
	// 成功写入时改写 settings.Provider 为 amagi-<name>；失败则回退
	// piProviderMapping 旧兜底（保持向后兼容，不阻断启动）。presetParams 透传
	// contextWindow/maxTokens/thinking 到 pi model 配置。presetModels：该 provider
	// 在 openai 公共预设桶（pi/omp 消费的桶）下的全部预设模型。启动写入是托管
	// 条目的整体替换语义，漏注会把统一同步写入的同 provider 其他预设模型挤掉。
	presetModels := launcher.ManagedPresetModels(providerID, *provider, a.Config.GetAllTerminalPresets(), a.Config.GetModalityProbeCache(), config.TerminalPresetOpenAI)
	if piCfg, cfgErr := launcher.BuildPiModelsConfig(providerID, *provider, settings.Model, apiKey, presetParams, presetModels); cfgErr == nil {
		// 启动防呆：官方 OpenAI + 显式 chat + 推理预设的组合会在首个请求 400
		//（gpt-6.x 起函数工具与 reasoning_effort 在 Chat Completions 互斥），
		// 仅告警不阻断（用户显式选择 chat，保持尊重）。
		if warn := launcher.OfficialOpenAIChatReasoningWarning(*provider, presetParams); warn != "" {
			a.Log.Warn("pi", "接口协议与推理预设冲突", warn)
		}
		// WSL 内 pi 与 Windows 共享 agent 根（PI_CODING_AGENT_DIR 经 /p 翻译），
		// 故 WSL/原生统一写 Windows 侧 models.json。
		wroteProvider := false
		agentDir := defaultPiAgentDir()
		// 保留用户 models.json 中已有的 provider 和其他顶层配置，
		// 当次 amagi 生成的同名 provider 优先。
		piCfg = launcher.MergePiAgentConfig(piCfg, agentDir)
		if writeErr := launcher.WritePiAgentConfig(agentDir, piCfg); writeErr == nil {
			wroteProvider = true
			a.Log.Info("pi", "已写入 pi models.json", fmt.Sprintf("provider=%s baseURL=%s -> %s/models.json",
				launcher.PiProviderID(providerID), provider.EffectiveBaseURL(""), agentDir))
		} else {
			a.Log.Warn("pi", "写入 pi models.json 失败，回退内置 provider", writeErr.Error())
		}
		if wroteProvider {
			settings.Provider = launcher.PiProviderID(providerID)
		} else {
			settings.Provider, _ = piProviderMapping(*provider)
		}
	} else {
		a.Log.Warn("pi", "生成 pi models.json 失败，回退内置 provider", cfgErr.Error())
		settings.Provider, _ = piProviderMapping(*provider)
	}

	// 冗余兜底：注入对应 API Key env var（与 OpenCode 的双路注入一致）。
	// pi 自定义 provider 已内嵌 apiKey，这里仅作为额外保险。
	if apiKey != "" {
		if _, apiKeyEnv := piProviderMapping(*provider); apiKeyEnv != "" {
			envOverrides[apiKeyEnv] = apiKey
		}
	}
	return settings, nil
}

// writeOmpProviderModelsAtLaunch 承担 LaunchOmpSession 的 providerSyncMu 临界区
// （与 writePiProviderModelsAtLaunch 对称，D5）：构建 + 写入 models.yml
// （WSL/Windows 两侧）+ API key env 兜底注入，返回可能更新的 launchSettings。
// provider 不可读时原样返回（既有行为）。WSL 路径构建/写入失败均阻断（D5：
// omp 与 pi 同判——ompProviderMapping 回退会丢自定义 baseURL，distro 内 omp
// 将以第三方 key 误打官方 endpoint）；Windows 路径维持既有回退语义。
// 锁纪律同 Pi：defer Unlock 保证全部 return 路径释放锁。
func (a *App) writeOmpProviderModelsAtLaunch(providerID string, launchSettings ompLaunchSettings, presetParams config.Parameters, wslDistro string, envOverrides map[string]string) (ompLaunchSettings, error) {
	a.providerSyncMu.Lock()
	defer a.providerSyncMu.Unlock()

	provider, err := a.Config.GetProvider(providerID)
	if err != nil {
		return launchSettings, nil // provider 不可读：不改 settings、不注入 env（既有行为）
	}
	launchSettings = resolveOmpLaunchSettings(*provider, launchSettings.Model, presetParams)
	apiKey, _ := a.getProviderAPIKey(providerID, *provider)

	// 合并写入 omp 标准 agent 目录的 models.yml。成功写入时改写
	// launchSettings.Provider 为 amagi-<name>；Windows 路径失败则回退
	// ompProviderMapping 旧兜底（保持向后兼容，不阻断启动）；WSL 路径构建/
	// 写入失败均阻断（不静默换非等价身份）。presetModels 同 Pi：取该 provider
	// 在 openai 公共预设桶下的全部预设模型（启动写入是托管条目整体替换语义，
	// 漏注会把统一同步写入的同 provider 其他预设模型挤掉）。
	presetModels := launcher.ManagedPresetModels(providerID, *provider, a.Config.GetAllTerminalPresets(), a.Config.GetModalityProbeCache(), config.TerminalPresetOpenAI)
	if ompCfg, cfgErr := launcher.BuildOmpModelsConfig(providerID, *provider, launchSettings.Model, apiKey, presetParams, presetModels); cfgErr == nil {
		// 启动防呆：与 writePiProviderModelsAtLaunch 同构（官方 OpenAI + 显式
		// chat + 推理预设 → 首个请求 400），仅告警不阻断。
		if warn := launcher.OfficialOpenAIChatReasoningWarning(*provider, presetParams); warn != "" {
			a.Log.Warn("omp", "接口协议与推理预设冲突", warn)
		}
		// WSL 模式：omp 在 distro 内运行，配置必须落到 WSL 侧 agent root
		//（提交走与 WriteWSLOmpAgentConfig 同族的 SA-06 staging 原语）；非 WSL 路径不变。
		wroteProvider := false
		if wslDistro != "" {
			wslAgentDir, wErr := launcher.WriteWSLOmpAgentConfig(wslDistro, ompCfg)
			if wErr == nil {
				wroteProvider = true
				a.Log.Info("omp", "已写入 omp models.yml (WSL)", fmt.Sprintf("provider=%s baseURL=%s distro=%s -> %s/models.yml",
					launcher.OmpProviderID(providerID), provider.EffectiveBaseURL(""), wslDistro, wslAgentDir))
			} else {
				// D5：WSL 路径写入失败阻断（与 Pi 同判），不回退丢 baseURL 的兜底。
				return launchSettings, wslOmpProviderWriteError(providerID, wErr)
			}
		} else {
			agentDir := defaultOmpAgentDir()
			// 保留用户 models.yml 中已有的 provider 和其他顶层配置，
			// 当次 amagi 生成的同名 provider 优先。
			pompCfg := launcher.MergeOmpModelsConfig(ompCfg, agentDir)
			writeErr := launcher.WriteOmpAgentConfig(agentDir, pompCfg)
			if writeErr == nil {
				wroteProvider = true
				a.Log.Info("omp", "已写入 omp models.yml", fmt.Sprintf("provider=%s baseURL=%s -> %s/models.yml",
					launcher.OmpProviderID(providerID), provider.EffectiveBaseURL(""), agentDir))
			} else {
				a.Log.Warn("omp", "写入 omp models.yml 失败，回退内置 provider", writeErr.Error())
			}
		}
		if wroteProvider {
			launchSettings.Provider = launcher.OmpProviderID(providerID)
		} else {
			launchSettings.Provider, _ = ompProviderMapping(*provider)
		}
	} else {
		// D5：WSL 路径构建失败阻断（与 Pi 的 wslPiProviderBuildError 同判）。
		if wslDistro != "" {
			return launchSettings, wslOmpProviderBuildError(providerID, cfgErr)
		}
		a.Log.Warn("omp", "生成 omp models.yml 失败，回退内置 provider", cfgErr.Error())
		launchSettings.Provider, _ = ompProviderMapping(*provider)
	}

	// 冗余兜底：注入对应 API Key env var（与 OpenCode/Pi 的双路注入一致）。
	// omp 自定义 provider 已内嵌 apiKey，这里仅作为额外保险。
	if apiKey != "" {
		if _, apiKeyEnv := ompProviderMapping(*provider); apiKeyEnv != "" {
			envOverrides[apiKeyEnv] = apiKey
		}
	}
	return launchSettings, nil
}

// wslOmpProviderBuildError / wslOmpProviderWriteError 是 WSL 路径「选中
// provider 构建/写入失败 → 阻断启动」的中文报错（D5：与 Pi 同判，不静默回退
// 丢自定义 baseURL 的 ompProviderMapping 兜底）。独立成函数供单测钉文案。
func wslOmpProviderBuildError(providerID string, cfgErr error) error {
	return fmt.Errorf("无法构建 omp 供应商 %q 的模型配置：%v；本次未启动，未改用默认模型", providerID, cfgErr)
}

func wslOmpProviderWriteError(providerID string, writeErr error) error {
	return fmt.Errorf("无法写入 omp 供应商 %q 的 WSL 侧模型配置：%v；本次未启动，未改用默认模型", providerID, writeErr)
}

// piProviderMapping 把 amagi Provider 映射成 Pi 的内置 provider 名 + 对应 API Key env var。
//
// 注意：这是**回退路径**，仅当 LaunchPiSession 未能生成 pi models.json 时使用。
// 主路径是 launcher.BuildPiModelsConfig + WritePiAgentConfig，会把 amagi provider
// 翻译成 pi 的自定义命名 provider（"amagi-<name>"，含完整 baseURL/api/apiKey），
// 合并到 Pi 的标准 ~/.pi/agent/models.json 后加载。
//
// 此回退映射把第三方 Anthropic/OpenAI 兼容 provider 粗略归到 pi 内置的
// anthropic/openai，会丢失自定义 baseURL（导致第三方 provider 误打官方 endpoint），
// 因此仅作兜底，不应作为常态路径。
func piProviderMapping(p config.Provider) (piProvider, apiKeyEnv string) {
	if p.IsAnthropicCompatible() {
		return "anthropic", "ANTHROPIC_API_KEY"
	}
	if p.IsOpenAICompatible() {
		return "openai", "OPENAI_API_KEY"
	}
	return "", ""
}

// resolvePiLaunchSettings 解析 Pi 会话启动参数：确定 provider / model / thinking。
// requestedModel 为空时回退到 provider.DefaultModel。
// params.ReasoningEffort（low/medium/high/xhigh/max）直接作为 pi 的 --thinking 级别，
// 两者值域兼容；若 Thinking.Type=="disabled" 则强制 off。
//
// 返回的 Provider 是初始猜测（piProviderMapping 的内置 provider 名）；
// LaunchPiSession 在成功写入 pi models.json 后会用 "amagi-<name>" 覆盖它。
func resolvePiLaunchSettings(provider config.Provider, requestedModel string, params config.Parameters) piLaunchSettings {
	model := strings.TrimSpace(requestedModel)
	piProvider, _ := piProviderMapping(provider)
	if model == "" {
		model = strings.TrimSpace(provider.DefaultModel)
	}
	thinking := strings.TrimSpace(params.ReasoningEffort)
	// Thinking.Type=disabled 显式关闭思考（pi 用 off）。
	// 注意：只有 ReasoningEffort 为空且 Thinking 显式 disabled 时才映射 off，
	// 避免 disabled + 某个 effort 值的歧义（effort 优先）。
	if thinking == "" && params.Thinking != nil && params.Thinking.Type == "disabled" {
		thinking = "off"
	}
	return piLaunchSettings{
		Provider: piProvider,
		Model:    model,
		Thinking: thinking,
	}
}

// ompProviderMapping 把 amagi Provider 映射成 omp 的内置 provider 名 + 对应 API Key env var。
//
// 注意：这是**回退路径**，仅当 LaunchOmpSession 未能生成 omp models.yml 时使用。
// 主路径是 launcher.BuildOmpModelsConfig + WriteOmpAgentConfig，会把 amagi provider
// 翻译成 omp 的自定义命名 provider（"amagi-<name>"，含完整 baseURL/api/apiKey），
// 合并到 omp 的标准 ~/.omp/agent/models.yml 后加载。
//
// 此回退映射把第三方 Anthropic/OpenAI 兼容 provider 粗略归到 omp 内置的
// anthropic/openai，会丢失自定义 baseURL（导致第三方 provider 误打官方 endpoint），
// 因此仅作兜底，不应作为常态路径（复刻 piProviderMapping）。
func ompProviderMapping(p config.Provider) (ompProvider, apiKeyEnv string) {
	if p.IsAnthropicCompatible() {
		return "anthropic", "ANTHROPIC_API_KEY"
	}
	if p.IsOpenAICompatible() {
		return "openai", "OPENAI_API_KEY"
	}
	return "", ""
}

// resolveOmpLaunchSettings 解析 omp 会话启动参数：确定 provider / model / thinking
// （复刻 resolvePiLaunchSettings）。
// requestedModel 为空时回退到 provider.DefaultModel。
// params.ReasoningEffort（low/medium/high/xhigh/max）直接作为 omp 的 --thinking 级别，
// 两者值域兼容；若 Thinking.Type=="disabled" 则强制 off。
//
// 返回的 Provider 是初始猜测（ompProviderMapping 的内置 provider 名）；
// LaunchOmpSession 在成功写入 omp models.yml 后会用 "amagi-<name>" 覆盖它。
func resolveOmpLaunchSettings(provider config.Provider, requestedModel string, params config.Parameters) ompLaunchSettings {
	model := strings.TrimSpace(requestedModel)
	ompProvider, _ := ompProviderMapping(provider)
	if model == "" {
		model = strings.TrimSpace(provider.DefaultModel)
	}
	thinking := strings.TrimSpace(params.ReasoningEffort)
	// Thinking.Type=disabled 显式关闭思考（omp 用 off）。
	// 注意：只有 ReasoningEffort 为空且 Thinking 显式 disabled 时才映射 off，
	// 避免 disabled + 某个 effort 值的歧义（effort 优先）。
	if thinking == "" && params.Thinking != nil && params.Thinking.Type == "disabled" {
		thinking = "off"
	}
	return ompLaunchSettings{
		Provider: ompProvider,
		Model:    model,
		Thinking: thinking,
	}
}

// GetProvidersByType 返回指定类型的 Provider 列表（type 为 "openai" 或 "anthropic"）
// 使用双格式兼容方法判断 Provider 类型。
func (a *App) GetProvidersByType(providerType string) map[string]config.Provider {
	allProviders := a.Config.GetProviders()
	result := make(map[string]config.Provider)
	for name, p := range allProviders {
		switch providerType {
		case "openai":
			if p.IsOpenAICompatible() {
				result[name] = p
			}
		case "anthropic":
			if p.IsAnthropicCompatible() {
				result[name] = p
			}
		}
	}
	return result
}

// SetPluginSubItemEnabled 设置插件子项启用/禁用状态
// pluginId: 插件 ID
// subItemType: 子项类型（skill/hook/command/agent/mcp）
// subItemId: 子项名称
// enabled: 是否启用
func (a *App) SetPluginSubItemEnabled(pluginId string, subItemType string, subItemId string, enabled bool) error {
	a.Log.Info("plugin", "设置插件子项状态", fmt.Sprintf("plugin=%s type=%s id=%s enabled=%v", pluginId, subItemType, subItemId, enabled))

	// Claude 与 Codex 插件 ID 都使用 `name@marketplace` 格式（见 plugin/service.go 与
	// codexplugin/helpers.go 的 splitPluginID），不能用 strings.Contains(@) 区分。
	// 改为查询 Claude 已安装插件注册表：命中则派 Claude service，否则派 Codex service。
	isClaude, err := a.isClaudePlugin(pluginId)
	if err != nil {
		// 注册表读取失败时无法确定引擎归属：显式失败而非静默落到 Codex no-op
		//（否则 Claude 插件的开关会无声不生效，用户无从排查）。
		return fmt.Errorf("无法确定插件 %s 的引擎归属（读取 Claude 注册表失败：%v），请重试", pluginId, err)
	}
	if isClaude {
		return a.Plugins.SetPluginSubItemEnabled(pluginId, subItemType, subItemId, enabled)
	}
	return a.CodexPlugins.SetPluginSubItemEnabled(pluginId, subItemType, subItemId, enabled)
}

// isClaudePlugin 判断 pluginId 是否为 Claude 引擎下已安装的插件。
// 以 Claude 注册表（~/.claude/plugins/installed_plugins.json，经 plugin.Service 抽象）
// 为单一真相源，避免基于字符串启发式误判；读取失败返回错误由调用方显式处理。
func (a *App) isClaudePlugin(pluginId string) (bool, error) {
	if a.Plugins == nil {
		return false, nil
	}
	plugins, err := a.Plugins.GetInstalledPlugins()
	if err != nil {
		return false, err
	}
	for i := range plugins {
		if plugins[i].ID == pluginId {
			return true, nil
		}
	}
	return false, nil
}

func (a *App) CheckForUpdate() (*updater.UpdateInfo, error) {
	return a.Updater.CheckForUpdate()
}

func (a *App) DownloadAndApplyUpdate() error {
	return a.Updater.DownloadAndApply(func(downloaded, total int64) {
		wailsRuntime.EventsEmit(a.ctx, "update:progress", map[string]any{
			"downloaded": downloaded,
			"total":      total,
		})
	})
}

// GetGitHubToken 返回当前配置的 GitHub Token。
func (a *App) GetGitHubToken() string {
	return a.Settings.GetGitHubToken()
}

// SetGitHubToken 保存 GitHub Token 并同步到 Updater。
func (a *App) SetGitHubToken(token string) error {
	if err := a.Settings.SetGitHubToken(token); err != nil {
		return err
	}
	a.Updater.SetToken(token)
	return nil
}

// --- 日志系统 API ---

// GetLogs 获取日志条目（支持过滤）
func (a *App) GetLogs(level string, source string, keyword string, limit int) []logging.Entry {
	return a.Log.GetEntries(level, source, keyword, limit)
}

// GetLogSources 获取所有日志来源
func (a *App) GetLogSources() []string {
	return a.Log.GetSources()
}

// GetLogFiles 获取日志文件列表
func (a *App) GetLogFiles() []string {
	return a.Log.GetLogFiles()
}

// GetLogFileContent 获取日志文件内容
func (a *App) GetLogFileContent(filename string) (string, error) {
	return a.Log.GetLogFileContent(filename)
}

// ClearLogs 清除内存日志
func (a *App) ClearLogs() {
	a.Log.ClearEntries()
	a.Log.Info("app", "内存日志已清除")
}

// ExportLogs 导出日志为JSON
func (a *App) ExportLogs() (string, error) {
	return a.Log.ExportJSON()
}

// --- 诊断与密钥查询 API ---

// HasProviderAPIKey reports whether an API key exists for the given provider or
// legacy provider key name.
func (a *App) HasProviderAPIKey(provider string) bool {
	return a.Secrets.HasAPIKey(provider)
}

// GetProviderAPIKey returns the stored API key for the given provider or
// legacy provider key name.
func (a *App) GetProviderAPIKey(provider string) (string, error) {
	return a.Secrets.GetAPIKey(provider)
}

// getKeyDiagnostics 返回所有提供商的密钥来源诊断信息
func (a *App) getKeyDiagnostics() map[string]map[string]string {
	providers := a.Config.GetProviderNames()
	return a.Secrets.GetKeyDiagnostics(providers)
}

// ExportConfigToFile 将所有可移植配置合并导出为 JSON 文件。
// 通过系统对话框让用户选择保存位置；导出文件仅对当前用户可读。
func (a *App) ExportConfigToFile() (string, error) {
	a.portableConfigMu.Lock()
	defer a.portableConfigMu.Unlock()

	// 优先弹出保存对话框，避免配置快照中的慢速系统服务（例如 macOS
	// 钥匙串授权）让用户误以为按钮没有响应。
	savePath, err := wailsRuntime.SaveFileDialog(a.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "导出完整配置",
		DefaultFilename: "amagi-codebox-complete-config.json",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON 文件 (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("open save dialog: %w", err)
	}

	// 用户取消了对话框
	if savePath == "" {
		a.Log.Info("app", "用户取消了配置导出")
		return "", nil
	}

	a.Log.Info("app", "开始导出配置")
	exportCfg, err := a.buildCompleteExportConfig()
	if err != nil {
		return "", err
	}

	data, err := json.MarshalIndent(exportCfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal export config: %w", err)
	}
	data = append(data, '\n')

	// atomic 写入用户选择的路径
	if err := atomicWriteFile(savePath, data); err != nil {
		return "", fmt.Errorf("write export file: %w", err)
	}
	a.Log.Info("app", "配置已导出", savePath)

	return savePath, nil
}

// ImportConfigFromFile 通过文件选择对话框导入 JSON 配置文件。
// v2 文件采用完整快照替换语义；v1 文件继续按旧协议兼容导入。
func (a *App) ImportConfigFromFile() (string, error) {
	a.portableConfigMu.Lock()
	defer a.portableConfigMu.Unlock()
	a.Log.Info("app", "开始导入配置")

	// 弹出文件选择对话框
	filePath, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "导入配置",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON 文件 (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("open file dialog: %w", err)
	}

	// 用户取消了对话框
	if filePath == "" {
		a.Log.Info("app", "用户取消了配置导入")
		return "", nil
	}

	// 读取文件内容
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	exportCfg, exportRaw, err := decodeConfigExport(data)
	if err != nil {
		return "", err
	}

	if exportRaw.Portable != nil {
		portable, err := decodePortableConfig(exportCfg.Portable)
		if err != nil {
			return "", err
		}
		if err := a.applyCompleteConfig(exportCfg, portable, exportRaw.OpenCodePresets != nil); err != nil {
			return "", fmt.Errorf("import complete config: %w", err)
		}
		msg := fmt.Sprintf("完整配置导入成功：%d 个提供商。请重启应用以载入全部设置", len(exportCfg.Providers))
		a.Log.Info("app", msg, filePath)
		return msg, nil
	}

	// 遍历 providers 并导入
	importCount := 0
	for name, ep := range exportCfg.Providers {
		if err := a.saveImportedProviderAPIKey(name, selectImportedProviderAPIKey(ep)); err != nil {
			a.Log.Warn("app", "保存 provider API key 失败", fmt.Sprintf("provider=%s err=%v", name, err))
		}

		provider := buildProviderFromExportProvider(ep)

		if err := a.Config.SaveProvider(name, provider); err != nil {
			return "", fmt.Errorf("save provider %q: %w", name, err)
		}

		importCount++
	}

	// 导入 AgentTeams 配置（如果存在）
	if exportCfg.AgentTeams.TeammateMode != "" || exportCfg.AgentTeams.Enabled {
		if err := a.Config.SetAgentTeams(exportCfg.AgentTeams); err != nil {
			a.Log.Warn("app", "导入 AgentTeams 配置失败", err.Error())
		}
	}

	// 导入 preset 快照。
	// 为避免 omitempty / 字段缺失导致旧数据残留，nil 视为空快照。
	hasExplicitOpenCodeSnapshot := exportRaw.OpenCodePresets != nil
	if err := a.Config.ReplaceImportedPresetSnapshots(exportCfg.TerminalPresets, exportCfg.OpenCodePresets, hasExplicitOpenCodeSnapshot); err != nil {
		a.Log.Warn("app", "导入 preset 快照失败", err.Error())
	} else {
		a.Log.Info("app", "preset 快照已导入")
	}
	if err := a.Secrets.Save(); err != nil {
		return "", fmt.Errorf("save imported provider secrets: %w", err)
	}
	if err := a.syncProvidersToHarnesses(); err != nil {
		return "", a.providerSyncError("提供商配置导入", err)
	}

	msg := fmt.Sprintf("成功导入 %d 个提供商配置", importCount)
	a.Log.Info("app", msg, filePath)
	return msg, nil
}

// GetProviderExportJSON 返回指定提供商的配置（含 API key）的格式化 JSON 字符串，
// 供前端 JSON 编辑功能使用。
// 支持双格式结构导出，同时保留旧字段兼容。
func (a *App) GetProviderExportJSON(providerName string) (string, error) {
	provider, err := a.Config.GetProvider(providerName)
	if err != nil {
		return "", fmt.Errorf("get provider %q: %w", providerName, err)
	}

	apiKey, _ := a.getProviderAPIKey(providerName, *provider)
	ep := buildExportProvider(*provider, apiKey)

	data, err := json.MarshalIndent(ep, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal provider JSON: %w", err)
	}
	return string(data), nil
}

// SaveProviderFromJSON 将前端传入的 JSON 字符串解析后保存到指定提供商，
// 若 APIKey 有变更则同步更新密钥存储。
// 支持双格式结构导入，同时兼容旧 JSON 格式。
// API key 不会写入 CodeBox 的 models.json；为了让独立 harness 可直接使用统一
// provider，它会同步到权限为 0600 的 harness provider 配置。各 harness 自己的认证
// 存储（OpenCode auth.json、Pi auth.json、OMP agent.db）不会被修改。
func (a *App) SaveProviderFromJSON(providerName string, jsonStr string) error {
	trimmedName := strings.TrimSpace(providerName)
	if trimmedName == "" || trimmedName != providerName || strings.Contains(trimmedName, "/") {
		return fmt.Errorf("invalid provider name %q: must be non-empty, trimmed, and must not contain '/'", providerName)
	}
	var ep config.ExportProvider
	if err := json.Unmarshal([]byte(jsonStr), &ep); err != nil {
		return fmt.Errorf("invalid JSON format: %w", err)
	}
	var requestOptions struct {
		ClearAPIKey bool `json:"clear_api_key"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &requestOptions); err != nil {
		return fmt.Errorf("invalid JSON format: %w", err)
	}

	apiKey := selectImportedProviderAPIKey(ep)
	secretsChanged := false
	if requestOptions.ClearAPIKey {
		a.deleteProviderAPIKeys(providerName)
		secretsChanged = true
	} else if strings.TrimSpace(apiKey) != "" {
		if err := a.saveImportedProviderAPIKey(providerName, apiKey); err != nil {
			return fmt.Errorf("save provider API key for %q: %w", providerName, err)
		}
		secretsChanged = true
	}

	provider := buildProviderFromExportProvider(ep)

	if err := a.Config.SaveProvider(providerName, provider); err != nil {
		return fmt.Errorf("save provider %q: %w", providerName, err)
	}
	if secretsChanged {
		if err := a.Secrets.Save(); err != nil {
			return fmt.Errorf("provider saved but API key persistence failed: %w", err)
		}
	}
	if err := a.syncProvidersToHarnesses(); err != nil {
		return a.providerSyncError("提供商配置", err)
	}

	a.Log.Info("app", "已从 JSON 保存提供商配置", providerName)
	return nil
}

// UpdateProvider 统一编辑提供商入口：支持改名 + 属性更新 + 密钥更新。
//   - oldName == newName：仅更新属性，复用 SaveProviderFromJSON 路径（零副作用）。
//   - oldName != newName（改名）：先 config 迁移（RenameProvider 原子写盘），
//     再覆盖新属性（SaveProvider），最后 secrets 密钥迁移并显式落盘。
//
// providerJSON 为完整的 ExportProvider JSON（含可编辑属性与可选 API Key）。
// API Key 为空表示"保持不变"——后端会迁移旧密钥到新 name。
//
// 失败降级（设计 4.6）：config 已原子写成功后 secrets.Save 失败时，不回滚 config，
// 返回带友好提示的 error，用户重新填写密钥即可。
func (a *App) UpdateProvider(oldName, newName string, providerJSON string) error {
	// —— 1. 校验（持锁前，不依赖 config 状态）——
	if oldName == "" {
		return errors.New("provider name is required")
	}
	trimmedNew := strings.TrimSpace(newName)
	if trimmedNew == "" {
		return errors.New("provider name is required")
	}
	if strings.Contains(trimmedNew, "/") {
		return fmt.Errorf("invalid provider name %q: must not contain '/'", trimmedNew)
	}
	newName = trimmedNew

	// —— 2. 解析 ExportProvider JSON ——
	var ep config.ExportProvider
	if err := json.Unmarshal([]byte(providerJSON), &ep); err != nil {
		return fmt.Errorf("invalid JSON format: %w", err)
	}

	// —— 3. 分流：未改名走现成路径 ——
	if oldName == newName {
		return a.SaveProviderFromJSON(newName, providerJSON)
	}

	// —— 改名分支 ——
	// 3a. 预读旧密钥（config 改动前读取，确保可读）。
	// 只读 secrets cache（统一 key + legacy 回退），不查环境变量——环境变量不可迁移。
	oldKey := ""
	if oldProv, err := a.Config.GetProvider(oldName); err == nil && oldProv != nil {
		oldKey = a.readStoredProviderAPIKey(oldName, *oldProv)
	}

	// 3b. config 迁移（原子写盘）
	if err := a.Config.RenameProvider(oldName, newName); err != nil {
		return err
	}

	// 3c. 覆盖新属性（此时 newName 已存在，upsert）。
	// 复用 buildProviderFromExportProvider，保持 JSON 结构与现有路径一致。
	newProvider := buildProviderFromExportProvider(ep)
	if err := a.Config.SaveProvider(newName, newProvider); err != nil {
		// config 已改名（步骤 3b 成功），属性未更新——不回滚，前端可重试。
		a.Log.Warn("app", "provider 改名后属性覆盖失败，可重试", oldName+" -> "+newName+": "+err.Error())
		return fmt.Errorf("rename succeeded but save new properties failed: %w", err)
	}

	// 3d. secrets 迁移（三分支）
	newKey := selectImportedProviderAPIKey(ep)
	secretsChanged := false
	switch {
	case newKey != "":
		// 用户填了新密钥：写入 newName 的统一 key。
		if err := a.Secrets.SetAPIKey(newName, newKey); err != nil {
			return fmt.Errorf("set new API key for %q: %w", newName, err)
		}
		secretsChanged = true
		// 删除 oldName 的所有密钥条目（统一 key + legacy）。
		a.deleteProviderAPIKeys(oldName)
	case oldKey != "":
		// 用户未填新密钥（保持不变）：迁移旧密钥到 newName。
		if err := a.Secrets.SetAPIKey(newName, oldKey); err != nil {
			return fmt.Errorf("migrate API key to %q: %w", newName, err)
		}
		a.deleteProviderAPIKeys(oldName)
		secretsChanged = true
	}
	// newKey 空 且 oldKey 空：无密钥可迁，跳过。

	// 3e. secrets 落盘（仅在发生变更时）
	if secretsChanged {
		if err := a.Secrets.Save(); err != nil {
			// 设计 4.6：config 已一致，secrets 内存 cache 已改但 Save 失败。
			// 不回滚 config（反向操作风险更高）。降级为提示用户重填密钥。
			a.Log.Warn("app", "provider 改名后密钥落盘失败，请重新填写密钥",
				oldName+" -> "+newName+": "+err.Error())
			return fmt.Errorf("config renamed but secrets save failed: %w; please re-enter API key for %s", err, newName)
		}
	}
	if err := a.syncProvidersToHarnesses(); err != nil {
		return a.providerSyncError("提供商配置", err)
	}

	a.Log.Info("app", "provider 已改名", oldName+" -> "+newName)
	return nil
}

// readStoredProviderAPIKey 读取 provider 在 secrets cache 中的统一密钥，
// 回退到 legacy providerName:format 命名。不查询环境变量（环境变量不可迁移）。
func (a *App) readStoredProviderAPIKey(providerName string, provider config.Provider) string {
	if key, _ := a.Secrets.GetAPIKey(providerName); strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key)
	}
	for _, format := range legacyProviderAPIKeyCandidates(provider) {
		if key, err := a.Secrets.GetAPIKey(providerName + ":" + format); err == nil {
			if trimmed := strings.TrimSpace(key); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

// deleteProviderAPIKeys 删除指定 provider 在 secrets cache 中的所有密钥条目，
// 包含统一 key（providerName）与 legacy 命名（providerName:anthropic / providerName:openai）。
// 用于改名后清理旧 name 的残留，避免 secrets.enc 出现指向已不存在 provider 的孤儿条目。
func (a *App) deleteProviderAPIKeys(providerName string) {
	_ = a.Secrets.DeleteAPIKey(providerName)
	_ = a.Secrets.DeleteAPIKey(providerName + ":anthropic")
	_ = a.Secrets.DeleteAPIKey(providerName + ":openai")
}

// DeleteProvider 删除指定服务商配置。
func (a *App) DeleteProvider(name string) error {
	if name == "" {
		return errors.New("provider name is required")
	}
	if err := a.Config.DeleteProvider(name); err != nil {
		return err
	}
	var deleteErrs []error
	if a.Secrets != nil {
		a.deleteProviderAPIKeys(name)
		if err := a.Secrets.Save(); err != nil {
			deleteErrs = append(deleteErrs, fmt.Errorf("provider deleted but secret cleanup failed: %w", err))
		}
	}
	if err := a.syncProvidersToHarnesses(); err != nil {
		deleteErrs = append(deleteErrs, a.providerSyncError("提供商删除", err))
	}
	return errors.Join(deleteErrs...)
}

// --- URL 历史 API ---

// GetUrlHistory 获取指定Provider的URL历史
func (a *App) GetUrlHistory(providerID string) ([]string, error) {
	return a.Config.GetUrlHistory(providerID)
}

// AddUrlToHistory 添加URL到历史记录（自动去重并调整到最前）
func (a *App) AddUrlToHistory(providerID, url string) error {
	return a.Config.AddUrlToHistory(providerID, url)
}

// RemoveUrlFromHistory 从历史记录中删除指定URL
func (a *App) RemoveUrlFromHistory(providerID, url string) error {
	return a.Config.RemoveUrlFromHistory(providerID, url)
}

// --- 自定义环境变量 API ---

// GetEnvVars 返回所有自定义环境变量
func (a *App) GetEnvVars() ([]envvars.EnvVar, error) {
	return a.EnvVars.GetAll(), nil
}

// SetEnvVar 设置单个自定义环境变量（不存在则新增，存在则更新）
func (a *App) SetEnvVar(key, value string) error {
	return a.EnvVars.Set(key, value)
}

// SetEnvVarPathAllow 设置/取消单个自定义环境变量的「路径值跨界放行」标记
// （P4-A2 per-key 放行门）。开启后该键的 Windows 盘符路径值不再被 WSL 启动链
// 剥离，并经 WSLENV /p 标志跨界翻译为 /mnt 路径；保留键（WSLENV/PATH/PATHEXT）
// 拒绝。值本身不变，仅翻转标记并持久化到 envvars.json。
func (a *App) SetEnvVarPathAllow(key string, allow bool) error {
	return a.EnvVars.SetPathAllow(key, allow)
}

// DeleteEnvVar 删除指定 key 的自定义环境变量
func (a *App) DeleteEnvVar(key string) error {
	return a.EnvVars.Delete(key)
}

// ImportEnvVars 从 JSON 字符串导入自定义环境变量（全量替换）
func (a *App) ImportEnvVars(jsonStr string) error {
	return a.EnvVars.Import(jsonStr)
}

// ExportEnvVars 导出自定义环境变量为 JSON 字符串
func (a *App) ExportEnvVars() (string, error) {
	return a.EnvVars.Export()
}

// GetEnvVarsJSON 获取所有自定义环境变量的 JSON 格式（供 JSON 编辑器使用）
func (a *App) GetEnvVarsJSON() (string, error) {
	return a.EnvVars.GetJSON()
}

// SaveEnvVarsJSON 从 JSON 字符串保存自定义环境变量（供 JSON 编辑器使用）
func (a *App) SaveEnvVarsJSON(jsonStr string) error {
	return a.EnvVars.SaveJSON(jsonStr)
}

// ExportEnvVarsToFile 弹出保存对话框，将自定义环境变量导出到用户选择的 JSON 文件。
func (a *App) ExportEnvVarsToFile() error {
	data, err := a.EnvVars.Export()
	if err != nil {
		return fmt.Errorf("export envvars: %w", err)
	}

	savePath, err := wailsRuntime.SaveFileDialog(a.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "导出环境变量",
		DefaultFilename: "envvars.json",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON 文件 (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return fmt.Errorf("open save dialog: %w", err)
	}
	if savePath == "" {
		return nil // 用户取消
	}

	if err := atomicWriteFile(savePath, []byte(data+"\n")); err != nil {
		return fmt.Errorf("write envvars file: %w", err)
	}
	a.Log.Info("app", "环境变量已导出", savePath)
	return nil
}

// ImportEnvVarsFromFile 弹出打开对话框，从用户选择的 JSON 文件导入自定义环境变量（全量替换）。
func (a *App) ImportEnvVarsFromFile() error {
	filePath, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "导入环境变量",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON 文件 (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return fmt.Errorf("open file dialog: %w", err)
	}
	if filePath == "" {
		return nil // 用户取消
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	// 剥离 UTF-8 BOM（Windows 编辑器可能在文件开头添加 BOM）
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}

	if err := a.EnvVars.Import(string(data)); err != nil {
		return fmt.Errorf("import envvars: %w", err)
	}
	a.Log.Info("app", "环境变量已导入", filePath)
	return nil
}

// GetEnvVarsGlobalSyncStatus 返回环境变量全局同步状态
func (a *App) GetEnvVarsGlobalSyncStatus() envvars.GlobalSyncStatus {
	return a.EnvVars.GetGlobalSyncStatus()
}

// SetEnvVarsGlobalSyncEnabled 开启或关闭环境变量全局同步
func (a *App) SetEnvVarsGlobalSyncEnabled(enabled bool) (envvars.GlobalSyncStatus, error) {
	return a.EnvVars.SetGlobalSyncEnabled(enabled)
}

// --- 全局 OpenCode 配置 API ---

// GetOpenCodeConfig 读取全局 OpenCode 配置文件内容（JSON 文本）。
// 若文件不存在则返回默认空配置；若文件内容非法 JSON 则原样返回供用户修正。
func (a *App) GetOpenCodeConfig() (string, error) {
	return a.OpenCodeConfig.GetOpenCodeConfig()
}

// SaveOpenCodeConfig 校验并保存全局 OpenCode 配置文件。
// content 必须为合法 JSON，否则返回错误。
// 保存采用原子写入（先写临时文件再 rename），避免损坏。
func (a *App) SaveOpenCodeConfig(content string) error {
	if !a.providerSyncEnabled {
		return a.OpenCodeConfig.SaveOpenCodeConfig(content)
	}
	a.providerSyncMu.Lock()
	defer a.providerSyncMu.Unlock()
	if err := a.OpenCodeConfig.SaveOpenCodeConfig(content); err != nil {
		return err
	}
	return a.syncProvidersToHarnessesLocked()
}

// GetOpenCodeConfigPath 返回全局 OpenCode 配置文件的绝对路径，供前端展示。
func (a *App) GetOpenCodeConfigPath() (string, error) {
	return a.OpenCodeConfig.GetOpenCodeConfigPath()
}

// --- Pi (amagi-pi) 配置 API ---

// GetAmagiConfig 读取 pi 的 amagi.json 配置内容（JSON 文本）。
func (a *App) GetAmagiConfig() (string, error) {
	return a.PiConfig.GetAmagiConfig()
}

// SaveAmagiConfig 校验并保存 amagi.json（原子写入）。WSL 内 pi 共享 Windows
// agent 根（1.3.126），无需再向 distro 供给标脏。
func (a *App) SaveAmagiConfig(content string) error {
	return a.PiConfig.SaveAmagiConfig(content)
}

// GetAmagiConfigPath 返回 amagi.json 的绝对路径，供前端展示。
func (a *App) GetAmagiConfigPath() (string, error) {
	return a.PiConfig.GetAmagiConfigPath()
}

// GetPiModelCatalog 读取 models.json 抽取 provider→model 目录（只读，不含密钥），
// 供前端可视化配置的下拉关联使用。
func (a *App) GetPiModelCatalog() (string, error) {
	return a.PiConfig.GetPiModelCatalog()
}

// GetPiModelsConfig 读取 pi 的 models.json 注册表全文（含 apiKey，本地文件）。
func (a *App) GetPiModelsConfig() (string, error) {
	return a.PiConfig.GetModelsConfig()
}

// SavePiModelsConfig 校验并保存 models.json（原子写入）。WSL 内 pi 共享
// Windows agent 根（1.3.126），无需再向 distro 供给标脏。
func (a *App) SavePiModelsConfig(content string) error {
	return a.PiConfig.SaveModelsConfig(content)
}

// GetPiModelsConfigPath 返回 models.json 的绝对路径，供前端展示。
func (a *App) GetPiModelsConfigPath() (string, error) {
	return a.PiConfig.GetModelsConfigPath()
}

// GetPiAuthConfig 读取 pi 的 auth.json 提供商凭据全文（含明文密钥，仅本地编辑用）。
func (a *App) GetPiAuthConfig() (string, error) {
	return a.PiConfig.GetAuthConfig()
}

// SavePiAuthConfig 校验并保存 auth.json（原子写入）。
func (a *App) SavePiAuthConfig(content string) error {
	return a.PiConfig.SaveAuthConfig(content)
}

// GetPiAuthConfigPath 返回 auth.json 的绝对路径，供前端展示。
func (a *App) GetPiAuthConfigPath() (string, error) {
	return a.PiConfig.GetAuthConfigPath()
}

// --- OMP (oh-my-pi) 配置 API ---

// GetOmpConfig 读取 omp 的 config.yml 配置内容（YAML 文本）。
func (a *App) GetOmpConfig() (string, error) {
	return a.OmpConfig.GetOmpConfig()
}

// SaveOmpConfig 校验并保存 config.yml（原子写入）。
func (a *App) SaveOmpConfig(content string) error {
	return a.OmpConfig.SaveOmpConfig(content)
}

// GetOmpConfigPath 返回 config.yml 的绝对路径，供前端展示。
func (a *App) GetOmpConfigPath() (string, error) {
	return a.OmpConfig.GetOmpConfigPath()
}

// GetOmpModelCatalog 读取 models.yml 抽取 provider→model 目录（只读，不含密钥），
// 供前端可视化配置的下拉关联使用。
func (a *App) GetOmpModelCatalog() (string, error) {
	return a.OmpConfig.GetOmpModelCatalog()
}

// GetOmpModelsConfig 读取 omp 的 models.yml 注册表全文（含 apiKey，本地文件）。
func (a *App) GetOmpModelsConfig() (string, error) {
	return a.OmpConfig.GetModelsConfig()
}

// SaveOmpModelsConfig 校验并保存 models.yml（原子写入）。
func (a *App) SaveOmpModelsConfig(content string) error {
	return a.OmpConfig.SaveModelsConfig(content)
}

// GetOmpModelsConfigPath 返回 models.yml 的绝对路径，供前端展示。
func (a *App) GetOmpModelsConfigPath() (string, error) {
	return a.OmpConfig.GetModelsConfigPath()
}

// --- 终端预设 API ---

// GetTerminalPresets 获取指定终端类型的所有预设。
func (a *App) GetTerminalPresets(terminalType string) (map[string]config.TerminalPreset, error) {
	return a.Config.GetTerminalPresets(terminalType)
}

// SaveTerminalPreset 保存指定终端类型的预设。
func (a *App) SaveTerminalPreset(terminalType string, presetName string, preset config.TerminalPreset) error {
	if err := a.Config.SaveTerminalPreset(terminalType, presetName, preset); err != nil {
		return err
	}
	return a.providerSyncError("公共预设", a.syncProvidersToHarnesses())
}

// DeleteTerminalPreset 删除指定终端类型的预设。
func (a *App) DeleteTerminalPreset(terminalType string, presetName string) error {
	if err := a.Config.DeleteTerminalPreset(terminalType, presetName); err != nil {
		return err
	}
	return a.providerSyncError("公共预设", a.syncProvidersToHarnesses())
}

// MigrateProviderPresetsToTerminal 将旧的 provider.presets 迁移到 terminal_presets。
// 返回 (迁移数量, error)。
func (a *App) MigrateProviderPresetsToTerminal() (int, error) {
	count, _, err := a.Config.MigrateProviderPresetsToTerminal()
	return count, err
}

// GetMergedTerminalPresets 返回指定终端类型的合并预设列表（新体系优先，旧体系回退）。
func (a *App) GetMergedTerminalPresets(terminalType string) ([]config.MergedTerminalPreset, error) {
	return a.Config.GetMergedTerminalPresets(terminalType)
}

// ResolveTerminalPreset 按 terminal type + key 解析出 terminal preset 的详情。
// 返回值: (providerName, model, hasOpenCodeCfg, openCodeCfgJSON, found)
func (a *App) ResolveTerminalPreset(terminalType string, key string) (string, string, string, bool) {
	provName, tp, err := a.Config.ResolveTerminalPreset(terminalType, key)
	if err != nil || tp == nil {
		return "", "", "", false
	}
	ocCfg := ""
	if len(tp.OpenCodeCfg) > 0 {
		ocCfg = string(tp.OpenCodeCfg)
	}
	return provName, tp.Model, ocCfg, true
}

// --- 已保存工作目录 API ---

// GetSavedWorkDirs 获取已保存的工作目录列表。
func (a *App) GetSavedWorkDirs() ([]settings.WorkDirEntry, error) {
	return a.Settings.GetSavedWorkDirs(), nil
}

// AddSavedWorkDir 添加工作目录（去重，按 path 去重；label 空则用路径末段），返回最新列表。
func (a *App) AddSavedWorkDir(path string, label string) ([]settings.WorkDirEntry, error) {
	if err := a.Settings.AddSavedWorkDir(path, label); err != nil {
		return nil, err
	}
	return a.Settings.GetSavedWorkDirs(), nil
}

// RemoveSavedWorkDir 移除工作目录，返回最新列表。
func (a *App) RemoveSavedWorkDir(path string) ([]settings.WorkDirEntry, error) {
	if err := a.Settings.RemoveSavedWorkDir(path); err != nil {
		return nil, err
	}
	return a.Settings.GetSavedWorkDirs(), nil
}
