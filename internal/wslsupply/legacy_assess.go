package wslsupply

// legacy_assess.go — PR01 攻坚重做：旧指纹与实际固定视觉文件的当次资格核验
//（attack-contract「PR01：legacy 核验与用户解除」）。旧公式指纹
// LegacyGeneration 与当前目标实际文件共同裁决；A 发布→只检查 B→升级时 A 阻、
// B 不误伤；无归属/unknown 保真，不授写权、不全局误阻所有 WSL；手动处理后
// 可经 ConfirmLegacyVisionAbsent 真实解除。

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// legacyGenerationOf 用旧公式（sha256(machine-id + "|" + uid + "|" + home)，无
// distro）由同一次 Identity 探测导出旧代指纹；machine-id 缺失时仍 unknown。
// 不重新走 cached Home 探测。
func legacyGenerationOf(machineID, uid, home string) string {
	if machineID == "" {
		return GenerationUnknown
	}
	sum := sha256.Sum256([]byte(machineID + "|" + uid + "|" + home))
	return hexEncode(sum[:])
}

// SensitiveAssessment 是一次敏感消费资格核验的结论（脱敏：无 hash/正文）。
type SensitiveAssessment struct {
	Unit        UnitID
	Consumption Consumption
	// RiskPending 表示存在需要用户处理的旧副本/未决操作事实。
	RiskPending bool
	// Reason 是结构化原因（状态页/闸门错误文案用，不含路径正文）。
	Reason string
	// Path 提示用户处理的固定文件名（不含任何内容）。
	Path string
}

