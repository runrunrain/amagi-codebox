// import.go — zip/本地文件夹导入（canonical 共用技能纵切）：把外部技能目录
// 导入 ~/.agents/skills-disabled/<目录名>（默认停用为保守策略：不自动分发到
// 各 CLI 私有目录，用户在面板显式启用后才对读取 ~/.agents/skills 的 CLI 生效）。
//
// 校验能力对齐 internal/sharedext 导入实现（skills.go 的 safeUnzip /
// collectSkillDirs / zip 上限常量——该包由并行线程负责退役改造，此处按同一
// 语义独立实现，不 import、不修改其文件）：
//   - 路径穿越（../ 段、绝对路径、Windows 盘符）→ 拒绝整个 zip；
//   - 符号链接：zip 内链接条目跳过；目录导入时链接子目录不收编、链接文件
//     不复制、链接的 SKILL.md 视为缺失（内容受控原则）；
//   - 上限：单文件 10MB、SKILL.md 1MB、单技能目录总量 50MB / 2000 文件
//     （复制与解包同样强制——zip 以 header 与实际解压字节双重校验）。
//
// 与 sharedext 导入的语义差异（有意）：
//   - 技能标识 = 目录名（与 List/SetEnabled/Delete 的稳定标识一致），
//     frontmatter name 仅作 DisplayName 回退展示；
//   - 不覆盖同名条目：启用根 / 禁用根任一存在同名，或名字为托管名
//     amagi-codebox，一律跳过并在 skipped 里说明；
//   - 部分成功不返回 error：imported/skipped 双列表如实返回，仅整体失败
//     （一个都没导入）才返回 error（App 信封无需解析错误文本）。

package sharedskills

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 导入安全上限（数值对齐 internal/sharedext skills.go 的 zip 上限）。
const (
	importMaxTotalBytes = 50 << 20 // 单个技能目录解包/复制总量 50MB
	importMaxFileCount  = 2000     // 单个技能目录文件数（不含目录）
	importMaxFileBytes  = 10 << 20 // 单文件 10MB
	importSkillMDBytes  = 1 << 20  // SKILL.md 1MB
	importScanMaxDepth  = 4        // 收集技能目录的防御性深度上限（相对扫描根）
)

// marketProvenanceMarker 是共用市场部署写入技能目录的隐藏来源凭据文件名
// （字面对齐 internal/sharedmarket/provenance.go 的 markerFileName——该包由
// 并行线程负责，不 import 不修改；文件名是稳定契约，凭据格式演进走其
// 内部 version 字段）。手动导入不得带入此类 CodeBox 私有市场元数据
// （Leader steer #1）：zip 解包与目录复制两条路径都在入口处剥离该文件，
// 防止伪造凭据把手动条目伪装成市场部署（误导卸载认领/来源判定）。
const marketProvenanceMarker = ".amagi-market-provenance.json"

// skillCandidate 是一个待导入的技能目录候选：dir 为源目录（zip 的临时解包
// 目录或用户选择的目录树下），name 为导入后的目录名（稳定标识）。
type skillCandidate struct {
	dir  string
	name string
}

