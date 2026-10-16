package wslsupply

// skills_submit.go — 技能目录的 staging 提交、前像快照与条件撤销（P3-S3）。
//
// 写入走既有私有 staging 原语语义（design §四：先静态校验再写 staging，最后
// 只替换已登记目录——hash 条件）：唯一临时名（os.MkdirTemp）→ 写文件（0644，
// 非密钥内容）→ rename 交换（目标现存先挪到唯一隐藏备份名）→ distro 内
// chmod 批量补偿（644 文件/755 目录，9P 不落 POSIX 位；必须发生在交换后，
// 目标路径此刻才存在）→ 读回校验（树 hash 复核）。交换/chmod/校验任一步
// 失败均恢复前像或删除新增（restorePrev），不留半提交状态。
//
// 候选读取总量有界（skillCandidateCap，与前像封顶对齐）：超限报因拒绝供给，
// 不静默截断。前像快照有界（skillPreimageCap）：超限不快照，撤销按
// unavailable 处置（诚实标注而非假装可恢复）。目标侧符号链接只可能来自用户
// 手改（本系统从不写链接）；撤销恢复链接 best-effort，恢复写同样经 chmod
// 补偿 + 树 hash 读回校验（与主提交原语同构，F7）。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skillPreimageCap 是撤销前像快照的内存上限（非密钥文本资产，超限不强留）。
const skillPreimageCap = 8 << 20

// skillCandidateCap 是 Apply 阶段候选读取的总量上限（F5：与前像封顶对齐，
// 超限报因拒绝供给——不静默截断、不无界读入）。计划阶段同名原因码
// oversize-skill 先行阻断。
const skillCandidateCap = skillPreimageCap

// skillChmodBatch 是单次 distro chmod 补偿的路径批量（wsl.exe 命令行长度约束）。
const skillChmodBatch = 50

// skillWriteFile 是一条待写入（或前像）文件：实体化后的常规文件字节。
type skillWriteFile struct {
	RelPath string
	Bytes   []byte
}

// skillPreimageEntry 是前像树条目（含用户手改可能留下的符号链接）。
type skillPreimageEntry struct {
	RelPath    string
	IsLink     bool
	LinkTarget string
	Bytes      []byte
}

// readSkillCandidateFiles 在 Apply 阶段读取清单条目的源字节并复核 hash
// （扫描后来源已变 → 拒绝写入，要求重扫）；总量受 skillCandidateCap 约束
// （F5：超限报因拒绝供给，不静默截断、不无界读入）。候选只在内存存在。
func readSkillCandidateFiles(m *SkillManifest, limits SkillsScanLimits) ([]skillWriteFile, error) {
	out := make([]skillWriteFile, 0, len(m.Files))
	total := int64(0)
	for i := range m.Files {
		e := m.Files[i]
		if e.srcAbs == "" {
			return nil, fmt.Errorf("skill entry %q has no source path (rescan required)", e.RelPath)
		}
		data, err := readFileBounded(e.srcAbs, limits.MaxFileBytes)
		if err != nil {
			return nil, fmt.Errorf("read skill file %s: %w", e.RelPath, err)
		}
		if hexHash(data) != e.Hash {
			return nil, fmt.Errorf("skill file %s changed since scan; re-check before apply", e.RelPath)
		}
		total += int64(len(data))
		if total > skillCandidateCap {
			return nil, fmt.Errorf("skill total %d bytes exceeds candidate budget %d bytes; not supplied", total, skillCandidateCap)
		}
		out = append(out, skillWriteFile{RelPath: e.RelPath, Bytes: data})
	}
	return out, nil
}

