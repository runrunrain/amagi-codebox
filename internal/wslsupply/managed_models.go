package wslsupply

// managed_models.go — pi/managed-models 单元（P3-S1 托管 provider 分发的
// ManagedNamespace 适配器，design §四 Pi models.json 行 + §七「现有 pi provider」）。
//
// 期望集从 Config/Secrets 业务快照构建（App 注入 PiManagedModelsSource；不复制
// 宿主 models.json 整文件）；候选生成 = 严格解析既有 distro models.json（两级
// 损坏保护）→ amagi-* 命名空间整体替换（失败 provider 保留不误 prune）→ 登录
// 预设 contextWindow 窄字段补齐；提交走协调器私有 staging 原语。
//
// 归属判定（ManagedNamespace，无冻结条件）：无记录+目标存在不冻结（合并保留
// 域外内容）；有记录+目标被外部修改也允许更新（amagi-* 归 CodeBox，合并天然
// 保留外部非 amagi 编辑；计划-执行窗口内的变化由 CAS 前置复核拒绝旧计划，
// 下次启动收敛）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"amagi-codebox/internal/launcher"
)

// piManagedModelsTransform 是本单元的转换版本（内容语义变化时递增）。
const piManagedModelsTransform = "pi-managed-models-v1"

// PiManagedModelsFile 是 pi/managed-models 单元的目标文件（相对 .pi/agent）。
const PiManagedModelsFile = "models.json"

// PiLoginModelOverrideSpec 是登录预设 contextWindow 的窄字段托管描述（镜像
// launcher.PiLoginModelOverride；wslsupply 不让业务结构从 launcher 泄漏给 App）。
type PiLoginModelOverrideSpec struct {
	Provider      string
	Model         string
	ContextWindow int // >0 才分发（补齐语义；清除语义属 S2 接管框架）
}

// PiManagedModelsSpec 是一次业务快照构建出的期望集。
type PiManagedModelsSpec struct {
	// Providers：成功构建的 amagi-* provider 条目（完整期望集）。
	Providers map[string]any
	// RetainProviders：本轮构建失败的 amagi-* id——合并时保留既有条目
	//（不以残缺集合 prune 旧项，design §七）。
	RetainProviders []string
	// LoginOverrides：登录预设 contextWindow（cw>0）。
	LoginOverrides []PiLoginModelOverrideSpec
	// BuildErrors：逐 provider 构建失败原因（不含密钥正文；并入结果 Notes）。
	BuildErrors []string
}

// PiManagedModelsSource 是 App 注入的业务快照源：从 Config/Secrets 同源构建
// amagi-* 期望集（与宿主 providerSync 同一构建规则，经 provider builders）。
type PiManagedModelsSource interface {
	DesiredPiManagedModels() PiManagedModelsSpec
}

// PiManagedModelsAdapter 实现 pi/managed-models 单元。
type PiManagedModelsAdapter struct {
	source PiManagedModelsSource
}

// NewPiManagedModelsAdapter 创建托管 models.json 分发适配器。
func NewPiManagedModelsAdapter(source PiManagedModelsSource) *PiManagedModelsAdapter {
	return &PiManagedModelsAdapter{source: source}
}

func (a *PiManagedModelsAdapter) Unit() UnitID          { return UnitPiManagedModels }
func (a *PiManagedModelsAdapter) Policy() Policy        { return PolicyManagedNamespace }
func (a *PiManagedModelsAdapter) Sensitive() bool       { return true } // provider 条目内嵌 apiKey
func (a *PiManagedModelsAdapter) RequiresConsent() bool { return false }
func (a *PiManagedModelsAdapter) Files() []string       { return []string{PiManagedModelsFile} }