// ImportFromZip 从 zip 压缩包导入技能：安全解包到系统临时目录后统一扫描
// 导入（临时目录 defer 清理，绝不落在 ~/.agents）。顶层直接放 SKILL.md（无
// 目录包裹）的 zip 以 zip 文件名去扩展名作为技能目录名（不可用时回退
// frontmatter name）；其余形态按「最浅层命中含 SKILL.md 的子目录」收集。
func (s *Service) ImportFromZip(zipPath string) (imported []Skill, skipped []string, err error) {
	tmp, err := os.MkdirTemp("", "sharedskills-unzip-*")
	if err != nil {
		return nil, nil, fmt.Errorf("创建临时目录失败：%w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := safeUnzip(zipPath, tmp); err != nil {
		return nil, nil, err
	}
	if hasRegularSkillMD(tmp) {
		name := zipTopLevelSkillName(zipPath, tmp)
		if name == "" {
			return nil, nil, errors.New("压缩包顶层为 SKILL.md，但 zip 文件名与 frontmatter name 均不可用作技能目录名")
		}
		return s.importCandidates([]skillCandidate{{dir: tmp, name: name}})
	}
	cands, err := collectSkillCandidates(tmp, importScanMaxDepth)
	if err != nil {
		return nil, nil, err
	}
	return s.importCandidates(cands)
}

// ImportFromDir 从本地文件夹导入技能：文件夹自身含（普通文件）SKILL.md →
// 视为单个技能（目录名即技能名）；否则扫描子目录收集（最浅层命中）。导入
// 路径本身是符号链接时拒绝。
func (s *Service) ImportFromDir(dirPath string) (imported []Skill, skipped []string, err error) {
	fi, err := os.Lstat(dirPath)
	if err != nil {
		return nil, nil, fmt.Errorf("访问导入目录失败：%w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("导入路径不能是符号链接：%s", dirPath)
	}
	if !fi.IsDir() {
		return nil, nil, fmt.Errorf("导入路径不是目录：%s", dirPath)
	}
	if hasRegularSkillMD(dirPath) {
		name := filepath.Base(filepath.Clean(dirPath))
		if !validName(name) {
			return nil, nil, fmt.Errorf("目录名 %q 不可用作技能目录名（不允许为空、隐藏目录或包含路径分隔符）", name)
		}
		return s.importCandidates([]skillCandidate{{dir: dirPath, name: name}})
	}
	cands, err := collectSkillCandidates(dirPath, importScanMaxDepth)
	if err != nil {
		return nil, nil, err
	}
	return s.importCandidates(cands)
}

// importCandidates 批量导入候选：先整体预检（名称合法性 / 托管名 / 两根
// 同名 / 批内重名），全部被跳过时不创建任何目录；有可导入项才创建禁用根
// 并逐个经「暂存目录 + rename」落盘（原子性：rename 前复核目标不存在，防
// Unix rename 静默覆盖空目录；失败清理暂存目录）。返回值语义见文件头。
func (s *Service) importCandidates(cands []skillCandidate) ([]Skill, []string, error) {
	sort.Slice(cands, func(i, j int) bool { return cands[i].name < cands[j].name })

	type planned struct{ c skillCandidate }
	var plan []planned
	var skipped []string
	seen := map[string]bool{} // 批内重名：仅首个进入计划
	for _, c := range cands {
		switch {
		case !validName(c.name):
			skipped = append(skipped, fmt.Sprintf("%s（目录名不可用作技能名）", c.name))
			continue
		case c.name == ManagedSkillName:
			skipped = append(skipped, fmt.Sprintf("%s（%s）", c.name, managedSharedSkillMessage))
			continue
		case seen[c.name]:
			skipped = append(skipped, fmt.Sprintf("%s（本次导入中重名，仅导入首个）", c.name))
			continue
		}
		taken, terr := s.nameTaken(c.name)
		if terr != nil {
			skipped = append(skipped, fmt.Sprintf("%s（检查同名目录失败：%v）", c.name, terr))
			continue
		}
		if taken {
			skipped = append(skipped, fmt.Sprintf("%s（同名已存在（启用或停用），拒绝覆盖）", c.name))
			continue
		}
		if !hasRegularSkillMD(c.dir) { // 收集器已保证；防御性复核
			skipped = append(skipped, fmt.Sprintf("%s（缺少普通文件 SKILL.md）", c.name))
			continue
		}
		seen[c.name] = true
		plan = append(plan, planned{c})
	}

	if len(plan) == 0 {
		if len(skipped) == 0 {
			return nil, nil, errors.New("没有发现可导入的技能目录（须含 SKILL.md）")
		}
		return nil, skipped, fmt.Errorf("没有可导入的技能：%s", strings.Join(skipped, "、"))
	}

	// 落点根此时才创建；记录是否本次新建（全部导入失败时回滚删除空根）。
	dstRoot := s.disabledRoot()
	_, rerr := os.Lstat(dstRoot)
	rootMissing := errors.Is(rerr, fs.ErrNotExist)
	if rerr != nil && !rootMissing {
		return nil, skipped, fmt.Errorf("检查目录失败：%s（%v）", dstRoot, rerr)
	}
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return nil, skipped, fmt.Errorf("创建目录失败：%s（%v）", dstRoot, err)
	}

	var imported []Skill
	for _, p := range plan {
		if reason := s.importOne(p.c, dstRoot); reason != nil {
			skipped = append(skipped, *reason)
			continue
		}
		imported = append(imported, s.inspect(p.c.name, dstRoot, false))
	}

	if len(imported) == 0 {
		if rootMissing {
			// 回滚：本次新建的禁用根仍为空目录时尽力删除（非空天然失败，
			// 失败仅记日志，不掩盖原错误）。
			if rmErr := os.Remove(dstRoot); rmErr != nil && s.log != nil {
				s.log.Debug("sharedskills", "回滚清理新建目录失败（忽略）", "dir="+dstRoot, "err="+rmErr.Error())
			}
		}
		if len(skipped) > 0 {
			return nil, skipped, fmt.Errorf("导入失败：%s", strings.Join(skipped, "、"))
		}
		return nil, nil, errors.New("导入失败：未知错误")
	}
	if skipped == nil {
		skipped = []string{}
	}
	return imported, skipped, nil
}

// importOne 落盘单个技能：复制到禁用根下的隐藏暂存目录（.importing-*，
// List 会跳过隐藏目录，导入中途不显形），复核目标不存在后 rename 到最终
// 名。失败返回跳过原因（调用方聚合），暂存目录已清理。
func (s *Service) importOne(c skillCandidate, dstRoot string) *string {
	staging, err := os.MkdirTemp(dstRoot, ".importing-")
	if err != nil {
		reason := fmt.Sprintf("%s（创建暂存目录失败：%v）", c.name, err)
		return &reason
	}
	fail := func(format string, args ...any) *string {
		_ = os.RemoveAll(staging)
		reason := fmt.Sprintf("%s（%s）", c.name, fmt.Sprintf(format, args...))
		if s.log != nil {
			s.log.Debug("sharedskills", "技能导入失败", "skill="+c.name, "reason="+reason)
		}
		return &reason
	}
	if err := copySkillTree(c.dir, staging); err != nil {
		return fail("%v", err)
	}
	final := filepath.Join(dstRoot, c.name)
	// rename 前复核：预检后目标可能被并发放入（Unix rename 会静默覆盖空
	// 目录），存在即拒绝，绝不覆盖。
	if _, derr := os.Lstat(final); derr == nil {
		return fail("同名目录已存在，拒绝覆盖")
	} else if !errors.Is(derr, fs.ErrNotExist) {
		return fail("检查目标目录失败：%v", derr)
	}
	if err := renameDir(staging, final); err != nil {
		return fail("落盘失败：%v", err)
	}
	if s.log != nil {
		s.log.Info("sharedskills", "导入共用技能（默认停用）", fmt.Sprintf("name=%s from=%s", c.name, c.dir))
	}
	return nil
}

// nameTaken 检查 name 是否已存在于启用根或禁用根（同名条目拒绝覆盖）。
func (s *Service) nameTaken(name string) (bool, error) {
	for _, root := range []string{s.SkillsRoot(), s.disabledRoot()} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			return true, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("读取目录信息失败：%s（%v）", filepath.Join(root, name), err)
		}
	}
	return false, nil
}

