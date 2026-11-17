package main

// app_sharedmarket_test.go — 共用插件市场 App 门面的纯逻辑测试
// （claudeMarketplaceCloneSource 源转换 + buildClaudeImportViews 导入视图组装）。
// 涉及 git clone 的导入/更新/部署路径由 internal/sharedmarket 的服务级测试
// 覆盖（runGit 注入），此处不重复联网面。

import (
	"testing"

	"amagi-codebox/internal/plugin"
	"amagi-codebox/internal/sharedmarket"
)

func TestClaudeMarketplaceCloneSourcePriority(t *testing.T) {
	cases := []struct {
		name             string
		repo, url, local string
		want             string
		wantErr          bool
	}{
		{"url 优先", "owner/repo", "https://git.example.com/m.git", "C:\\m", "https://git.example.com/m.git", false},
		{"github repo 合成", "anthropics/claude-plugins-official", "", "", "https://github.com/anthropics/claude-plugins-official.git", false},
		{"本地 installLocation 兜底", "", "", `C:\Users\x\.claude\plugins\marketplaces\mk`, `C:\Users\x\.claude\plugins\marketplaces\mk`, false},
		{"全空报错", "  ", "", "", "", true},
	}
	for _, tc := range cases {
		got, err := claudeMarketplaceCloneSource(tc.repo, tc.url, tc.local)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%s: expected error, got %q", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBuildClaudeImportViews(t *testing.T) {
	marketplaces := []plugin.Marketplace{
		{Name: "vexel-plugins-marketplace", Source: "git", URL: "https://github.com/runrunrain/vexel-plugins-marketplace.git"},
		{Name: "claude-plugins-official", Source: "github", Repo: "anthropics/claude-plugins-official"},
		{Name: "local-mk", Source: "", InstallLocation: `D:\markets\local-mk`},
		{Name: "", Source: "git", URL: "https://example.com/anonymous.git"}, // 无名条目剔除
	}
	shared := []sharedmarket.Marketplace{
		{Name: "vexel-plugins-marketplace"},
	}

	views := buildClaudeImportViews(marketplaces, shared)
	if len(views) != 3 {
		t.Fatalf("expected 3 views (empty-name dropped), got %d: %+v", len(views), views)
	}
	byName := map[string]ClaudeMarketplaceForImport{}
	for _, view := range views {
		byName[view.Name] = view
	}

	if view := byName["vexel-plugins-marketplace"]; !view.Imported || view.Source != "https://github.com/runrunrain/vexel-plugins-marketplace.git" {
		t.Fatalf("vexel view wrong: %+v", view)
	}
	if view := byName["claude-plugins-official"]; view.Imported || view.Source != "https://github.com/anthropics/claude-plugins-official.git" {
		t.Fatalf("official view wrong: %+v", view)
	}
	if view := byName["local-mk"]; view.Source != `D:\markets\local-mk` {
		t.Fatalf("local-mk view wrong: %+v", view)
	}
}
