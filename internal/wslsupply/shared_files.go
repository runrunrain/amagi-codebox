package wslsupply

// shared_files.go — shared/vision-export 与 shared/modality-kb 单元（P3-S1 共享
// 小文件分发，design §四共享 ~/.agents 表）。
//
// vision-export（amagi-media-models.json，契约 docs/vision-export-contract.md
// v1.5）：单向受管导出，字节逐字来自宿主导出文件（schema 不扩充）；来源
// updated_at 变化但规范化业务内容不变 → 零动作；per-target 首次发布须明确
// 同意（密钥跨界授权，GrantUnitConsent）；已托管后被手改 → 冻结（保留本地）；
// 来源已变化（含撤回）而目标冻结/不可达 → 「密钥副本未确认撤回」可见，不虚报
// 已删除。
//
// modality-kb（amagi-modalities.json）：schema 校验后按规范化模型 ID 补缺；
// distro 已存在且不同的记录保留并报冲突，不反写宿主；损坏拒绝不覆盖。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"amagi-codebox/internal/config"
)

const (
	visionExportTransform = "vision-export-v1"
	modalityKBTransform   = "modality-kb-v1"
	// VisionExportFile / ModalityKBFile 是两个单元的目标文件（相对 ~/.agents）。
	VisionExportFile = "amagi-media-models.json"
	ModalityKBFile   = "amagi-modalities.json"
	// modalityKBExpectVersion 是学习层 schema 的受支持版本。
	modalityKBExpectVersion = 1
)

// SharedFileSource 是 App 注入的宿主侧共享文件内容源（读 Config 管理的真实
// 导出路径；测试注入固定内容或错误）。
type SharedFileSource interface {
	// VisionExportBytes 返回宿主 ~/.agents/amagi-media-models.json 字节
	//（不存在/不可读返回错误——单元以 blocked 呈现，不臆造空导出）。
	VisionExportBytes() ([]byte, error)
	// ModalityKBBytes 返回宿主 ~/.agents/amagi-modalities.json 字节（不存在
	// 视为空知识库，返回 nil, nil）。
	ModalityKBBytes() ([]byte, error)
}

// ---------------------------------------------------------------------------
// vision-export
// ---------------------------------------------------------------------------

// VisionExportAdapter 实现 shared/vision-export 单元。
type VisionExportAdapter struct {
	source SharedFileSource
}

// NewVisionExportAdapter 创建视觉清单受管导出适配器。
func NewVisionExportAdapter(source SharedFileSource) *VisionExportAdapter {
	return &VisionExportAdapter{source: source}
}

func (a *VisionExportAdapter) Unit() UnitID          { return UnitSharedVisionExport }
func (a *VisionExportAdapter) Policy() Policy        { return PolicyManagedNamespace }
func (a *VisionExportAdapter) Sensitive() bool       { return true } // 条目内嵌明文 api_key
func (a *VisionExportAdapter) RequiresConsent() bool { return true } // 密钥跨界授权
func (a *VisionExportAdapter) Files() []string       { return []string{VisionExportFile} }

// Plan（同意门已在协调器统一拦截）：来源不可读 → blocked（source-unavailable
// 或区分 SA-09 撤回语义）；目标存在但无登记（用户自有文件）→ 冻结；有登记但
// 目标被手改/删除 → 冻结；目标被手改且来源已变化（可能含撤回）→
// withdrawal-unconfirmed（密钥副本未确认撤回）；来源与目标均未变 → 跳过；
// 其余（首次发布/来源变化含撤回）→ 写入。
//
// SA-09 撤回判定：宿主导出文件被删除（os.ErrNotExist）且本目标曾发布过 →
// 撤回——目标副本仍等于上次写入时移除（ActionRemoveProjection）；被手改 →
// withdrawal-unconfirmed-frozen（不静默沿用旧密钥，也不静默删除用户改动）；
// 未曾发布（无登记）→ 无事可做（ActionNone）。瞬时读错误（非删除）不足以
// 判定撤回意图，维持 source-unavailable 阻断，不动副本。
func (a *VisionExportAdapter) Plan(_ SupplyRequest, snap *Snapshot, state *StateEntry) (PlannedUnit, error) {
	src, err := a.visionBytes()
	if err != nil {
		unit := PlannedUnit{Action: ActionBlocked, BlockedReason: "source-unavailable", Policy: PolicyManagedNamespace, RelPath: VisionExportFile}
		recorded := state != nil && state.LastWrittenHash != ""
		if !errors.Is(err, os.ErrNotExist) {
			return unit, nil // 瞬时读错误：不是撤回，副本不动
		}
		if !recorded {
			unit.Action = ActionNone // 从未发布且宿主无导出：无事可做
			unit.BlockedReason = ""
			return unit, nil
		}
		// 宿主撤回（导出文件已删除）且本目标持有受管副本。
		meta, _ := snap.File(VisionExportFile)
		if meta.Exists && meta.Hash != state.LastWrittenHash {
			unit.BlockedReason = "withdrawal-unconfirmed-frozen"
			unit.Freeze = true
			return unit, nil
		}
		unit.Action = ActionRemoveProjection
		unit.BlockedReason = ""
		if meta.Exists {
			unit.ExpectedTargetHash = meta.Hash
		}
		return unit, nil
	}
	sourceHash := hexHash(normalizedStripUpdatedAt(src))
	unit := PlannedUnit{RelPath: VisionExportFile, TransformVersion: visionExportTransform, Policy: PolicyManagedNamespace}
	meta, _ := snap.File(VisionExportFile)
	recorded := state != nil && state.LastWrittenHash != ""
	sourceChanged := recorded && state.SourceHash != sourceHash
	switch {
	case !recorded && meta.Exists:
		unit.Action = ActionBlocked
		unit.BlockedReason = "unmanaged-existing"
		unit.Freeze = true
	case recorded && !meta.Exists:
		unit.Action = ActionBlocked
		unit.BlockedReason = "local-deletion-frozen"
		unit.Freeze = true
	case recorded && meta.Hash != state.LastWrittenHash:
		unit.Action = ActionBlocked
		if sourceChanged {
			// 目标被手改而来源已变化（典型：用户在 Windows 侧关闭了
			// vision_export）：撤回无法确认，密钥副本仍在 WSL——明确可见，
			// 不虚报已删除（design §四）。
			unit.BlockedReason = "withdrawal-unconfirmed-frozen"
		} else {
			unit.BlockedReason = "local-modification-frozen"
		}
		unit.Freeze = true
	case recorded && !sourceChanged:
		unit.Action = ActionNone
	default: // 首次发布（无记录+目标缺失）或来源变化（更新/撤回）
		unit.Action = ActionWriteProjection
		if meta.Exists {
			unit.ExpectedTargetHash = meta.Hash
		}
	}
	return unit, nil
}

