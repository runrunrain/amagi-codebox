package sharedmarket

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"amagi-codebox/internal/platform"
)

type skillUpdateRoot struct {
	root, previous, next *os.Root
	agentsID, rootID     string
	enabled              bool
	stagingName          string
	keepBackup           bool
}

type stagedSkillUpdate struct {
	root             *skillUpdateRoot
	previous, next   provenanceMarker
	moved, published bool
}

// Stage every replacement before changing live directories. Keep the originals
// until the market state is committed, so a failed update can restore them.
func (s *Service) refreshDeployedSkills(market, plugin string, skills []deployableSkill, commit func() error) ([]string, []string, error) {
	agents, agentsID, err := openAgents(s.userHome)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = agents.Close() }()
	sources := make(map[string]deployableSkill, len(skills))
	for _, skill := range skills {
		sources[skill.Name] = skill
	}
	retained := []string{}
	var pending []*stagedSkillUpdate
	for _, candidate := range s.skillRootCandidates() {
		root, rootID, err := openPlainDir(agents, skillsDir(candidate.enabled))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		batch := &skillUpdateRoot{root: root, agentsID: agentsID, rootID: rootID, enabled: candidate.enabled}
		defer s.closeSkillUpdateRoot(batch)
		entries, err := fs.ReadDir(root.FS(), ".")
		if err != nil {
			return nil, nil, fmt.Errorf("读取技能目录失败：%w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || !validName(entry.Name()) {
				continue
			}
			marker, ok := batch.readMarker(root, entry.Name())
			if !ok || marker.Marketplace != market || marker.Plugin != plugin {
				continue
			}
			skill, ok := sources[marker.Skill]
			if !ok {
				retained = append(retained, marker.Skill)
				continue
			}
			replacement, err := batch.stage(skill, marker)
			if err != nil {
				return nil, nil, fmt.Errorf("暂存技能 %q 的新版失败：%w", marker.Skill, err)
			}
			pending = append(pending, replacement)
		}
	}

	updated := []string{}
	for i, skill := range pending {
		if err := skill.publish(); err != nil {
			return nil, nil, errors.Join(fmt.Errorf("更新技能 %q 失败：%w", skill.previous.Skill, err), restoreSkillUpdates(pending[:i+1]))
		}
		updated = append(updated, skill.previous.Skill)
	}
	if err := commit(); err != nil {
		return nil, nil, errors.Join(err, restoreSkillUpdates(pending))
	}
	sort.Strings(updated)
	sort.Strings(retained)
	return updated, retained, nil
}

func (b *skillUpdateRoot) stage(skill deployableSkill, marker provenanceMarker) (*stagedSkillUpdate, error) {
	if b.next == nil {
		name := ".update-staging-" + rand.Text()
		if err := b.root.Mkdir(name, 0o700); err != nil {
			return nil, err
		}
		b.stagingName = name
		staging, _, err := openPlainDir(b.root, name)
		if err != nil {
			return nil, err
		}
		defer func() { _ = staging.Close() }()
		for _, subdir := range []string{"previous", "next"} {
			if err := staging.Mkdir(subdir, 0o700); err != nil {
				return nil, err
			}
		}
		b.previous, _, err = openPlainDir(staging, "previous")
		if err != nil {
			return nil, err
		}
		b.next, _, err = openPlainDir(staging, "next")
		if err != nil {
			return nil, err
		}
	}
	next, err := stageSkill(b.next, skill, marker)
	if err != nil {
		return nil, err
	}
	return &stagedSkillUpdate{root: b, previous: marker, next: next}, nil
}

func (b *skillUpdateRoot) readMarker(dir *os.Root, skill string) (provenanceMarker, bool) {
	return readProvenanceMarker(dir, skill, b.agentsID, b.rootID, b.enabled)
}

func moveSkill(from, to *os.Root, name string) error {
	// The native no-replace move uses paths. Match both parents to the open
	// handles before publishing or restoring a skill.
	for _, dir := range []*os.Root{from, to} {
		info, err := os.Lstat(dir.Name())
		if err != nil {
			return err
		}
		if _, err := dirIdentity(dir, info); err != nil {
			return err
		}
	}
	return platform.MoveNoReplace(filepath.Join(from.Name(), name), filepath.Join(to.Name(), name))
}

func (u *stagedSkillUpdate) publish() error {
	b, name := u.root, u.previous.Skill
	if got, ok := b.readMarker(b.root, name); !ok || got != u.previous {
		return fmt.Errorf("技能目录或来源记录已变更")
	}
	// Use the same no-replace operation for isolation and publication. On a
	// filesystem that lacks this operation the live skill never moves.
	if err := moveSkill(b.root, b.previous, name); err != nil {
		// A post-move check can fail after the filesystem has moved the entry.
		_, statErr := b.previous.Lstat(name)
		u.moved = statErr == nil
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			b.keepBackup = true
			return errors.Join(err, fmt.Errorf("检查技能备份失败，请检查 %s：%w", b.previous.Name(), statErr))
		}
		return err
	}
	u.moved = true
	if got, ok := b.readMarker(b.previous, name); !ok || got != u.previous {
		return fmt.Errorf("技能目录或来源记录在更新期间发生变化")
	}
	err := moveSkill(b.next, b.root, name)
	got, ok := b.readMarker(b.root, name)
	u.published = ok && got == u.next
	if err != nil {
		return err
	}
	if !u.published {
		return fmt.Errorf("无法验证更新后的技能目录")
	}
	return nil
}

func restoreSkillUpdates(pending []*stagedSkillUpdate) error {
	var errs []error
	for i := len(pending) - 1; i >= 0; i-- {
		skill := pending[i]
		if err := skill.restore(); err != nil {
			skill.root.keepBackup = true
			name := skill.previous.Skill
			errs = append(errs, fmt.Errorf("恢复技能 %q 失败，请检查备份目录 %s：%w",
				name, filepath.Join(skill.root.previous.Name(), name), err))
		}
	}
	return errors.Join(errs...)
}

func (u *stagedSkillUpdate) restore() error {
	b, name := u.root, u.previous.Skill
	if !u.moved {
		return nil
	}
	if u.published {
		if got, ok := b.readMarker(b.root, name); !ok || got != u.next {
			return fmt.Errorf("当前技能目录已被替换")
		}
		if err := b.root.Rename(name, filepath.Join(b.stagingName, "next", name)); err != nil {
			return err
		}
		if got, ok := b.readMarker(b.next, name); !ok || got != u.next {
			return errors.Join(fmt.Errorf("恢复期间技能目录发生变化"), moveSkill(b.next, b.root, name))
		}
	}
	return moveSkill(b.previous, b.root, name)
}

func (s *Service) closeSkillUpdateRoot(b *skillUpdateRoot) {
	defer func() { _ = b.root.Close() }()
	if b.next != nil {
		_ = b.next.Close()
	}
	if b.previous != nil {
		_ = b.previous.Close()
	}
	if b.stagingName != "" && !b.keepBackup {
		if err := b.root.RemoveAll(b.stagingName); err != nil && s.log != nil {
			s.log.Warn("sharedmarket", "清理技能更新暂存目录失败", fmt.Sprintf("dir=%s err=%v", filepath.Join(b.root.Name(), b.stagingName), err))
		}
	}
}
