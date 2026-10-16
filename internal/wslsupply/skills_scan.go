package wslsupply

// skills_scan.go — Windows ~/.agents/skills 的有界扫描与静态兼容检查（P3-S3，
// design §四共享 ~/.agents 表 skills/ 行 + §八批次 S3）。
//
// 扫描以 skill 目录为最小单元：每个顶层目录产出一份目录内容清单（相对路径、
// 类型、大小、hash），作为增量基线。扫描有界（深度/单 skill 文件数/单文件字节/
// 总文件数上限，可配），超限的 skill 标记 Incomplete——「扫描未完成」不冒充
// 完成，该 skill 不可选入供给。
//
// 静态兼容检查（只查不执行——禁止自动运行安装/验证脚本）：
//   - 绝对链接（含盘符/UNC 形态）→ absolute-link，不复制；
//   - 解析目标不在本 skill 树内（含跨 skill 链接）→ out-of-tree-link，不复制；
//   - 链接环/超长链 → cyclic-link，不复制；
//   - 指向目录的链接 → directory-link-unsupported（未支持，不复制）；
//   - Windows 专用脚本 .ps1/.cmd/.bat → windows-script（明确「未支持」而非
//     原样复制）；.exe/.dll/.msi/.com → windows-binary 同样不复制；
//   - 超过单文件字节上限 → oversize-file（无法安全供给）；
//   - 非常规形态（reparse/junction/非常规位）→ non-regular-entry（无法保留）。
// 本 skill 树内解析为常规文件的相对链接可移植：目标侧实体化为常规文件（9P 从
// Windows 建符号链接不可靠，实体化保证消费者可直接发现内容）。
//
// 排除物（不阻断、不复制、预览可见）：.git、node_modules、缓存目录、凭据文件
//（.env/*.pem/*.key 等）、会话产物（sessions/）。排除 ≠ 不兼容：排除项不是
// 功能内容；不兼容项会让整个 skill 不入供给（不产半残 skill）。

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillsScanLimits 是有界扫描的上限（可配；零值字段由 WithDefaults 补默认）。
type SkillsScanLimits struct {
	MaxDepth         int   // skill 内目录深度上限（skill 根=0）
	MaxFilesPerSkill int   // 单 skill 登记条目上限
	MaxFileBytes     int64 // 单文件字节上限（超出 → oversize-file）
	MaxTotalFiles    int   // 整个 skills 根登记条目总上限（超出 → 扫描未完成）
}

// DefaultSkillsScanLimits 返回默认上限（大目录后台分批扫描仍能收敛）。
func DefaultSkillsScanLimits() SkillsScanLimits {
	return SkillsScanLimits{
		MaxDepth:         8,
		MaxFilesPerSkill: 4000,
		MaxFileBytes:     16 << 20,
		MaxTotalFiles:    60000,
	}
}

func (l SkillsScanLimits) withDefaults() SkillsScanLimits {
	if l.MaxDepth <= 0 {
		l.MaxDepth = 8
	}
	if l.MaxFilesPerSkill <= 0 {
		l.MaxFilesPerSkill = 4000
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = 16 << 20
	}
	if l.MaxTotalFiles <= 0 {
		l.MaxTotalFiles = 60000
	}
	return l
}

// 不兼容原因码（App 层据此映射中文说明）。
const (
	SkillIncompatAbsoluteLink   = "absolute-link"
	SkillIncompatOutOfTreeLink  = "out-of-tree-link"
	SkillIncompatCyclicLink     = "cyclic-link"
	SkillIncompatDirLink        = "directory-link-unsupported"
	SkillIncompatWindowsScript  = "windows-script"
	SkillIncompatWindowsBinary  = "windows-binary"
	SkillIncompatOversizeFile   = "oversize-file"
	SkillIncompatNonRegularFile = "non-regular-entry"
)

// skillIncompatReasonText 是原因码的中文说明（脱敏：不含链接目标正文）。
func skillIncompatReasonText(code string) string {
	switch code {
	case SkillIncompatAbsoluteLink:
		return "绝对路径符号链接（不可移植，未复制）"
	case SkillIncompatOutOfTreeLink:
		return "链接解析到本 skill 目录之外（越界链接，未复制）"
	case SkillIncompatCyclicLink:
		return "循环/超长符号链接（未复制）"
	case SkillIncompatDirLink:
		return "指向目录的符号链接（未支持，未复制）"
	case SkillIncompatWindowsScript:
		return "Windows 专用脚本（.ps1/.cmd/.bat，未支持，未复制）"
	case SkillIncompatWindowsBinary:
		return "Windows 二进制（.exe/.dll 等，不可在 Linux 运行，未复制）"
	case SkillIncompatOversizeFile:
		return "超过单文件字节上限（无法安全供给）"
	case SkillIncompatNonRegularFile:
		return "非常规文件形态（reparse/junction 等，无法保留）"
	default:
		return "不兼容项：" + code
	}
}

