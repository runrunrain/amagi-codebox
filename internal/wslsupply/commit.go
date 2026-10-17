package wslsupply

// commit.go — 攻坚契约的统一文件/目录提交生命周期（attack-contract「文件
// 提交与恢复」）：写前验证前像与候选 → 保存不可变 intent 并读回 → 复核 →
// 保留 displaced 的 no-replace 发布 → 读回验证 → settled 同次主清单确认 →
// 按终结顺序清理。任一阶段出错都返回保留事实的 CommitReceipt（error 非 nil
// 时 receipt 也不可丢）。取消停止后继业务写入，不取消已发生写入的事实登记
// 与有限恢复责任（独立有界 recoveryCtx）。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// 确定性故障接缝（测试与生产同一生命周期；持续故障不是仅第一次破坏）
// ---------------------------------------------------------------------------

// 提交生命周期阶段名（fault seam / 观察码共用）。
const (
	stagePrepare  = "prepare"
	stageIntent   = "intent"
	stageMove     = "move"
	stagePublish  = "publish"
	stageVerify   = "verify"
	stageFinalize = "finalize"
	stageCleanup  = "cleanup"
)

// commitFault 描述一个注入故障：stage 命中时返回 err。times=0 表示持续
// 故障（每次命中都触发）；times>0 表示只触发前 N 次。
type commitFault struct {
	stage string
	times int
	err   error
}

// commitFaults 是锁内故障表（测试注入；生产恒空）。
type commitFaults struct {
	mu     sync.Mutex
	faults []commitFault
	hits   map[string]int
}

func newCommitFaults() *commitFaults {
	return &commitFaults{hits: map[string]int{}}
}

// add 注入一个故障。
func (f *commitFaults) add(stage string, times int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.faults = append(f.faults, commitFault{stage: stage, times: times, err: err})
}

// fail 返回该阶段应注入的错误（nil = 放行）。
func (f *commitFaults) fail(stage string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.faults {
		ft := &f.faults[i]
		if ft.stage != stage {
			continue
		}
		hit := f.hits[stage]
		if ft.times > 0 && hit >= ft.times {
			continue
		}
		f.hits[stage] = hit + 1
		return ft.err
	}
	return nil
}

// errNoReplaceExists 表示 no-replace 发布遇到已存在的目标（外部版本出现，
// 绝不覆盖）。
var errNoReplaceExists = errors.New("no-replace destination already exists")

// isNoReplaceExists 判定 no-replace 发布/放回是否因目标已存在而被拒。生产原语
// platform.MoveNoReplace 按契约返回 os.ErrExist 族（Windows 9P 上的
// ACCESS_DENIED 仅在双方实体可观察时以 errors.Join(os.ErrExist, …) 归类）；
// errNoReplaceExists 保留给注入接缝。此前只认哨兵，生产冲突从不命中
// publish-conflict 分支（Windows 真机并发用例暴露）。
// 归类只决定「保留两处」的报告形态，绝不授权覆盖。
func isNoReplaceExists(err error) bool {
	return errors.Is(err, errNoReplaceExists) || errors.Is(err, os.ErrExist)
}

// errVerifyReadback 标记「读回字节失配」类验证失败（区别于 chmod/权限失败：
// 字节相符时的读回失配可能是瞬时读毛刺；权限失败永不视为瞬时成功）。
var errVerifyReadback = errors.New("read-back mismatch")

// publishNoReplaceFn 把 source 原子发布/移动到不存在的 destination；目标
// 已存在时返回 ErrExist 类错误（platform.ErrMoveNoReplaceUnsupported 语义由
// G0 平台原语提供：文件/目录均支持、分 OS 原子实现、不支持即 error 不降级
// 覆盖）。测试可注入确定性接缝。
var publishNoReplaceFn = platform.MoveNoReplace

// recoveryBudget 是取消后独立恢复的总预算（每步至多一次；同步 I/O 不能被
// ctx 强制打断，预算耗尽留 pending，不搞 goroutine 假超时）。
const recoveryBudget = 10 * time.Second

// boundedRecoveryCtx 构造与请求取消独立的有限恢复预算。
func boundedRecoveryCtx(requestCtx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(requestCtx), recoveryBudget)
}

// ---------------------------------------------------------------------------
// commitFile：常规文件统一提交
// ---------------------------------------------------------------------------