// managedSharedSkillMessage 是导入面拒绝托管名的统一文案（与 App 门面
// SetEnabled/Delete 的拒绝文案同义；App 层文案见 app_sharedskills.go）。
const managedSharedSkillMessage = "amagi-codebox 为应用托管技能名，保留"

// zipTopLevelSkillName 推导「顶层直接放 SKILL.md」zip 的技能目录名：zip
// 文件名去扩展名优先，不可用时回退 frontmatter name；两者都不可用返回空。
func zipTopLevelSkillName(zipPath, dir string) string {
	base := filepath.Base(zipPath)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if validName(base) {
		return base
	}
	meta, hasMD, err := readSkillMetadata(dir)
	if err == nil && hasMD && validName(meta.Name) {
		return meta.Name
	}
	return ""
}

// collectSkillCandidates BFS 收集含 SKILL.md 的技能目录（最浅层命中：命中
// 目录收编且不再深入其子树）。链接目录不收编也不深入；隐藏目录不可能成为
// 合法技能名，直接剪枝。maxDepth 为相对 root 的深度上限（root 自身由调用
// 方先行判定，收集结果只含 root 的后代）。
func collectSkillCandidates(root string, maxDepth int) ([]skillCandidate, error) {
	var out []skillCandidate
	type item struct {
		dir   string
		depth int
	}
	queue := []item{{dir: root, depth: 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		entries, err := os.ReadDir(cur.dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("读取目录失败：%s（%w）", cur.dir, err)
		}
		for _, e := range entries {
			if !e.IsDir() || e.Type()&os.ModeSymlink != 0 || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(cur.dir, e.Name())
			if hasRegularSkillMD(p) {
				out = append(out, skillCandidate{dir: p, name: e.Name()})
				continue // 命中：其子树（可能嵌套更多技能）忽略
			}
			queue = append(queue, item{dir: p, depth: cur.depth + 1})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("未发现任何含 SKILL.md 的技能目录（路径: %s）", root)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// hasRegularSkillMD 报告目录是否含普通文件 SKILL.md（Lstat：链接的
// SKILL.md 不是普通文件，视为缺失——导入面拒绝链接）。
func hasRegularSkillMD(dir string) bool {
	fi, err := os.Lstat(filepath.Join(dir, "SKILL.md"))
	return err == nil && fi.Mode().IsRegular()
}

// ---- 有界复制（目录导入路径；zip 导入的候选在解包时已受同等上限约束， ----
// ---- 复制自临时目录时同样走这里，双保险）                                  ----

// importBudget 是单技能目录复制的预算（文件数与总字节数）。
type importBudget struct {
	files int
	total int64
}

// copySkillTree 有界复制技能目录树到 dst（dst 已存在）：目录 0755、文件
// 0644；符号链接与非普通文件（socket/device 等）跳过；超限返回错误。
func copySkillTree(src, dst string) error {
	var b importBudget
	return copyTreeBounded(src, dst, &b)
}

func copyTreeBounded(src, dst string, b *importBudget) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("读取源目录失败：%s（%w）", src, err)
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			continue // 符号链接跳过（内容受控原则）
		}
		name := e.Name()
		if strings.EqualFold(name, marketProvenanceMarker) {
			continue // CodeBox 私有市场凭据不得随手动导入带入（steer #1）
		}
		switch {
		case e.IsDir():
			if err := os.MkdirAll(filepath.Join(dst, name), 0o755); err != nil {
				return fmt.Errorf("创建目录失败：%s（%w）", filepath.Join(dst, name), err)
			}
			if err := copyTreeBounded(filepath.Join(src, name), filepath.Join(dst, name), b); err != nil {
				return err
			}
		case e.Type().IsRegular():
			if err := copyFileBounded(filepath.Join(src, name), filepath.Join(dst, name), name, b); err != nil {
				return err
			}
		}
		// 其余类型（socket/device/irregular 等）跳过。
	}
	return nil
}

// copyFileBounded 有界复制单文件：先按声明的 size 预检（清晰错误），拷贝
// 时以 LimitReader 兜底（size 谎报/竞态），实拷字节计入总量预算。
func copyFileBounded(src, dst, name string, b *importBudget) error {
	b.files++
	if b.files > importMaxFileCount {
		return fmt.Errorf("文件数超过上限 %d", importMaxFileCount)
	}
	limit := int64(importMaxFileBytes)
	if name == "SKILL.md" {
		limit = importSkillMDBytes
	}
	if fi, err := os.Stat(src); err == nil && fi.Size() > limit {
		return fmt.Errorf("文件 %s 超过 %dMB 上限", name, limit>>20)
	}
	sfp, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("读取 %s 失败：%w", name, err)
	}
	defer func() { _ = sfp.Close() }()
	dfp, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("创建 %s 失败：%w", dst, err)
	}
	n, err := io.Copy(dfp, io.LimitReader(sfp, limit+1))
	cerr := dfp.Close()
	if err != nil {
		return fmt.Errorf("复制 %s 失败：%w", name, err)
	}
	if cerr != nil {
		return fmt.Errorf("写入 %s 失败：%w", dst, cerr)
	}
	if n > limit {
		return fmt.Errorf("文件 %s 实际大小超过 %dMB 上限", name, limit>>20)
	}
	b.total += n
	if b.total > importMaxTotalBytes {
		return fmt.Errorf("技能目录总大小超过 %dMB 上限", importMaxTotalBytes>>20)
	}
	return nil
}