// snapshotSkillPreimage 有界读取目标侧现存树作为撤销前像（超限 → snapshot=false）。
func snapshotSkillPreimage(uncSkillDir string, limits SkillsScanLimits) (entries []skillPreimageEntry, existed, snapshot bool, treeHash string) {
	st := scanTargetSkillDir(uncSkillDir, limits)
	if !st.Exists {
		return nil, false, false, ""
	}
	if st.Unverifiable {
		return nil, true, false, ""
	}
	var walk func(dir, relPrefix string, depth int) bool
	total := int64(0)
	walk = func(dir, relPrefix string, depth int) bool {
		des, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		for _, e := range des {
			rel := relPrefix + e.Name()
			abs := filepath.Join(dir, e.Name())
			switch {
			case e.Type()&os.ModeSymlink != 0:
				// F1：符号链接判定先于 IsDir（Windows 目录形态链接不递归，
				// 按链接条目快照，恢复时 best-effort 重建）。
				t, err := os.Readlink(abs)
				if err != nil {
					return false
				}
				entries = append(entries, skillPreimageEntry{RelPath: rel, IsLink: true, LinkTarget: t})
			case e.IsDir():
				if depth+1 > limits.MaxDepth || !walk(abs, rel+"/", depth+1) {
					return false
				}
			default:
				data, err := readFileBounded(abs, limits.MaxFileBytes)
				if err != nil {
					return false
				}
				total += int64(len(data))
				if total > skillPreimageCap {
					return false
				}
				entries = append(entries, skillPreimageEntry{RelPath: rel, Bytes: data})
			}
		}
		return true
	}
	if !walk(uncSkillDir, "", 0) {
		return nil, true, false, st.TreeHash
	}
	return entries, true, true, st.TreeHash
}

// applySkillUnit 执行一个技能单元的写入（Apply 阶段，applyMu 已持有）：
// 复核扫描缓存仍便携 → 读取并复核候选字节 → CAS 目标树 hash → 前像快照 →
// staging 提交 → 登记。部分失败逐项返回，不虚报成功。
func (c *Coordinator) applySkillUnit(_ context.Context, target TargetRef, planned PlannedUnit, adopt bool) UnitResult {
	name, ok := ParseSkillUnit(planned.Unit)
	if !ok || c.skills == nil {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitBlocked,
			Detail: "unit-adapter-missing", Err: errors.New("skills scope unavailable")}
	}
	limits := c.skills.Limits()
	m := c.skills.manifest(name)
	if m == nil {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitBlocked, Detail: "source-deleted"}
	}
	if reason := m.NotPortableReason(); reason != "" {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitBlocked, Detail: reason}
	}
	files, err := readSkillCandidateFiles(m, limits)
	if err != nil {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: "candidate-unavailable", Err: err}
	}
	written := make([]SkillFileEntry, 0, len(files))
	for _, f := range files {
		written = append(written, SkillFileEntry{RelPath: f.RelPath, Size: int64(len(f.Bytes)), Hash: hexHash(f.Bytes)})
	}
	writtenTreeHash := skillTreeHash(written)
	// CAS 前置复核：目标树自计划以来被外部改动 → 拒绝旧计划（adopt 接管路径
	// 已在协调器按预览 hash 完成 CAS，这里同样复核写入时刻状态一致）。
	uncSkillDir := filepath.Join(target.UNCRoot, filepath.FromSlash(skillRelDir(name)))
	tgt := scanTargetSkillDir(uncSkillDir, limits)
	if tgt.Unverifiable {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: tgt.Reason, Err: errors.New("cannot verify target skill tree before write")}
	}
	if tgt.TreeHash != planned.ExpectedTargetHash {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: "target-changed-since-planning",
			Err:    errors.New("target skill tree changed since planning (external edit retained)")}
	}
	prev, prevExisted, prevSnapshot, _ := snapshotSkillPreimage(uncSkillDir, limits)
	undo, err := c.submitSkillDir(target, name, files, writtenTreeHash)
	if err != nil {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: "write-failed", Err: err}
	}
	undo.unit = planned.Unit
	undo.prevFiles = preimageAsEntries(prev)
	undo.prevExisted = prevExisted
	undo.prevSnapshot = prevSnapshot
	undo.isDir = true
	undo.writtenTreeHash = writtenTreeHash
	outcome := "written"
	if adopt {
		outcome = "takeover-written"
	}
	notes := skillApplyNotes(m)
	c.recordProjectionState(target, planned.Unit, PolicyTrackedProjection, m.SourceHash, writtenTreeHash, skillsTransformVersion, nil, outcome)
	if len(notes) > 0 {
		logWarn(c.log, "wslsupply", "技能供给附带诊断", strings.Join(notes, "; "))
	}
	detail := outcome
	if len(notes) > 0 {
		detail += "; " + strings.Join(notes, "; ")
	}
	return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitApplied, Detail: detail, Undo: undo}
}