// commitFile 执行一次文件提交生命周期（调用方持有 applyMu）。返回的
// receipt 永远非 nil；receipt.Err 非 nil 时错误路径的事实（intent/材料/
// 已发生副作用）仍完整保留在 receipt 内。
func (c *Coordinator) commitFile(ctx context.Context, req CommitRequest) *CommitReceipt {
	targetKey := targetKeyOf(req.Target)
	opID := newOpID()
	uncPath := filepath.Join(req.Target.UNCRoot, filepath.FromSlash(req.RelPath))
	dir := filepath.Dir(uncPath)
	rc := &CommitReceipt{
		OpID: opID, TargetKey: targetKey, Unit: req.Unit, RelPath: req.RelPath,
		Kind: req.Kind, Effect: OpEffectNone, Registration: OpRegistrationPending, Recovery: OpRecoveryNone,
		AfterHash: hexHash(req.Candidate),
	}
	fail := func(stage string, err error, detail string) *CommitReceipt {
		rc.Err = err
		rc.ErrDetail = detail
		c.recordObservation(targetKey, opID, req.Unit, rc.Effect, rc.Registration, rc.Recovery, detail, stage)
		return rc
	}

	// 0) 同路径未决 intent 先 reconcile（不唤醒其它 distro，只处理请求目标）。
	if report := c.reconcileTargetPath(ctx, req.Target, req.Unit, req.RelPath); report.blocked {
		return fail(stagePrepare, fmt.Errorf("pending operation %s on same path unresolved: %s", report.opID, report.reason), "pending-intent-conflict")
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(stagePrepare, fmt.Errorf("mkdir supply target dir: %w", err), "prepare-failed")
	}
	// 1) 读前像并分类。
	before, beforeClass := readPreimageClass(uncPath)
	switch beforeClass {
	case preimageUnreadable, preimageUnsupported:
		return fail(stagePrepare, fmt.Errorf("read supply target preimage: %s", beforeClass), "target-unreadable")
	case preimageRegular:
		rc.BeforeExists = true
		rc.BeforeHash = hexHash(before)
	}
	// CAS 复核：目标必须仍等于计划预期值（拒绝旧计划，不用旧备份替换新目标）。
	if !req.SkipBeforeCAS {
		if err := verifyExpectedBefore(rc.BeforeHash, rc.BeforeExists, req.ExpectedBefore); err != nil {
			return fail(stagePrepare, err, "target-changed-since-planning")
		}
	}
	if err := c.faults.fail(stagePrepare); err != nil {
		return fail(stagePrepare, err, "prepare-failed")
	}

	stagingBase := ".amagi-staging/" + opID
	// 2) 前像持久备份（不可逆移动前已经持久且完整验证）。
	if rc.BeforeExists {
		backupRel, err := c.persistPreimageBackup(ctx, req.Target, opID, stagingBase+"/before", before, req.Sensitive)
		if err != nil {
			rc.Recovery = OpRecoveryUnavailable
			return fail(stagePrepare, err, "backup-unavailable") // 原目标不动
		}
		rc.BackupRel = backupRel
		rc.Recovery = OpRecoveryAvailable
	}

	// 3) 候选 staging（预验证字节/权限）。
	candidateRel, err := c.persistCandidateStaging(ctx, req.Target, opID, stagingBase+"/candidate", req.Candidate, req.Sensitive)
	if err != nil {
		c.cleanupMaterial(req.Target, rc, stagePrepare)
		return fail(stagePrepare, err, "candidate-unavailable")
	}
	rc.StagingRel = candidateRel

	// 4) displaced 名预留在 intent 前（第一次移动前已记录）。
	displacedRel := ""
	if rc.BeforeExists {
		displacedRel = relPathOf(req.Target, filepath.Join(dir, ".amagi-supply-displaced-"+opID))
		rc.DisplacedRel = displacedRel
	}

	// 5) intent 保存 + 读回（失败 → 原目标不动，不先写后补记）。
	intent := OperationIntent{
		OpID: opID, Kind: req.Kind, TargetKey: targetKey,
		TargetDistro: req.Target.Distro, TargetHome: req.Target.Home,
		TargetCLI: string(req.Target.CLI),
		Unit: req.Unit, RelPath: req.RelPath, Sensitive: req.Sensitive,
		BeforeExists: rc.BeforeExists, BeforeHash: rc.BeforeHash, AfterHash: hexHash(req.Candidate),
		BackupRel: rc.BackupRel, StagingRel: rc.StagingRel, DisplacedRel: displacedRel,
		PluginMeta: req.PluginMeta,
		CreatedAt:  time.Now(),
	}
	if err := c.state.saveIntent(intent); err != nil {
		c.cleanupMaterial(req.Target, rc, stageIntent)
		return fail(stageIntent, err, "intent-unavailable")
	}
	if err := c.faults.fail(stageIntent); err != nil {
		// intent 已在盘：清理材料前保留 intent 由 reconcile 处置（effect none）。
		c.recordObservation(targetKey, opID, req.Unit, OpEffectNone, OpRegistrationPending, OpRecoveryNone, "intent-failed", stageIntent)
		rc.Err = err
		rc.ErrDetail = "intent-failed"
		return rc
	}

	// 6) move 边界：ctx 检查 + 复核 backup 与目标。
	if err := ctx.Err(); err != nil {
		rc.Err = fmt.Errorf("commit canceled before move: %w", err)
		rc.ErrDetail = "context-canceled"
		c.recordObservation(targetKey, opID, req.Unit, OpEffectNone, OpRegistrationPending, rc.Recovery, "context-canceled", stageMove)
		c.cleanupAfterCanceledPrepare(req.Target, rc)
		return rc
	}
	if err := recheckBeforeIntact(uncPath, rc.BeforeHash, rc.BeforeExists); err != nil {
		return fail(stageMove, err, "target-changed-before-move")
	}
	if rc.BackupRel != "" {
		if err := verifyMaterialBytes(req.Target, rc.BackupRel, before); err != nil {
			rc.Recovery = OpRecoveryUnavailable
			return fail(stageMove, err, "backup-invalid-before-move") // 坏备份不能换目标
		}
	}

	// 7) 前向发布：移走目标 → 复验实体 → no-replace 发布候选。
	if rc.BeforeExists {
		if err := c.faults.fail(stageMove); err != nil {
			return c.classifyAfterMoveError(ctx, req, rc, before, err)
		}
		if err := os.Rename(uncPath, absOf(req.Target, displacedRel)); err != nil {
			return c.classifyAfterMoveError(ctx, req, rc, before, err)
		}
		// 移动后复验该实体仍是预期 before（外部竞争/坏路径检测）。
		if got, err := os.ReadFile(absOf(req.Target, displacedRel)); err != nil || hexHash(got) != rc.BeforeHash {
			rc.Effect = OpEffectUnknown
			rc.Recovery = OpRecoveryConflict
			// 尝试 no-replace 放回（原路径又出现新目标则保留两处）。
			c.tryDisplaceBack(req.Target, rc, absOf(req.Target, displacedRel), uncPath)
			return fail(stageMove, fmt.Errorf("moved entity no longer matches preimage (err=%v)", err), "move-verify-failed")
		}
	}
	if err := ctx.Err(); err != nil {
		// 第一次移动已是副作用：用独立有限恢复责任放回（不当作零写）。
		c.recoverAfterCancel(ctx, req, rc, before)
		if rc.Effect == OpEffectRestored {
			rc.Err = fmt.Errorf("commit canceled after move-aside: target restored from preimage: %w", err)
			rc.ErrDetail = "canceled-restored"
		} else {
			rc.Err = fmt.Errorf("commit canceled after move-aside: %w (target state: %s; preimage kept at %s)", err, rc.Effect, rc.DisplacedRel)
			rc.ErrDetail = "canceled-recovery-pending"
		}
		c.recordObservation(targetKey, opID, req.Unit, rc.Effect, OpRegistrationPending, rc.Recovery, rc.ErrDetail, stageMove)
		return rc
	}
	if err := c.faults.fail(stagePublish); err != nil {
		return c.classifyAfterMoveError(ctx, req, rc, before, err)
	}
	if err := publishNoReplaceFn(absOf(req.Target, candidateRel), uncPath); err != nil {
		if isNoReplaceExists(err) {
			// 外部版本出现：保留两处，不覆盖（候选留在 staging）。
			rc.Effect = OpEffectUnknown
			rc.Recovery = OpRecoveryConflict
			return fail(stagePublish, fmt.Errorf("external version appeared at target during publish; kept both (candidate staged at %s, displaced preimage at %s): %w", candidateRel, displacedRel, err), "publish-conflict")
		}
		return c.classifyAfterMoveError(ctx, req, rc, before, err)
	}
	rc.Effect = OpEffectCommitted

	// 8) 发布后精确读回 + 敏感权限验证。
	verifyErr := c.verifyPublished(ctx, req.Target, req.RelPath, req.Candidate, req.Sensitive)
	if ferr := c.faults.fail(stageVerify); ferr != nil {
		verifyErr = ferr
	}
	if verifyErr != nil {
		// 已发生副作用：条件恢复（backup 有效则 displaced 流程恢复；首建
		// 删除半提交；权限类失败不视为瞬时成功）。瞬时读回毛刺（字节相符）
		// 继续走 finalize（提交实际完好）。
		if c.recoverUnverifiedCommit(ctx, req, rc, before, verifyErr) {
			// 瞬时读毛刺：提交完好，继续 finalize。
		} else {
			c.recordObservation(targetKey, opID, req.Unit, rc.Effect, OpRegistrationPending, rc.Recovery, rc.ErrDetail, stageVerify)
			return rc
		}
	}

	// 9) finalize：settled + StateEntry 同次主清单保存。
	if serr := c.finalizeCommit(req, rc, "committed"); serr != nil {
		if ferr := c.faults.fail(stageFinalize); ferr != nil {
			rc.Err = fmt.Errorf("wsl supply state persist failed after commit (content kept on target; intent %s retained; next run treats it as committed-unregistered until reconciled): %v", opID, ferr)
		} else {
			rc.Err = fmt.Errorf("wsl supply state persist failed after commit (content kept on target; intent %s retained; next run treats it as committed-unregistered until reconciled): %w", opID, serr)
		}
		rc.ErrDetail = "content-committed-state-unpersisted"
		rc.Registration = OpRegistrationPending
		c.recordObservation(targetKey, opID, req.Unit, OpEffectCommitted, OpRegistrationPending, rc.Recovery, rc.ErrDetail, stageFinalize)
		rc.UndoLease = c.newUndoLease(req, rc, before)
		c.setLeaseActive(opID, true) // 同上：租用跟随句柄，不等补偿才生效
		return rc
	}
	rc.Registration = OpRegistrationConfirmed
	rc.UndoLease = c.newUndoLease(req, rc, before)
	// 撤销租用自提交起生效：活跃句柄期间 Reconcile/新写不清理本操作材料
	//（契约：保留撤销租用直到调用方确认生命周期）。
	c.setLeaseActive(opID, true)
	c.cleanupMaterial(req.Target, rc, stageCleanup) // 备份/displaced/staging；intent 留给租约 Release
	c.recordObservation(targetKey, opID, req.Unit, OpEffectCommitted, OpRegistrationConfirmed, OpRecoveryNone, "committed", stageCleanup)
	return rc
}

