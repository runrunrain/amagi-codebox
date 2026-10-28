package claudeaccounts

import "fmt"

// ClaudeAccountRotationConfig 是自动轮换配置值对象（design §3.2）。
// 持久化接线（AppConfig.ClaudeAccountRotation / claude_account_rotation
// JSON 字段、深拷贝、导入导出排除）属于后续接线任务；本文件只提供结构、
// 默认值、区间校验与「零值 = 关闭」语义。
type ClaudeAccountRotationConfig struct {
	SchemaVersion           int     `json:"schema_version"`
	Enabled                 bool    `json:"enabled"`
	Strategy                string  `json:"strategy"`
	ThresholdPercent        int     `json:"threshold_percent"`
	CooldownSeconds         int     `json:"cooldown_seconds"`
	HysteresisPoints        int     `json:"hysteresis_points"`
	ManualRespectSeconds    int     `json:"manual_respect_seconds"`
	AcknowledgedRiskVersion *string `json:"acknowledged_risk_version,omitempty"`
}

// 轮换策略白名单（design §6.2：v1 只做 best；未知 strategy 拒绝，不默默回退）。
const (
	StrategyBest            = "best"
	rotationSchemaVersion   = 1
	rotationMinThreshold    = 80
	rotationMaxThreshold    = 99
	rotationMinHysteresis   = 5
	rotationMaxHysteresis   = 30
	rotationMinCooldown     = 300
	rotationMaxCooldown     = 3600
	rotationMinRespect      = 0
	rotationMaxRespect      = 86400
)

// DefaultRotationConfig 返回设计 §3.2 的默认值（threshold 90 / cooldown 300 /
// hysteresis 10 / respect 3600 / strategy best / enabled false）。
func DefaultRotationConfig() *ClaudeAccountRotationConfig {
	return &ClaudeAccountRotationConfig{
		SchemaVersion:        rotationSchemaVersion,
		Enabled:              false,
		Strategy:             StrategyBest,
		ThresholdPercent:     90,
		CooldownSeconds:      300,
		HysteresisPoints:     10,
		ManualRespectSeconds: 3600,
	}
}

// IsZero 报告配置是否为全零值（旧配置无字段视为关闭，design §3.2/§11）。
func (c *ClaudeAccountRotationConfig) IsZero() bool {
	return c == nil ||
		(c.SchemaVersion == 0 && !c.Enabled && c.Strategy == "" &&
			c.ThresholdPercent == 0 && c.CooldownSeconds == 0 &&
			c.HysteresisPoints == 0 && c.ManualRespectSeconds == 0 &&
			c.AcknowledgedRiskVersion == nil)
}

// EffectiveEnabled 返回生效的启用状态：nil 或零值配置一律视为关闭。
func (c *ClaudeAccountRotationConfig) EffectiveEnabled() bool {
	return c != nil && !c.IsZero() && c.Enabled
}

// Validate 严格校验区间与白名单（design §3.2：后端即使前端不显示也须验证；
// 未知 strategy 拒绝，不能默默回退）。零值配置视为「关闭」，直接通过。
func (c *ClaudeAccountRotationConfig) Validate() error {
	if c.IsZero() {
		return nil
	}
	if c.SchemaVersion != rotationSchemaVersion {
		return fmt.Errorf("claude_account_rotation: schema_version 必须为 %d，实际 %d", rotationSchemaVersion, c.SchemaVersion)
	}
	switch c.Strategy {
	case StrategyBest:
	case "":
		return fmt.Errorf("claude_account_rotation: strategy 不能为空（启用前必须显式选择，白名单：%s）", StrategyBest)
	default:
		return fmt.Errorf("claude_account_rotation: 未知 strategy %q（白名单：%s）", c.Strategy, StrategyBest)
	}
	if c.ThresholdPercent < rotationMinThreshold || c.ThresholdPercent > rotationMaxThreshold {
		return fmt.Errorf("claude_account_rotation: threshold_percent 必须在 %d..%d，实际 %d", rotationMinThreshold, rotationMaxThreshold, c.ThresholdPercent)
	}
	if c.HysteresisPoints < rotationMinHysteresis || c.HysteresisPoints > rotationMaxHysteresis {
		return fmt.Errorf("claude_account_rotation: hysteresis_points 必须在 %d..%d，实际 %d", rotationMinHysteresis, rotationMaxHysteresis, c.HysteresisPoints)
	}
	if c.CooldownSeconds < rotationMinCooldown || c.CooldownSeconds > rotationMaxCooldown {
		return fmt.Errorf("claude_account_rotation: cooldown_seconds 必须在 %d..%d，实际 %d", rotationMinCooldown, rotationMaxCooldown, c.CooldownSeconds)
	}
	if c.ManualRespectSeconds < rotationMinRespect || c.ManualRespectSeconds > rotationMaxRespect {
		return fmt.Errorf("claude_account_rotation: manual_respect_seconds 必须在 %d..%d，实际 %d", rotationMinRespect, rotationMaxRespect, c.ManualRespectSeconds)
	}
	return nil
}