// 排除物名单（不阻断供给，仅记录）。
var (
	skillsExcludedDirNames = map[string]bool{
		".git": true, "node_modules": true, "__pycache__": true, ".cache": true,
		".pytest_cache": true, ".mypy_cache": true, ".venv": true, "venv": true,
		".tox": true, ".idea": true, "sessions": true,
	}
	skillsExcludedFileNames = map[string]bool{
		".DS_Store": true, ".env": true, ".env.local": true, ".env.production": true,
		"credentials.json": true, "secrets.json": true,
		"id_rsa": true, "id_ed25519": true, ".npmrc": true, ".netrc": true,
	}
	skillsExcludedFileExts = map[string]bool{
		".pem": true, ".key": true, ".pfx": true, ".p12": true, ".crt": true,
	}
	skillsWindowsScriptExts = map[string]bool{".ps1": true, ".cmd": true, ".bat": true}
	skillsWindowsBinaryExts = map[string]bool{".exe": true, ".dll": true, ".msi": true, ".com": true}
)

// SkillIncompat 是一条不兼容项（相对路径 + 原因码 + 中文说明）。
type SkillIncompat struct {
	RelPath string
	Code    string
	Reason  string
}

// SkillFileEntry 是清单中的可移植条目（实体化视角：链接条目的 Hash 是解析目标
// 文件内容的 hash；LinkTarget 非空表示该条目来自符号链接）。
type SkillFileEntry struct {
	RelPath    string `json:"rel"`
	Size       int64  `json:"size"`
	Hash       string `json:"hash"`
	LinkTarget string `json:"linkTarget,omitempty"`
	// srcAbs 是 Apply 阶段读取字节的主机绝对路径（内存态，不序列化不出包）。
	srcAbs string
}

// SkillManifest 是单 skill 的扫描产物。
type SkillManifest struct {
	Name             string
	Files            []SkillFileEntry // 供词序按 RelPath 排序
	Excluded         []string         // 排除物相对路径（目录带尾 /）
	Incompatible     []SkillIncompat
	Incomplete       bool
	IncompleteReason string
	TotalBytes       int64
	SourceHash       string // 规范化聚合 hash（与目标树 hash 同构：同步即相等）
}

// Portable 报告该 skill 是否可整体供给（扫描完成、无不兼容项、有内容）。
// 含不兼容项的 skill 整体不入供给——不产半残 skill；空目录同样不可供给。
func (m *SkillManifest) Portable() bool {
	return m != nil && !m.Incomplete && len(m.Incompatible) == 0 && len(m.Files) > 0
}

// NotPortableReason 返回不可移植的原因码（便携时空串）：scan-incomplete /
// incompatible-items / empty。
func (m *SkillManifest) NotPortableReason() string {
	if m == nil {
		return "scan-incomplete"
	}
	switch {
	case m.Incomplete:
		return "scan-incomplete"
	case len(m.Incompatible) > 0:
		return "incompatible-items"
	case len(m.Files) == 0:
		return "empty"
	}
	return ""
}

// skillTreeHash 是清单/目标树的统一规范化 hash：按 RelPath 排序后串联
// rel + size + bytes-hash。来源与目标用同一函数，同步状态 ⇔ 两 hash 相等。
func skillTreeHash(files []SkillFileEntry) string {
	sorted := make([]SkillFileEntry, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].RelPath < sorted[j].RelPath })
	var b strings.Builder
	for _, f := range sorted {
		b.WriteString(f.RelPath)
		b.WriteByte(0)
		b.WriteString(fmt.Sprintf("%d", f.Size))
		b.WriteByte(0)
		b.WriteString(f.Hash)
		b.WriteByte('\n')
	}
	return hexHash([]byte(b.String()))
}

// errSkillLinkCycle 表示链接解析成环或超过跳数上限；errSkillLinkAbsolute
// 表示链中段出现绝对形态链接（调用方映射为 absolute-link）。
var (
	errSkillLinkCycle    = errors.New("skill symlink chain is cyclic or too deep")
	errSkillLinkAbsolute = errors.New("skill symlink chain hits an absolute link")
)