// ---- 安全解包（语义与 internal/sharedext skills.go 的 safeUnzip 一致） ----

// safeUnzip 安全解包 zip 到 dest：路径穿越（../ 段、绝对路径、盘符）→ 拒绝
// 整个导入；符号链接条目跳过；总大小 / 文件数 / 单文件超限拒绝（header 与
// 实际解压字节双重校验，防 header 谎报）。
func safeUnzip(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开 zip 失败：%w", err)
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
		if strings.EqualFold(zipEntryBase(name), marketProvenanceMarker) {
			continue // CodeBox 私有市场凭据不得随手动导入带入（steer #1，任意深度）；穿越校验先行
		}
		count++
		if count > importMaxFileCount {
			return fmt.Errorf("zip 文件数超过上限 %d，拒绝导入", importMaxFileCount)
		}
		if int64(f.UncompressedSize64) > importMaxFileBytes {
			return fmt.Errorf("zip 内文件 %s 超过单文件上限 %dMB，拒绝导入", name, importMaxFileBytes>>20)
		}
		if name == "SKILL.md" && int64(f.UncompressedSize64) > importSkillMDBytes {
			return fmt.Errorf("SKILL.md 超过 %dMB 上限，拒绝导入", importSkillMDBytes>>20)
		}
		total += int64(f.UncompressedSize64)
		if total > importMaxTotalBytes {
			return fmt.Errorf("zip 总解包大小超过上限 %dMB，拒绝导入", importMaxTotalBytes>>20)
		}
		n, xerr := extractZipEntry(f, filepath.Join(dest, filepath.FromSlash(name)))
		if xerr != nil {
			return xerr
		}
		written += n
		if written > importMaxTotalBytes {
			// header 谎报兜底：实际解压字节总量超限同样拒绝。
			return fmt.Errorf("zip 实际解包总量超过上限 %dMB，拒绝导入", importMaxTotalBytes>>20)
		}
	}
	return nil
}

