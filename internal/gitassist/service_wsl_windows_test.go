//go:build windows

package gitassist

import (
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// TestLinuxStyleWorkDirPreCheck pins the P4-A1/E7 contract: a Linux-form
// workDir (/mnt/<drive>, ~/..., bare ~, bare /, /home/...) is intercepted by
// validateWorkDirForWindowsGit BEFORE any git.exe invocation — every public
// entry returns the structured actionable error instead of the bare fatal
// (`fatal: cannot change to '...'`) the Windows git.exe would emit.
func TestLinuxStyleWorkDirPreCheck(t *testing.T) {
	svc := New(nil, nil, nil)
	linuxForms := []string{"/mnt/x/WorkSpace/repo", "~/projects", "~", "/", "/home/u/repo"}

	type probe struct {
		name string
		call func(workDir string) error
	}
	probes := []probe{
		{"RepoInfo", func(wd string) error {
			_, err := svc.RepoInfo(wd)
			return err
		}},
		{"ListBranches", func(wd string) error {
			_, err := svc.ListBranches(wd)
			return err
		}},
		{"SwitchBranch", func(wd string) error {
			return svc.SwitchBranch(wd, "main")
		}},
		{"SummarizeDiff", func(wd string) error {
			_, err := svc.SummarizeDiff(wd)
			return err
		}},
		{"CommitAll", func(wd string) error {
			return svc.CommitAll(wd, "msg")
		}},
		{"CommitStaged", func(wd string) error {
			return svc.CommitStaged(wd, "msg")
		}},
		{"Push", func(wd string) error {
			_, err := svc.Push(wd)
			return err
		}},
	}

	for _, p := range probes {
		for _, wd := range linuxForms {
			err := p.call(wd)
			if err == nil {
				t.Fatalf("%s(%q) must reject Linux-form workDir", p.name, wd)
			}
			msg := err.Error()
			if !strings.Contains(msg, linuxStyleWorkDirMarker) || !strings.Contains(msg, wd) {
				t.Errorf("%s(%q) error must be the structured pre-check message, got: %v", p.name, wd, err)
			}
			// 零 git.exe 调用的证明：错误不来自 git 进程（无 fatal/stderr 形态）。
			if strings.Contains(msg, "fatal") || strings.Contains(msg, "cannot change to") {
				t.Errorf("%s(%q) leaked a bare git.exe failure — pre-check must fire first, got: %v", p.name, wd, err)
			}
		}
	}

	// RepoInfo 的关键差异：不得把形态问题吞成 IsGitRepo=false（那会伪装成
	// 「不是 git 仓库」，前端拿不到可操作文案）。
	info, err := svc.RepoInfo("/mnt/x/WorkSpace/repo")
	if err == nil || info.IsGitRepo {
		t.Fatalf("RepoInfo on Linux-form workDir must error (not IsGitRepo=false), got info=%+v err=%v", info, err)
	}
}

// TestGitAssistLinuxWorkDirRecordsDrvFSAdvisory verifies the DrvFS advisory
// entry point: only the /mnt/<drive> form feeds the shared registry (source
// "gitassist"); ext4-style Linux paths stay advisory-free.
func TestGitAssistLinuxWorkDirRecordsDrvFSAdvisory(t *testing.T) {
	svc := New(nil, nil, nil)

	if _, err := svc.RepoInfo("/mnt/f/drvfs-probe"); err == nil {
		t.Fatal("expected Linux-form workDir to be rejected")
	}
	found := false
	for _, e := range platform.DrvFSAdvisories() {
		if e.Source == "gitassist" && e.WorkDir == "/mnt/f/drvfs-probe" {
			found = true
			if e.MountPath != "/mnt/f/drvfs-probe" {
				t.Errorf("advisory mountPath = %q, want /mnt/f/drvfs-probe", e.MountPath)
			}
		}
	}
	if !found {
		t.Fatal("expected a source=gitassist DrvFS advisory for the /mnt workDir")
	}

	// ext4 形态：报形态错误但不写 DrvFS advisory。
	if _, err := svc.RepoInfo("/home/u/ext4-probe"); err == nil {
		t.Fatal("expected Linux-form workDir to be rejected")
	}
	for _, e := range platform.DrvFSAdvisories() {
		if e.WorkDir == "/home/u/ext4-probe" {
			t.Fatalf("ext4 workDir must not record a DrvFS advisory: %#v", e)
		}
	}

	// Windows 形态 workDir 不拦截、不写 advisory（正常路径回归锚定）。
	if err := validateWorkDirForWindowsGit(`X:\repo`); err != nil {
		t.Fatalf("Windows-form workDir must pass the pre-check, got: %v", err)
	}
	for _, e := range platform.DrvFSAdvisories() {
		if e.Source == "gitassist" && e.WorkDir == `X:\repo` {
			t.Fatalf("passing workDir must not record an advisory: %#v", e)
		}
	}
}
