package sharedmarket

// provenance_test.go — 来源凭据（provenance 标记）与显式卸载的回归测试：
//   - 部署只在成功发布的目录内写入受限隐藏标记（旧版无凭据目录不认领）；
//   - 标记读取严格校验（普通文件/限额/不走链接/版本/身份/名称），篡改即无效；
//   - 凭据跟随技能目录移动（skills ↔ skills-disabled）；
//   - List 的 DeployedSkills 与卸载只认凭据证据，禁止市场目录/技能名碰撞推断；
//   - 卸载保留同名手动条目与无凭据目录，不触碰市场 clone。
// 全部走 git 替身 + t.TempDir() fixture，不触真实用户配置、不联网。

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// addFixtureMarket 经 git 替身 Add 一个标准 fixture 市场（name 同时是市场名）。
func addFixtureMarket(t *testing.T, svc *Service, stub *gitStub, name string) {
	t.Helper()
	stub.respond = func(args []string) (string, error) {
		if args[0] == "clone" {
			fixtureMarket(t, args[len(args)-1])
		}
		return "", nil
	}
	if _, err := svc.Add("https://example.com/"+name+".git", name); err != nil {
		t.Fatalf("Add %s: %v", name, err)
	}
}

// markerPath 返回技能目录内凭据文件路径。
func markerPath(svc *Service, skill string) string {
	return filepath.Join(svc.skillsRoot(), skill, markerFileName)
}

func TestDeployWritesProvenanceMarker(t *testing.T) {
	svc, stub := newTestService(t)
	addFixtureMarket(t, svc, stub, "mk")

	if _, err := svc.DeployPlugin("mk", "alpha"); err != nil {
		t.Fatalf("DeployPlugin: %v", err)
	}
	for _, skill := range []string{"skill-one", "skill-two"} {
		p := markerPath(svc, skill)
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatalf("deployed skill %s should carry provenance marker: %v", skill, err)
		}
		if !fi.Mode().IsRegular() {
			t.Fatalf("marker must be a regular file: %s", p)
		}
		if fi.Mode().Perm()&0o222 != 0 {
			t.Fatalf("marker should be read-only (受限): %s perm=%v", p, fi.Mode().Perm())
		}
		origin, err := svc.SharedSkillOrigin(skill)
		if err != nil || origin == nil {
			t.Fatalf("SharedSkillOrigin(%s): %v %v", skill, origin, err)
		}
		if origin.Marketplace != "mk" || origin.Plugin != "alpha" || origin.Skill != skill || !origin.Enabled {
			t.Fatalf("origin wrong: %+v", origin)
		}
		if _, perr := time.Parse(time.RFC3339, origin.DeployedAt); perr != nil {
			t.Fatalf("deployedAt not RFC3339: %q (%v)", origin.DeployedAt, perr)
		}
	}

	// 根级 SKILL.md 插件：整目录部署同样带凭据
	if _, err := svc.DeployPlugin("mk", "beta-solo"); err != nil {
		t.Fatalf("DeployPlugin beta-solo: %v", err)
	}
	origin, err := svc.SharedSkillOrigin("beta-solo")
	if err != nil || origin == nil || origin.Plugin != "beta-solo" {
		t.Fatalf("beta-solo origin wrong: %v %v", origin, err)
	}

	// 手动条目（无凭据）：不认领 → nil, nil
	if err := os.MkdirAll(filepath.Join(svc.skillsRoot(), "manual-one"), 0o755); err != nil {
		t.Fatal(err)
	}
	origin, err = svc.SharedSkillOrigin("manual-one")
	if err != nil || origin != nil {
		t.Fatalf("manual dir must not be claimed: %v %v", origin, err)
	}
	// 不存在的技能：明确报错
	if origin, err := svc.SharedSkillOrigin("ghost"); err == nil || origin != nil {
		t.Fatalf("unknown skill should error: %v %v", origin, err)
	}
	// 无效名拒绝
	if _, err := svc.SharedSkillOrigin("../escape"); err == nil {
		t.Fatal("invalid skill name must be rejected")
	}
}

