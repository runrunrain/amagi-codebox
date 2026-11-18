package sharedmarket

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	agentsDir         = ".agents"
	activeSkillsDir   = "skills"
	disabledSkillsDir = "skills-disabled"
)

// openPlainDir checks the entry itself, then verifies that the opened handle
// still refers to that entry. Paths below the trusted home are root-relative.
func openPlainDir(parent *os.Root, name string) (*os.Root, string, error) {
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, "", err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, "", fmt.Errorf("拒绝链接或非普通目录：%s", filepath.Join(parent.Name(), name))
	}
	r, err := parent.OpenRoot(name)
	if err != nil {
		return nil, "", err
	}
	id, err := dirIdentity(r, before)
	if err != nil {
		_ = r.Close()
		return nil, "", err
	}
	return r, id, nil
}

func dirIdentity(r *os.Root, before os.FileInfo) (string, error) {
	f, err := r.Open(".")
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !fi.IsDir() || !os.SameFile(before, fi) {
		return "", fmt.Errorf("目录实例已变更：%s", r.Name())
	}
	return fileIdentity(f)
}

func openAgents(home string) (*os.Root, string, error) {
	r, err := os.OpenRoot(home)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = r.Close() }()
	return openPlainDir(r, agentsDir)
}

func mkdirIfMissing(parent *os.Root, name string) error {
	if err := parent.Mkdir(name, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	return nil
}

func skillsDir(enabled bool) string {
	if enabled {
		return activeSkillsDir
	}
	return disabledSkillsDir
}