// Candidate 逐字发布宿主导出字节（schema 不扩充）；SourceHash 是规范化
// （updated_at 剥离）业务内容 hash。
func (a *VisionExportAdapter) Candidate(_ context.Context, _ TargetRef, _ *StateEntry) (ProjectionCandidate, error) {
	src, err := a.visionBytes()
	if err != nil {
		return ProjectionCandidate{}, err
	}
	return ProjectionCandidate{
		Content:          src,
		SourceHash:       hexHash(normalizedStripUpdatedAt(src)),
		TransformVersion: visionExportTransform,
	}, nil
}

func (a *VisionExportAdapter) visionBytes() ([]byte, error) {
	if a.source == nil {
		return nil, fmt.Errorf("vision export source unavailable")
	}
	src, err := a.source.VisionExportBytes()
	if err != nil {
		return nil, err
	}
	if len(src) == 0 {
		return nil, fmt.Errorf("host vision export file is empty or missing")
	}
	return src, nil
}

// normalizedStripUpdatedAt 剥离 updated_at 字段后做 canonical 序列化（map 键序
// 确定）：来源时间戳变化但业务内容不变 → 零动作（design §四共享表）。
func normalizedStripUpdatedAt(data []byte) []byte {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return data // 非法来源：退回原始字节参与比较（写入侧另有严格校验）
	}
	delete(obj, "updated_at")
	if b, err := json.Marshal(obj); err == nil {
		return b
	}
	return data
}

// ---------------------------------------------------------------------------
// modality-kb
// ---------------------------------------------------------------------------

// ModalityKBAdapter 实现 shared/modality-kb 单元。
type ModalityKBAdapter struct {
	source SharedFileSource
}

// NewModalityKBAdapter 创建多模态学习层补缺适配器。
func NewModalityKBAdapter(source SharedFileSource) *ModalityKBAdapter {
	return &ModalityKBAdapter{source: source}
}

func (a *ModalityKBAdapter) Unit() UnitID          { return UnitSharedModalityKB }
func (a *ModalityKBAdapter) Policy() Policy        { return PolicyManagedNamespace }
func (a *ModalityKBAdapter) Sensitive() bool       { return false } // 布尔能力结论，无密钥
func (a *ModalityKBAdapter) RequiresConsent() bool { return false }
func (a *ModalityKBAdapter) Files() []string       { return []string{ModalityKBFile} }

func (a *ModalityKBAdapter) Plan(_ SupplyRequest, snap *Snapshot, state *StateEntry) (PlannedUnit, error) {
	src, err := a.modalityBytes()
	if err != nil {
		return PlannedUnit{Action: ActionBlocked, BlockedReason: "source-unavailable", Policy: PolicyManagedNamespace}, nil
	}
	sourceHash := hexHash(normalizedStripUpdatedAt(src))
	unit := PlannedUnit{RelPath: ModalityKBFile, TransformVersion: modalityKBTransform, Policy: PolicyManagedNamespace}
	meta, _ := snap.File(ModalityKBFile)
	recorded := state != nil && state.LastWrittenHash != ""
	unchanged := recorded && meta.Exists && meta.Hash == state.LastWrittenHash && state.SourceHash == sourceHash
	if unchanged {
		unit.Action = ActionNone
		return unit, nil
	}
	unit.Action = ActionWriteProjection // 补缺合并（加性，不冻结）
	if meta.Exists {
		unit.ExpectedTargetHash = meta.Hash
	}
	return unit, nil
}