func preimageAsEntries(prev []skillPreimageEntry) []skillPreimageEntry {
	if len(prev) == 0 {
		return nil
	}
	return append([]skillPreimageEntry(nil), prev...)
}

// skillApplyNotes 生成脱敏附带说明（排除物摘要；不兼容项阻断供给不会走到写入）。
func skillApplyNotes(m *SkillManifest) []string {
	if len(m.Excluded) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("已排除 %d 项（.git/node_modules/缓存/凭据/会话产物）", len(m.Excluded))}
}

// submitSkillDir 经私有 staging 提交整个 skill 目录：唯一临时目录 → 写文件
// → rename 交换 → distro chmod 批量补偿（644 文件/755 目录；失败恢复前像）
// → 树 hash 读回校验。交换/chmod 失败均恢复原目录或清理新增。
func (c *Coordinator) submitSkillDir(target TargetRef, name string, files []skillWriteFile, expectedTreeHash string) (*CompensationHandle, error) {
	parent := filepath.Join(target.UNCRoot, "skills")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir skills root: %w", err)
	}
	targetDir := filepath.Join(parent, name)
	staging, err := os.MkdirTemp(parent, ".amagi-supply-skill-*")
	if err != nil {
		return nil, fmt.Errorf("create skill staging dir: %w", err)
	}
	cleanupStaging := func() { _ = os.RemoveAll(staging) }
	dirs := map[string]bool{skillRelDir(name): true}
	for _, f := range files {
		unc := filepath.Join(staging, filepath.FromSlash(f.RelPath))
		if err := os.MkdirAll(filepath.Dir(unc), 0o755); err != nil {
			cleanupStaging()
			return nil, fmt.Errorf("mkdir skill staging subpath: %w", err)
		}
		if err := os.WriteFile(unc, f.Bytes, 0o644); err != nil {
			cleanupStaging()
			return nil, fmt.Errorf("write skill staging file %s: %w", f.RelPath, err)
		}
		for d := relDirsOf(f.RelPath); d != ""; d = relDirsOf(d) {
			dirs[skillRelDir(name)+"/"+d] = true
			if !strings.Contains(d, "/") {
				break
			}
		}
	}
	// 交换：目标现存 → 先挪到唯一隐藏名，再换入 staging；第二步失败恢复。
	// chmod 补偿在交换后执行（目标路径此刻才存在）；失败恢复前像或删除新增
	//（非密钥内容，不留半提交状态）。
	prevDir := ""
	if _, statErr := os.Lstat(targetDir); statErr == nil {
		prevDir, err = reserveHiddenName(parent, ".amagi-supply-prev-*")
		if err != nil {
			cleanupStaging()
			return nil, fmt.Errorf("reserve skill preimage slot: %w", err)
		}
		if err := os.Rename(targetDir, prevDir); err != nil {
			_ = os.Remove(prevDir)
			cleanupStaging()
			return nil, fmt.Errorf("move aside existing skill dir: %w", err)
		}
	}
	restorePrev := func(cause error) (*CompensationHandle, error) {
		_ = os.RemoveAll(targetDir)
		if prevDir != "" {
			if rbErr := os.Rename(prevDir, targetDir); rbErr != nil {
				return nil, fmt.Errorf("%v (rollback failed: %v; backup kept at %s)", cause, rbErr, filepath.Base(prevDir))
			}
		} else {
			_ = os.RemoveAll(staging)
		}
		return nil, cause
	}
	if err := os.Rename(staging, targetDir); err != nil {
		if prevDir != "" {
			if rbErr := os.Rename(prevDir, targetDir); rbErr != nil {
				cleanupStaging()
				return nil, fmt.Errorf("commit skill dir: %w (rollback failed: %v; backup kept at %s)", err, rbErr, filepath.Base(prevDir))
			}
		}
		cleanupStaging()
		return nil, fmt.Errorf("commit skill dir: %w", err)
	}
	if err := c.chmodSkillTree(target, name, files, dirs); err != nil {
		return restorePrev(fmt.Errorf("chmod skill tree in distro: %w", err))
	}
	// 读回校验：树 hash 必须等于预期；失败同样恢复前像。
	got := scanTargetSkillDir(targetDir, c.skillLimits())
	if got.Unverifiable || got.TreeHash != expectedTreeHash {
		return restorePrev(fmt.Errorf("verify skill tree after commit: hash mismatch (unverifiable=%v)", got.Unverifiable))
	}
	if prevDir != "" {
		if rmErr := os.RemoveAll(prevDir); rmErr != nil {
			logWarn(c.log, "wslsupply", "技能目录交换后清理旧副本失败（隐藏残留，不影响使用）", filepath.Base(prevDir))
		}
	}
	return &CompensationHandle{
		target:      target,
		relPath:     skillRelDir(name),
		uncPath:     targetDir,
		linuxPath:   target.LinuxRoot + "/" + skillRelDir(name),
		writtenHash: expectedTreeHash,
	}, nil
}