func TestMarkerValidationRejectsTampering(t *testing.T) {
	svc, stub := newTestService(t)
	addFixtureMarket(t, svc, stub, "mk")
	if _, err := svc.DeployPlugin("mk", "alpha"); err != nil {
		t.Fatalf("DeployPlugin: %v", err)
	}

	// 0444 只读：篡改前先放开权限
	chmodWritable := func(skill string) {
		t.Helper()
		if err := os.Chmod(markerPath(svc, skill), 0o644); err != nil {
			t.Fatalf("chmod marker: %v", err)
		}
	}
	cases := []struct {
		name   string
		skill  string
		writer func(p string)
	}{
		{"坏 JSON", "skill-one", func(p string) { os.WriteFile(p, []byte("not-json{"), 0o644) }},
		{"版本不符", "skill-one", func(p string) {
			os.WriteFile(p, []byte(`{"version":99,"marketplace":"mk","plugin":"alpha","skill":"skill-one"}`), 0o644)
		}},
		{"技能名与目录不符", "skill-one", func(p string) {
			os.WriteFile(p, []byte(`{"version":1,"marketplace":"mk","plugin":"alpha","skill":"skill-two"}`), 0o644)
		}},
		{"市场名非法", "skill-one", func(p string) {
			os.WriteFile(p, []byte(`{"version":1,"marketplace":"../evil","plugin":"alpha","skill":"skill-one"}`), 0o644)
		}},
		{"插件名空", "skill-one", func(p string) {
			os.WriteFile(p, []byte(`{"version":1,"marketplace":"mk","plugin":"  ","skill":"skill-one"}`), 0o644)
		}},
		{"超限", "skill-one", func(p string) {
			os.WriteFile(p, []byte(`{"version":1,"marketplace":"mk","padding":"`+strings.Repeat("x", markerMaxBytes)+`"}`), 0o644)
		}},
	}
	original, err := os.ReadFile(markerPath(svc, "skill-one"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		chmodWritable(tc.skill)
		tc.writer(markerPath(svc, tc.skill))
		if origin, err := svc.SharedSkillOrigin(tc.skill); err != nil || origin != nil {
			t.Fatalf("%s: tampered marker must yield no origin: %v %v", tc.name, origin, err)
		}
		// 恢复合法凭据，供下一用例复用同一路径
		if err := os.WriteFile(markerPath(svc, tc.skill), original, 0o644); err != nil {
			t.Fatalf("restore marker: %v", err)
		}
	}

	// 凭据为符号链接：不跟随、不认领（不支持 symlink 的平台跳过）
	validTarget, err := os.ReadFile(markerPath(svc, "skill-two"))
	if err != nil {
		t.Fatal(err)
	}
	chmodWritable("skill-two")
	if err := os.Remove(markerPath(svc, "skill-two")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "fake-marker.json")
	if err := os.WriteFile(outside, validTarget, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, markerPath(svc, "skill-two")); err == nil {
		if origin, err := svc.SharedSkillOrigin("skill-two"); err != nil || origin != nil {
			t.Fatalf("symlinked marker must yield no origin: %v %v", origin, err)
		}
	} else {
		t.Skipf("platform does not support symlink: %v", err)
	}

	// ListSkillOrigins 只列有效凭据；skill-one 凭据已被上面的用例恢复为合法
	origins, err := svc.ListSkillOrigins()
	if err != nil {
		t.Fatalf("ListSkillOrigins: %v", err)
	}
	bySkill := map[string]SkillOrigin{}
	for _, o := range origins {
		bySkill[o.Skill] = o
	}
	if _, ok := bySkill["skill-one"]; !ok {
		t.Fatalf("skill-one with valid marker should be listed: %+v", origins)
	}
	if _, ok := bySkill["skill-two"]; ok {
		t.Fatalf("skill-two with symlinked marker must not be listed: %+v", origins)
	}
}

func TestOriginFollowsDisabledMove(t *testing.T) {
	svc, stub := newTestService(t)
	addFixtureMarket(t, svc, stub, "mk")
	if _, err := svc.DeployPlugin("mk", "alpha"); err != nil {
		t.Fatalf("DeployPlugin: %v", err)
	}

	// 模拟共用技能子页停用：skills → skills-disabled（rename 不触碰目录内容，
	// 凭据跟随目录移动）
	disabled := svc.disabledSkillsRoot()
	if err := os.MkdirAll(disabled, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(svc.skillsRoot(), "skill-one"), filepath.Join(disabled, "skill-one")); err != nil {
		t.Fatal(err)
	}
	origin, err := svc.SharedSkillOrigin("skill-one")
	if err != nil || origin == nil {
		t.Fatalf("SharedSkillOrigin after disable: %v %v", origin, err)
	}
	if origin.Enabled || !strings.HasPrefix(origin.Path, disabled) {
		t.Fatalf("origin should follow the disabled dir: %+v", origin)
	}

	// 停用态目录也参与批量枚举与 DeployedSkills 组装
	origins, err := svc.ListSkillOrigins()
	if err != nil || len(origins) != 2 {
		t.Fatalf("ListSkillOrigins should cover disabled dir: %v %+v", err, origins)
	}
	list, err := svc.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v %+v", err, list)
	}
	for _, p := range list[0].Plugins {
		if p.Name == "alpha" && strings.Join(p.DeployedSkills, ",") != "skill-one,skill-two" {
			t.Fatalf("alpha DeployedSkills wrong (disabled dir included by evidence): %+v", p)
		}
	}
}

