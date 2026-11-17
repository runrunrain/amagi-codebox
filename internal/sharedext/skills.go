package sharedext

// skills.go — 公共 skill 管理：zip/目录导入（安全解包 + frontmatter 解析）、
// 列表（Missing 标记）、删除（先回收后清库）、启停与 targets diff 分发。

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// zip 安全上限（任务书）：总解包 50MB、文件数 2000、单文件 10MB、SKILL.md 1MB。
const (
	zipMaxTotalBytes = 50 << 20
	zipMaxFileCount  = 2000
	zipMaxFileBytes  = 10 << 20
	skillMDMaxBytes  = 1 << 20
	// collectSkillMaxDepth 是 zip 导入 BFS 收集 skill 目录的防御性深度上限。
	collectSkillMaxDepth = 4
)

// ListSharedSkills 返回 registry 全量 skill（按 name 字典序）。
// SourcePath 失踪的条目保留返回并标记 Missing=true（附日志），不报错。
func (s *Service) ListSharedSkills() ([]SharedSkill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, err := s.loadRegistryLocked()
	if err != nil {
		return nil, err
	}
	out := make([]SharedSkill, 0, len(reg.Skills))
	for name, skill := range reg.Skills {
		if _, err := os.Stat(skill.SourcePath); err != nil {
			skill.Missing = true
			s.warn("公共 skill 仓库目录缺失", fmt.Sprintf("skill=%s path=%s", name, skill.SourcePath))
		}
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ImportSkillFromZip 从 zip 压缩包导入公共 skill。
// 安全解包到临时目录后走统一导入：entry 名含 `..`/绝对路径/盘符 → 拒绝整个
// 导入；符号链接条目跳过；超 size/文件数上限拒绝整个导入。一个 zip 可含多个
// skill（最浅层命中，忽略更深层嵌套）；frontmatter name 优先，缺省用目录名；
// 同名已存在的条目跳过并聚合到错误文本。
func (s *Service) ImportSkillFromZip(zipPath string) ([]SharedSkill, error) {
	tmp, err := os.MkdirTemp("", "sharedext-unzip-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := safeUnzip(zipPath, tmp); err != nil {
		return nil, err
	}
	return s.importFromScanRoot(tmp)
}

// ImportSkillFromDir 从本地目录导入公共 skill。
// dir 自身含 SKILL.md → 单 skill 导入；否则扫描一层子目录。
func (s *Service) ImportSkillFromDir(dirPath string) ([]SharedSkill, error) {
	info, err := os.Stat(dirPath)
	if err != nil {
		return nil, fmt.Errorf("访问导入目录失败: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("导入路径不是目录: %s", dirPath)
	}
	return s.importFromScanRoot(dirPath)
}

// importFromScanRoot 统一导入入口：在 scanRoot 下收集含 SKILL.md 的目录
// （最浅层命中），逐个复制入库并写 registry。部分失败（同名/非法名）不阻断
// 其余导入，错误文本聚合列出跳过项。
func (s *Service) importFromScanRoot(scanRoot string) ([]SharedSkill, error) {
	candidates, err := collectSkillDirs(scanRoot, collectSkillMaxDepth)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("未发现任何含 SKILL.md 的 skill 目录（路径: %s）", scanRoot)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	reg, err := s.loadRegistryLocked()
	if err != nil {
		return nil, err
	}

	var imported []SharedSkill
	var skipped []string
	for _, dir := range candidates {
		dirName := filepath.Base(dir)
		skill, perr := readSkillMeta(dir)
		if perr != nil {
			skipped = append(skipped, fmt.Sprintf("%s（%v）", dirName, perr))
			continue
		}
		if _, exists := reg.Skills[skill.Name]; exists {
			skipped = append(skipped, fmt.Sprintf("%s（同名已存在，跳过）", skill.Name))
			continue
		}
		dst := filepath.Join(s.skillsRepoDir(), skill.Name)
		if err := copyTree(dir, dst); err != nil {
			skipped = append(skipped, fmt.Sprintf("%s（复制失败: %v）", skill.Name, err))
			continue
		}
		skill.SourcePath = dst
		reg.Skills[skill.Name] = skill
		imported = append(imported, skill)
	}

	if len(imported) > 0 {
		if err := s.saveRegistryLocked(reg); err != nil {
			return nil, err
		}
		s.info("导入公共 skill", fmt.Sprintf("成功 %d 个，跳过 %d 个", len(imported), len(skipped)))
	}
	if len(skipped) > 0 {
		return imported, fmt.Errorf("部分 skill 未导入: %s", strings.Join(skipped, "、"))
	}
	return imported, nil
}

// collectSkillDirs BFS 收集含 SKILL.md 的目录：命中目录收编且不再深入其
// 子树（最浅层命中，忽略更深层嵌套）；maxDepth 为相对 root 的扫描深度上限。
func collectSkillDirs(root string, maxDepth int) ([]string, error) {
	var out []string
	type item struct {
		dir   string
		depth int
	}
	queue := []item{{dir: root, depth: 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if hasSkillMD(cur.dir) {
			out = append(out, cur.dir)
			continue // 命中：其子树（可能嵌套更多 skill）忽略
		}
		if cur.depth >= maxDepth {
			continue
		}
		entries, err := os.ReadDir(cur.dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("读取目录 %s: %w", cur.dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				queue = append(queue, item{dir: filepath.Join(cur.dir, entry.Name()), depth: cur.depth + 1})
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// hasSkillMD 报告目录是否含 SKILL.md（大小写敏感，与各 CLI 惯例一致）。
func hasSkillMD(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil && !info.IsDir() && info.Mode().IsRegular()
}

// readSkillMeta 读取 skill 目录的元数据：SKILL.md 必须存在且 ≤1MB；
// frontmatter name/description 优先，name 缺省/非法回退目录名，
// description 缺省取正文第一段。
func readSkillMeta(dir string) (SharedSkill, error) {
	mdPath := filepath.Join(dir, "SKILL.md")
	info, err := os.Stat(mdPath)
	if err != nil {
		return SharedSkill{}, fmt.Errorf("缺少 SKILL.md: %w", err)
	}
	if !info.Mode().IsRegular() {
		return SharedSkill{}, errors.New("SKILL.md 不是普通文件")
	}
	if info.Size() > skillMDMaxBytes {
		return SharedSkill{}, fmt.Errorf("SKILL.md 超过 1MB 上限（%d 字节）", info.Size())
	}
	content, err := os.ReadFile(mdPath)
	if err != nil {
		return SharedSkill{}, fmt.Errorf("读取 SKILL.md: %w", err)
	}

	meta := parseFrontmatter(string(content))
	dirName := filepath.Base(dir)

	name := strings.TrimSpace(meta["name"])
	if !validName(name) {
		name = dirName // 坏 frontmatter（无闭合 ---、非 map 等）→ 回退目录名
	}
	if !validName(name) {
		return SharedSkill{}, fmt.Errorf("skill 名 %q 不可用作目录名", dirName)
	}
	description := strings.TrimSpace(meta["description"])
	if description == "" {
		description = extractFirstParagraph(string(content))
	}

	return SharedSkill{
		Name:        name,
		Description: description,
		Origin:      "manual",
		Enabled:     false,
		CreatedAt:   nowRFC3339(),
	}, nil
}

// safeUnzip 安全解包 zip 到 dest：路径穿越（../、绝对路径、盘符）→ 拒绝整个
// 导入；符号链接条目跳过；总大小/文件数/单文件超限拒绝。
func safeUnzip(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开 zip 失败: %w", err)
	}
	defer func() { _ = r.Close() }()

	var total int64
	written := int64(0)
	count := 0
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			continue // 符号链接条目跳过
		}
		name := filepath.ToSlash(f.Name)
		if err := safeZipEntryName(name); err != nil {
			return err
		}
		count++
		if count > zipMaxFileCount {
			return fmt.Errorf("zip 文件数超过上限 %d，拒绝导入", zipMaxFileCount)
		}
		if int64(f.UncompressedSize64) > zipMaxFileBytes {
			return fmt.Errorf("zip 内文件 %s 超过单文件上限 %dMB，拒绝导入", name, zipMaxFileBytes>>20)
		}
		total += int64(f.UncompressedSize64)
		if total > zipMaxTotalBytes {
			return fmt.Errorf("zip 总解包大小超过上限 %dMB，拒绝导入", zipMaxTotalBytes>>20)
		}

		target := filepath.Join(dest, filepath.FromSlash(name))
		n, err := extractZipEntry(f, target)
		if err != nil {
			return err
		}
		written += n
		if written > zipMaxTotalBytes {
			// header 谎报兑底：实际解压字节总量超限同样拒绝。
			return fmt.Errorf("zip 实际解包总量超过上限 %dMB，拒绝导入", zipMaxTotalBytes>>20)
		}
	}
	return nil
}

// safeZipEntryName 校验单个 entry 名：拒绝 `..` 段、绝对路径与 Windows 盘符。
func safeZipEntryName(name string) error {
	if name == "" {
		return errors.New("zip 内含空路径 entry，拒绝导入")
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return fmt.Errorf("zip entry %q 使用绝对路径，拒绝导入", name)
	}
	if len(name) >= 2 && name[1] == ':' && isASCIILetter(name[0]) {
		return fmt.Errorf("zip entry %q 使用盘符路径，拒绝导入", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return fmt.Errorf("zip entry %q 含 .. 路径穿越，拒绝导入", name)
		}
	}
	return nil
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// extractZipEntry 解压单个文件 entry 到 target（目录 0755、文件 0644；
// 实际解压字节流再校验一次大小，防 header 谎报）；返回实际写入字节数。
func extractZipEntry(f *zip.File, target string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, fmt.Errorf("创建目录 %s: %w", filepath.Dir(target), err)
	}
	src, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("打开 zip entry %s: %w", f.Name, err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, fmt.Errorf("创建文件 %s: %w", target, err)
	}
	n, err := io.Copy(dst, io.LimitReader(src, zipMaxFileBytes+1))
	cerr := dst.Close()
	if err != nil {
		return 0, fmt.Errorf("解压 %s: %w", f.Name, err)
	}
	if cerr != nil {
		return 0, fmt.Errorf("写入 %s: %w", target, cerr)
	}
	if n > zipMaxFileBytes {
		return 0, fmt.Errorf("zip entry %s 实际大小超过单文件上限，拒绝导入", f.Name)
	}
	return n, nil
}

// DeleteSharedSkill 删除一条公共 skill（仅 manual 来源；插件来源不入本表）。
// 顺序：先按 Deployed 清单从各 CLI 回收，再删仓库目录与 registry 条目。
func (s *Service) DeleteSharedSkill(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, err := s.loadRegistryLocked()
	if err != nil {
		return err
	}
	skill, ok := reg.Skills[name]
	if !ok {
		return fmt.Errorf("公共 skill %q 不存在", name)
	}

	if len(skill.Deployed) > 0 {
		if err := s.undeploySkillLocked(&skill); err != nil {
			return fmt.Errorf("回收 skill %s 失败: %w", name, err)
		}
	}
	if skill.SourcePath != "" {
		if err := os.RemoveAll(skill.SourcePath); err != nil {
			return fmt.Errorf("删除 skill 仓库目录 %s: %w", skill.SourcePath, err)
		}
	}
	delete(reg.Skills, name)
	if err := s.saveRegistryLocked(reg); err != nil {
		return err
	}
	s.info("删除公共 skill", name)
	return nil
}

// SetSharedSkillEnabled 启用/停用一条公共 skill。
// enabled=true → 分发到 Targets 全部 CLI（重复启用 = 重新复制覆盖，幂等）；
// enabled=false → 回收全部 Deployed。
func (s *Service) SetSharedSkillEnabled(name string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, err := s.loadRegistryLocked()
	if err != nil {
		return err
	}
	skill, ok := reg.Skills[name]
	if !ok {
		return fmt.Errorf("公共 skill %q 不存在", name)
	}

	if enabled {
		targets := skill.Targets
		if len(targets) == 0 {
			// 未勾选目标时不分发，仅记录启停状态。
			skill.Enabled = true
			reg.Skills[name] = skill
			return s.saveRegistryLocked(reg)
		}
		deployErr := s.deploySkillLocked(&skill, targets)
		// M1：部分失败时已成功引擎的 Deployed 增量（deploy 循环内逐引擎
		// 追加）仍先落盘，再聚合返回错误——防止「磁盘已写、registry 未记」
		// 的永久残留（禁用/删除按 Deployed 回收不掉）。Enabled 记录实际
		// 部署状态：全部失败且从未部署过任何引擎才停用。
		skill.Enabled = deployErr == nil || len(skill.Deployed) > 0
		if skill.Enabled {
			skill.DeployedAt = nowRFC3339()
		}
		reg.Skills[name] = skill
		if err := s.saveRegistryLocked(reg); err != nil {
			return err
		}
		return deployErr
	}
	undeployErr := s.undeploySkillLocked(&skill)
	// M1：部分回收失败也先落盘（已回收引擎移出 Deployed，失败者保留待重试）。
	skill.Enabled = false
	reg.Skills[name] = skill
	if err := s.saveRegistryLocked(reg); err != nil {
		return err
	}
	return undeployErr
}

// SetSharedSkillTargets 更新 skill 的目标引擎并 diff 分发：
// 新增目标复制、移除目标回收；Enabled=false 时只存不分发。
func (s *Service) SetSharedSkillTargets(name string, targets []Engine) error {
	normalized, err := normalizeEngines(targets)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	reg, err := s.loadRegistryLocked()
	if err != nil {
		return err
	}
	skill, ok := reg.Skills[name]
	if !ok {
		return fmt.Errorf("公共 skill %q 不存在", name)
	}

	old := skill.Targets
	skill.Targets = normalized

	if skill.Enabled {
		// diff：新增目标分发、移除目标回收（Deployed 是实际状态依据）。
		var add []Engine
		for _, e := range normalized {
			if !containsEngine(old, e) {
				add = append(add, e)
			}
		}
		var remove []Engine
		for _, e := range old {
			if !containsEngine(normalized, e) {
				remove = append(remove, e)
			}
		}
		var opErrs []error
		if len(remove) > 0 {
			if err := s.undeploySkillEnginesLocked(&skill, remove); err != nil {
				opErrs = append(opErrs, err)
			}
		}
		if len(add) > 0 {
			if err := s.deploySkillLocked(&skill, add); err != nil {
				opErrs = append(opErrs, err)
			}
		}
		reg.Skills[name] = skill
		if err := s.saveRegistryLocked(reg); err != nil {
			return err
		}
		// M1：部分失败先落盘实际 Deployed 状态，再返回聚合错误。
		return joinErrors(opErrs)
	}
	reg.Skills[name] = skill
	return s.saveRegistryLocked(reg)
}