// takeOverSkill 执行技能单元的「预览后确认接管」：以预览时目标树 hash 做
// CAS（旧预览不覆盖新编辑）；未同意仍拦截（与 F-3 同构：接管是显式写入，
// 不得绕过 per-target 同意门）；随后按当前来源重建基准（adopt=true）。
func (c *Coordinator) takeOverSkill(ctx context.Context, distro string, unit UnitID, name, expectedTargetHash string) (UnitResult, error) {
	if c.skills == nil {
		return UnitResult{}, fmt.Errorf("skills scope unavailable")
	}
	c.applyMu.Lock()
	defer c.applyMu.Unlock()

	target, err := c.ResolveTarget(ctx, distro, CLICommon)
	if err != nil {
		return UnitResult{}, fmt.Errorf("resolve wsl supply target: %w", err)
	}
	st := c.state.load()
	entry := st.entryFor(unit, target.Generation)
	if !entry.Consented {
		return UnitResult{Unit: unit, Policy: PolicyTrackedProjection, Outcome: UnitBlocked,
			Detail: "awaiting-consent",
			Err:    errors.New("skill is not consented for this target; import it first")}, nil
	}
	uncSkillDir := filepath.Join(target.UNCRoot, filepath.FromSlash(skillRelDir(name)))
	tgt := scanTargetSkillDir(uncSkillDir, c.skillLimits())
	if tgt.Unverifiable {
		return UnitResult{Unit: unit, Policy: PolicyTrackedProjection, Outcome: UnitFailed,
			Detail: tgt.Reason, Err: errors.New("cannot verify target skill tree")}, nil
	}
	if tgt.TreeHash != expectedTargetHash {
		return UnitResult{Unit: unit, Policy: PolicyTrackedProjection, Outcome: UnitBlocked,
			Detail: "preview-expired",
			Err:    errors.New("target skill tree changed since preview; re-check before takeover")}, nil
	}
	planned := PlannedUnit{Unit: unit, Policy: PolicyTrackedProjection, RelPath: skillRelDir(name),
		ExpectedTargetHash: expectedTargetHash, Action: ActionWriteProjection, TransformVersion: skillsTransformVersion}
	return c.applySkillUnit(ctx, target, planned, true), nil
}

// relDirsOf 返回相对路径的父目录链首个父目录（"a/b/c.txt" → "a/b"）。
func relDirsOf(rel string) string {
	i := strings.LastIndexAny(rel, "/\\")
	if i <= 0 {
		return ""
	}
	return rel[:i]
}

