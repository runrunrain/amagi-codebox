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
	"time"
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

// skillPreimageEntry 是前像树条目（含用户手改可能留下的符号链接与空目录
// ——空目录不入树 hash 但必须保真：结构丢失不是「内容一致」）。
type skillPreimageEntry struct {
	RelPath    string
	IsLink     bool
	IsDir      bool
	LinkTarget string
	Bytes      []byte
}

// readSkillCandidateFiles 在 Apply 阶段读取清单条目的源字节并复核 hash
// （扫描后来源已变 → 拒绝写入，要求重扫）；总量受 skillCandidateCap 约束
// （F5：超限报因拒绝供给，不静默截断、不无界读入）。候选只在内存存在。
// SA-05：读取前重验路径身份——扫描后被替换成链接/非常规形态的条目直接拒绝
// （不迫逐新链接目标；hash 复核仍兜底内容一致性）。
func readSkillCandidateFiles(m *SkillManifest, limits SkillsScanLimits) ([]skillWriteFile, error) {
	out := make([]skillWriteFile, 0, len(m.Files))
	total := int64(0)
	for i := range m.Files {
		e := m.Files[i]
		if e.srcAbs == "" {
			return nil, fmt.Errorf("skill entry %q has no source path (rescan required)", e.RelPath)
		}
		if info, lerr := os.Lstat(e.srcAbs); lerr != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("skill entry %q source is no longer a regular file (rescan required)", e.RelPath)
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
				before := len(entries)
				if depth+1 > limits.MaxDepth || !walk(abs, rel+"/", depth+1) {
					return false
				}
				if len(entries) == before {
					// 空目录：结构保真（不入树 hash；恢复时重建目录本体）。
					entries = append(entries, skillPreimageEntry{RelPath: rel, IsDir: true})
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
// staging 提交 → 登记。部分失败逐项返回，不虚报成功。PR07：入口与不可逆
// 交换前检查 ctx——已取消的接管/供给请求零新写。
func (c *Coordinator) applySkillUnit(ctx context.Context, target TargetRef, planned PlannedUnit, adopt bool) UnitResult {
	if err := ctx.Err(); err != nil {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitBlocked,
			Detail: "context-canceled", Err: err}
	}
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
	_ = prevExisted
	outcome := "written"
	if adopt {
		outcome = "takeover-written"
	}
	rcpt := c.submitDirUnit(ctx, CommitRequest{
		Target: target, Unit: planned.Unit, RelPath: skillRelDir(name),
		ExpectedBefore: planned.ExpectedTargetHash, Kind: OpKindDirSwap,
		Dir: true, DirFiles: files, DirTreeHash: writtenTreeHash,
		Register: func(st *StateFile) {
			registerProjectionEntryIn(st, target, planned.Unit, PolicyTrackedProjection, m.SourceHash,
				writtenTreeHash, skillsTransformVersion, nil, outcome)
		},
	})
	if rcpt.UndoLease != nil {
		rcpt.UndoLease.prevFiles = preimageAsEntries(prev)
		rcpt.UndoLease.prevSnapshot = prevSnapshot
	}
	res := unitResultFromReceipt(planned.Unit, planned.Policy, rcpt, "write-failed")
	if rcpt.Err == nil {
		notes := skillApplyNotes(m)
		if len(notes) > 0 {
			logWarn(c.log, "wslsupply", "技能供给附带诊断", strings.Join(notes, "; "))
		}
		res.Outcome = UnitApplied
		res.Detail = outcome
		if len(notes) > 0 {
			res.Detail += "; " + strings.Join(notes, "; ")
		}
	}
	return res
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

// submitDirUnit 经统一提交生命周期提交一个目录单元（S3 技能目录与 S4 用户
// 资源目录共用；攻坚契约「目录 swap」）：staging 树准备/权限与结构验证 →
// 保存含 target/prev/staging/displaced 的 intent → ctx 检查 → target 移到
// prev → 复验 prev → ctx 检查 → no-replace 发布 staging → 验证目标 → 主清单
// 确认。取消语义：第一次移动前取消原目标不动；移走旧目录后取消由独立有限
// recoveryCtx 恢复或明确保留位置（空目录也不丢）；第二次发布后取消/失败按
// receipt 条件恢复，失败保留 intent。不得递归提交/恢复。
func (c *Coordinator) submitDirUnit(ctx context.Context, req CommitRequest) *CommitReceipt {
	targetKey := targetKeyOf(req.Target)
	opID := newOpID()
	relDir := req.RelPath
	parent := filepath.Dir(filepath.Join(req.Target.UNCRoot, filepath.FromSlash(relDir)))
	targetDir := filepath.Join(req.Target.UNCRoot, filepath.FromSlash(relDir))
	rc := &CommitReceipt{
		OpID: opID, TargetKey: targetKey, Unit: req.Unit, RelPath: relDir,
		Kind: OpKindDirSwap, Effect: OpEffectNone, Registration: OpRegistrationPending, Recovery: OpRecoveryNone,
		AfterHash: req.DirTreeHash,
	}
	fail := func(stage string, err error, detail string) *CommitReceipt {
		rc.Err = err
		rc.ErrDetail = detail
		c.recordObservation(targetKey, opID, req.Unit, rc.Effect, rc.Registration, rc.Recovery, detail, stage)
		return rc
	}
	// 1) staging 树准备与结构验证。
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fail(stagePrepare, fmt.Errorf("mkdir target parent dir: %w", err), "prepare-failed")
	}
	staging, err := os.MkdirTemp(parent, ".amagi-supply-skill-*")
	if err != nil {
		return fail(stagePrepare, fmt.Errorf("create skill staging dir: %w", err), "prepare-failed")
	}
	rc.StagingRel = relPathOf(req.Target, staging)
	dirs := map[string]bool{relDir: true}
	for _, f := range req.DirFiles {
		unc := filepath.Join(staging, filepath.FromSlash(f.RelPath))
		if err := os.MkdirAll(filepath.Dir(unc), 0o755); err != nil {
			_ = os.RemoveAll(staging)
			return fail(stagePrepare, fmt.Errorf("mkdir skill staging subpath: %w", err), "prepare-failed")
		}
		if err := os.WriteFile(unc, f.Bytes, 0o644); err != nil {
			_ = os.RemoveAll(staging)
			return fail(stagePrepare, fmt.Errorf("write skill staging file %s: %w", f.RelPath, err), "prepare-failed")
		}
		for d := relDirsOf(f.RelPath); d != ""; d = relDirsOf(d) {
			dirs[relDir+"/"+d] = true
			if !strings.Contains(d, "/") {
				break
			}
		}
	}
	// staging 树 hash 验证（结构+字节完整）。
	if got := scanTargetSkillDir(staging, c.skillLimits()); got.Unverifiable || got.TreeHash != req.DirTreeHash {
		_ = os.RemoveAll(staging)
		return fail(stagePrepare, fmt.Errorf("verify staging tree (unverifiable=%v)", got.Unverifiable), "prepare-failed")
	}
	// 2) prev/displaced 命名 + intent（含 target/prev/staging/displaced）。
	prevDir := ""
	beforeTreeHash := ""
	if _, statErr := os.Lstat(targetDir); statErr == nil {
		prevDir = filepath.Join(parent, ".amagi-supply-prev-"+opID)
		rc.DisplacedRel = relPathOf(req.Target, prevDir)
		rc.BeforeExists = true
		// 不可逆移动前对构建基准复验（一致性修正 dir-cas）：staging 构建期间目标
		// 树可能被外部改动——基准不符则拒绝发布，未授权变更不覆盖。
		if !req.SkipBeforeCAS && req.ExpectedBefore != "" {
			tgt := scanTargetSkillDir(targetDir, c.skillLimits())
			if tgt.Unverifiable {
				_ = os.RemoveAll(staging)
				return fail(stagePrepare, errors.New("cannot verify target skill tree before move"), "target-unreadable")
			}
			if tgt.TreeHash != req.ExpectedBefore {
				_ = os.RemoveAll(staging)
				return fail(stageMove, errors.New("target skill tree changed since planning (external edit retained)"), "target-changed-before-move")
			}
			beforeTreeHash = tgt.TreeHash
		}
	}
	intent := OperationIntent{
		OpID: opID, Kind: OpKindDirSwap, TargetKey: targetKey,
		TargetDistro: req.Target.Distro, TargetHome: req.Target.Home,
		TargetCLI: string(req.Target.CLI),
		Unit: req.Unit, RelPath: relDir, BeforeExists: rc.BeforeExists,
		StagingRel: rc.StagingRel, DisplacedRel: rc.DisplacedRel,
		PrevTreeHash: beforeTreeHash,
		AfterHash: req.DirTreeHash, CreatedAt: time.Now(),
	}
	if err := c.state.saveIntent(intent); err != nil {
		_ = os.RemoveAll(staging)
		return fail(stageIntent, err, "intent-unavailable")
	}
	// 3) ctx 检查 → target 移到 prev → 复验 prev。
	if err := ctx.Err(); err != nil {
		_ = os.RemoveAll(staging)
		_ = c.state.deleteIntent(opID) // 未发生任何目标移动：清理本次材料
		return fail(stageMove, fmt.Errorf("submit dir unit canceled before swap: %w", err), "context-canceled")
	}
	if prevDir != "" {
		if err := c.faults.fail(stageMove); err != nil {
			_ = os.RemoveAll(staging)
			return fail(stageMove, err, "move-failed")
		}
		if err := os.Rename(targetDir, prevDir); err != nil {
			_ = os.RemoveAll(staging)
			return fail(stageMove, fmt.Errorf("move aside existing dir: %w", err), "move-failed")
		}
		// 复验 prev（真实旧目录保留为 prev，结构与构建基准双验证；空目录/链接
		// 不丢）。搬移后验证不符 → 外部版本在扫描与搬移之间落进旧树：保全外部
		// 版本（no-replace 放回），不发布候选。
		pv := scanTargetSkillDir(prevDir, c.skillLimits())
		if pv.Unverifiable || (beforeTreeHash != "" && pv.TreeHash != beforeTreeHash) {
			rc.Effect = OpEffectUnknown
			// 移动后失败：把 prev 放回（no-replace；外部新目标拒绝保留两处）。
			if rbErr := publishNoReplaceFn(prevDir, targetDir); rbErr != nil {
				rc.Recovery = OpRecoveryConflict
				rc.Err = fmt.Errorf("verify prev dir failed (unverifiable=%v); rollback failed: %v; backup kept at %s", pv.Unverifiable, rbErr, filepath.Base(prevDir))
				rc.ErrDetail = "move-verify-failed"
				c.recordObservation(targetKey, opID, req.Unit, rc.Effect, rc.Registration, rc.Recovery, rc.ErrDetail, stageMove)
				return rc
			}
			_ = os.RemoveAll(staging)
			return fail(stageMove, fmt.Errorf("verify prev dir: mismatch after move (unverifiable=%v)", pv.Unverifiable), "restored-after-failed-move")
		}
	}
	// 4) ctx 检查（第二次不可逆动作前）→ no-replace 发布 staging。
	if err := ctx.Err(); err != nil {
		// 移走旧目录后取消：独立有限恢复（恢复 prev 或明确保留位置）。
		c.recoverAfterCancel(ctx, req, rc, nil)
		if rc.Effect != OpEffectRestored {
			rc.Err = fmt.Errorf("commit dir canceled after move-aside: %w (target state: %s; prev kept at %s)", ctx.Err(), rc.Effect, rc.DisplacedRel)
			rc.ErrDetail = "canceled-recovery-pending"
		} else {
			rc.Err = fmt.Errorf("commit dir canceled after move-aside: %w (prev restored)", ctx.Err())
			rc.ErrDetail = "canceled-restored"
		}
		c.recordObservation(targetKey, opID, req.Unit, rc.Effect, rc.Registration, rc.Recovery, rc.ErrDetail, stageMove)
		return rc
	}
	if err := c.faults.fail(stagePublish); err != nil {
		c.recoverPublishFailure(req, rc, prevDir, staging, err)
		return rc
	}
	if err := publishNoReplaceFn(staging, targetDir); err != nil {
		if isNoReplaceExists(err) {
			rc.Effect = OpEffectUnknown
			rc.Recovery = OpRecoveryConflict
			return fail(stagePublish, fmt.Errorf("external target appeared during dir publish; both retained (staging %s, prev %s): %w", rc.StagingRel, rc.DisplacedRel, err), "publish-conflict")
		}
		c.recoverPublishFailure(req, rc, prevDir, staging, err)
		return rc
	}
	rc.Effect = OpEffectCommitted
	// 5) 验证目标（chmod 补偿 + 树 hash 读回）。
	if err := c.chmodTree(ctx, req.Target, relDir, req.DirFiles, dirs); err != nil {
		if ferr := c.faults.fail(stageVerify); ferr != nil {
			err = ferr
		}
		c.recoverPublishFailure(req, rc, prevDir, "", fmt.Errorf("chmod skill tree in distro: %w", err))
		return rc
	}
	got := scanTargetSkillDir(targetDir, c.skillLimits())
	if ferr := c.faults.fail(stageVerify); ferr != nil {
		got = SkillTargetState{Unverifiable: true}
	}
	if got.Unverifiable || got.TreeHash != req.DirTreeHash {
		c.recoverPublishFailure(req, rc, prevDir, "", fmt.Errorf("verify skill tree after commit: hash mismatch (unverifiable=%v)", got.Unverifiable))
		return rc
	}
	// 6) finalize：settled + 主清单同次确认。
	if serr := c.finalizeCommit(req, rc, "committed"); serr != nil {
		rc.Err = fmt.Errorf("wsl supply state persist failed after dir commit (tree kept; intent %s retained): %w", opID, serr)
		rc.ErrDetail = "content-committed-state-unpersisted"
		rc.Registration = OpRegistrationPending
		rc.UndoLease = c.newDirUndoLease(req, rc, beforeTreeHash)
		c.setLeaseActive(opID, true) // 同上：租用跟随句柄生效
		c.recordObservation(targetKey, opID, req.Unit, OpEffectCommitted, OpRegistrationPending, rc.Recovery, rc.ErrDetail, stageFinalize)
		return rc
	}
	rc.Registration = OpRegistrationConfirmed
	rc.UndoLease = c.newDirUndoLease(req, rc, beforeTreeHash)
	// 一致性修正（resource-abort）：真实旧树（prev）保留至 Release/补偿终结
	// ——提交成功即删 prev 会让后续 Compensate 只剩内存快照（资源路径无快
	// 照 → unavailable 且旧内容已不在）。材料清理由撤销租约生命周期承担
	//（ReleaseCompensations / 无租约后的 reconcile finishMaterials）。
	c.setLeaseActive(opID, true)
	c.recordObservation(targetKey, opID, req.Unit, OpEffectCommitted, OpRegistrationConfirmed, OpRecoveryAvailable, "committed", stageCleanup)
	return rc
}

// recoverPublishFailure 处理目录发布/chmod/验证失败：目标缺失时 no-replace
// 放回 prev（外部新目标拒绝保留两处）；失败保留 prev 与 intent，空目录也不
// Remove。不递归提交/恢复。
func (c *Coordinator) recoverPublishFailure(req CommitRequest, rc *CommitReceipt, prevDir, staging string, cause error) {
	rc.Effect = OpEffectUnknown
	if cur := scanTargetSkillDir(filepath.Join(req.Target.UNCRoot, filepath.FromSlash(rc.RelPath)), c.skillLimits()); cur.Exists && cur.TreeHash == req.DirTreeHash && !cur.Unverifiable {
		rc.Effect = OpEffectCommitted // 树实际已落地（如 chmod/读回毛刺）
	}
	if prevDir == "" {
		// 首建：目标缺失时清 staging；目标在时保留现状。
		if staging != "" {
			_ = os.RemoveAll(staging)
		}
		rc.Err = cause
		rc.ErrDetail = "commit-dir-unknown"
		c.recordObservation(rc.TargetKey, rc.OpID, req.Unit, rc.Effect, rc.Registration, rc.Recovery, rc.ErrDetail, stagePublish)
		return
	}
	targetDir := filepath.Join(req.Target.UNCRoot, filepath.FromSlash(rc.RelPath))
	if _, err := os.Lstat(targetDir); err == nil {
		if rc.Effect == OpEffectCommitted {
			// 已落地仅验证未过：保留现状与 prev 材料。
			rc.Err = fmt.Errorf("%v (tree committed but unverified; prev kept at %s)", cause, filepath.Base(prevDir))
			rc.ErrDetail = "committed-unverified"
		} else {
			rc.Recovery = OpRecoveryConflict
			rc.Err = fmt.Errorf("%v (target present, prev kept at %s; both retained)", cause, filepath.Base(prevDir))
			rc.ErrDetail = "commit-dir-conflict"
		}
		c.recordObservation(rc.TargetKey, rc.OpID, req.Unit, rc.Effect, rc.Registration, rc.Recovery, rc.ErrDetail, stagePublish)
		return
	}
	// 目标缺失且本次移走了旧树：no-replace 放回 prev。
	if rbErr := publishNoReplaceFn(prevDir, targetDir); rbErr != nil {
		rc.Recovery = OpRecoveryConflict
		rc.Err = fmt.Errorf("%v (restore prev failed: %v; prev kept at %s)", cause, rbErr, filepath.Base(prevDir))
		rc.ErrDetail = "restore-failed-prev-kept"
		c.recordObservation(rc.TargetKey, rc.OpID, req.Unit, rc.Effect, rc.Registration, rc.Recovery, rc.ErrDetail, stagePublish)
		return
	}
	// 放回后验证：不匹配则保留两处并如实报告。
	if pv := scanTargetSkillDir(prevDir, c.skillLimits()); pv.Unverifiable {
		rc.Err = fmt.Errorf("%v (prev put back but unverifiable: %s)", cause, pv.Reason)
		rc.ErrDetail = "restored-unverified"
	} else {
		rc.Effect = OpEffectRestored
		rc.Err = fmt.Errorf("%v (prev restored)", cause)
		rc.ErrDetail = "restored-after-failed-publish"
		_ = c.state.deleteIntent(rc.OpID)
	}
	if staging != "" {
		_ = os.RemoveAll(staging)
	}
	c.recordObservation(rc.TargetKey, rc.OpID, req.Unit, rc.Effect, rc.Registration, rc.Recovery, rc.ErrDetail, stagePublish)
}

// newDirUndoLease 构造目录提交的撤销租用（携带持久 prev 树位置与基准树
// hash：补偿优先用真实 prev 树 no-replace 放回，内存快照仅作 prev 丢失时
// 的有界兜底）。
func (c *Coordinator) newDirUndoLease(req CommitRequest, rc *CommitReceipt, beforeTreeHash string) *CompensationHandle {
	h := &CompensationHandle{
		target: req.Target, relPath: req.RelPath,
		uncPath: filepath.Join(req.Target.UNCRoot, filepath.FromSlash(req.RelPath)),
		linuxPath: req.Target.LinuxRoot + "/" + req.RelPath,
		writtenHash: req.DirTreeHash, opID: rc.OpID, targetKey: rc.TargetKey,
		backupRel: rc.BackupRel, stagingRel: rc.StagingRel, displacedRel: rc.DisplacedRel,
		unit: req.Unit,
		isDir: true, writtenTreeHash: req.DirTreeHash,
		prevExisted: rc.BeforeExists, prevTreeHash: beforeTreeHash,
	}
	return h
}

// takeOverSkill 执行技能单元的「预览后确认接管」：以预览时目标树 hash 做
// CAS（旧预览不覆盖新编辑）；未同意仍拦截（与 F-3 同构：接管是显式写入，
// 不得绕过 per-target 同意门）；随后按当前来源重建基准（adopt=true）。
func (c *Coordinator) takeOverSkill(ctx context.Context, distro string, unit UnitID, name, expectedTargetHash string) (UnitResult, error) {
	if c.skills == nil {
		return UnitResult{}, fmt.Errorf("skills scope unavailable")
	}
	if err := ctx.Err(); err != nil {
		return UnitResult{Unit: unit, Policy: PolicyTrackedProjection, Outcome: UnitBlocked,
			Detail: "context-canceled", Err: err}, nil
	}
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	// PR07：等锁期间取消的请求不得进入接管写入。
	if err := ctx.Err(); err != nil {
		return UnitResult{Unit: unit, Policy: PolicyTrackedProjection, Outcome: UnitBlocked,
			Detail: "context-canceled", Err: err}, nil
	}

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

// chmodTree 批量 distro chmod 补偿（文件 644 / 目录 755；分批防命令行超长）。
// relDir 是目标相对目录（如 "skills/foo"、".claude/commands/foo"）。
func (c *Coordinator) chmodTree(ctx context.Context, target TargetRef, relDir string, files []skillWriteFile, dirs map[string]bool) error {
	base := strings.TrimRight(target.LinuxRoot, "/") + "/" + relDir
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
		if err := c.probes.Chmod(ctx, target.Distro, "644", filePaths[i:end]...); err != nil {
			return fmt.Errorf("chmod 644 skill files in distro: %w", err)
		}
	}
	for i := 0; i < len(dirList); i += skillChmodBatch {
		end := i + skillChmodBatch
		if end > len(dirList) {
			end = len(dirList)
		}
		if err := c.probes.Chmod(ctx, target.Distro, "755", dirList[i:end]...); err != nil {
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

// compensateSkillDir 撤销一次目录写入（skills 与 resources 同一生命周期；调
// 用方 Compensate 持 applyMu）。CAS：目标树仍等于本次写入才恢复。恢复优先
// 用持久 prev 树（真实前像，保留至 Release/补偿终结：结构/空目录/链接/外部
// 后改均保全）——displace 本次写入 → 复验 → no-replace 放回 prev → 树复验；
// prev 材料丢失时退回内存快照（有界）重建；两者皆失 → unavailable（如实报
// 告，不臆造恢复）。先保存 compensation intent（ParentOpID）再恢复，不递归。
func (c *Coordinator) compensateSkillDir(h *CompensationHandle) CompensationOutcome {
	out := CompensationOutcome{Unit: h.unit, RelPath: h.relPath}
	// 撤销租用仍在：新写/reconcile 不得清理本操作材料。
	if h.opID != "" {
		c.setLeaseActive(h.opID, true)
	}
	compOpID := newOpID()
	if h.opID != "" {
		if err := c.state.saveIntent(OperationIntent{
			OpID: compOpID, Kind: OpKindCompensate, TargetKey: h.targetKey,
			TargetDistro: h.target.Distro, TargetHome: h.target.Home,
			TargetCLI: string(h.target.CLI),
			Unit: h.unit, RelPath: h.relPath,
			BeforeExists: h.prevExisted, AfterHash: h.writtenTreeHash,
			PrevTreeHash: h.prevTreeHash,
			ParentOpID: h.opID, CreatedAt: time.Now(),
		}); err != nil {
			// 补偿 intent 保存失败：不开始恢复写，如实报 pending。
			out.Disposition = CompensationUnavailable
			out.Message = "compensation intent persist failed: " + err.Error()
			return out
		}
	}
	settle := func(disposition string) CompensationOutcome {
		out.Disposition = CompensationConfirmed
		if h.opID != "" {
			c.finishCompensation(h, compOpID, disposition)
		}
		return out
	}
	current := scanTargetSkillDir(h.uncPath, c.skillLimits())
	if current.Unverifiable {
		out.Disposition = CompensationIndeterminate
		out.Message = current.Reason
		return out
	}
	if !current.Exists {
		// 目标已不在（可能被用户删除）：视为已撤销。
		return settle("restored")
	}
	if current.TreeHash != h.writtenTreeHash {
		out.Disposition = CompensationSkipped
		out.Message = "target changed after write; external content retained"
		if h.opID != "" {
			c.finishCompensation(h, compOpID, "not-applied")
		}
		return out
	}
	if !h.prevExisted {
		if err := os.RemoveAll(h.uncPath); err != nil && !os.IsNotExist(err) {
			out.Disposition = CompensationUnavailable
			out.Message = err.Error()
			return out
		}
		return settle("restored")
	}
	// 优先：持久 prev 树（真实前像）no-replace 恢复。
	if restored, msg := c.restoreDirFromPersistedPrev(h); restored {
		return settle("restored")
	} else if msg != "" {
		// prev 材料在但恢复失败（冲突/不可验证）：如实报 unavailable，不删目标。
		out.Disposition = CompensationUnavailable
		out.Message = msg
		c.recordObservation(h.targetKey, compOpID, h.unit, OpEffectUnknown, OpRegistrationPending, OpRecoveryAvailable, "compensation-pending", "compensate")
		return out
	}
	// 兜底：内存快照重建（有界；prev 树丢失时）。空目录/链接同样保真。
	if !h.prevSnapshot {
		out.Disposition = CompensationUnavailable
		out.Message = "preimage unavailable (persistent prev tree missing and in-memory snapshot unavailable/oversized)"
		c.recordObservation(h.targetKey, compOpID, h.unit, OpEffectUnknown, OpRegistrationPending, OpRecoveryUnavailable, "compensation-pending", "compensate")
		return out
	}
	if err := c.restoreSkillPreimage(h); err != nil {
		out.Disposition = CompensationUnavailable
		out.Message = err.Error()
		c.recordObservation(h.targetKey, compOpID, h.unit, OpEffectUnknown, OpRegistrationPending, OpRecoveryAvailable, "compensation-pending", "compensate")
		return out
	}
	return settle("restored")
}

// restoreDirFromPersistedPrev 用持久 prev 树恢复目录前像：displace 本次写入
// 到唯一 compensated 名 → 复验 displaced 是本次写入 → no-replace 发布 prev →
// 树 hash 复验 → 清理 displaced。prev 材料不存在时返回 (false, "")（调用方
// 退回快照兜底）；恢复失败返回 (false, message)（两处均保留，不删任何一处）。
func (c *Coordinator) restoreDirFromPersistedPrev(h *CompensationHandle) (bool, string) {
	if h.displacedRel == "" {
		return false, ""
	}
	prevPath := absOf(h.target, h.displacedRel)
	pinfo, perr := os.Lstat(prevPath)
	if perr != nil {
		if errors.Is(perr, os.ErrNotExist) {
			return false, "" // prev 材料已不在：退回快照兜底
		}
		return false, "prev material unreadable: " + perr.Error()
	}
	if !pinfo.IsDir() {
		return false, "prev material is not a directory"
	}
	pv := scanTargetSkillDir(prevPath, c.skillLimits())
	if pv.Unverifiable {
		return false, "prev tree unverifiable: " + pv.Reason
	}
	if h.prevTreeHash != "" && pv.TreeHash != h.prevTreeHash {
		return false, fmt.Sprintf("prev tree hash mismatch (expected baseline); prev kept at %s", filepath.Base(prevPath))
	}
	// displace 本次写入（目标已 CAS 验证 == writtenTreeHash）到唯一名。
	displaced := h.uncPath + ".compensated-" + h.opID
	if _, derr := os.Lstat(displaced); derr == nil {
		return false, "compensated displaced slot occupied: " + filepath.Base(displaced)
	}
	if err := os.Rename(h.uncPath, displaced); err != nil {
		return false, "displace current tree: " + err.Error()
	}
	dv := scanTargetSkillDir(displaced, c.skillLimits())
	if dv.Unverifiable || dv.TreeHash != h.writtenTreeHash {
		// 移走的不是本次写入：no-replace 放回，保留两处。
		if rbErr := publishNoReplaceFn(displaced, h.uncPath); rbErr != nil {
			return false, fmt.Sprintf("displaced tree mismatch; put-back failed (%v); both retained", rbErr)
		}
		return false, "displaced tree no longer matches this write; external version retained"
	}
	if err := publishNoReplaceFn(prevPath, h.uncPath); err != nil {
		if isNoReplaceExists(err) {
			return false, fmt.Sprintf("external target appeared during restore; both retained (prev at %s, displaced at %s)", filepath.Base(prevPath), filepath.Base(displaced))
		}
		return false, fmt.Sprintf("publish prev tree (no-replace): %v (prev kept at %s, displaced at %s)", err, filepath.Base(prevPath), filepath.Base(displaced))
	}
	got := scanTargetSkillDir(h.uncPath, c.skillLimits())
	if got.Unverifiable || (h.prevTreeHash != "" && got.TreeHash != h.prevTreeHash) {
		// 已放回但复验不符：材料保留，如实报告（不回滚成删目标）。
		return false, fmt.Sprintf("restored tree hash mismatch/unverifiable; prev kept at %s, displaced at %s", filepath.Base(prevPath), filepath.Base(displaced))
	}
	_ = os.RemoveAll(displaced)
	return true, ""
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
		if e.IsDir {
			// 空目录：结构保真重建（不入树 hash，但恢复时不得丢）。
			unc := filepath.Join(staging, filepath.FromSlash(e.RelPath))
			if err := os.MkdirAll(unc, 0o755); err != nil {
				return err
			}
			continue
		}
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
	if err := c.chmodTree(context.Background(), h.target, h.relPath, preimageFiles(h.prevFiles), preimageDirsFor(h.relPath, h.prevFiles)); err != nil {
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

// preimageDirsFor 汇总前像目录集（单元根 + 各父目录）供 755 补偿。
func preimageDirsFor(relDir string, prev []skillPreimageEntry) map[string]bool {
	dirs := map[string]bool{relDir: true}
	for _, e := range prev {
		for d := relDirsOf(e.RelPath); d != ""; d = relDirsOf(d) {
			dirs[relDir+"/"+d] = true
			if !strings.Contains(d, "/") {
				break
			}
		}
	}
	return dirs
}

// preimageTreeHash 计算前像的规范化树 hash（与 scanTargetSkillDir 同构：链接
// 条目 hash("link:"+target)，文件条目 size+hash(bytes)；目录条目不入 hash——
// 与扫描面一致，空目录只作结构保真不参与字节比对）——恢复后读回比对的
// 期望值。
func preimageTreeHash(prev []skillPreimageEntry) string {
	entries := make([]SkillFileEntry, 0, len(prev))
	for _, e := range prev {
		if e.IsDir {
			continue
		}
		if e.IsLink {
			entries = append(entries, SkillFileEntry{RelPath: e.RelPath, Hash: hexHash([]byte("link:" + e.LinkTarget))})
			continue
		}
		entries = append(entries, SkillFileEntry{RelPath: e.RelPath, Size: int64(len(e.Bytes)), Hash: hexHash(e.Bytes)})
	}
	return skillTreeHash(entries)
}