// Candidate 做 schema 校验后的补缺合并：distro 已存在的同 ID 记录保留（结论
// 不同 → 冲突报告，不反写宿主）；仅补齐缺失 ID。损坏的 distro 文件 → 拒绝
// 不覆盖（ErrTargetCorrupt）。
func (a *ModalityKBAdapter) Candidate(_ context.Context, target TargetRef, _ *StateEntry) (ProjectionCandidate, error) {
	src, err := a.modalityBytes()
	if err != nil {
		return ProjectionCandidate{}, err
	}
	host := modalityKBModels(src)
	if host == nil {
		return ProjectionCandidate{}, fmt.Errorf("host modality kb is not a v%d schema object", modalityKBExpectVersion)
	}
	existing, err := os.ReadFile(filepath.Join(target.UNCRoot, filepath.FromSlash(ModalityKBFile)))
	if err != nil && !os.IsNotExist(err) {
		return ProjectionCandidate{}, fmt.Errorf("read distro modality kb: %w", err)
	}
	merged := map[string]json.RawMessage{}
	var notes []string
	if len(existing) > 0 {
		distro := modalityKBModels(existing)
		if distro == nil {
			return ProjectionCandidate{}, fmt.Errorf("%w: distro amagi-modalities.json is not a v%d schema object", ErrTargetCorrupt, modalityKBExpectVersion)
		}
		for id, entry := range distro {
			merged[id] = entry
		}
	}
	// 规范化 ID 排序遍历：结果确定（map 序列化键序确定 + 冲突报告稳定）。
	ids := make([]string, 0, len(host))
	for id := range host {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		entry := host[id]
		if prev, ok := merged[id]; ok {
			if modalityConclusion(prev) != modalityConclusion(entry) {
				notes = append(notes, fmt.Sprintf("模型 %s 的 WSL 侧结论与宿主不同，保留 WSL 侧（不反写宿主）", id))
			}
			continue // 已存在（无论同异）：保留 distro 记录，只报冲突
		}
		merged[id] = entry
	}
	updatedAt := modalityKBUpdatedAt(src)
	content, mErr := marshalModalityKB(merged, updatedAt)
	if mErr != nil {
		return ProjectionCandidate{}, fmt.Errorf("marshal merged modality kb: %w", mErr)
	}
	return ProjectionCandidate{
		Content:          content,
		SourceHash:       hexHash(normalizedStripUpdatedAt(src)),
		TransformVersion: modalityKBTransform,
		Notes:            notes,
	}, nil
}

func (a *ModalityKBAdapter) modalityBytes() ([]byte, error) {
	if a.source == nil {
		return nil, fmt.Errorf("modality kb source unavailable")
	}
	return a.source.ModalityKBBytes()
}

// modalityKBModels 解析学习层文件为「规范化模型 ID → 条目 RawMessage」；nil
// 表示 schema 不符（version 非 1 / models 非对象 / 根非对象）。
func modalityKBModels(data []byte) map[string]json.RawMessage {
	if len(data) == 0 {
		return map[string]json.RawMessage{}
	}
	var file struct {
		Version int                        `json:"version"`
		Models  map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(data, &file); err != nil || file.Version != modalityKBExpectVersion || file.Models == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(file.Models))
	for id, entry := range file.Models {
		out[config.NormalizeModalityKBModelID(id)] = entry
	}
	return out
}

// modalityConclusion 提取条目的能力结论（vision/video 布尔对）——冲突判定只看
// 结论，不看 source/probedAt 元数据。
func modalityConclusion(entry json.RawMessage) [2]bool {
	var parsed struct {
		Vision bool `json:"vision"`
		Video  bool `json:"video"`
	}
	_ = json.Unmarshal(entry, &parsed)
	return [2]bool{parsed.Vision, parsed.Video}
}

// marshalModalityKB 序列化补缺后的学习层（version=1 + 宿主 updated_at + 合并
// models；map 键序确定）。
func marshalModalityKB(models map[string]json.RawMessage, updatedAt string) ([]byte, error) {
	out := map[string]any{"version": modalityKBExpectVersion, "models": models}
	if updatedAt != "" {
		out["updated_at"] = updatedAt
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// modalityKBUpdatedAt 提取宿主文件的 updated_at（合并结果沿用宿主时间）。
func modalityKBUpdatedAt(hostSrc []byte) string {
	var probe struct {
		UpdatedAt string `json:"updated_at"`
	}
	if json.Unmarshal(hostSrc, &probe) == nil {
		return probe.UpdatedAt
	}
	return ""
}
