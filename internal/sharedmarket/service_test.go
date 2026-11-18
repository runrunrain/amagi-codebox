package sharedmarket

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitStub：记录调用并按用例回调（默认成功 no-op）。
type gitStub struct {
	calls   [][]string
	respond func(args []string) (string, error)
}

func (g *gitStub) run(ctx context.Context, args ...string) (string, error) {
	g.calls = append(g.calls, args)
	if g.respond != nil {
		return g.respond(args)
	}
	return "", nil
}

// fixtureMarket 在 dst 预置一个「已 clone」的市场目录（模拟 git clone 产物）。
func fixtureMarket(t *testing.T, dst string) {
	t.Helper()
	write := func(rel, content string) {
		p := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir fixture: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", p, err)
		}
	}
	write(".claude-plugin/marketplace.json", `{
		"name": "fixture-market",
		"description": "测试市场",
		"plugins": [
			{"name": "alpha", "source": "./plugins/alpha", "description": "阿尔法", "version": "1.0.0", "author": {"name": "Tester"}},
			{"name": "external-one", "description": "外部引用", "source": {"source": "git-subdir", "url": "https://example.com/x.git"}},
			{"name": "beta-solo", "source": "./plugins/beta-solo", "description": "单技能插件"}
		]
	}`)
	write("plugins/alpha/.claude-plugin/plugin.json", `{"name":"alpha"}`)
	write("plugins/alpha/skills/skill-one/SKILL.md", "---\nname: skill-one\ndescription: 第一个技能\n---\n# one\n")
	write("plugins/alpha/skills/skill-two/SKILL.md", "---\nname: skill-two\ndescription: 第二个技能\n---\n# two\n")
	write("plugins/alpha/skills/not-a-skill/readme.txt", "no SKILL.md")
	write("plugins/beta-solo/SKILL.md", "---\nname: beta-solo\ndescription: 根级技能\n---\n# beta\n")
}

func newTestService(t *testing.T) (*Service, *gitStub) {
	t.Helper()
	svc := New(t.TempDir(), nil)
	stub := &gitStub{}
	svc.runGit = stub.run
	return svc, stub
}

func TestAddClonesValidatesAndPersists(t *testing.T) {
	svc, stub := newTestService(t)
	// clone 替身：把 fixture 复制到目标目录（args 末位即 dst）
	stub.respond = func(args []string) (string, error) {
		dst := args[len(args)-1]
		fixtureMarket(t, dst)
		return "cloned", nil
	}

	market, err := svc.Add("https://github.com/runrunrain/vexel-plugins-marketplace.git", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if market.Name != "vexel-plugins-marketplace" {
		t.Fatalf("name should derive from URL basename, got %q", market.Name)
	}
	if market.PluginCount != 3 || market.Description != "测试市场" {
		t.Fatalf("catalog not scanned: %+v", market)
	}

	// 状态持久化：重新 List 可见
	list, err := svc.List()
	if err != nil || len(list) != 1 || list[0].Name != "vexel-plugins-marketplace" {
		t.Fatalf("List after Add: %v %+v", err, list)
	}

	// 插件条目：本地 source 枚举技能、外部 source 标记
	byName := map[string]Plugin{}
	for _, p := range list[0].Plugins {
		byName[p.Name] = p
	}
	if got := byName["alpha"]; len(got.Skills) != 2 || got.Author != "Tester" || got.Description != "阿尔法" {
		t.Fatalf("alpha entry wrong: %+v", got)
	}
	if got := byName["external-one"]; !got.ExternalSource || len(got.Skills) != 0 {
		t.Fatalf("external entry should be marked ExternalSource: %+v", got)
	}
	if got := byName["beta-solo"]; len(got.Skills) != 1 || got.Skills[0] != "beta-solo" {
		t.Fatalf("root-SKILL.md plugin should deploy as one skill: %+v", got)
	}
}

func TestAddRejectsInvalidMarketAndRollsBack(t *testing.T) {
	svc, stub := newTestService(t)
	// clone 替身：产物缺 marketplace.json
	stub.respond = func(args []string) (string, error) {
		dst := args[len(args)-1]
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return "", err
		}
		return "cloned", nil
	}

	if _, err := svc.Add("https://example.com/not-a-market.git", ""); err == nil {
		t.Fatal("Add should reject repos without .claude-plugin/marketplace.json")
	}
	// 回滚：clone 子目录与状态都不留痕（marketplaces 根目录为空壳保留，无副作用）
	if _, err := os.Lstat(svc.clonePath("not-a-market")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("clone dir should be cleaned up, stat err=%v", err)
	}
	list, err := svc.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("state should stay empty: %v %+v", err, list)
	}

	// git 失败同样回滚
	stub.respond = func(args []string) (string, error) {
		return "fatal: repository not found", errors.New("exit status 128")
	}
	if _, err := svc.Add("https://example.com/ghost.git", ""); err == nil || !strings.Contains(err.Error(), "clone") {
		t.Fatalf("git failure should surface: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(svc.Root(), "ghost")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed clone should leave no dir, stat err=%v", err)
	}
}