// ---------------------------------------------------------------------------
// 辅助：前像分类 / 备份 / 候选 / 验证 / 恢复
// ---------------------------------------------------------------------------

type preimageClass string

const (
	preimageAbsent      preimageClass = "absent"
	preimageRegular     preimageClass = "regular"
	preimageUnreadable  preimageClass = "unreadable"
	preimageUnsupported preimageClass = "unsupported-type"
)

// readPreimageClass 读取并分类目标前像。
func readPreimageClass(uncPath string) ([]byte, preimageClass) {
	info, err := os.Lstat(uncPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, preimageAbsent
		}
		return nil, preimageUnreadable
	}
	if !info.Mode().IsRegular() {
		return nil, preimageUnsupported
	}
	data, err := os.ReadFile(uncPath)
	if err != nil {
		return nil, preimageUnreadable
	}
	return data, preimageRegular
}

// verifyExpectedBefore 校验当前前像与计划预期一致（CAS；空预期 = absent）。
func verifyExpectedBefore(beforeHash string, existed bool, expected string) error {
	switch {
	case expected == "" && existed:
		return errors.New("target file appeared since planning (external content retained)")
	case expected != "" && !existed:
		return errors.New("target file disappeared since planning (local deletion retained)")
	case expected != "" && existed && beforeHash != expected:
		return errors.New("target file changed since planning (external edit retained)")
	}
	return nil
}

