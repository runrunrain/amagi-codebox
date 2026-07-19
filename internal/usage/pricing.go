package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// PricingService 是价格表持久化服务（仿 internal/settings/service.go 骨架）。
//
// 持久化：~/.amagi-codebox/usage-pricing.json（原子写：.tmp + os.Rename）。
// 加载失败时（如文件不存在或解析失败）回退到内置 seed，保证服务可用。
type PricingService struct {
	configPath string
	data       *PricingData
	mu         sync.RWMutex
}

// NewPricingService 创建价格表服务（未加载，需显式 Load）。
func NewPricingService(configDir string) *PricingService {
	return &PricingService{
		configPath: filepath.Join(configDir, "usage-pricing.json"),
		data:       defaultPricingData(),
	}
}

// Load 从磁盘加载价格表；文件不存在或解析失败时回退到 seed（不返回错误）。
func (p *PricingService) Load() error {
	p.mu.Lock()

	b, err := os.ReadFile(p.configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// 首次启动：使用 seed 并尝试持久化（失败仅日志，不阻塞）。
			p.data = defaultPricingData()
			p.mu.Unlock()
			return nil
		}
		p.mu.Unlock()
		return fmt.Errorf("read pricing file: %w", err)
	}

	var cfg PricingData
	if err := json.Unmarshal(b, &cfg); err != nil {
		p.mu.Unlock()
		return fmt.Errorf("parse pricing file: %w", err)
	}
	if cfg.Models == nil {
		cfg.Models = []ModelPricing{}
	}
	if cfg.FallbackPolicy.DefaultCurrency == "" {
		cfg.FallbackPolicy.DefaultCurrency = "USD"
	}
	if cfg.FallbackPolicy.UnknownModelStrategy == "" {
		cfg.FallbackPolicy.UnknownModelStrategy = "zero_cost"
	}
	if cfg.FallbackPolicy.CNYToUSDFixedRate == 0 {
		cfg.FallbackPolicy.CNYToUSDFixedRate = 0.14
	}
	changed := mergeMissingBuiltinPricing(&cfg, defaultPricingData())
	p.data = &cfg
	p.mu.Unlock()
	if changed {
		return p.Save()
	}
	return nil
}

// mergeMissingBuiltinPricing lets existing user pricing files receive new
// built-in models without overwriting prices the user explicitly customized.
// It is intentionally keyed by model pattern, the same key used by Resolve.
func mergeMissingBuiltinPricing(cfg, builtin *PricingData) bool {
	if cfg == nil || builtin == nil {
		return false
	}
	seen := make(map[string]struct{}, len(cfg.Models))
	for _, model := range cfg.Models {
		if model.ModelPattern != "" {
			seen[model.ModelPattern] = struct{}{}
		}
	}
	changed := false
	for _, model := range builtin.Models {
		if !model.IsBuiltin || model.ModelPattern == "" {
			continue
		}
		if _, exists := seen[model.ModelPattern]; exists {
			continue
		}
		cfg.Models = append(cfg.Models, model)
		seen[model.ModelPattern] = struct{}{}
		changed = true
	}
	if cfg.Version < builtin.Version {
		cfg.Version = builtin.Version
		changed = true
	}
	return changed
}

// Save 持久化价格表（原子写：.tmp + Rename）。
func (p *PricingService) Save() error {
	p.mu.RLock()
	cfg := p.data
	path := p.configPath
	p.mu.RUnlock()

	if cfg == nil {
		return errors.New("pricing not loaded")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir pricing dir: %w", err)
	}

	// 深拷贝切片，避免序列化期间被并发修改。
	modelsCopy := make([]ModelPricing, len(cfg.Models))
	copy(modelsCopy, cfg.Models)
	toWrite := &PricingData{
		Version:        cfg.Version,
		Models:         modelsCopy,
		FallbackPolicy: cfg.FallbackPolicy,
	}

	b, err := json.MarshalIndent(toWrite, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pricing: %w", err)
	}
	b = append(b, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write temp pricing: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace pricing: %w", err)
	}
	return nil
}

