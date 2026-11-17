package sharedext

// deploy.go — 分发引擎：skill 目录复制部署/回收、MCP server 写入/移除、
// 配置文件备份轮转。失败语义：单引擎失败聚合返回（errors.Join），已成功
// 引擎不回滚，registry 只记录实际成功的 Deployed 状态。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// backupKeep 是配置文件 .bak-sharedext-<unixts> 备份的保留份数。
const backupKeep = 3

// ---------------------------------------------------------------------------
// skill 部署 / 回收
// ---------------------------------------------------------------------------

// deploySkillLocked 把 skill 复制部署到 engines 全部目标。
// skill.Deployed 追加实际成功的引擎；失败聚合返回。
func (s *Service) deploySkillLocked(skill *SharedSkill, engines []Engine) error {
	src := skill.SourcePath
	if src == "" {
		src = filepath.Join(s.skillsRepoDir(), skill.Name)
		skill.SourcePath = src
	}
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("skill 仓库目录缺失（%s）: %w", src, err)
	}

	var errs []error
	for _, engine := range engines {
		dst, err := s.engineSkillsDir(engine)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", engine, err))
			continue
		}
		target := filepath.Join(dst, skill.Name)
		if err := s.replaceDir(src, target); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", engine, err))
			continue
		}
		if !containsEngine(skill.Deployed, engine) {
			skill.Deployed = append(skill.Deployed, engine)
		}
	}
	return joinErrors(errs)
}

// undeploySkillLocked 回收 skill 的全部 Deployed 目标并清空清单。
func (s *Service) undeploySkillLocked(skill *SharedSkill) error {
	return s.undeploySkillEnginesLocked(skill, skill.Deployed)
}

// undeploySkillEnginesLocked 从指定引擎回收 skill：只删「目标目录名 ==
// skill 名」的目录（Deployed 记录是唯一回收依据）；回收后从 Deployed 移除。
// 目录不存在视为已回收（幂等），不报错。
func (s *Service) undeploySkillEnginesLocked(skill *SharedSkill, engines []Engine) error {
	var errs []error
	for _, engine := range engines {
		dir, err := s.engineSkillsDir(engine)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", engine, err))
			continue
		}
		target := filepath.Join(dir, skill.Name)
		if err := os.RemoveAll(target); err != nil {
			errs = append(errs, fmt.Errorf("%s: 删除 %s: %w", engine, target, err))
			continue
		}
	}
	next := make([]Engine, 0, len(skill.Deployed))
	for _, e := range skill.Deployed {
		if !containsEngine(engines, e) {
			next = append(next, e)
		}
	}
	skill.Deployed = next
	return joinErrors(errs)
}

// replaceDir 以「临时目录 + rename」整体替换 target 目录（原子性优先）；
// target 不存在时直接 rename。rename 失败回退直接删除重制并日志。
func (s *Service) replaceDir(src, target string) error {
	return replaceDirLogged(src, target, s.warn)
}

type warnFunc func(message, detail string)

// replaceDirLogged 是 replaceDir 的可测试核心。
func replaceDirLogged(src, target string, warn warnFunc) error {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("创建目标父目录: %w", err)
	}
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(target)+".deploy-*")
	if err != nil {
		return fmt.Errorf("创建临时目录: %w", err)
	}
	if err := copyTree(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}

	if _, err := os.Lstat(target); err == nil {
		// 目标已存在：取一个不存在的唯一名移走旧目录，再让新目录 rename 就位。
		oldDir, oerr := uniqueAbsentPath(parent, filepath.Base(target)+".replaced-")
		if oerr == nil && os.Rename(target, oldDir) == nil {
			if rerr := os.Rename(tmp, target); rerr != nil {
				// 新目录就位失败：还原旧目录。
				_ = os.Rename(oldDir, target)
				_ = os.RemoveAll(tmp)
				return fmt.Errorf("替换目录失败: %w", rerr)
			}
			_ = os.RemoveAll(oldDir)
			return nil
		}
		// 移走旧目录失败 → 回退：直接删除旧目录后重试 rename。
		warn("目标旧目录 rename 失败，回退直接删除重制", target)
		if rerr := os.RemoveAll(target); rerr != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf("删除旧目标目录: %w", rerr)
		}
	}

	if err := os.Rename(tmp, target); err != nil {
		warn("临时目录 rename 失败，回退直接删除重制", fmt.Sprintf("%s: %v", target, err))
		_ = os.RemoveAll(tmp)
		return copyTree(src, target)
	}
	return nil
}