// isAbsoluteLikeLinkTarget 报告链接目标是否为绝对形态（Unix 绝对 / Windows
// 盘符 / UNC 前缀）。
func isAbsoluteLikeLinkTarget(t string) bool {
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "/") || strings.HasPrefix(t, `\\`) || strings.HasPrefix(t, "//") {
		return true
	}
	// Windows 盘符形态（C:\… 或 C:…），大小写不敏感。
	if len(t) >= 2 && t[1] == ':' {
		c := t[0]
		return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
	}
	return false
}

// extOf 返回小写扩展名（含点）。
func extOf(name string) string { return strings.ToLower(filepath.Ext(name)) }

// scanSkillDir 扫描单个 skill 目录（bounded）。budget 是全根共享的条目预算
// （nil 表示无总限）。任何读错误都标记 Incomplete——不冒充完成。
func scanSkillDir(root, name string, limits SkillsScanLimits, budget *int) *SkillManifest {
	limits = limits.withDefaults()
	m := &SkillManifest{Name: name}
	skillRoot := filepath.Join(root, name)
	stop := false
	var walk func(dir, relPrefix string, depth int)
	walk = func(dir, relPrefix string, depth int) {
		if stop {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			m.markIncomplete("read-error")
			return
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			if stop {
				return
			}
			base := e.Name()
			rel := relPrefix + base
			switch {
			case e.Type()&os.ModeSymlink != 0:
				// F1：符号链接判定必须先于 IsDir——Go 1.23+ Windows 下 junction/
				// 目录符号链接的 DirEntry 同时携带 ModeDir|ModeSymlink，先判 IsDir
				// 会把目录链接当普通目录递归，树外内容静默入清单。
				m.scanLink(skillRoot, filepath.Join(dir, base), rel, limits)
			case e.IsDir():
				if skillsExcludedDirNames[base] {
					m.Excluded = append(m.Excluded, rel+"/")
					continue
				}
				if depth+1 > limits.MaxDepth {
					m.markIncomplete("depth-limit")
					continue
				}
				walk(filepath.Join(dir, base), rel+"/", depth+1)
			case e.Type().IsRegular():
				m.scanRegularFile(filepath.Join(dir, base), rel, limits)
			default:
				m.Incompatible = append(m.Incompatible, SkillIncompat{
					RelPath: rel, Code: SkillIncompatNonRegularFile, Reason: skillIncompatReasonText(SkillIncompatNonRegularFile)})
			}
			if budget != nil {
				*budget++
			}
			if budget != nil && *budget > limits.MaxTotalFiles {
				m.markIncomplete("total-file-limit")
				return
			}
			if len(m.Files) > limits.MaxFilesPerSkill {
				m.markIncomplete("file-count-limit")
				stop = true
				return
			}
		}
	}
	walk(skillRoot, "", 0)
	m.SourceHash = skillTreeHash(m.Files)
	return m
}

func (m *SkillManifest) markIncomplete(reason string) {
	if !m.Incomplete {
		m.Incomplete = true
		m.IncompleteReason = reason
	}
}

// scanRegularFile 处理一个常规文件：排除物记录、Windows 专用形态判不兼容、
// 超限判不兼容，其余读内容（有界）入清单。
func (m *SkillManifest) scanRegularFile(abs, rel string, limits SkillsScanLimits) {
	base := filepath.Base(rel)
	if skillsExcludedFileNames[base] || skillsExcludedFileExts[extOf(base)] {
		m.Excluded = append(m.Excluded, rel)
		return
	}
	ext := extOf(base)
	if skillsWindowsScriptExts[ext] {
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: SkillIncompatWindowsScript, Reason: skillIncompatReasonText(SkillIncompatWindowsScript)})
		return
	}
	if skillsWindowsBinaryExts[ext] {
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: SkillIncompatWindowsBinary, Reason: skillIncompatReasonText(SkillIncompatWindowsBinary)})
		return
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() {
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: SkillIncompatNonRegularFile, Reason: skillIncompatReasonText(SkillIncompatNonRegularFile)})
		return
	}
	if info.Size() > limits.MaxFileBytes {
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: SkillIncompatOversizeFile, Reason: skillIncompatReasonText(SkillIncompatOversizeFile)})
		return
	}
	data, err := readFileBounded(abs, limits.MaxFileBytes)
	if err != nil {
		m.markIncomplete("read-error")
		return
	}
	m.Files = append(m.Files, SkillFileEntry{RelPath: rel, Size: int64(len(data)), Hash: hexHash(data), srcAbs: abs})
	m.TotalBytes += int64(len(data))
}

