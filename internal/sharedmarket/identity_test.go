package sharedmarket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deployedIdentityFixture(t *testing.T) *Service {
	t.Helper()
	svc, git := newTestService(t)
	addFixtureMarket(t, svc, git, "mk")
	if _, err := svc.DeployPlugin("mk", "beta-solo"); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestCopiedDeploymentIsNotOwned(t *testing.T) {
	svc := deployedIdentityFixture(t)
	skill := "beta-solo"
	original := filepath.Join(svc.skillsRoot(), skill)
	backup := filepath.Join(svc.disabledSkillsRoot(), skill)
	if err := os.CopyFS(backup, os.DirFS(original)); err != nil {
		t.Fatal(err)
	}
	// A new service proves that recognition doesn't rely on a live in-memory handle.
	svc = New(svc.userHome, nil)
	if OriginForSkill(original, true) == nil || OriginForSkill(backup, false) != nil {
		t.Fatal("only the original directory instance should have provenance")
	}
	report, err := svc.UninstallPlugin("mk", skill)
	if err != nil || len(report.Removed) != 1 {
		t.Fatalf("uninstall original: %+v, %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(backup, "SKILL.md")); err != nil {
		t.Fatalf("backup was deleted: %v", err)
	}
	restored := filepath.Join(svc.skillsRoot(), skill)
	if err := os.Rename(backup, restored); err != nil {
		t.Fatal(err)
	}
	if origin, err := svc.SharedSkillOrigin(skill); origin != nil || err != nil {
		t.Fatalf("restored backup claimed: %+v, %v", origin, err)
	}
	report, err = svc.UninstallPlugin("mk", skill)
	if err != nil || len(report.Removed) != 0 {
		t.Fatalf("uninstall removed restored backup: %+v, %v", report, err)
	}
}

func TestUninstallPreservesReplacementAfterMatch(t *testing.T) {
	svc := deployedIdentityFixture(t)
	agents, agentsID, err := openAgents(svc.userHome)
	if err != nil {
		t.Fatal(err)
	}
	defer agents.Close()
	root, rootID, err := openPlainDir(agents, activeSkillsDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	const skill = "beta-solo"
	// Replace the live name between the scan and the deletion phase, without relying on timing.
	want, ok := readProvenanceMarker(root, skill, agentsID, rootID, true)
	if !ok {
		t.Fatal("expected a matched deployment")
	}
	const backup = ".original"
	if err := root.Rename(skill, backup); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(root.Name(), backup)
	if err := os.CopyFS(filepath.Join(root.Name(), skill), os.DirFS(saved)); err != nil {
		t.Fatal(err)
	}
	err = uninstallMatchedSkill(root, agentsID, rootID, true, want)
	if err == nil || !strings.Contains(err.Error(), "未删除") {
		t.Fatalf("expected a recoverable mismatch, got %v", err)
	}
	matches, globErr := filepath.Glob(filepath.Join(root.Name(), ".uninstall-*", skill, "SKILL.md"))
	if globErr != nil || len(matches) != 1 || !strings.Contains(err.Error(), filepath.Dir(matches[0])) {
		t.Fatalf("expected a preserved replacement and its recovery path, paths=%v err=%v", matches, err)
	}
	before, err := os.ReadFile(filepath.Join(saved, "SKILL.md"))
	if err != nil {
		t.Fatalf("original deployment no longer intact: %v", err)
	}
	if after, err := os.ReadFile(matches[0]); err != nil || string(after) != string(before) {
		t.Fatalf("replacement no longer intact: %v", err)
	}
}

func TestDeploymentRejectsReplacementRoot(t *testing.T) {
	svc := deployedIdentityFixture(t)
	root := svc.skillsRoot()
	oldRoot := root + "-old"
	if err := os.Rename(root, oldRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	skill := "beta-solo"
	moved := filepath.Join(root, skill)
	if err := os.Rename(filepath.Join(oldRoot, skill), moved); err != nil {
		t.Fatal(err)
	}
	if OriginForSkill(moved, true) != nil {
		t.Fatal("replacement root was accepted")
	}
	report, err := svc.UninstallPlugin("mk", skill)
	if err != nil || len(report.Removed) != 0 {
		t.Fatalf("uninstall in replacement root: %+v, %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(moved, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestDeploymentRejectsReplacementAgents(t *testing.T) {
	svc := deployedIdentityFixture(t)
	agents := filepath.Join(svc.userHome, agentsDir)
	oldAgents := agents + "-old"
	if err := os.Rename(agents, oldAgents); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{activeSkillsDir, disabledSkillsDir} {
		if err := os.Rename(filepath.Join(oldAgents, dir), filepath.Join(agents, dir)); err != nil {
			t.Fatal(err)
		}
	}
	// The original skill and both original roots are present under a different parent.
	skill := "beta-solo"
	if origin, err := svc.SharedSkillOrigin(skill); origin != nil || err != nil {
		t.Fatalf("replacement .agents accepted: %+v, %v", origin, err)
	}
	if report, err := svc.UninstallPlugin("mk", skill); err != nil || len(report.Removed) != 0 {
		t.Fatalf("uninstall in replacement .agents: %+v, %v", report, err)
	}
}

func TestDeploymentRejectsRedirectedRoots(t *testing.T) {
	for _, dir := range []string{agentsDir, activeSkillsDir, disabledSkillsDir} {
		t.Run(dir, func(t *testing.T) {
			svc := deployedIdentityFixture(t)
			root := filepath.Join(svc.userHome, agentsDir)
			rel := filepath.Join(activeSkillsDir, "beta-solo")
			if dir == activeSkillsDir {
				root, rel = svc.skillsRoot(), "beta-solo"
			}
			if dir == disabledSkillsDir {
				root, rel = svc.disabledSkillsRoot(), "beta-solo"
				if err := os.Rename(filepath.Join(svc.skillsRoot(), rel), filepath.Join(root, rel)); err != nil {
					t.Fatal(err)
				}
			}
			relocated := filepath.Join(t.TempDir(), "relocated")
			if err := os.Rename(root, relocated); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(relocated, root); err != nil {
				t.Skipf("cannot create symlink: %v", err)
			}
			if OriginForSkill(filepath.Join(root, rel), dir != disabledSkillsDir) != nil {
				t.Fatal("redirected root was accepted")
			}
			if origins, err := svc.ListSkillOrigins(); err != nil || len(origins) != 0 {
				t.Fatalf("listed redirected provenance: %+v, %v", origins, err)
			}
			if _, err := svc.UninstallPlugin("mk", "beta-solo"); err == nil {
				t.Fatal("uninstall must reject redirected roots")
			}
			if _, err := svc.DeployPlugin("mk", "alpha"); err == nil {
				t.Fatal("deploy must reject redirected roots")
			}
			if err := svc.Remove("mk"); err == nil {
				t.Fatal("market removal must not bypass uninstall through a redirected root")
			}
			if _, err := os.Stat(filepath.Join(relocated, rel, "SKILL.md")); err != nil {
				t.Fatal(err)
			}
		})
	}
}
