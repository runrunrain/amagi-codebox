package sharedext

// aggregate.go — 聚合视图：registry 手动条目 + 调用方提供的插件来源
// providers 动态合并。Service 内缓存最终聚合结果 30s TTL；force 旁路；
// registry 写操作使缓存失效。排序：origin=manual 优先，name 字典序。

import (
	"sort"
	"time"
)

// aggCacheTTL 是聚合缓存的有效期。
const aggCacheTTL = 30 * time.Second

// GetAggregatedSkills 返回聚合 skill 视图。
// providers 由调用方（P2 app.go）组装（各插件引擎的 skill 清单与子项禁用
// 状态）；force=true 旁路缓存强制重建。
func (s *Service) GetAggregatedSkills(providers []PluginSkillsProvider, force bool) ([]AggregatedSkill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !force && s.aggCache != nil && time.Since(s.aggCachedAt) < aggCacheTTL {
		return append([]AggregatedSkill(nil), s.aggCache...), nil
	}

	reg, err := s.loadRegistryLocked()
	if err != nil {
		return nil, err
	}

	out := make([]AggregatedSkill, 0, len(reg.Skills))
	for _, skill := range reg.Skills {
		out = append(out, AggregatedSkill{
			Name:        skill.Name,
			Description: skill.Description,
			Origin:      "manual",
			Managed:     true, // manual：可删可启停
			Enabled:     skill.Enabled,
		})
	}
	for _, provider := range providers {
		for _, ps := range provider.Skills {
			name := ps.Name
			if name == "" {
				continue
			}
			// plugin 来源：只读（删除归插件处），Enabled = 子项未被禁用
			//（无记录默认 true）。同名 manual 条目已存在时跳过（manual 优先）。
			if _, clash := reg.Skills[name]; clash {
				continue
			}
			enabled := !provider.SubItemsDisabled["skill/"+name]
			out = append(out, AggregatedSkill{
				Name:         name,
				Description:  ps.Description,
				Origin:       "plugin",
				PluginEngine: provider.Engine,
				PluginID:     provider.PluginID,
				PluginName:   provider.PluginName,
				Managed:      false,
				Enabled:      enabled,
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		oi, oj := originRank(out[i].Origin), originRank(out[j].Origin)
		if oi != oj {
			return oi < oj
		}
		return out[i].Name < out[j].Name
	})

	s.aggCache = out
	s.aggCachedAt = time.Now()
	return append([]AggregatedSkill(nil), out...), nil
}

// originRank：manual 优先于 plugin。
func originRank(origin string) int {
	if origin == "manual" {
		return 0
	}
	return 1
}

// invalidateAggCacheLocked 使聚合缓存失效（registry 写操作后调用；调用方持锁）。
func (s *Service) invalidateAggCacheLocked() {
	s.aggCache = nil
	s.aggCachedAt = time.Time{}
}
