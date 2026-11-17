package sharedext

// aggregate_test.go — 覆盖清单 11：manual+plugin 合并、来源标记、禁用
// subitem 反映、TTL 缓存与 force、写操作失效缓存、排序。P5 后：providers
// 以惰性构建器传入（m2），缓存命中时构建器不执行。

import (
	"testing"
	"time"
)

// providersOf 把静态 providers 包装为惰性构建器（供 GetAggregatedSkills
// 的 m2 签名使用；调用计数场景用 countableProviders）。
func providersOf(list ...PluginSkillsProvider) func() []PluginSkillsProvider {
	return func() []PluginSkillsProvider { return list }
}

// countableProviders 同 providersOf，额外返回构建器调用次数指针（m2：
// 验证缓存命中不组装 providers）。
func countableProviders(list ...PluginSkillsProvider) (func() []PluginSkillsProvider, *int) {
	calls := 0
	return func() []PluginSkillsProvider {
		calls++
		return list
	}, &calls
}

// 覆盖 11a：manual + plugin 合并、来源标记、Managed/Enabled 语义。
func TestAggregatedSkillsMergeAndMarkers(t *testing.T) {
	env := newTestService(t)
	name := importOne(t, env, "manual-one")
	if err := env.svc.SetSharedSkillEnabled(name, true); err != nil {
		t.Fatal(err)
	}

	providers := []PluginSkillsProvider{
		{
			Engine:    EngineClaude,
			PluginID:  "demo@market",
			PluginName: "Demo Plugin",
			Skills: []PluginSkill{
				{Name: "plugin-skill-a", Description: "插件技能 A"},
				{Name: "plugin-skill-b", Description: "插件技能 B"},
			},
			SubItemsDisabled: map[string]bool{"skill/plugin-skill-b": true},
		},
	}

	agg, err := env.svc.GetAggregatedSkills(providersOf(providers...), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(agg) != 3 {
		t.Fatalf("期望 3 条聚合: %+v", agg)
	}

	// 排序：manual 优先，随后 name 字典序。
	if agg[0].Name != "manual-one" || agg[0].Origin != "manual" {
		t.Fatalf("manual 条目应排首: %+v", agg[0])
	}
	if agg[0].Managed != true || agg[0].Enabled != true {
		t.Fatalf("manual 条目 Managed=true 且启用: %+v", agg[0])
	}
	if agg[1].Name != "plugin-skill-a" || agg[1].Origin != "plugin" {
		t.Fatalf("plugin 条目一: %+v", agg[1])
	}
	if agg[1].PluginEngine != EngineClaude || agg[1].PluginID != "demo@market" || agg[1].PluginName != "Demo Plugin" {
		t.Fatalf("plugin 元数据: %+v", agg[1])
	}
	if agg[1].Managed != false || agg[1].Enabled != true {
		t.Fatalf("plugin 条目默认启用只读: %+v", agg[1])
	}
	// 禁用 subitem → Enabled=false。
	if agg[2].Name != "plugin-skill-b" || agg[2].Enabled != false {
		t.Fatalf("禁用 subitem 应反映: %+v", agg[2])
	}
}

// 覆盖 11b：同名时 manual 优先、plugin 条目跳过。
func TestAggregatedSkillsManualPriority(t *testing.T) {
	env := newTestService(t)
	importOne(t, env, "clash")
	providers := []PluginSkillsProvider{
		{Engine: EnginePi, PluginID: "p1", Skills: []PluginSkill{{Name: "clash", Description: "插件版"}}},
	}
	agg, err := env.svc.GetAggregatedSkills(providersOf(providers...), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(agg) != 1 || agg[0].Origin != "manual" {
		t.Fatalf("同名应只保留 manual: %+v", agg)
	}
}

// 覆盖 11c：TTL 缓存、force 旁路、registry 写操作失效缓存。
func TestAggregatedSkillsCache(t *testing.T) {
	env := newTestService(t)

	// 空聚合建立缓存。
	buildCalls := 0
	emptyBuild := func() []PluginSkillsProvider {
		buildCalls++
		return nil
	}
	agg, err := env.svc.GetAggregatedSkills(emptyBuild, false)
	if err != nil || len(agg) != 0 {
		t.Fatal(err)
	}
	if env.svc.aggCache == nil {
		t.Fatal("应已建立缓存")
	}
	if buildCalls != 1 {
		t.Fatalf("未命中重建应调用构建器，实际 %d 次", buildCalls)
	}

	// 非 force：providers 变化不触发重建（30s TTL 内读缓存），且构建器
	// 不被调用（m2：缓存命中不组装 providers）。
	providers := []PluginSkillsProvider{
		{Engine: EngineClaude, PluginID: "p", Skills: []PluginSkill{{Name: "cached-out"}}},
	}
	build, calls := countableProviders(providers...)
	agg, err = env.svc.GetAggregatedSkills(build, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(agg) != 0 {
		t.Fatalf("TTL 内应返回缓存结果: %+v", agg)
	}
	if *calls != 0 {
		t.Fatalf("m2：缓存命中不应组装 providers，构建器被调 %d 次", *calls)
	}

	// force：旁路缓存（构建器执行）。
	build, calls = countableProviders(providers...)
	agg, err = env.svc.GetAggregatedSkills(build, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(agg) != 1 || agg[0].Name != "cached-out" {
		t.Fatalf("force 应重建: %+v", agg)
	}
	if *calls != 1 {
		t.Fatalf("force 重建应调用构建器，实际 %d 次", *calls)
	}

	// TTL 过期：手动老化缓存后非 force 重建。
	env.svc.mu.Lock()
	env.svc.aggCachedAt = time.Now().Add(-aggCacheTTL - time.Second)
	env.svc.mu.Unlock()
	agg, err = env.svc.GetAggregatedSkills(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(agg) != 0 {
		t.Fatalf("过期后应按新 providers（空）重建: %+v", agg)
	}

	// registry 写操作（导入 skill）使缓存失效。
	importOne(t, env, "fresh-skill")
	agg, err = env.svc.GetAggregatedSkills(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range agg {
		if item.Name == "fresh-skill" {
			found = true
		}
	}
	if !found {
		t.Fatalf("缓存失效后应看到新导入 skill: %+v", agg)
	}

	// 删除同样失效缓存（覆盖 DeleteSharedSkill 路径）。
	if err := env.svc.DeleteSharedSkill("fresh-skill"); err != nil {
		t.Fatal(err)
	}
	agg, err = env.svc.GetAggregatedSkills(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range agg {
		if item.Name == "fresh-skill" {
			t.Fatalf("删除后不应再出现: %+v", agg)
		}
	}
}