// zipEntryBase 取 slash 形态 entry 名的末段（zip entry 名恒为 slash 形态，
// 用字符串切分而非 filepath，避免 Windows 反斜杠语义）。
func zipEntryBase(slashName string) string {
	if i := strings.LastIndex(slashName, "/"); i >= 0 {
		return slashName[i+1:]
	}
	return slashName
}

// safeZipEntryName 校验单个 entry 名：拒绝空路径、`..` 段、绝对路径与
// Windows 盘符。
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

// extractZipEntry 解压单个文件 entry 到 target（目录 0755、文件 0644）。
// 解压时以 LimitReader 限制单文件上限，返回实际写入字节数（调用方计入
// 总量预算，兜底 header 谎报）。
func extractZipEntry(f *zip.File, target string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, fmt.Errorf("创建目录失败：%s（%w）", filepath.Dir(target), err)
	}
	src, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("打开 zip entry %s 失败：%w", f.Name, err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, fmt.Errorf("创建文件失败：%s（%w）", target, err)
	}
	n, err := io.Copy(dst, io.LimitReader(src, importMaxFileBytes+1))
	cerr := dst.Close()
	if err != nil {
		return 0, fmt.Errorf("解压 %s 失败：%w", f.Name, err)
	}
	if cerr != nil {
		return 0, fmt.Errorf("写入 %s 失败：%w", target, cerr)
	}
	if n > importMaxFileBytes {
		return 0, fmt.Errorf("zip entry %s 实际大小超过单文件上限，拒绝导入", f.Name)
	}
	return n, nil
}