// Plan 是纯计算：来源（规范化期望集）与目标（上次写入字节）都未变 → 跳过；
// 其余（首次创建/来源变化/目标被外部改写）→ 写入。ManagedNamespace 无冻结条件。
func (a *PiManagedModelsAdapter) Plan(_ SupplyRequest, snap *Snapshot, state *StateEntry) (PlannedUnit, error) {
	sourceHash := piManagedSpecHash(a.desired())
	unit := PlannedUnit{
		RelPath: PiManagedModelsFile, TransformVersion: piManagedModelsTransform,
		Policy: PolicyManagedNamespace,
	}
	meta, _ := snap.File(PiManagedModelsFile)
	recorded := state != nil && state.LastWrittenHash != ""
	unchanged := recorded && meta.Exists && meta.Hash == state.LastWrittenHash && state.SourceHash == sourceHash
	if unchanged {
		unit.Action = ActionNone
		return unit, nil
	}
	unit.Action = ActionWriteProjection
	if meta.Exists {
		unit.ExpectedTargetHash = meta.Hash
	}
	return unit, nil
}

// Candidate 读取既有 distro models.json 并生成托管候选（损坏拒绝不覆盖）。
func (a *PiManagedModelsAdapter) Candidate(_ context.Context, target TargetRef) (ProjectionCandidate, error) {
	spec := a.desired()
	existing, err := os.ReadFile(filepath.Join(target.UNCRoot, filepath.FromSlash(PiManagedModelsFile)))
	if err != nil {
		if !os.IsNotExist(err) {
			return ProjectionCandidate{}, fmt.Errorf("read distro pi models.json: %w", err)
		}
		existing = nil
	}
	overrides := make([]launcher.PiLoginModelOverride, 0, len(spec.LoginOverrides))
	for _, ov := range spec.LoginOverrides {
		overrides = append(overrides, launcher.PiLoginModelOverride{Provider: ov.Provider, Model: ov.Model, ContextWindow: ov.ContextWindow})
	}
	content, err := launcher.BuildWSLPiManagedModelsCandidate(existing, spec.Providers, spec.RetainProviders, overrides)
	if err != nil {
		if errors.Is(err, launcher.ErrWSLManagedModelsCorrupt) {
			// 两级损坏保护：翻译为协调器哨兵（Detail=target-corrupt，闸门可区分）。
			return ProjectionCandidate{}, fmt.Errorf("%w: %v", ErrTargetCorrupt, err)
		}
		return ProjectionCandidate{}, err
	}
	return ProjectionCandidate{
		Content:          content,
		SourceHash:       piManagedSpecHash(spec),
		TransformVersion: piManagedModelsTransform,
		Notes:            spec.BuildErrors,
	}, nil
}

func (a *PiManagedModelsAdapter) desired() PiManagedModelsSpec {
	if a.source == nil {
		return PiManagedModelsSpec{}
	}
	return a.source.DesiredPiManagedModels()
}

// piManagedSpecHash 计算期望集的规范化 hash：Providers + RetainProviders +
// LoginOverrides（排序后）的 canonical JSON（map 序列化键序确定）；BuildErrors
// 文本不参与（错误链措辞变化不应触发写入，RetainProviders 集合已捕获失败面
// 变化）。来源侧无论以何种顺序产出 spec，hash 恒定。
func piManagedSpecHash(spec PiManagedModelsSpec) string {
	overrides := make([]string, 0, len(spec.LoginOverrides))
	for _, ov := range spec.LoginOverrides {
		overrides = append(overrides, fmt.Sprintf("%s\x00%s\x00%d", ov.Provider, ov.Model, ov.ContextWindow))
	}
	sort.Strings(overrides)
	retain := append([]string(nil), spec.RetainProviders...)
	sort.Strings(retain)
	payload, err := json.Marshal(map[string]any{
		"providers": spec.Providers,
		"retain":    retain,
		"overrides": overrides,
	})
	if err != nil {
		return hexHash([]byte(fmt.Sprintf("marshal-error:%v", err)))
	}
	return hexHash(payload)
}