// recheckBeforeIntact 在不可逆移动前复核目标仍为预期前像。
func recheckBeforeIntact(uncPath string, beforeHash string, existed bool) error {
	if !existed {
		if _, err := os.Lstat(uncPath); err == nil {
			return errors.New("target appeared before move")
		} else if !errors.Is(err, os.ErrNotExist) {
			return errors.New("target unreadable before move")
		}
		return nil
	}
	got, err := os.ReadFile(uncPath)
	if err != nil || hexHash(got) != beforeHash {
		return fmt.Errorf("target no longer matches expected preimage before move (err=%v)", err)
	}
	return nil
}

// relPathOf 把目标根下绝对路径转回相对路径（内部生成材料用）。
func relPathOf(t TargetRef, abs string) string {
	rel, err := filepath.Rel(t.UNCRoot, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

// absOf 把相对路径解析回目标根绝对路径。
func absOf(t TargetRef, rel string) string {
	return filepath.Join(t.UNCRoot, filepath.FromSlash(rel))
}

// persistPreimageBackup 把前像持久化到目标 .amagi-staging/recovery-<opID>/
// before：验证 0700 staging → 写 → Sync/Close → distro chmod 600 → 复验
// mode 与完整字节 → 最终读回。任何失败 → 中止（原目标仍未变）。
func (c *Coordinator) persistPreimageBackup(ctx context.Context, target TargetRef, opID, rel string, before []byte, sensitive bool) (string, error) {
	stagingUNC, _, err := c.stagingDir(ctx, target, sensitive)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(stagingUNC, "recovery-"+opID)
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return "", fmt.Errorf("mkdir recovery dir: %w", err)
	}
	finalPath := filepath.Join(dst, "before")
	if err := writeVerifiedFile(finalPath, before, 0o600); err != nil {
		return "", fmt.Errorf("persist preimage backup: %w", err)
	}
	finalRel := relPathOf(target, finalPath)
	if finalRel == "" {
		_ = os.Remove(finalPath)
		return "", errors.New("derive preimage backup rel path")
	}
	if sensitive {
		// chmod/验证路径必须与实际落盘路径同源派生（relPathOf）——此前用调用方
		// 拼的 rel（缺 recovery- 前缀）导致 chmod 指向不存在的路径，正常更新被
		// 误判 backup-unavailable（已证实的错位缺陷）。
		linuxPath := strings.TrimSuffix(target.LinuxRoot, "/") + "/" + finalRel
		if err := c.probes.Chmod(ctx, target.Distro, "600", linuxPath); err != nil {
			_ = os.Remove(finalPath)
			return "", fmt.Errorf("chmod 600 preimage backup in distro: %w", err)
		}
		if mode, ok := c.probes.StatMode(ctx, target.Distro, linuxPath); !ok || mode != "600" {
			_ = os.Remove(finalPath)
			return "", fmt.Errorf("verify preimage backup mode: got %q ok=%v (expected 600)", mode, ok)
		}
	}
	// 最终备份名再次读回（完整字节）。
	got, err := os.ReadFile(finalPath)
	if err != nil || hexHash(got) != hexHash(before) {
		_ = os.Remove(finalPath)
		return "", fmt.Errorf("verify preimage backup readback (err=%v, equal=%v)", err, hexHash(got) == hexHash(before))
	}
	return finalRel, nil
}

// persistCandidateStaging 准备并预验证候选 staging（字节/类型/权限）。
func (c *Coordinator) persistCandidateStaging(ctx context.Context, target TargetRef, opID, rel string, candidate []byte, sensitive bool) (string, error) {
	stagingUNC, _, err := c.stagingDir(ctx, target, sensitive)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(stagingUNC, opID)
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return "", fmt.Errorf("mkdir candidate staging dir: %w", err)
	}
	finalPath := filepath.Join(dst, "candidate")
	if err := writeVerifiedFile(finalPath, candidate, 0o600); err != nil {
		return "", fmt.Errorf("persist candidate staging: %w", err)
	}
	finalRel := relPathOf(target, finalPath)
	if finalRel == "" {
		_ = os.Remove(finalPath)
		return "", errors.New("derive candidate staging rel path")
	}
	if sensitive {
		// 与 backup 同源派生（chmod 验证路径 == 实际落盘路径）。
		linuxPath := strings.TrimSuffix(target.LinuxRoot, "/") + "/" + finalRel
		if err := c.probes.Chmod(ctx, target.Distro, "600", linuxPath); err != nil {
			_ = os.Remove(finalPath)
			return "", fmt.Errorf("chmod 600 candidate staging in distro: %w", err)
		}
		if mode, ok := c.probes.StatMode(ctx, target.Distro, linuxPath); !ok || mode != "600" {
			_ = os.Remove(finalPath)
			return "", fmt.Errorf("verify candidate staging mode: got %q ok=%v (expected 600)", mode, ok)
		}
	}
	got, err := os.ReadFile(finalPath)
	if err != nil || hexHash(got) != hexHash(candidate) {
		_ = os.Remove(finalPath)
		return "", fmt.Errorf("verify candidate staging readback (err=%v)", err)
	}
	return finalRel, nil
}