// fixtureCollidingMarket：市场 mk2，插件 "cloner" 声明与 mk/alpha 同名的技能
// skill-one（用于证明 DeployedSkills 不做名称碰撞推断）。
func fixtureCollidingMarket(t *testing.T, dst string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dst, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	catalog := `{"name":"colliding-market","plugins":[
		{"name":"cloner","source":"./p","description":"同名技能插件"}]}`
	if err := os.WriteFile(filepath.Join(dst, ".claude-plugin", "marketplace.json"), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dst, "p", "skills", "skill-one"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "p", "skills", "skill-one", "SKILL.md"), []byte("---\nname: skill-one\n---\n# clone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeployedSkillsNeverInferByNameCollision(t *testing.T) {
	svc, stub := newTestService(t)
	addFixtureMarket(t, svc, stub, "mk")
	if _, err := svc.DeployPlugin("mk", "alpha"); err != nil {
		t.Fatalf("DeployPlugin: %v", err)
	}

	// 第二个市场含同名技能条目：凭据指向 mk/alpha，绝不能算到 mk2/cloner 头上
	stub.respond = func(args []string) (string, error) {
		if args[0] == "clone" {
			fixtureCollidingMarket(t, args[len(args)-1])
		}
		return "", nil
	}
	if _, err := svc.Add("https://example.com/mk2.git", "mk2"); err != nil {
		t.Fatalf("Add mk2: %v", err)
	}

	list, err := svc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, market := range list {
		for _, p := range market.Plugins {
			switch {
			case market.Name == "mk" && p.Name == "alpha":
				if strings.Join(p.DeployedSkills, ",") != "skill-one,skill-two" {
					t.Fatalf("mk/alpha DeployedSkills wrong: %+v", p)
				}
			case market.Name == "mk2" && p.Name == "cloner":
				if len(p.DeployedSkills) != 0 {
					t.Fatalf("name collision must not be attributed to mk2/cloner: %+v", p)
				}
			}
		}
	}
	// 无凭据的同名手动目录同样不计入：预置手动 skill-one 于 mk2 加入前？
	// （上面的部署只发生一次，这里补验：手动 beta-solo 同名目录无凭据）
	if err := os.MkdirAll(filepath.Join(svc.skillsRoot(), "beta-solo"), 0o755); err != nil {
		t.Fatal(err)
	}
	list, err = svc.List()
	if err != nil {
		t.Fatalf("List again: %v", err)
	}
	for _, market := range list {
		for _, p := range market.Plugins {
			if market.Name == "mk" && p.Name == "beta-solo" && strings.Join(p.DeployedSkills, ",") != "" {
				t.Fatalf("manual beta-solo dir has no credential, must not count: %+v", p)
			}
		}
	}
}

func TestUninstallPluginRemovesOnlyEvidenceMatched(t *testing.T) {
	svc, stub := newTestService(t)
	addFixtureMarket(t, svc, stub, "mk")
	if _, err := svc.DeployPlugin("mk", "alpha"); err != nil {
		t.Fatalf("DeployPlugin alpha: %v", err)
	}
	if _, err := svc.DeployPlugin("mk", "beta-solo"); err != nil {
		t.Fatalf("DeployPlugin beta-solo: %v", err)
	}

	// 构造三类「保留」现场：
	// 1) skill-two 抽掉凭据 → 无凭据目录（等同旧版部署/手动改名）；
	if err := os.Chmod(markerPath(svc, "skill-two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(markerPath(svc, "skill-two")); err != nil {
		t.Fatal(err)
	}
	// 2) skill-one 移入停用根 → 凭据匹配的停用目录（应被卸载）；
	disabled := svc.disabledSkillsRoot()
	if err := os.MkdirAll(disabled, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(svc.skillsRoot(), "skill-one"), filepath.Join(disabled, "skill-one")); err != nil {
		t.Fatal(err)
	}
	// 3) 手动条目 manual-one（与任何插件无关）。
	if err := os.MkdirAll(filepath.Join(svc.skillsRoot(), "manual-one"), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := svc.UninstallPlugin("mk", "alpha")
	if err != nil {
		t.Fatalf("UninstallPlugin: %v", err)
	}
	if strings.Join(report.Removed, ",") != "skill-one" {
		t.Fatalf("only evidence-matched (disabled) skill-one should be removed: %+v", report)
	}
	// 无凭据 skill-two 保留；beta-solo（其他插件）保留；manual-one 保留
	for _, kept := range []string{
		filepath.Join(svc.skillsRoot(), "skill-two"),
		filepath.Join(svc.skillsRoot(), "beta-solo"),
		filepath.Join(svc.skillsRoot(), "manual-one"),
	} {
		if _, err := os.Lstat(kept); err != nil {
			t.Fatalf("credential-less or other-plugin dir must be kept: %s (%v)", kept, err)
		}
	}
	// 市场 clone 与状态不受影响（不得删除市场 clone）
	if _, err := os.Stat(svc.clonePath("mk")); err != nil {
		t.Fatalf("market clone must survive uninstall: %v", err)
	}
	list, err := svc.List()
	if err != nil || len(list) != 1 || list[0].Name != "mk" || list[0].PluginCount != 3 {
		t.Fatalf("market state must be intact: %v %+v", err, list)
	}
	for _, p := range list[0].Plugins {
		if p.Name == "alpha" && len(p.DeployedSkills) != 0 {
			t.Fatalf("alpha should have no deployed skills after uninstall: %+v", p)
		}
	}

	// 幂等重卸载：无凭据匹配 → 空报告非错误
	report, err = svc.UninstallPlugin("mk", "alpha")
	if err != nil || len(report.Removed) != 0 {
		t.Fatalf("repeat uninstall should be empty success: %v %+v", err, report)
	}
	// 无效参数拒绝
	if _, err := svc.UninstallPlugin("../evil", "alpha"); err == nil {
		t.Fatal("invalid market name must be rejected")
	}
	if _, err := svc.UninstallPlugin("mk", "  "); err == nil {
		t.Fatal("empty plugin name must be rejected")
	}
	// 卸载后可重新部署：被删的 skill-one 重新写入凭据；无凭据保留的
	// skill-two 目标已存在 → 跳过不覆盖（同名手动/无凭据条目保护语义）
	rep, err := svc.DeployPlugin("mk", "alpha")
	if err != nil {
		t.Fatalf("redeploy after uninstall: %v", err)
	}
	if strings.Join(rep.Deployed, ",") != "skill-one" || strings.Join(rep.Skipped, ",") != "skill-two" {
		t.Fatalf("redeploy should re-claim skill-one and skip credential-less skill-two: %+v", rep)
	}
}

func TestDisabledNameCollisionAndMissingCatalogPreserveUninstall(t *testing.T) {
	svc, stub := newTestService(t)
	addFixtureMarket(t, svc, stub, "mk")
	manual := filepath.Join(svc.disabledSkillsRoot(), "skill-one")
	if err := os.MkdirAll(manual, 0o755); err != nil {
		t.Fatal(err)
	}
	report, err := svc.DeployPlugin("mk", "alpha")
	if err != nil || strings.Join(report.Skipped, ",") != "skill-one" || strings.Join(report.Deployed, ",") != "skill-two" {
		t.Fatalf("manual disabled name must not be shadowed: %+v %v", report, err)
	}
	if err := os.Remove(filepath.Join(svc.clonePath("mk"), ".claude-plugin", "marketplace.json")); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.List()
	if err != nil || len(rows) != 1 || !rows[0].Missing || len(rows[0].Plugins) != 1 || rows[0].Plugins[0].Name != "alpha" {
		t.Fatalf("missing catalog must retain uninstall entry: %+v %v", rows, err)
	}
	if err := svc.Remove("mk"); err == nil {
		t.Fatal("must uninstall recorded deployments before removing market")
	}
	if _, err := svc.UninstallPlugin("mk", "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Remove("mk"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manual); err != nil {
		t.Fatal("manual skill must survive", err)
	}
}

func TestDeployMarkerWriteFailureLeavesNoPublish(t *testing.T) {
	svc, stub := newTestService(t)
	addFixtureMarket(t, svc, stub, "mk")

	// 市场仓库内预置「同名凭据路径为目录」→ copyDir 复制后写标记失败：
	// 该技能不得以无凭据形态发布（发布 = 内容 + 凭据成对）
	evilDir := filepath.Join(svc.clonePath("mk"), "plugins", "alpha", "skills", "skill-one", markerFileName)
	if err := os.MkdirAll(evilDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.DeployPlugin("mk", "alpha"); err == nil {
		t.Fatal("deploy must fail when marker cannot be written")
	} else if !strings.Contains(err.Error(), "来源凭据") {
		t.Fatalf("error should disclose marker failure: %v", err)
	}
	for _, skill := range []string{"skill-one", "skill-two"} {
		if _, err := os.Lstat(filepath.Join(svc.skillsRoot(), skill)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("skill %s must not be published without credential, stat err=%v", skill, err)
		}
	}
	// 暂存不留残渣
	if entries, err := os.ReadDir(svc.skillsRoot()); err != nil || len(entries) != 0 {
		t.Fatalf("staging must be cleaned: %v, err=%v", entries, err)
	}
}

func TestMarketShippedMarkerIsOverwrittenNotTrusted(t *testing.T) {
	svc, stub := newTestService(t)
	addFixtureMarket(t, svc, stub, "mk")

	// 市场仓库在技能目录内预置伪造凭据（宣称来自别的市场）：部署覆写，
	// 以部署时写入的为准——外部导入的伪造标记不生效
	fake := provenanceMarker{Version: 1, Marketplace: "other-market", Plugin: "other-plugin", Skill: "skill-one"}
	b, err := json.Marshal(fake)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc.clonePath("mk"), "plugins", "alpha", "skills", "skill-one", markerFileName), b, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.DeployPlugin("mk", "alpha"); err != nil {
		t.Fatalf("DeployPlugin must succeed by overwriting shipped marker: %v", err)
	}
	origin, err := svc.SharedSkillOrigin("skill-one")
	if err != nil || origin == nil || origin.Marketplace != "mk" || origin.Plugin != "alpha" {
		t.Fatalf("shipped fake marker must be overwritten by deploy-time credential: %v %v", origin, err)
	}
}