// reserveHiddenName 在 parent 下保留一个唯一隐藏名（先 MkdirTemp 占位再删除，
// rename 落位）。失败返回错误。
func reserveHiddenName(parent, pattern string) (string, error) {
	d, err := os.MkdirTemp(parent, pattern)
	if err != nil {
		return "", err
	}
	if err := os.Remove(d); err != nil {
		return "", err
	}
	return d, nil
}

// chmodSkillTree 批量 distro chmod 补偿（文件 644 / 目录 755；分批防命令行
// 超长）。
func (c *Coordinator) chmodSkillTree(target TargetRef, name string, files []skillWriteFile, dirs map[string]bool) error {
	base := strings.TrimRight(target.LinuxRoot, "/") + "/" + skillRelDir(name)
	filePaths := make([]string, 0, len(files))
	for _, f := range files {
		filePaths = append(filePaths, base+"/"+f.RelPath)
	}
	dirList := make([]string, 0, len(dirs))
	for d := range dirs {
		dirList = append(dirList, strings.TrimRight(target.LinuxRoot, "/")+"/"+d)
	}
	sort.Strings(dirList)
	for i := 0; i < len(filePaths); i += skillChmodBatch {
		end := i + skillChmodBatch
		if end > len(filePaths) {
			end = len(filePaths)
		}
		if err := c.probes.Chmod(target.Distro, "644", filePaths[i:end]...); err != nil {
			return fmt.Errorf("chmod 644 skill files in distro: %w", err)
		}
	}
	for i := 0; i < len(dirList); i += skillChmodBatch {
		end := i + skillChmodBatch
		if end > len(dirList) {
			end = len(dirList)
		}
		if err := c.probes.Chmod(target.Distro, "755", dirList[i:end]...); err != nil {
			return fmt.Errorf("chmod 755 skill dirs in distro: %w", err)
		}
	}
	return nil
}

// skillLimits 返回技能扫描上限（skills 作用域未注册时用默认）。
func (c *Coordinator) skillLimits() SkillsScanLimits {
	if c.skills != nil {
		return c.skills.Limits()
	}
	return DefaultSkillsScanLimits()
}

// compensateSkillDir 撤销一次技能目录写入（CAS：目标树 hash 仍等于本次写入
// 才恢复前像/删除新增；外部修改保留）。
func (c *Coordinator) compensateSkillDir(h *CompensationHandle) CompensationOutcome {
	outcome := CompensationOutcome{Unit: h.unit, RelPath: h.relPath}
	current := scanTargetSkillDir(h.uncPath, c.skillLimits())
	if current.Unverifiable {
		outcome.Disposition = CompensationIndeterminate
		outcome.Message = current.Reason
		return outcome
	}
	if !current.Exists {
		// 目标已不在（可能被用户删除）：视为已撤销。
		outcome.Disposition = CompensationConfirmed
		return outcome
	}
	if current.TreeHash != h.writtenTreeHash {
		outcome.Disposition = CompensationSkipped
		outcome.Message = "target changed after write; external content retained"
		return outcome
	}
	if !h.prevExisted {
		if err := os.RemoveAll(h.uncPath); err != nil && !os.IsNotExist(err) {
			outcome.Disposition = CompensationUnavailable
			outcome.Message = err.Error()
			return outcome
		}
		outcome.Disposition = CompensationConfirmed
		return outcome
	}
	if !h.prevSnapshot {
		outcome.Disposition = CompensationUnavailable
		outcome.Message = "preimage snapshot unavailable (oversized or unverifiable tree)"
		return outcome
	}
	if err := c.restoreSkillPreimage(h); err != nil {
		outcome.Disposition = CompensationUnavailable
		outcome.Message = err.Error()
		return outcome
	}
	outcome.Disposition = CompensationConfirmed
	return outcome
}

// skillPreimageRenameFn 是恢复写最后一步 rename 的接缝（生产 os.Rename；
// 测试注入失败钉住 F2：rename 失败时唯一持久前像必须保留在 staging）。
var skillPreimageRenameFn = os.Rename

