package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 视觉模型导出（契约 docs/vision-export-contract.md §2）。
// 将带 Vision/Video 标记的 TerminalPreset 全量导出到
// ~/.agents/amagi-media-models.json，供 amagi-media-understanding 等 skill 消费。
// 字段名与文件格式即 API，单方不得擅改。
const (
	// VisionExportVersion 导出文件格式版本。
	VisionExportVersion = 1
	// VisionModelsPathEnv 环境变量：覆盖导出路径（测试用）。
	VisionModelsPathEnv = "AMAGI_MEDIA_MODELS_PATH"
	// visionExportFileName 默认导出文件名（位于 ~/.agents 下）。
	visionExportFileName = "amagi-media-models.json"
	// visionDefaultPriority vision_priority 为 0 时的归一化默认值（小者优先）。
	visionDefaultPriority = 100
)

// VisionExportParameters 透传给视觉调用方的推理参数（契约 §2：仅
// reasoning_effort / max_tokens / temperature / top_p）。
type VisionExportParameters struct {
	ReasoningEffort string  `json:"reasoning_effort,omitempty"`
	MaxTokens       int     `json:"max_tokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
	TopP            float64 `json:"top_p,omitempty"`
}

// VisionExportModel 导出的单个视觉模型条目（契约 §2 Schema）。
type VisionExportModel struct {
	ID           string                  `json:"id"` // provider/presetName
	Provider     string                  `json:"provider"`
	Preset       string                  `json:"preset"`
	Model        string                  `json:"model"`
	BaseURL      string                  `json:"base_url"`
	APIKey       string                  `json:"api_key"` // resolver 拿不到时写空串，消费方 fallback 读 auth_key_env
	AuthKeyEnv   string                  `json:"auth_key_env"`
	APIType      string                  `json:"api_type"`
	Capabilities []string                `json:"capabilities"` // "image" / "video"
	Priority     int                     `json:"priority"`     // 小者优先；0 归一化为 100
	Parameters   *VisionExportParameters `json:"parameters,omitempty"`
}

// VisionExportFile 导出文件根结构（契约 §2 Schema）。
type VisionExportFile struct {
	Version   int                 `json:"version"`
	UpdatedAt string              `json:"updated_at"` // RFC3339
	Models    []VisionExportModel `json:"models"`     // 无标记时为 []，区分「未配置」与「文件缺失」
}

// VisionModelsExportPath 返回导出路径：AMAGI_MEDIA_MODELS_PATH 覆盖，
// 默认 ~/.agents/amagi-media-models.json。
func VisionModelsExportPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv(VisionModelsPathEnv)); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".agents", visionExportFileName), nil
}

// ExportVisionModels 按契约 §2 幂等全量重导出视觉模型文件：
// 遍历 terminal_presets 的 anthropic 与 openai 两桶（标记独立于所在桶），
// 仅导出 Vision 或 Video 为 true、且关联 provider 为 OpenAI 兼容
// （EffectiveType == "openai"）的 preset；anthropic-only provider 跳过（v1 边界）。
// 无任何带标记 preset 时也写文件（models: []）。
//
// resolver 返回 provider 的明文 API key；拿不到（nil 或空串）时条目 api_key
// 写空串，消费方 fallback 读环境变量 auth_key_env（provider 的 auth_key 标识）。
// config 包不得直接依赖 secrets 包，resolver 由 App 组装时注入（契约 §2）。
//
// 调用方须保证 cfg 在调用期间不被并发修改（ConfigService 在写锁下调用）。
// 写失败仅返回错误，由调用方记 log，不阻断主流程。
func ExportVisionModels(cfg *AppConfig, resolver func(provider string) string) error {
	export := VisionExportFile{
		Version:   VisionExportVersion,
		UpdatedAt: time.Now().Format(time.RFC3339),
		Models:    []VisionExportModel{},
	}
	if cfg != nil && cfg.TerminalPresets != nil {
		for _, tt := range ValidTerminalPresetTypes() {
			for key, tp := range cfg.TerminalPresets.GetMap(tt) {
				if entry, ok := buildVisionExportModel(cfg, key, tp, resolver); ok {
					export.Models = append(export.Models, entry)
				}
			}
		}
	}
	// map 遍历无序：按 id 排序保证幂等全量重导出的字节级稳定（updated_at 除外）。
	sort.Slice(export.Models, func(i, j int) bool {
		return export.Models[i].ID < export.Models[j].ID
	})
	return writeVisionExportFile(&export)
}

// buildVisionExportModel 构建单个导出条目；返回 ok=false 表示该 preset 不导出
// （无标记 / provider 缺失 / anthropic-only provider）。
func buildVisionExportModel(cfg *AppConfig, key string, tp TerminalPreset, resolver func(provider string) string) (VisionExportModel, bool) {
	if !tp.Vision && !tp.Video {
		return VisionExportModel{}, false
	}
	provider, ok := cfg.Models[tp.Provider]
	if !ok {
		// provider 已被删除：无法解析端点，跳过该标记条目。
		return VisionExportModel{}, false
	}
	// v1 边界（契约 §2）：仅导出 OpenAI 兼容 provider（type=openai 或
	// EffectiveType 为 openai）；anthropic-only 跳过。
	if !strings.EqualFold(provider.EffectiveType(), "openai") {
		return VisionExportModel{}, false
	}

	// preset 短名：terminal_presets 的 key 为稳定 key（provider/shortName），
	// 优先从 key 派生；异常 key 回退 tp.Name / key 本身。
	presetName := tp.Name
	if prefix := tp.Provider + "/"; strings.HasPrefix(key, prefix) {
		presetName = strings.TrimPrefix(key, prefix)
	} else if presetName == "" {
		presetName = key
	}

	// 模型：preset 覆盖 provider 默认值（「留空使用 Provider 默认值」语义）。
	model := tp.Model
	if model == "" {
		model = provider.DefaultModel
	}

	caps := []string{}
	if tp.Vision {
		caps = append(caps, "image")
	}
	if tp.Video {
		caps = append(caps, "video")
	}

	priority := tp.VisionPriority
	if priority == 0 {
		priority = visionDefaultPriority
	}

	apiKey := ""
	if resolver != nil {
		apiKey = strings.TrimSpace(resolver(tp.Provider))
	}

	var params *VisionExportParameters
	if p := tp.Parameters; p.ReasoningEffort != "" || p.MaxTokens != 0 || p.Temperature != 0 || p.TopP != 0 {
		params = &VisionExportParameters{
			ReasoningEffort: p.ReasoningEffort,
			MaxTokens:       p.MaxTokens,
			Temperature:     p.Temperature,
			TopP:            p.TopP,
		}
	}

	return VisionExportModel{
		ID:       tp.Provider + "/" + presetName,
		Provider: tp.Provider,
		Preset:   presetName,
		Model:    model,
		// 存储/导出保持用户原始值（参照 BuildExportProvider 对
		// EffectiveBaseURLRaw 的用法）；归一化由消费端负责。
		BaseURL:      provider.EffectiveBaseURLRaw("openai"),
		APIKey:       apiKey,
		AuthKeyEnv:   provider.EffectiveAuthKey("openai"),
		APIType:      "openai",
		Capabilities: caps,
		Priority:     priority,
		Parameters:   params,
	}, true
}

// writeVisionExportFile 原子写盘（tmp+rename），目录 0755、文件 0600。
func writeVisionExportFile(export *VisionExportFile) error {
	path, err := VisionModelsExportPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vision models: %w", err)
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir vision models dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write vision models temp: %w", err)
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace vision models: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	return nil
}