// scanLink 处理一个符号链接：同名/同扩展排除物套用同一名单（F4：凭据类
// 名单对链接形态同样生效，不实体化）；绝对/越界/循环/目录链接判不兼容；
// 本 skill 树内解析到常规文件的链接实体化入清单（内容 hash 取解析目标）。
func (m *SkillManifest) scanLink(skillRoot, abs, rel string, limits SkillsScanLimits) {
	base := filepath.Base(rel)
	if skillsExcludedFileNames[base] || skillsExcludedFileExts[extOf(base)] {
		m.Excluded = append(m.Excluded, rel)
		return
	}
	target, err := os.Readlink(abs)
	if err != nil {
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: SkillIncompatNonRegularFile, Reason: skillIncompatReasonText(SkillIncompatNonRegularFile)})
		return
	}
	if isAbsoluteLikeLinkTarget(target) {
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: SkillIncompatAbsoluteLink, Reason: skillIncompatReasonText(SkillIncompatAbsoluteLink)})
		return
	}
	resolved, err := resolveSkillLink(skillRoot, abs, 16)
	if err != nil {
		code := SkillIncompatNonRegularFile
		if errors.Is(err, errSkillLinkCycle) {
			code = SkillIncompatCyclicLink
		} else if errors.Is(err, errSkillLinkAbsolute) {
			code = SkillIncompatAbsoluteLink
		}
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: code, Reason: skillIncompatReasonText(code)})
		return
	}
	fi, err := os.Lstat(resolved)
	if err != nil {
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: SkillIncompatNonRegularFile, Reason: skillIncompatReasonText(SkillIncompatNonRegularFile)})
		return
	}
	if fi.IsDir() {
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: SkillIncompatDirLink, Reason: skillIncompatReasonText(SkillIncompatDirLink)})
		return
	}
	if !fi.Mode().IsRegular() {
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: SkillIncompatNonRegularFile, Reason: skillIncompatReasonText(SkillIncompatNonRegularFile)})
		return
	}
	if !withinDir(skillRoot, resolved) {
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: SkillIncompatOutOfTreeLink, Reason: skillIncompatReasonText(SkillIncompatOutOfTreeLink)})
		return
	}
	// 链接名与目标名任一是 Windows 专用形态即不兼容（消费者两种路径都可能碰）。
	if skillsWindowsScriptExts[extOf(rel)] || skillsWindowsScriptExts[extOf(resolved)] {
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: SkillIncompatWindowsScript, Reason: skillIncompatReasonText(SkillIncompatWindowsScript)})
		return
	}
	if skillsWindowsBinaryExts[extOf(rel)] || skillsWindowsBinaryExts[extOf(resolved)] {
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: SkillIncompatWindowsBinary, Reason: skillIncompatReasonText(SkillIncompatWindowsBinary)})
		return
	}
	if fi.Size() > limits.MaxFileBytes {
		m.Incompatible = append(m.Incompatible, SkillIncompat{
			RelPath: rel, Code: SkillIncompatOversizeFile, Reason: skillIncompatReasonText(SkillIncompatOversizeFile)})
		return
	}
	data, err := readFileBounded(resolved, limits.MaxFileBytes)
	if err != nil {
		m.markIncomplete("read-error")
		return
	}
	m.Files = append(m.Files, SkillFileEntry{
		RelPath: rel, Size: int64(len(data)), Hash: hexHash(data), LinkTarget: target, srcAbs: resolved,
	})
	m.TotalBytes += int64(len(data))
}

// resolveSkillLink 沿符号链接链解析（跳数上限防环），返回最终路径。
func resolveSkillLink(skillRoot, linkAbs string, maxHops int) (string, error) {
	cur := linkAbs
	for hop := 0; hop < maxHops; hop++ {
		target, err := os.Readlink(cur)
		if err != nil {
			// 已不是链接：解析终点。
			return cur, nil
		}
		if isAbsoluteLikeLinkTarget(target) {
			return "", errSkillLinkAbsolute
		}
		next := target
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(cur), next)
		}
		if filepath.Clean(next) == filepath.Clean(skillRoot) || filepath.Clean(next) == filepath.Clean(linkAbs) {
			return "", errSkillLinkCycle
		}
		cur = next
	}
	return "", errSkillLinkCycle
}

// withinDir 报告 path 是否严格位于 dir 内（等于 dir 不算）。
func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)
}

// readFileBounded 读取至多 max+1 字节（超出即错误，绝不整读超大文件）。
func readFileBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("file exceeds %d bytes", max)
	}
	return data, nil
}

// listSkillDirs 列出根下可作为 skill 的顶层目录（隐藏目录与文件不列）。
func listSkillDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}