func TestAddDuplicateNameRefused(t *testing.T) {
	svc, stub := newTestService(t)
	stub.respond = func(args []string) (string, error) {
		fixtureMarket(t, args[len(args)-1])
		return "", nil
	}
	if _, err := svc.Add("https://example.com/market.git", "dup"); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if _, err := svc.Add("https://example.com/other.git", "dup"); err == nil {
		t.Fatal("duplicate name must be refused")
	}
}

func TestUpdatePullsAndRecoversMissingClone(t *testing.T) {
	svc, stub := newTestService(t)
	stub.respond = func(args []string) (string, error) {
		if args[0] == "clone" {
			fixtureMarket(t, args[len(args)-1])
		}
		return "", nil
	}
	if _, err := svc.Add("https://example.com/market.git", "mk"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// 常规更新走 pull --ff-only
	if _, err := svc.Update("mk"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	pulled := false
	for _, call := range stub.calls {
		if len(call) >= 4 && call[0] == "-C" && call[2] == "pull" {
			pulled = true
		}
	}
	if !pulled {
		t.Fatalf("expected git -C <dir> pull --ff-only, calls=%v", stub.calls)
	}

	// clone 目录丢失 → Missing 标记 + Update 重新 clone 恢复
	if err := os.RemoveAll(svc.clonePath("mk")); err != nil {
		t.Fatalf("remove clone: %v", err)
	}
	list, err := svc.List()
	if err != nil || len(list) != 1 || !list[0].Missing {
		t.Fatalf("missing clone should be flagged: %v %+v", err, list)
	}
	if _, err := svc.Update("mk"); err != nil {
		t.Fatalf("Update should re-clone missing dir: %v", err)
	}
	list, err = svc.List()
	if err != nil || len(list) != 1 || list[0].Missing || list[0].PluginCount != 3 {
		t.Fatalf("re-clone should restore market: %v %+v", err, list)
	}
}

func TestRemoveDeletesCloneAndState(t *testing.T) {
	svc, stub := newTestService(t)
	stub.respond = func(args []string) (string, error) {
		if args[0] == "clone" {
			fixtureMarket(t, args[len(args)-1])
		}
		return "", nil
	}
	if _, err := svc.Add("https://example.com/market.git", "mk"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := svc.Remove("mk"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Lstat(svc.clonePath("mk")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("clone dir should be gone, stat err=%v", err)
	}
	list, err := svc.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("state should be empty after remove: %v %+v", err, list)
	}
	if err := svc.Remove("mk"); err == nil {
		t.Fatal("removing non-existent market should fail")
	}
}

func TestDeployPluginCopiesSkillsWithCollisionSkip(t *testing.T) {
	svc, stub := newTestService(t)
	stub.respond = func(args []string) (string, error) {
		if args[0] == "clone" {
			fixtureMarket(t, args[len(args)-1])
		}
		return "", nil
	}
	if _, err := svc.Add("https://example.com/market.git", "mk"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	report, err := svc.DeployPlugin("mk", "alpha")
	if err != nil {
		t.Fatalf("DeployPlugin: %v", err)
	}
	if len(report.Deployed) != 2 || len(report.Skipped) != 0 {
		t.Fatalf("expected 2 deployed: %+v", report)
	}
	for _, skill := range []string{"skill-one", "skill-two"} {
		if _, err := os.Stat(filepath.Join(svc.skillsRoot(), skill, "SKILL.md")); err != nil {
			t.Fatalf("deployed skill missing %s: %v", skill, err)
		}
	}

	// 预置冲突目录后再部署：跳过不覆盖
	if err := os.MkdirAll(filepath.Join(svc.skillsRoot(), "skill-two"), 0o755); err != nil {
		t.Fatal(err)
	}
	report, err = svc.DeployPlugin("mk", "alpha")
	if err != nil {
		t.Fatalf("re-DeployPlugin: %v", err)
	}
	if len(report.Skipped) != 2 || len(report.Deployed) != 0 {
		t.Fatalf("existing targets should be skipped: %+v", report)
	}

	// 根级 SKILL.md 插件：整目录部署为同名技能
	report, err = svc.DeployPlugin("mk", "beta-solo")
	if err != nil {
		t.Fatalf("DeployPlugin beta-solo: %v", err)
	}
	if len(report.Deployed) != 1 || report.Deployed[0] != "beta-solo" {
		t.Fatalf("root-skill plugin deploy wrong: %+v", report)
	}
}

func TestDeployPluginRejectsExternalAndUnknown(t *testing.T) {
	svc, stub := newTestService(t)
	stub.respond = func(args []string) (string, error) {
		if args[0] == "clone" {
			fixtureMarket(t, args[len(args)-1])
		}
		return "", nil
	}
	if _, err := svc.Add("https://example.com/market.git", "mk"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if _, err := svc.DeployPlugin("mk", "external-one"); err == nil || !strings.Contains(err.Error(), "外部") {
		t.Fatalf("external-source plugin should be rejected honestly: %v", err)
	}
	if _, err := svc.DeployPlugin("mk", "no-such-plugin"); err == nil {
		t.Fatal("unknown plugin should be rejected")
	}
	if _, err := svc.DeployPlugin("ghost-market", "alpha"); err == nil {
		t.Fatal("unknown market should be rejected")
	}
}

func TestNameFromSource(t *testing.T) {
	cases := map[string]string{
		"https://github.com/runrunrain/vexel-plugins-marketplace.git": "vexel-plugins-marketplace",
		"https://github.com/owner/repo/":                              "repo",
		"/local/path/to/market":                                       "market",
		`C:\Users\x\some-market`:                                      "some-market",
		"https://example.com/":                                        "example.com", // 裸域名无路径：派生名为域名，后续 clone 失败会如实报错
	}
	for source, want := range cases {
		if got := nameFromSource(source); got != want {
			t.Fatalf("nameFromSource(%q) = %q, want %q", source, got, want)
		}
	}
	if validName("../escape") || validName(".hidden") || validName("a/b") {
		t.Fatal("validName should reject traversal/hidden/multi-segment names")
	}
}

// fixtureTraversingMarket 预置含恶意插件名/祖先链接的市场（F01/F08 回归）：
// 根级 SKILL.md 插件名为 ../../evil（穿越名）；normal 插件含合法技能 good-one；
// 支持符号链接的平台追加 escape/link → 仓库外目录的 via-link 条目。
func fixtureTraversingMarket(t *testing.T, dst string) {
	t.Helper()
	write := func(rel, content string) {
		p := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir fixture: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", p, err)
		}
	}
	catalog := `{"name":"evil-market","plugins":[
		{"name":"../../evil","source":"./payload"},
		{"name":"normal","source":"./normal"}`
	// 祖先符号链接穿越（F08）：链接目标在仓库外；无权限建链接的平台跳过该条目
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "skills", "outsider", "."), 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "skills", "outsider", "SKILL.md"), []byte("---\nname: outsider\n---\n# x\n"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	escapeDir := filepath.Join(dst, "escape")
	if err := os.MkdirAll(escapeDir, 0o755); err != nil {
		t.Fatalf("mkdir escape: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(escapeDir, "link")); err == nil {
		catalog += `,
		{"name":"via-link","source":"./escape/link"}`
	}
	catalog += `]}`
	write(".claude-plugin/marketplace.json", catalog)
	write("payload/SKILL.md", "---\nname: evil\ndescription: 穿越名插件\n---\n# evil\n")
	write("normal/skills/good-one/SKILL.md", "---\nname: good-one\ndescription: 合法技能\n---\n# good\n")
}

func TestTraversingPluginNameIsNotDeployable(t *testing.T) {
	svc, stub := newTestService(t)
	stub.respond = func(args []string) (string, error) {
		if args[0] == "clone" {
			fixtureTraversingMarket(t, args[len(args)-1])
		}
		return "", nil
	}
	if _, err := svc.Add("https://example.com/evil-market.git", "evil-market"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	list, err := svc.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v %+v", err, list)
	}
	for _, plugin := range list[0].Plugins {
		switch plugin.Name {
		case "../../evil":
			// 穿越名插件：名称段校验失败 → 不可枚举任何可部署技能
			if len(plugin.Skills) != 0 {
				t.Fatalf("traversing plugin name must not enumerate deployable skills: %+v", plugin)
			}
		case "via-link":
			// 祖先链接插件：真实路径已越出 clone → 不可部署
			if len(plugin.Skills) != 0 {
				t.Fatalf("symlink-escaped plugin must not be deployable: %+v", plugin)
			}
		case "normal":
			if len(plugin.Skills) != 1 || plugin.Skills[0] != "good-one" {
				t.Fatalf("normal plugin should enumerate its skill: %+v", plugin)
			}
		}
	}

	// 部署穿越名插件 → 明确拒绝（skillDirs 名称段校验 + 出口 containment）
	if _, err := svc.DeployPlugin("evil-market", "../../evil"); err == nil {
		t.Fatal("deploying traversing plugin name must fail")
	}
	// 部署祖先链接插件 → 拒绝
	if _, err := svc.DeployPlugin("evil-market", "via-link"); err == nil {
		t.Fatal("deploying symlink-escaped plugin must fail")
	}
	// 越界目标不曾被创建；仓库外技能未被复制进来
	if _, err := os.Lstat(filepath.Join(svc.userHome, "evil")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("traversal must not create directories outside skills root, stat err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(svc.skillsRoot(), "outsider")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("outside skill must not be deployed, stat err=%v", err)
	}
}

func TestDeployReportArraysNeverSerializeAsNull(t *testing.T) {
	svc, stub := newTestService(t)
	stub.respond = func(args []string) (string, error) {
		if args[0] == "clone" {
			fixtureMarket(t, args[len(args)-1])
		}
		return "", nil
	}
	if _, err := svc.Add("https://example.com/market.git", "mk"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// 全部成功（skipped 空）与全部跳过（deployed 空）都必须序列化为 []，
	// 否则前端 .length 消费 null 会 TypeError（F02）
	report, err := svc.DeployPlugin("mk", "alpha")
	if err != nil {
		t.Fatalf("DeployPlugin: %v", err)
	}
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"deployed":[`) || !strings.Contains(string(b), `"skipped":[`) {
		t.Fatalf("deploy report arrays must serialize as [] not null: %s", b)
	}

	// 重复部署：全跳过分支同样不得为 null
	report, err = svc.DeployPlugin("mk", "alpha")
	if err != nil {
		t.Fatalf("re-DeployPlugin: %v", err)
	}
	b, _ = json.Marshal(report)
	if !strings.Contains(string(b), `"deployed":[`) || !strings.Contains(string(b), `"skipped":[`) {
		t.Fatalf("skipped-only report arrays must serialize as [] not null: %s", b)
	}
	if len(report.Skipped) != 2 || len(report.Deployed) != 0 {
		t.Fatalf("re-deploy should skip all: %+v", report)
	}
}

func TestDeployLeavesNoStagingResidue(t *testing.T) {
	svc, stub := newTestService(t)
	stub.respond = func(args []string) (string, error) {
		if args[0] == "clone" {
			fixtureMarket(t, args[len(args)-1])
		}
		return "", nil
	}
	if _, err := svc.Add("https://example.com/market.git", "mk"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if _, err := svc.DeployPlugin("mk", "alpha"); err != nil {
		t.Fatalf("DeployPlugin: %v", err)
	}
	// F03：staging 部署不留暂存残留（成功后清理；隐藏目录不进入共用技能扫描）
	entries, err := os.ReadDir(svc.skillsRoot())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".deploy-staging") {
			t.Fatalf("staging root must be cleaned after deploy: %s", entry.Name())
		}
	}
	for _, skill := range []string{"skill-one", "skill-two"} {
		if _, err := os.Stat(filepath.Join(svc.skillsRoot(), skill, "SKILL.md")); err != nil {
			t.Fatalf("deployed skill missing %s: %v", skill, err)
		}
	}
}

func TestAddRollsBackCloneWhenStateWriteFails(t *testing.T) {
	svc, stub := newTestService(t)
	stub.respond = func(args []string) (string, error) {
		if args[0] == "clone" {
			fixtureMarket(t, args[len(args)-1])
		}
		return "", nil
	}
	// F04：预置 marketplaces.json.tmp 为目录 → 状态落盘失败 → Add 应回滚 clone，
	// 否则市场不可见且重加被「目标目录已存在」卡死
	agentsDir := filepath.Join(svc.userHome, ".agents")
	if err := os.MkdirAll(filepath.Join(agentsDir, "marketplaces.json.tmp"), 0o755); err != nil {
		t.Fatalf("preset tmp dir: %v", err)
	}

	if _, err := svc.Add("https://example.com/market.git", "mk"); err == nil {
		t.Fatal("Add must fail when state write fails")
	}
	if _, err := os.Lstat(svc.clonePath("mk")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("clone must be rolled back on state write failure, stat err=%v", err)
	}
	list, err := svc.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("state must stay empty after failed add: %v %+v", err, list)
	}
}
