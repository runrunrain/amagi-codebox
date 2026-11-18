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

	updated, err := svc.UpdatePlugin(initial.Name, "alpha")
	if err != nil || updated.Version != "2.0.0" || !updated.Updatable {
		t.Fatalf("update: %+v, %v", updated, err)
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