// AssessSensitiveConsumption 在 effect/启动门运行：只核验当前目标固定视觉
// 清单路径，不扫所有 distro，不返回 hash/正文。runFacts 携带本次运行的结果
// 事实（nil 表示尚未运行——按持久事实判定）。
func (c *Coordinator) AssessSensitiveConsumption(ctx context.Context, target TargetRef, unit UnitID, res *UnitResult) SensitiveAssessment {
	out := SensitiveAssessment{Unit: unit, Consumption: ConsumptionUnknown}
	if unit != UnitSharedVisionExport {
		// 其它敏感单元（未来）无 legacy 面；按当前登记与本次结果判定。
		return c.assessFromFacts(target, unit, res)
	}
	// 1) 未决持久操作优先（真实 fact，不是 LastOutcome 字符串）。
	if opID, pending := c.PendingSensitiveOperation(target.Distro, unit); pending {
		out.RiskPending = true
		out.Consumption = ConsumptionUnsafe
		out.Reason = "pending-operation:" + opID
		out.Path = VisionExportFile
		return out
	}
	stCopy := c.state.load()
	// 2) 本次运行结果事实（写已发生/未知/敏感资格缺失不放行）——登记面
	//（当前代受管副本）优先裁决；无受管登记时不立即放行，先经 3/4 的持久
	// 线索面（legacy evidence）再下结论。
	resFailDetail := ""
	if res != nil && (res.Outcome == UnitFailed || res.Outcome == UnitBlocked) {
		if entrySensitiveCopyPossible(stCopy.entryFor(unit, target.Generation)) {
			out.RiskPending = true
			out.Consumption = ConsumptionUnsafe
			out.Reason = "run-failed-with-managed-copy:" + res.Detail
			return out
		}
		resFailDetail = res.Detail
	}
	visionPath := filepath.Join(target.UNCRoot, VisionExportFile)
	data, err := os.ReadFile(visionPath)
	fileAbsent := errors.Is(err, os.ErrNotExist)
	fileHash := ""
	if err == nil {
		fileHash = hexHash(data)
	}
	st := stCopy
	entry := st.entryFor(unit, target.Generation)
	// 3) 当前代受管登记。
	if entrySensitiveCopyPossible(entry) {
		switch {
		case fileAbsent:
			out.Consumption = ConsumptionAbsent
			out.Reason = "managed-copy-externally-deleted"
		case err != nil:
			out.Consumption = ConsumptionUnsafe
			out.RiskPending = true
			out.Reason = "managed-copy-unreadable"
		default:
			// 副本在：权限可证 0600 → safe；否则 unknown（收紧复验由运行路径
			// 完成；评估面不主动改权限）。
			linuxPath := target.LinuxRoot + "/" + VisionExportFile
			if mode, ok := c.probes.StatMode(ctx, target.Distro, linuxPath); ok && mode == "600" {
				out.Consumption = ConsumptionSafe
			} else {
				out.Consumption = ConsumptionUnknown
				out.RiskPending = true
				out.Reason = "managed-copy-perms-unverified"
			}
			out.Path = VisionExportFile
		}
		return out
	}
	// 4) LegacyEvidence 逐目标核验（旧指纹 × 实际文件）。
	targetKey := targetKeyOf(target)
	legacyGen := target.LegacyGeneration
	for _, ev := range st.LegacyEvidence {
		if ev.Unit != unit || ev.TerminalRiskCleared() {
			continue
		}
		if ev.EntryLastHash == "" && !ev.LowTrust {
			// 单有 Consented 无写基准：不伪造已发布事实（consent-only 线索不
			// 重建受管副本风险）。
			continue
		}
		if d, resolved := ev.Resolved[targetKey]; resolved {
			_ = d
			continue // 该完整身份已核验处置
		}
		fingerprintMatch := ev.Fingerprint != "" && legacyGen != GenerationUnknown && legacyGen != "" && ev.Fingerprint == legacyGen
		topClueMatch := ev.TopDistro != "" && ev.TopDistro == target.Distro
		// 内容正证据：当前文件字节与历史写入 hash 一致——当前路径存在旧发布
		// 副本（不证明是原 owner，只关联本目标风险；指纹是弱证据，内容是正证
		// 据，两者独立）。指纹 unknown/不匹配不得因此解除待确认。
		contentMatch := ev.EntryLastHash != "" && fileHash != "" && fileHash == ev.EntryLastHash
		switch {
		case fileAbsent:
			// 本次无该消费副本：放行本目标，仍保留全局 unresolved evidence。
			out.Consumption = ConsumptionAbsent
			out.Reason = "legacy-clue-present-copy-absent"
		case err != nil:
			out.Consumption = ConsumptionUnknown
			out.RiskPending = true
			out.Reason = "legacy-clue-file-unreadable"
		case fingerprintMatch:
			out.Consumption = ConsumptionUnsafe
			out.RiskPending = true
			out.Reason = "legacy-fingerprint-match"
			out.Path = VisionExportFile
		case contentMatch:
			// 契约矩阵「旧指纹不同、文件 hash 匹配」：当前路径有旧发布字节，阻
			// 未授权消费；不自动写/删，不证明原 owner。
			out.Consumption = ConsumptionUnsafe
			out.RiskPending = true
			out.Reason = "legacy-content-hash-match"
			out.Path = VisionExportFile
		case topClueMatch && ev.LowTrust:
			// v2 派生低可信线索 + 文件在：要求确认，不自动写/删。
			out.Consumption = ConsumptionUnknown
			out.RiskPending = true
			out.Reason = "legacy-v2-clue-file-present"
			out.Path = VisionExportFile
		default:
			// 旧指纹不同、文件可读且不同、无其它关联线索：按用户自有保留，
			// 不误标（只对当前路径成立）。
			if out.Consumption == ConsumptionUnknown {
				out.Consumption = ConsumptionUserOwned
				out.Reason = "unrelated-file-user-owned"
			}
		}
		return out
	}
	// 5) 无任何线索：普通未受管（有本次失败时归因到运行明细，普通可选失败
	// 不误阻）。
	if err != nil && !fileAbsent {
		out.Consumption = ConsumptionUnknown
		out.Reason = "file-unreadable-no-history"
		return out
	}
	out.Consumption = ConsumptionAbsent
	if resFailDetail != "" {
		out.Reason = "no-copy-history:" + resFailDetail
	} else {
		out.Reason = "never-managed"
	}
	return out
}