// stagingDir 返回 .amagi-staging 目录：敏感单元走 ensurePrivateStaging 的
// 0700 distro 内验证（fail closed）；普通单元只需宿主侧私有目录（chmod 700
// 尽力而为，不做 distro 内探测门——无秘密不适用隔离验证契约）。
func (c *Coordinator) stagingDir(ctx context.Context, target TargetRef, sensitive bool) (string, string, error) {
	if sensitive {
		return c.ensurePrivateStaging(ctx, target)
	}
	unc := filepath.Join(target.UNCRoot, ".amagi-staging")
	if err := os.MkdirAll(unc, 0o700); err != nil {
		return "", "", fmt.Errorf("mkdir supply staging dir: %w", err)
	}
	_ = os.Chmod(unc, 0o700)
	linux := strings.TrimSuffix(target.LinuxRoot, "/") + "/.amagi-staging"
	return unc, linux, nil
}

// writeVerifiedFile：唯一 tmp → 写 → Sync → Close → chmod → 读回 → rename。
func writeVerifiedFile(finalPath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(finalPath)
	tmp, err := os.CreateTemp(dir, ".stage-*")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if got, rerr := os.ReadFile(tmpPath); rerr != nil || hexHash(got) != hexHash(data) {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("verify tmp readback (err=%v)", rerr)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

// verifyMaterialBytes 复核目标根下某材料文件仍是预期字节。
func verifyMaterialBytes(target TargetRef, rel string, expect []byte) error {
	got, err := os.ReadFile(absOf(target, rel))
	if err != nil {
		return fmt.Errorf("read material %s: %w", rel, err)
	}
	if hexHash(got) != hexHash(expect) {
		return fmt.Errorf("material %s no longer matches expected bytes", rel)
	}
	return nil
}

// verifyPublished 发布后精确读回候选字节与敏感权限。
func (c *Coordinator) verifyPublished(ctx context.Context, target TargetRef, relPath string, candidate []byte, sensitive bool) error {
	uncPath := absOf(target, relPath)
	got, err := os.ReadFile(uncPath)
	if err != nil || hexHash(got) != hexHash(candidate) {
		return fmt.Errorf("%w after commit (err=%v)", errVerifyReadback, err)
	}
	linuxPath := target.LinuxRoot + "/" + strings.TrimPrefix(relPath, "/")
	if sensitive {
		if mode, ok := c.probes.StatMode(ctx, target.Distro, linuxPath); !ok || mode != "600" {
			if err := c.probes.Chmod(ctx, target.Distro, "600", linuxPath); err != nil {
				return fmt.Errorf("retighten after commit: %w", err)
			}
			if mode, ok := c.probes.StatMode(ctx, target.Distro, linuxPath); !ok || mode != "600" {
				return fmt.Errorf("mode unverified after commit: got %q ok=%v (expected 600)", mode, ok)
			}
		}
		return nil
	}
	// 普通单元沿用 M2 语义：rename 后 distro 内 chmod 600 补偿（9P 不落
	// POSIX 位）；失败触发条件恢复（restoreFromVerifiedBackup / 首建删除）。
	if err := c.probes.Chmod(ctx, target.Distro, "600", linuxPath); err != nil {
		return fmt.Errorf("chmod 600 supply file in distro: %w", err)
	}
	return nil
}

// classifyAfterMoveError：move/publish 返回失败 → 重新分类实际目标，不能
// 假定未写。目标缺失且 displaced 在 → no-replace 放回；外部新目标保留两处。
func (c *Coordinator) classifyAfterMoveError(ctx context.Context, req CommitRequest, rc *CommitReceipt, before []byte, cause error) *CommitReceipt {
	uncPath := absOf(req.Target, rc.RelPath)
	rc.Effect = OpEffectUnknown
	if cur, err := os.ReadFile(uncPath); err == nil {
		switch {
		case hexHash(cur) == hexHash(req.Candidate):
			rc.Effect = OpEffectCommitted // 移动其实已落地
		case rc.BeforeExists && hexHash(cur) == rc.BeforeHash:
			rc.Effect = OpEffectNone // 目标未被动过
		default:
			rc.Recovery = OpRecoveryConflict
		}
	} else if errors.Is(err, os.ErrNotExist) && rc.DisplacedRel != "" {
		// 目标缺失且本次移走了旧实体：no-replace 放回（外部新目标拒绝）。
		if rbErr := publishNoReplaceFn(absOf(req.Target, rc.DisplacedRel), uncPath); rbErr != nil {
			rc.Recovery = OpRecoveryUnavailable
			rc.Err = fmt.Errorf("%v (restore-displaced failed: %v; preimage kept at %s)", cause, rbErr, rc.DisplacedRel)
			rc.ErrDetail = "restore-failed-preimage-kept"
			c.recordObservation(rc.TargetKey, rc.OpID, req.Unit, rc.Effect, rc.Registration, rc.Recovery, rc.ErrDetail, stageMove)
			return rc
		}
		rc.Effect = OpEffectNone
		rc.Recovery = OpRecoveryNone
		rc.Err = fmt.Errorf("%v (preimage restored from displaced)", cause)
		rc.ErrDetail = "restored-after-failed-move"
		c.cleanupMaterial(req.Target, rc, stageCleanup)
		c.recordObservation(rc.TargetKey, rc.OpID, req.Unit, rc.Effect, rc.Registration, OpRecoveryNone, rc.ErrDetail, stageMove)
		return rc
	} else if errors.Is(err, os.ErrNotExist) {
		rc.Effect = OpEffectNone
	}
	if rc.ErrDetail == "" {
		rc.Err = cause
		rc.ErrDetail = "commit-move-unknown"
	}
	c.recordObservation(rc.TargetKey, rc.OpID, req.Unit, rc.Effect, rc.Registration, rc.Recovery, rc.ErrDetail, stageMove)
	return rc
}

// tryDisplaceBack 用 no-replace 把 displaced 实体放回原路径；原路径又出现
// 新目标就保留两处并报冲突，绝不删任何一处。
func (c *Coordinator) tryDisplaceBack(target TargetRef, rc *CommitReceipt, displaced, original string) {
	if err := publishNoReplaceFn(displaced, original); err != nil {
		rc.Recovery = OpRecoveryConflict
		logWarn(c.log, "wslsupply", "恢复落位前出现外部新目标，两处均保留",
			"displaced="+filepath.Base(displaced)+" original="+filepath.Base(original))
	}
}

// recoverAfterCancel：move-aside 后取消的独立有限恢复（recoveryCtx；不动
// 外部新目标；失败保留 prev，空目录也不可 Remove）。
func (c *Coordinator) recoverAfterCancel(requestCtx context.Context, req CommitRequest, rc *CommitReceipt, before []byte) {
	rctx, cancel := boundedRecoveryCtx(requestCtx)
	defer cancel()
	uncPath := absOf(req.Target, rc.RelPath)
	if _, err := os.Lstat(uncPath); err == nil {
		// 原路径已有实体（候选发布已完成或外部版本）：保留现状，条件恢复
		// 交给 compensate 路径。
		return
	}
	if rc.DisplacedRel == "" {
		return
	}
	displaced := absOf(req.Target, rc.DisplacedRel)
	if _, derr := os.Lstat(displaced); derr != nil {
		return
	}
	if err := publishNoReplaceFn(displaced, uncPath); err != nil {
		rc.Recovery = OpRecoveryUnavailable
		logWarn(c.log, "wslsupply", "取消后恢复旧目录/文件失败（保留 prev，供人工恢复）", filepath.Base(displaced))
		return
	}
	// 读回确认 canceled-restored。
	if got, err := os.ReadFile(uncPath); err == nil && (!rc.BeforeExists || hexHash(got) == rc.BeforeHash) {
		rc.Effect = OpEffectRestored
		rc.Recovery = OpRecoveryNone
		c.cleanupMaterial(req.Target, rc, stageCleanup)
		_ = rctx
		return
	}
	if rc.BeforeExists {
		rc.Effect = OpEffectUnknown
	}
}

// recoverUnverifiedCommit 处理发布后验证失败：读回毛刺（字节相符且仅读回
// 失配类错误）返回 true 继续 finalize；权限/chmod 失败或内容失配做条件恢复
// （backup 有效则 displaced 流程恢复；首建删除半提交；无恢复材料保留现状），
// 返回 false。
func (c *Coordinator) recoverUnverifiedCommit(requestCtx context.Context, req CommitRequest, rc *CommitReceipt, before []byte, verifyErr error) bool {
	uncPath := absOf(req.Target, rc.RelPath)
	cur, curErr := os.ReadFile(uncPath)
	curMatches := curErr == nil && hexHash(cur) == hexHash(req.Candidate)
	switch {
	case curMatches && errors.Is(verifyErr, errVerifyReadback):
		// 瞬时读毛刺：提交实际完好（保留 verified 结论，继续 finalize）。
		rc.Effect = OpEffectCommitted
		rc.Err = nil
		rc.ErrDetail = ""
		return true
	case curErr == nil && rc.BeforeExists && hexHash(cur) == rc.BeforeHash:
		rc.Effect = OpEffectNone
	default:
		rc.Effect = OpEffectUnknown
	}
	// 首建且目标即本次写入（无外部内容）：chmod/权限失败删除半提交（本次
	// 写入可证明归属，不碰任何外部字节）。
	if !rc.BeforeExists && curMatches {
		if rmErr := os.Remove(uncPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			rc.Effect = OpEffectUnknown
			rc.Err = fmt.Errorf("%v (half-committed removal failed: %v)", verifyErr, rmErr)
			rc.ErrDetail = "half-committed-removal-failed"
			return false
		}
		rc.Effect = OpEffectNone
		rc.Err = fmt.Errorf("%v (half-committed target removed)", verifyErr)
		rc.ErrDetail = "removed-half-committed"
		c.cleanupMaterial(req.Target, rc, stageCleanup)
		_ = c.state.deleteIntent(rc.OpID)
		return false
	}
	// 有前像且 backup 已验证 → 有界恢复（不先删当前；外部后改保留）。
	if rc.BackupRel != "" && (curMatches || rc.Effect == OpEffectUnknown) {
		rctx, cancel := boundedRecoveryCtx(requestCtx)
		defer cancel()
		if berr := c.restoreFromVerifiedBackup(rctx, req, rc, before); berr == nil {
			rc.Effect = OpEffectRestored
			rc.Err = fmt.Errorf("%v; target restored from verified preimage backup", verifyErr)
			rc.ErrDetail = "restored-from-backup"
			c.cleanupMaterial(req.Target, rc, stageCleanup)
			_ = c.state.deleteIntent(rc.OpID)
			return false
		} else {
			rc.Err = fmt.Errorf("%v; recovery failed: %v (preimage kept at %s; target left intact, not deleted)", verifyErr, berr, rc.BackupRel)
			rc.ErrDetail = "recovery-unavailable"
			if rc.Recovery == OpRecoveryNone {
				rc.Recovery = OpRecoveryUnavailable
			}
			return false
		}
	}
	rc.Err = verifyErr
	if rc.Effect == OpEffectCommitted || curMatches {
		rc.Err = fmt.Errorf("%v (content present but unverified)", verifyErr)
		rc.ErrDetail = "committed-unverified"
	} else if rc.ErrDetail == "" {
		rc.ErrDetail = "verify-failed"
	}
	return false
}

// restoreFromVerifiedBackup 用独立恢复 staging 以 no-replace 落位已验证
// backup（先移走当前目标到 displaced、核验、再放回；不先删当前）。
func (c *Coordinator) restoreFromVerifiedBackup(rctx context.Context, req CommitRequest, rc *CommitReceipt, before []byte) error {
	if err := rctx.Err(); err != nil {
		return err
	}
	uncPath := absOf(req.Target, rc.RelPath)
	cur, err := os.ReadFile(uncPath)
	if err != nil {
		return fmt.Errorf("read current target: %w", err)
	}
	if hexHash(cur) != hexHash(req.Candidate) {
		// 目标已是外部版本：保留，不覆盖。
		rc.Recovery = OpRecoveryConflict
		return fmt.Errorf("target changed to unknown content; external version retained")
	}
	// 移走本次写入到唯一 restore-displaced 名（与发布期的 displaced 并存，
	// 不覆盖已有材料）。
	restoreDisplaced := relPathOf(req.Target, filepath.Join(filepath.Dir(uncPath), ".amagi-supply-displaced-"+rc.OpID+"-restore"))
	if restoreDisplaced == "" {
		return errors.New("cannot derive restore displaced name")
	}
	displaced := absOf(req.Target, restoreDisplaced)
	if _, derr := os.Lstat(displaced); derr == nil {
		return fmt.Errorf("restore displaced slot occupied: %s", restoreDisplaced)
	}
	if err := os.Rename(uncPath, displaced); err != nil {
		return fmt.Errorf("displace current commit: %w", err)
	}
	if got, derr := os.ReadFile(displaced); derr != nil || hexHash(got) != hexHash(req.Candidate) {
		// 移走的不是本次写入：no-replace 放回，保留两处。
		c.tryDisplaceBack(req.Target, rc, displaced, uncPath)
		return fmt.Errorf("displaced entity no longer matches this commit")
	}
	// 独立恢复 staging（复验 backup 字节后 no-replace 落位）。
	if err := verifyMaterialBytes(req.Target, rc.BackupRel, before); err != nil {
		return err
	}
	if err := publishNoReplaceFn(absOf(req.Target, rc.BackupRel), uncPath); err != nil {
		return fmt.Errorf("publish verified backup (no-replace): %w", err)
	}
	got, rerr := os.ReadFile(uncPath)
	if rerr != nil || hexHash(got) != hexHash(before) {
		return fmt.Errorf("verify restored bytes (err=%v)", rerr)
	}
	_ = os.Remove(displaced)
	_ = os.RemoveAll(absOf(req.Target, restoreDisplaced))
	return nil
}

// cleanupAfterCanceledPrepare：intent 已保存但尚未移动目标时取消 → 清理
// 本次材料（backup/staging/displaced 未使用），intent 删除（无副作用发生）。
func (c *Coordinator) cleanupAfterCanceledPrepare(target TargetRef, rc *CommitReceipt) {
	c.cleanupMaterial(target, rc, stageCleanup)
	_ = c.state.deleteIntent(rc.OpID)
}

// cleanupMaterial 有界清理本次操作材料（backup/staging/displaced）；失败仅
// 报 residual，不无界删材料、不重复业务。清理不影响 intent/settled 事实。
func (c *Coordinator) cleanupMaterial(target TargetRef, rc *CommitReceipt, stage string) {
	if err := c.faults.fail(stageCleanup); err != nil && stage != stagePrepare {
		logWarn(c.log, "wslsupply", "材料清理被故障接缝拦截（保留材料）", rc.OpID)
		return
	}
	for _, rel := range []string{rc.BackupRel, rc.StagingRel, rc.DisplacedRel} {
		if rel == "" {
			continue
		}
		if err := os.RemoveAll(absOf(target, rel)); err != nil && !errors.Is(err, os.ErrNotExist) {
			logWarn(c.log, "wslsupply", "提交材料清理失败（隐藏残留，不影响使用）", rel)
		}
	}
	// staging/recovery 目录壳（opID 子目录已由上一步删除；recovery-<opID>
	// 目录可能只剩空壳）。
	_ = os.RemoveAll(filepath.Join(target.UNCRoot, ".amagi-staging", rc.OpID))
}

// finalizeCommit 同次主清单保存 settled 事实 + 调用方登记闭包（StateEntry
// 更新与 SettledOperations 同次写）。
func (c *Coordinator) finalizeCommit(req CommitRequest, rc *CommitReceipt, disposition string) error {
	return c.state.mutate(func(st *StateFile) {
		if st.SettledOperations == nil {
			st.SettledOperations = map[string]SettledOperation{}
		}
		st.SettledOperations[rc.OpID] = SettledOperation{
			TargetKey: rc.TargetKey, Unit: req.Unit, Disposition: disposition,
			SettledAt: time.Now(), AfterHash: rc.AfterHash,
		}
		if req.Register != nil {
			req.Register(st)
		}
		if req.Unit != "" {
			for i := range st.Units {
				if st.Units[i].Unit == req.Unit && st.Units[i].Generation == req.Target.Generation {
					st.Units[i].LastOperationID = rc.OpID
				}
			}
		}
	})
}

// newUndoLease 构造撤销租用句柄（携带持久位置与完整目标）。
func (c *Coordinator) newUndoLease(req CommitRequest, rc *CommitReceipt, before []byte) *CompensationHandle {
	h := newCompensationHandle(req.Target, req.RelPath, req.Candidate, before, rc.BeforeExists, req.Sensitive)
	h.opID = rc.OpID
	h.targetKey = rc.TargetKey
	h.backupRel = rc.BackupRel
	h.stagingRel = rc.StagingRel
	h.displacedRel = rc.DisplacedRel
	h.unit = req.Unit
	return h
}