// restoreSkillPreimage 重建前像树：staging 写回（链接条目 best-effort）→
// 删当前 → rename 落位 → distro chmod 补偿 + 树 hash 读回校验（F7：与主
// 提交原语同构，不信任 9P 默认位）。rename 失败时**保留 staging 不清理**
// （F2：那是唯一持久前像副本，清理等于在自身失败路径上销毁它声称保留的
// 备份）；写入阶段失败才清理 staging。
func (c *Coordinator) restoreSkillPreimage(h *CompensationHandle) error {
	parent := filepath.Dir(h.uncPath)
	staging, err := os.MkdirTemp(parent, ".amagi-supply-skill-*")
	if err != nil {
		return fmt.Errorf("restore staging: %w", err)
	}
	keepStaging := false
	defer func() {
		if !keepStaging {
			_ = os.RemoveAll(staging)
		}
	}()
	for _, e := range h.prevFiles {
		unc := filepath.Join(staging, filepath.FromSlash(e.RelPath))
		if err := os.MkdirAll(filepath.Dir(unc), 0o755); err != nil {
			return err
		}
		if e.IsLink {
			if err := os.Symlink(e.LinkTarget, unc); err != nil {
				return fmt.Errorf("restore symlink %s: %w", e.RelPath, err)
			}
			continue
		}
		if err := os.WriteFile(unc, e.Bytes, 0o644); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(h.uncPath); err != nil {
		return err
	}
	if err := skillPreimageRenameFn(staging, h.uncPath); err != nil {
		keepStaging = true // F2：唯一持久前像副本保留在 staging，供人工恢复
		return fmt.Errorf("restore rename: %w (preimage kept in staging %s)", err, filepath.Base(staging))
	}
	// F7：恢复写同样经 chmod 补偿 + 树 hash 读回校验（与主提交原语同构）。
	if err := c.chmodSkillTree(h.target, filepath.Base(h.uncPath), preimageFiles(h.prevFiles), preimageDirs(h.prevFiles, filepath.Base(h.uncPath))); err != nil {
		return fmt.Errorf("chmod restored skill tree in distro: %w", err)
	}
	got := scanTargetSkillDir(h.uncPath, c.skillLimits())
	expected := preimageTreeHash(h.prevFiles)
	if got.Unverifiable || got.TreeHash != expected {
		return fmt.Errorf("verify restored skill tree: hash mismatch (unverifiable=%v)", got.Unverifiable)
	}
	return nil
}

// preimageFiles 把前像条目转为 chmod 所需的文件载荷（链接无字节，不参与 644）。
func preimageFiles(prev []skillPreimageEntry) []skillWriteFile {
	out := make([]skillWriteFile, 0, len(prev))
	for _, e := range prev {
		if !e.IsLink {
			out = append(out, skillWriteFile{RelPath: e.RelPath, Bytes: e.Bytes})
		}
	}
	return out
}

// preimageDirs 汇总前像目录集（skill 根 + 各父目录）供 755 补偿。
func preimageDirs(prev []skillPreimageEntry, name string) map[string]bool {
	dirs := map[string]bool{"skills/" + name: true}
	for _, e := range prev {
		for d := relDirsOf(e.RelPath); d != ""; d = relDirsOf(d) {
			dirs["skills/"+name+"/"+d] = true
			if !strings.Contains(d, "/") {
				break
			}
		}
	}
	return dirs
}

// preimageTreeHash 计算前像的规范化树 hash（与 scanTargetSkillDir 同构：链接
// 条目 hash("link:"+target)，文件条目 size+hash(bytes)）——恢复后读回比对
// 的期望值。
func preimageTreeHash(prev []skillPreimageEntry) string {
	entries := make([]SkillFileEntry, 0, len(prev))
	for _, e := range prev {
		if e.IsLink {
			entries = append(entries, SkillFileEntry{RelPath: e.RelPath, Hash: hexHash([]byte("link:" + e.LinkTarget))})
			continue
		}
		entries = append(entries, SkillFileEntry{RelPath: e.RelPath, Size: int64(len(e.Bytes)), Hash: hexHash(e.Bytes)})
	}
	return skillTreeHash(entries)
}