// AssessWSLSensitiveConsumption 是 App/启动闸门消费的入口：解析目标后按
// 当前目标固定路径核验（无法解析目标 → unknown，不猜默认 /home/u）。
func (c *Coordinator) AssessWSLSensitiveConsumption(ctx context.Context, distro string, cli CLI, unit UnitID, res *UnitResult) SensitiveAssessment {
	target, err := c.ResolveTarget(ctx, distro, cli)
	if err != nil {
		// 未解析可信 Home/路径：不猜默认 /home/u；逐目标文件核验不可达时退回
		// 持久事实面（pending intent / 受管副本可能 / legacy 线索）——已知风险
		// 不能因探测失败消失；确无线索的普通目标不误阻。
		if opID, pending := c.PendingSensitiveOperation(distro, unit); pending {
			return SensitiveAssessment{Unit: unit, Consumption: ConsumptionUnsafe,
				RiskPending: true, Reason: "pending-operation:" + opID, Path: VisionExportFile}
		}
		if c.SensitiveCopyPending(distro, unit) || c.LegacySensitiveCopyPending(distro) {
			return SensitiveAssessment{Unit: unit, Consumption: ConsumptionUnknown,
				RiskPending: true, Reason: "target-unresolved-with-copy-history", Path: VisionExportFile}
		}
		return SensitiveAssessment{Unit: unit, Consumption: ConsumptionAbsent,
			Reason: "target-unresolved-no-history"}
	}
	return c.AssessSensitiveConsumption(ctx, target, unit, res)
}

// assessFromFacts 是非视觉敏感单元的通用评估（当前只有视觉单元，预留）。
func (c *Coordinator) assessFromFacts(target TargetRef, unit UnitID, res *UnitResult) SensitiveAssessment {
	out := SensitiveAssessment{Unit: unit, Consumption: ConsumptionNotApplicable}
	return out
}

// ConfirmLegacyVisionAbsent 是「手动处理后确认当前目标 absent」的真实解除
// 入口：复用目标解析与现场核验，持久记录该完整身份/路径处置；保存失败返回
// 未持久确认（不删除原 evidence）。
func (c *Coordinator) ConfirmLegacyVisionAbsent(ctx context.Context, distro string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("confirm legacy vision absent: %w", err)
	}
	target, err := c.ResolveTarget(ctx, distro, CLICommon)
	if err != nil {
		return fmt.Errorf("resolve wsl supply target: %w", err)
	}
	visionPath := filepath.Join(target.UNCRoot, VisionExportFile)
	if _, serr := os.Lstat(visionPath); serr == nil {
		return fmt.Errorf("WSL 目标 %s（Home %s）仍存在 %s：请先在 WSL 内手动处理该文件后重试；当前状态仍是未撤回", distro, target.Home, "~/.agents/"+VisionExportFile)
	} else if !errors.Is(serr, os.ErrNotExist) {
		return fmt.Errorf("无法确认 WSL 目标 %s 的 %s 状态（%v）；不视为已确认 absent", distro, VisionExportFile, serr)
	}
	targetKey := targetKeyOf(target)
	err = c.state.mutate(func(st *StateFile) {
		changed := false
		for i := range st.LegacyEvidence {
			ev := &st.LegacyEvidence[i]
			if ev.Unit != UnitSharedVisionExport {
				continue
			}
			if ev.Resolved == nil {
				ev.Resolved = map[string]string{}
			}
			if _, ok := ev.Resolved[targetKey]; !ok {
				ev.Resolved[targetKey] = "absent-confirmed"
				changed = true
			}
			// 键位双写（与 resolveLegacyEvidenceIn 同族：裸 generation 键供按代际
			// 迭代的消费面收敛）。
			if gen, _, found := strings.Cut(targetKey, "|"); found && gen != "" {
				ev.Resolved[gen] = "absent-confirmed"
			}
		}
		_ = changed
	})
	if err != nil {
		// 保存失败：返回未持久确认，不删除原 evidence。
		return fmt.Errorf("确认已核验但未持久化（原线索保留；修复存储后重试）: %w", err)
	}
	return nil
}

// LegacyEvidenceSnapshot 返回证据面（诊断/状态页；脱敏不含 hash 值本身——
// Resolved/指纹存在性可展示）。
func (c *Coordinator) LegacyEvidenceSnapshot() []LegacyEvidenceEntry {
	return c.state.load().LegacyEvidence
}
