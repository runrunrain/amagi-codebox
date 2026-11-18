package sharedmarket

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUpdatePluginIsolatesCatalogAndDeploymentSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	upstream := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = upstream
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(message string) {
		git("add", "--all")
		git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", message)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(upstream, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "--quiet")
	fixtureMarket(t, upstream)
	commit("Initial market")

	home := t.TempDir()
	svc := New(home, nil)
	initial, err := svc.Add(upstream, "fixture-market")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(filepath.Join(upstream, "plugins", "alpha"), filepath.Join(upstream, "plugins", "alpha-new")); err != nil {
		t.Fatal(err)
	}
	write(catalogPath, `{"plugins":[
		{"name":"alpha","source":"./plugins/alpha-new","version":"2.0.0"},
		{"name":"beta-solo","source":"./plugins/beta-solo","version":"2.0.0"}
	]}`)
	write("plugins/alpha-new/skills/skill-one/SKILL.md", "# alpha v2\n")
	commit("Update both plugins and move alpha")

	report, err := svc.UpdatePlugin(initial.Name, "alpha")
	updated := report.Plugin
	if err != nil || updated.Version != "2.0.0" || !updated.Updatable || len(report.UpdatedSkills) != 0 {
		t.Fatalf("update: %+v, %v", report, err)
	}
	svc = New(home, nil)
	markets, err := svc.List()
	if err != nil || len(markets) != 1 {
		t.Fatalf("reopen market: %+v, %v", markets, err)
	}
	market := markets[0]
	if market.LastUpdated != initial.LastUpdated || !reflect.DeepEqual(market.Plugins, append([]Plugin{updated}, initial.Plugins[1:]...)) {
		t.Fatalf("single update changed the rest of the market: %+v", market)
	}

	if _, err := svc.DeployPlugin(initial.Name, "alpha"); err != nil {
		t.Fatal(err)
	}
	deployed, err := os.ReadFile(filepath.Join(svc.skillsRoot(), "skill-one", "SKILL.md"))
	if err != nil || !strings.Contains(string(deployed), "alpha v2") {
		t.Fatalf("deployment did not use the updated plugin: %q, %v", deployed, err)
	}

	// A failed state commit must leave the previous snapshot readable and remove the new one.
	if err := os.Mkdir(svc.statePath()+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdatePlugin(initial.Name, "alpha"); err == nil {
		t.Fatal("update unexpectedly succeeded with a blocked state file")
	}
	if err := os.Remove(svc.statePath() + ".tmp"); err != nil {
		t.Fatal(err)
	}
	snapshots, err := filepath.Glob(filepath.Join(svc.Root(), pluginSnapshotPrefix+"*"))
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("expected one committed snapshot after failure: %v, %v", snapshots, err)
	}
	markets, err = svc.List()
	if err != nil || len(markets) != 1 || markets[0].Plugins[0].Version != updated.Version {
		t.Fatalf("failed update lost the previous plugin version: %+v, %v", markets, err)
	}

	market, err = svc.Update(initial.Name)
	if err != nil || len(market.Plugins) != 2 || market.Plugins[1].Version != "2.0.0" {
		t.Fatalf("whole-market update: %+v, %v", market, err)
	}
	if _, err := os.Stat(snapshots[0]); !os.IsNotExist(err) {
		t.Fatalf("whole-market update left the old snapshot: %v", err)
	}
}

func TestUpdatePluginRefreshesOwnedSkillsInTheirOriginalRoots(t *testing.T) {
	svc, git := newTestService(t)
	addFixtureMarket(t, svc, git, "mk")
	writeSkill := func(dir, body string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	readSkill := func(dir string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	writeSkill(filepath.Join(svc.clonePath("mk"), "plugins", "alpha", "skills", "retired"), "retired v1")
	if _, err := svc.DeployPlugin("mk", "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeployPlugin("mk", "beta-solo"); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(svc.skillsRoot(), "skill-one")
	manual := filepath.Join(svc.skillsRoot(), "skill-two")
	disabled := filepath.Join(svc.disabledSkillsRoot(), "skill-two")
	otherPlugin := filepath.Join(svc.skillsRoot(), "beta-solo")
	if err := os.Rename(manual, disabled); err != nil {
		t.Fatal(err)
	}
	writeSkill(manual, "manual copy")
	oldActive, oldDisabled, oldOther := readSkill(active), readSkill(disabled), readSkill(otherPlugin)
	oldOrigin := OriginForSkill(disabled, false)
	if oldOrigin == nil {
		t.Fatal("disabled skill must have valid provenance before updating")
	}
	git.respond = func(args []string) (string, error) {
		clone := args[len(args)-1]
		fixtureMarket(t, clone)
		for _, skill := range []string{"skill-one", "skill-two", "new-skill"} {
			writeSkill(filepath.Join(clone, "plugins", "alpha", "skills", skill), skill+" v2")
		}
		return "", nil
	}

	blocked := svc.statePath() + ".tmp"
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdatePlugin("mk", "alpha"); err == nil || !strings.Contains(err.Error(), "写市场状态临时文件失败") {
		t.Fatalf("expected a market state write failure after publishing: %v", err)
	}
	if readSkill(active) != oldActive || readSkill(disabled) != oldDisabled || OriginForSkill(disabled, false) == nil {
		t.Fatal("failed update did not restore the original deployments and provenance")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}

	report, err := svc.UpdatePlugin("mk", "alpha")
	if err != nil || !reflect.DeepEqual(report.UpdatedSkills, []string{"skill-one", "skill-two"}) ||
		!reflect.DeepEqual(report.RetainedSkills, []string{"retired"}) {
		t.Fatalf("unexpected update report: %+v, %v", report, err)
	}
	if readSkill(active) != "skill-one v2" || readSkill(disabled) != "skill-two v2" {
		t.Fatal("deployed contents were not updated in their original roots")
	}
	if readSkill(manual) != "manual copy" || readSkill(otherPlugin) != oldOther ||
		readSkill(filepath.Join(svc.skillsRoot(), "retired")) != "retired v1" {
		t.Fatal("a deployment or manual skill outside the update set was modified")
	}
	for _, root := range []string{svc.skillsRoot(), svc.disabledSkillsRoot()} {
		if _, err := os.Lstat(filepath.Join(root, "new-skill")); !os.IsNotExist(err) {
			t.Fatalf("new skill was automatically deployed: %v", err)
		}
		if staging, err := filepath.Glob(filepath.Join(root, ".update-staging-*")); err != nil || len(staging) != 0 {
			t.Fatalf("update left staging directories: %v, %v", staging, err)
		}
	}
	for _, state := range []struct {
		path    string
		enabled bool
	}{{active, true}, {disabled, false}} {
		origin := OriginForSkill(state.path, state.enabled)
		if origin == nil || origin.Enabled != state.enabled || origin.Marketplace != "mk" || origin.Plugin != "alpha" || origin.DeployedAt != oldOrigin.DeployedAt {
			t.Fatalf("updated skill lost its original state or provenance: %+v", origin)
		}
	}
}