// uniqueAbsentPath 生成 parent 下 prefix+随机后缀且当前不存在的路径（重试若干次）。
func uniqueAbsentPath(parent, prefix string) (string, error) {
	for i := 0; i < 8; i++ {
		candidate, err := os.MkdirTemp(parent, prefix+"*")
		if err != nil {
			continue
		}
		// MkdirTemp 占位获得唯一名后立即释放，交给调用方 rename 使用。
		if err := os.Remove(candidate); err != nil {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("无法为 %s 生成唯一临时名", prefix)
}

// copyTree 递归复制目录树（目录 0755、文件 0644；跳过符号链接条目）。
func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("访问源目录 %s: %w", src, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("源路径不是目录: %s", src)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("创建目录 %s: %w", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("读取源目录 %s: %w", src, err)
	}
	for _, entry := range entries {
		sPath := filepath.Join(src, entry.Name())
		dPath := filepath.Join(dst, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			continue // 符号链接跳过（内容受控原则）
		}
		if entry.IsDir() {
			if err := copyTree(sPath, dPath); err != nil {
				return err
			}
			continue
		}
		if !entry.Type().IsRegular() {
			continue // 非普通文件（socket/device 等）跳过
		}
		if err := copyFile(sPath, dPath); err != nil {
			return err
		}
	}
	return nil
}

// copyFile 复制单个文件（0644）。
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("读取 %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("写入 %s: %w", dst, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// MCP 部署 / 回收
// ---------------------------------------------------------------------------

// deployMCPLocked 把 MCP server def 写入 engines 全部目标。
// 写入前若目标已有同名 key 且内容与 registry 记录不一致 → 视为用户手改，
// 仍覆盖（CodeBox 管理优先）但日志 warn。
func (s *Service) deployMCPLocked(server *SharedMCPServer, engines []Engine) error {
	var errs []error
	for _, engine := range engines {
		target, err := s.engineMCPTarget(engine)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", engine, err))
			continue
		}
		if err := s.writeMCPServer(target, server.Name, server.Config); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", engine, err))
			continue
		}
		if !containsEngine(server.Deployed, engine) {
			server.Deployed = append(server.Deployed, engine)
		}
	}
	return joinErrors(errs)
}

// undeployMCPLocked 从 engines 各目标配置移除 server；Deployed 同步收缩。
func (s *Service) undeployMCPLocked(server *SharedMCPServer, engines []Engine) error {
	var errs []error
	for _, engine := range engines {
		target, err := s.engineMCPTarget(engine)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", engine, err))
			continue
		}
		if err := s.removeMCPServer(target, server.Name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", engine, err))
			continue
		}
	}
	next := make([]Engine, 0, len(server.Deployed))
	for _, e := range server.Deployed {
		if !containsEngine(engines, e) {
			next = append(next, e)
		}
	}
	server.Deployed = next
	return joinErrors(errs)
}

// writeMCPServer 把单个 server 写入目标配置文件（幂等，键不存在创建）。
func (s *Service) writeMCPServer(target mcpTarget, name string, def map[string]any) error {
	if target.Style == "toml" {
		data, _ := os.ReadFile(target.Path)
		if len(data) > 0 {
			if existing, ok := tomlSectionValue(data, name); ok && !mcpDefEqual(existing, def) {
				s.warn("目标已有同名 MCP server 且内容与仓库记录不一致，按 CodeBox 管理覆盖",
					fmt.Sprintf("file=%s server=%s", target.Path, name))
			}
		}
		updated, err := SetMCPServerSection(data, name, def)
		if err != nil {
			return err
		}
		return writeFileAtomic(target.Path, updated, 0o644)
	}

	data, err := os.ReadFile(target.Path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("读取 %s: %w", target.Path, err)
		}
		data = []byte("{}")
	}
	// 冲突检测：目标已有同名 key 且内容与 registry 记录不一致 → 覆盖但 warn。
	if existing := gjsonGetServer(data, target.Key, name); existing != nil {
		if !mcpDefEqual(existing, def) {
			s.warn("目标已有同名 MCP server 且内容与仓库记录不一致，按 CodeBox 管理覆盖",
				fmt.Sprintf("file=%s server=%s", target.Path, name))
		}
	}
	if target.BackedUp {
		if err := backupConfigFile(target.Path, data); err != nil {
			s.warn("配置文件备份失败，继续写入", fmt.Sprintf("file=%s: %v", target.Path, err))
		}
	}
	updated, err := sjsonSetServer(data, target.Key, name, def)
	if err != nil {
		return err
	}
	return writeFileAtomic(target.Path, updated, 0o644)
}

// removeMCPServer 从目标配置移除单个 server（键不存在不报错，幂等）。
func (s *Service) removeMCPServer(target mcpTarget, name string) error {
	if target.Style == "toml" {
		data, err := os.ReadFile(target.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("读取 %s: %w", target.Path, err)
		}
		updated, err := RemoveMCPServerSection(data, name)
		if err != nil {
			return err
		}
		if string(updated) == string(data) {
			return nil
		}
		return writeFileAtomic(target.Path, updated, 0o644)
	}

	data, err := os.ReadFile(target.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("读取 %s: %w", target.Path, err)
	}
	if gjsonGetServer(data, target.Key, name) == nil {
		return nil
	}
	if target.BackedUp {
		if err := backupConfigFile(target.Path, data); err != nil {
			s.warn("配置文件备份失败，继续写入", fmt.Sprintf("file=%s: %v", target.Path, err))
		}
	}
	updated, err := sjsonDeleteServer(data, target.Key, name)
	if err != nil {
		return err
	}
	return writeFileAtomic(target.Path, updated, 0o644)
}

// backupConfigFile 在写入前为配置文件做 .bak-sharedext-<unixts> 备份，
// 并按时间戳清理只保留最近 backupKeep 份。
func backupConfigFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ts := fmt.Sprintf("%d", nowUnix())
	backup := filepath.Join(dir, base+".bak-sharedext-"+ts)
	if err := os.WriteFile(backup, data, 0o644); err != nil {
		return err
	}
	return pruneBackups(dir, base)
}

// pruneBackups 删除 base 的 .bak-sharedext-* 备份中较旧的（保留最近 N 份）。
func pruneBackups(dir, base string) error {
	prefix := base + ".bak-sharedext-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			names = append(names, e.Name())
		}
	}
	if len(names) <= backupKeep {
		return nil
	}
	// 时间戳数值大 = 新；字典序对同长 unixts 等价数值序。
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names[backupKeep:] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// writeFileAtomic 临时文件 + rename 原子写入。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建目录: %w", err)
	}
	tmp := path + ".tmp-sharedext"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("写临时文件: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		if werr := os.WriteFile(path, data, perm); werr != nil {
			return fmt.Errorf("替换 %s: %w", path, err)
		}
	}
	return nil
}

func joinErrors(errs []error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return errors.Join(errs...)
	}
}