// Resolve 按 NormalizedModel 匹配价格。
//
// 匹配优先级（设计 6.6）：
//  1. 精确匹配 NormalizedModel == ModelPattern
//  2. 前缀匹配 NormalizedModel 以 ModelPattern 开头（按 ModelPattern 长度降序）；
//     例：表里 "claude-sonnet-4" 能匹配 "claude-sonnet-4-20250514"
//  3. 失配 → 返回 (zero_cost占位, false)
func (p *PricingService) Resolve(normalizedModel string) (ModelPricing, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.data == nil || normalizedModel == "" {
		return unknownModelPricing(p.defaultCurrency()), false
	}

	// 1. 精确匹配
	for _, m := range p.data.Models {
		if m.ModelPattern == normalizedModel {
			return m, true
		}
	}

	// 2. 前缀匹配（最长 ModelPattern 优先）
	type cand struct {
		mp     ModelPricing
		length int
	}
	var cands []cand
	for _, m := range p.data.Models {
		if m.ModelPattern == "" {
			continue
		}
		if len(normalizedModel) > len(m.ModelPattern) &&
			normalizedModel[:len(m.ModelPattern)] == m.ModelPattern {
			cands = append(cands, cand{m, len(m.ModelPattern)})
		}
	}
	if len(cands) > 0 {
		sort.SliceStable(cands, func(i, j int) bool {
			return cands[i].length > cands[j].length
		})
		return cands[0].mp, true
	}

	// 3. 失配兜底（zero_cost）
	return unknownModelPricing(p.defaultCurrency()), false
}

// List 返回价格表全量副本（按 ModelPattern 排序，内置在前）。
func (p *PricingService) List() []ModelPricing {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]ModelPricing, len(p.data.Models))
	copy(out, p.data.Models)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsBuiltin != out[j].IsBuiltin {
			return out[i].IsBuiltin // 内置在前
		}
		return out[i].ModelPattern < out[j].ModelPattern
	})
	return out
}

// Upsert 新增或更新（按 ID）。内置模型允许改价但保留 IsBuiltin=true。
func (p *PricingService) Upsert(mp ModelPricing) error {
	if mp.ModelPattern == "" {
		return errors.New("modelPattern is required")
	}
	if mp.CurrencyCode != "USD" && mp.CurrencyCode != "CNY" {
		return errors.New("currencyCode must be USD or CNY")
	}
	p.mu.Lock()
	// 自动生成 ID（非内置模型且未提供）
	if mp.ID == "" {
		mp.ID = "user-" + mp.ModelPattern
	}
	mp.UpdatedAt = time.Now().UTC()
	updated := false
	for i, existing := range p.data.Models {
		if existing.ID == mp.ID || existing.ModelPattern == mp.ModelPattern {
			// 保留 IsBuiltin 标记（不允许把内置模型改为非内置，反之亦然）
			builtin := existing.IsBuiltin
			mp.ID = existing.ID
			mp.IsBuiltin = builtin
			p.data.Models[i] = mp
			updated = true
			break
		}
	}
	if !updated {
		p.data.Models = append(p.data.Models, mp)
	}
	// 显式释放锁后再 Save（Save 内取 RLock）
	p.mu.Unlock()
	return p.Save()
}

// Delete 删除自定义模型；内置模型返回错误。
func (p *PricingService) Delete(id string) error {
	p.mu.Lock()
	for i, existing := range p.data.Models {
		if existing.ID == id {
			if existing.IsBuiltin {
				p.mu.Unlock()
				return errors.New("cannot delete builtin pricing (use edit instead)")
			}
			p.data.Models = append(p.data.Models[:i], p.data.Models[i+1:]...)
			p.mu.Unlock()
			return p.Save()
		}
	}
	p.mu.Unlock()
	return fmt.Errorf("pricing id %q not found", id)
}

// ResetBuiltin 把价格表重置为内置 seed（用户自定义全清）。
func (p *PricingService) ResetBuiltin() error {
	p.mu.Lock()
	p.data = defaultPricingData()
	p.mu.Unlock()
	return p.Save()
}

// defaultCurrency 返回兜底币种（用于失配模型）。
func (p *PricingService) defaultCurrency() string {
	if p.data != nil && p.data.FallbackPolicy.DefaultCurrency != "" {
		return p.data.FallbackPolicy.DefaultCurrency
	}
	return "USD"
}

// CNYToUSDRate 返回 CNY→USD 折算汇率（仅用于前端主币种汇总展示）。
func (p *PricingService) CNYToUSDRate() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.data != nil && p.data.FallbackPolicy.CNYToUSDFixedRate > 0 {
		return p.data.FallbackPolicy.CNYToUSDFixedRate
	}
	return 0.14
}
