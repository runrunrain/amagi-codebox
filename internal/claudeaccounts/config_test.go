package claudeaccounts

import "testing"

// 配置值对象（design §3.2）：字段与区间校验、策略白名单、零值=关闭。

func TestDefaultRotationConfig(t *testing.T) {
	cfg := DefaultRotationConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("默认配置应合法: %v", err)
	}
	if cfg.SchemaVersion != 1 || cfg.Strategy != StrategyBest || cfg.ThresholdPercent != 90 ||
		cfg.CooldownSeconds != 300 || cfg.HysteresisPoints != 10 || cfg.ManualRespectSeconds != 3600 {
		t.Fatalf("默认值与设计 §3.2 不符: %+v", cfg)
	}
	if cfg.Enabled {
		t.Fatal("默认应为关闭")
	}
}

func TestRotationConfigZeroMeansDisabled(t *testing.T) {
	var nilCfg *ClaudeAccountRotationConfig
	if !nilCfg.IsZero() || nilCfg.EffectiveEnabled() {
		t.Fatal("nil 配置应为零值/关闭")
	}
	var zero ClaudeAccountRotationConfig
	if !zero.IsZero() || zero.EffectiveEnabled() {
		t.Fatal("全零配置应为零值/关闭")
	}
	if err := zero.Validate(); err != nil {
		t.Fatalf("零值配置应直接通过（视为关闭）: %v", err)
	}
	enabled := DefaultRotationConfig()
	enabled.Enabled = true
	if !enabled.EffectiveEnabled() {
		t.Fatal("enabled 配置应生效")
	}
}

func TestRotationConfigRanges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ClaudeAccountRotationConfig)
	}{
		{"threshold 过低", func(c *ClaudeAccountRotationConfig) { c.ThresholdPercent = 79 }},
		{"threshold 过高", func(c *ClaudeAccountRotationConfig) { c.ThresholdPercent = 100 }},
		{"hysteresis 过低", func(c *ClaudeAccountRotationConfig) { c.HysteresisPoints = 4 }},
		{"hysteresis 过高", func(c *ClaudeAccountRotationConfig) { c.HysteresisPoints = 31 }},
		{"cooldown 过低", func(c *ClaudeAccountRotationConfig) { c.CooldownSeconds = 299 }},
		{"cooldown 过高", func(c *ClaudeAccountRotationConfig) { c.CooldownSeconds = 3601 }},
		{"respect 负值", func(c *ClaudeAccountRotationConfig) { c.ManualRespectSeconds = -1 }},
		{"respect 过高", func(c *ClaudeAccountRotationConfig) { c.ManualRespectSeconds = 86401 }},
		{"未知 strategy", func(c *ClaudeAccountRotationConfig) { c.Strategy = "consume-first" }},
		{"空 strategy", func(c *ClaudeAccountRotationConfig) { c.Strategy = "" }},
		{"未知 schema", func(c *ClaudeAccountRotationConfig) { c.SchemaVersion = 2 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultRotationConfig()
			tc.mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("期望校验失败")
			}
		})
	}
	// 边界值本身合法。
	for _, cfg := range []*ClaudeAccountRotationConfig{
		{SchemaVersion: 1, Strategy: StrategyBest, ThresholdPercent: 80, HysteresisPoints: 5, CooldownSeconds: 300, ManualRespectSeconds: 0},
		{SchemaVersion: 1, Strategy: StrategyBest, ThresholdPercent: 99, HysteresisPoints: 30, CooldownSeconds: 3600, ManualRespectSeconds: 86400},
	} {
		if err := cfg.Validate(); err != nil {
			t.Fatalf("边界配置应合法: %v", err)
		}
	}
}

func TestMaskEmail(t *testing.T) {
	cases := []struct{ in, want string }{
		{"user@example.com", "us***@example.com"},
		{"ab@example.com", "a***@example.com"},
		{"x@example.com", "x***@example.com"},
		{"not-an-email", "***"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := MaskEmail(tc.in); got != tc.want {
			t.Errorf("MaskEmail(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
