package main

// claude_accounts_facade.go — Claude 订阅账号轮换的 App facade（design §7.2，
// C-01：raw 服务不入 Bind，前端只经 App 方法访问）。
//
// 契约要点：
//   - PoolDTO / operation DTO 全部 credential-free（token/hash/完整邮箱不进
//     DTO/事件/日志，design §1.3-5、§7.2）。
//   - 长 I/O（capture/switch/reconcile 写路径）走后台 operation；事件只发
//     operationId/stage/state/generation，禁夹带邮箱/token/hash。
//   - 重复 requestId 同参数返回原操作、不同参数报冲突；结果缓存有界；重启
//     后不可识别的已结束 requestId 依赖池 revision/代际拒绝重放（不默默重放）。
//   - 错误码全集透传 1a reason.go（错误文本 "REASON: message" 前缀）。

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"amagi-codebox/internal/claudeaccounts"
)

// claudeAccountsRiskAckVersion 是启用自动轮换前必须确认的风险声明版本
// （design §7.2：校验能力与本运行周期 managed-only 确认）。
const claudeAccountsRiskAckVersion = "claude-rotation-v1"

// claudeAccountOperationCacheLimit 是进程内已完成 operation 结果缓存上限。
const claudeAccountOperationCacheLimit = 128

// errClaudeAccountOpBlocked 是 work 内部约定：operation 已被置为 blocked 终态
// （等待外部条件，如 roll-forward 需会话退出），runner 不得再改写状态（F06）。
var errClaudeAccountOpBlocked = errors.New("claude-accounts: operation blocked")

// ---------------------------------------------------------------------------
// DTO（credential-free）
// ---------------------------------------------------------------------------

// ClaudeAccountOperationDTO 是 design §7.2 建议的 operation 结构：
// {id,kind,stage,state,changed,activeGeneration,code,warnings[]}。
type ClaudeAccountOperationDTO struct {
	ID               string                       `json:"id"`
	Kind             string                       `json:"kind"`
	Stage            string                       `json:"stage"`
	State            string                       `json:"state"` // running|done|blocked|error
	Changed          bool                         `json:"changed"`
	ActiveGeneration uint64                       `json:"activeGeneration"`
	Code             string                       `json:"code,omitempty"`
	Warnings         []string                     `json:"warnings,omitempty"`
	Inspect          *ClaudeAccountNativeStateDTO `json:"inspect,omitempty"`
}

// ClaudeAccountHealthDTO 是账号健康度投影（credential-free，值来自 1a
// AccountHealth）。
type ClaudeAccountHealthDTO struct {
	ClaudeGrant    string `json:"claudeGrant"`
	PiGrant        string `json:"piGrant"`
	IdentityStatus string `json:"identityStatus,omitempty"`
	ReasonCode     string `json:"reasonCode,omitempty"`
}

// ClaudeAccountDTO 是账号卡投影（credential-free；邮箱默认遮罩，token/hash
// 不出边界，design §1.3-5/§7.1）。
type ClaudeAccountDTO struct {
	ID              string                 `json:"id"`
	Alias           string                 `json:"alias"`
	CreatedAt       string                 `json:"createdAt"`
	UpdatedAt       string                 `json:"updatedAt"`
	EmailMasked     string                 `json:"emailMasked,omitempty"`
	SeatTier        string                 `json:"seatTier,omitempty"`
	Active          bool                   `json:"active"`
	Health          ClaudeAccountHealthDTO `json:"health"`
	ClaudeExpiresAt *string                `json:"claudeExpiresAt,omitempty"`
}

// ClaudeAccountRotationConfigDTO 是轮换配置的非敏感镜像（design §3.2；写
// 入经 SetClaudeAccountRotation，facade 不暴露结构化写入面）。
type ClaudeAccountRotationConfigDTO struct {
	SchemaVersion           int     `json:"schema_version"`
	Enabled                 bool    `json:"enabled"`
	Strategy                string  `json:"strategy"`
	ThresholdPercent        int     `json:"threshold_percent"`
	CooldownSeconds         int     `json:"cooldown_seconds"`
	HysteresisPoints        int     `json:"hysteresis_points"`
	ManualRespectSeconds    int     `json:"manual_respect_seconds"`
	AcknowledgedRiskVersion *string `json:"acknowledged_risk_version,omitempty"`
}

// ClaudeAccountsPoolDTO 是 GetClaudeAccounts 的返回（design §7.1/§7.2）。
type ClaudeAccountsPoolDTO struct {
	Capability       string                          `json:"capability"` // supported|unsupported
	State            string                          `json:"state"`      // disabled|enabled|enrolling|needs-reconcile|blocked|unsupported
	Revision         uint64                          `json:"revision"`
	ActiveGeneration uint64                          `json:"activeGeneration"`
	ActiveID         string                          `json:"activeId,omitempty"`
	Accounts         []ClaudeAccountDTO              `json:"accounts"`
	Operation        *ClaudeAccountOperationDTO      `json:"operation,omitempty"`
	PauseReason      string                          `json:"pauseReason,omitempty"`
	RotationEnabled  bool                            `json:"rotationEnabled"`
	RotationConfig   *ClaudeAccountRotationConfigDTO `json:"rotationConfig,omitempty"`
	// EnrollmentID：持久化 enrollment 的可续接 ID（F09/design §7.2 安全文案：
	// 不含池秘密；刷新/重启后前端凭此重建向导，保存仍需精确 TxnID+revision）。
	EnrollmentID string `json:"enrollmentId,omitempty"`
}

// ClaudeAccountNativeStateDTO 是 inspect 的零写分类投影。
type ClaudeAccountNativeStateDTO struct {
	HasJournal              bool              `json:"hasJournal"`
	JournalPhase            string            `json:"journalPhase,omitempty"`
	Classifications         map[string]string `json:"classifications,omitempty"`
	MatchesActiveSnapshot   bool              `json:"matchesActiveSnapshot"`
	ExternalChangeSuspected bool              `json:"externalChangeSuspected"`
	ActiveID                string            `json:"activeId,omitempty"`
	Generation              uint64            `json:"generation"`
}

// ClaudeAccountReconcileResultDTO 是 ReconcileClaudeAccounts 的返回：inspect
// 同步返回报告，其余动作返回后台 operationId。
type ClaudeAccountReconcileResultDTO struct {
	OperationID string                       `json:"operationId,omitempty"`
	Inspect     *ClaudeAccountNativeStateDTO `json:"inspect,omitempty"`
}

// ---------------------------------------------------------------------------
// operation 注册表（有界缓存 + requestId 幂等）
// ---------------------------------------------------------------------------

type claudeAccountOperation struct {
	mu           sync.Mutex
	dto          ClaudeAccountOperationDTO
	requestID    string
	paramsDigest string
	startedAt    time.Time
	started      bool
	done         chan struct{}
}

// claudeOpBegin 登记或复用一个 operation。返回；conflict=true 表示重复
// requestId 携带了不同参数（调用方返回 REVISION_CONFLICT）。
func (a *App) claudeOpBegin(kind, requestID, paramsDigest string, generation uint64) (op *claudeAccountOperation, conflict bool) {
	a.claudeAccountsOpsMu.Lock()
	defer a.claudeAccountsOpsMu.Unlock()
	if requestID != "" {
		if existingID, ok := a.claudeAccountsRequests[requestID]; ok {
			if existing := a.claudeAccountsOps[existingID]; existing != nil {
				if existing.paramsDigest != paramsDigest {
					return nil, true
				}
				return existing, false
			}
		}
	}
	id := "ca-" + uuid.NewString()
	op = &claudeAccountOperation{
		dto: ClaudeAccountOperationDTO{
			ID: id, Kind: kind, Stage: "running", State: "running",
			ActiveGeneration: generation,
		},
		requestID:    requestID,
		paramsDigest: paramsDigest,
		startedAt:    time.Now(),
		done:         make(chan struct{}),
	}
	if a.claudeAccountsOps == nil {
		a.claudeAccountsOps = map[string]*claudeAccountOperation{}
	}
	a.claudeAccountsOps[id] = op
	a.claudeAccountsOpsOrder = append(a.claudeAccountsOpsOrder, id)
	// 有界缓存（F15）：仅淘汰「已完成」的最旧 victim；运行中项不淘汰（同
	// requestId 幂等与在途查询依赖它），改试次旧者；全部在途时允许临时超过
	// 上限（在途操作数本身有界，不会无限增长），不无限循环。
	for len(a.claudeAccountsOpsOrder) > claudeAccountOperationCacheLimit {
		evicted := false
		for i, oldest := range a.claudeAccountsOpsOrder {
			victim := a.claudeAccountsOps[oldest]
			if victim == nil {
				// 陈旧索引（防御）：直接移除队列项。
				a.claudeAccountsOpsOrder = append(a.claudeAccountsOpsOrder[:i], a.claudeAccountsOpsOrder[i+1:]...)
				evicted = true
				break
			}
			select {
			case <-victim.done:
				a.claudeAccountsOpsOrder = append(a.claudeAccountsOpsOrder[:i], a.claudeAccountsOpsOrder[i+1:]...)
				delete(a.claudeAccountsOps, oldest)
				for req, target := range a.claudeAccountsRequests {
					if target == oldest {
						delete(a.claudeAccountsRequests, req)
					}
				}
				evicted = true
				break
			default:
				// 运行中：跳过，试次旧者（不放回队尾避免死循环）。
			}
			if evicted {
				break
			}
		}
		if !evicted {
			break // 全部在途：本轮不淘汰
		}
	}
	if requestID != "" {
		if a.claudeAccountsRequests == nil {
			a.claudeAccountsRequests = map[string]string{}
		}
		a.claudeAccountsRequests[requestID] = id
	}
	return op, false
}

// claudeOpUpdate 在锁内更新 DTO 并发事件（仅 operationId/stage/state/generation）。
func (a *App) claudeOpUpdate(op *claudeAccountOperation, mutate func(*ClaudeAccountOperationDTO)) {
	op.mu.Lock()
	mutate(&op.dto)
	dto := op.dto
	op.mu.Unlock()
	a.emitClaudeAccountOperationEvent(dto)
}

// emitClaudeAccountOperationEvent 只发 operationId/stage/state/generation
// （design §7.2：事件禁夹带邮箱/token/hash/request body）。
func (a *App) emitClaudeAccountOperationEvent(dto ClaudeAccountOperationDTO) {
	if a.ctx == nil {
		return
	}
	wailsRuntime.EventsEmit(a.ctx, "claude-accounts:operation", map[string]any{
		"operationId": dto.ID,
		"stage":       dto.Stage,
		"state":       dto.State,
		"generation":  dto.ActiveGeneration,
	})
}

// runClaudeAccountOperation 登记后台 operation 并在 goroutine 中执行 work。
// 幂等：重复 requestId 同参数直接返回原 operationId。
func (a *App) runClaudeAccountOperation(kind, requestID, paramsDigest string, generation uint64, work func(ctx context.Context, op *claudeAccountOperation) error) (string, error) {
	op, conflict := a.claudeOpBegin(kind, requestID, paramsDigest, generation)
	if conflict {
		return "", claudeaccounts.NewErr(claudeaccounts.ReasonRevisionConflict, "重复 requestId 携带不同参数")
	}
	op.mu.Lock()
	if op.started {
		// 同参数重复请求：返回原 operation（幂等，不重放、不双开 goroutine）。
		opID := op.dto.ID
		op.mu.Unlock()
		return opID, nil
	}
	op.started = true
	opID := op.dto.ID
	op.mu.Unlock()
	go func() {
		defer close(op.done)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err := work(ctx, op)
		a.refreshClaudeAccountsCache(context.Background())
		if errors.Is(err, errClaudeAccountOpBlocked) {
			// F06：work 已把 operation 置为 blocked 终态（如恢复遇到占用 lease /
			// needs-reconcile），不是 done——不能宣告成功后丢弃 outcome。
			if a.Log != nil {
				a.Log.Info("claude-accounts", "operation 等待外部条件", fmt.Sprintf("kind=%s", kind))
			}
			return
		}
		if err != nil {
			code := claudeaccounts.ReasonOf(err)
			a.claudeOpUpdate(op, func(d *ClaudeAccountOperationDTO) {
				d.State = "error"
				d.Stage = "failed"
				d.Code = string(code)
			})
			if a.Log != nil {
				a.Log.Warn("claude-accounts", "operation 失败", fmt.Sprintf("kind=%s code=%s", kind, code))
			}
			return
		}
		a.claudeOpUpdate(op, func(d *ClaudeAccountOperationDTO) {
			d.State = "done"
			d.Stage = "commit"
			d.Changed = true
			d.ActiveGeneration = a.claudeAccountsGeneration()
		})
	}()
	return opID, nil
}

// claudeOpMarkBlocked 在 work 内把 operation 置为 blocked 终态并返回哨兵错误
// （F06：未完成的恢复不是 done；调用方返回该错误使 runner 停止改写）。
func (a *App) claudeOpMarkBlocked(op *claudeAccountOperation, code, detail string) error {
	a.claudeOpUpdate(op, func(d *ClaudeAccountOperationDTO) {
		d.State = "blocked"
		d.Stage = "waiting"
		d.Code = code
		d.Warnings = append(d.Warnings, detail)
	})
	return errClaudeAccountOpBlocked
}

// claudeAccountsGeneration 读取缓存池代际（无池为 0）。
func (a *App) claudeAccountsGeneration() uint64 {
	a.claudeAccountsMu.RLock()
	defer a.claudeAccountsMu.RUnlock()
	if a.claudeAccountsPool == nil {
		return 0
	}
	return a.claudeAccountsPool.ActiveGeneration
}

// claudeAccountsServiceOrErr 返回领域服务；未装配/平台不支持返回稳定错误。
func (a *App) claudeAccountsServiceOrErr() (*claudeaccounts.Service, error) {
	if a.claudeAccounts == nil {
		return nil, claudeaccounts.NewErr(claudeaccounts.ReasonUnsupportedPlatform, "账号池服务未装配")
	}
	if a.claudeAccountsUnsupportedPlatform() {
		return nil, claudeaccounts.NewErr(claudeaccounts.ReasonUnsupportedPlatform, "Claude 订阅账号轮换仅支持 macOS（v1）")
	}
	return a.claudeAccounts, nil
}

// ---------------------------------------------------------------------------
// facade 方法（design §7.2）
// ---------------------------------------------------------------------------

// GetClaudeAccounts 返回账号池快照（capability/state/revision/generation/
// accounts/当前 operation/pauseReason/轮换配置）。
func (a *App) GetClaudeAccounts() (*ClaudeAccountsPoolDTO, error) {
	svc, err := a.claudeAccountsServiceOrErr()
	if err != nil {
		if a.claudeAccounts == nil {
			return nil, err
		}
		return &ClaudeAccountsPoolDTO{Capability: "unsupported", State: "unsupported", Accounts: []ClaudeAccountDTO{}}, nil
	}
	a.refreshClaudeAccountsCache(context.Background())
	dto := &ClaudeAccountsPoolDTO{Capability: "supported", Accounts: []ClaudeAccountDTO{}}
	accounts, listErr := svc.ListAccounts(context.Background())
	if listErr == nil && accounts != nil {
		for _, account := range accounts {
			dto.Accounts = append(dto.Accounts, claudeAccountDTOFromDomain(account))
		}
	}
	a.claudeAccountsMu.RLock()
	pool, loadErr := a.claudeAccountsPool, a.claudeAccountsLoadErr
	a.claudeAccountsMu.RUnlock()
	if loadErr != nil {
		dto.State = "blocked"
		dto.PauseReason = string(claudeaccounts.ReasonOf(loadErr))
		return dto, nil
	}
	if pool == nil {
		dto.State = "disabled"
	} else if txn := pool.Transaction; txn != nil {
		if txn.Kind == claudeaccounts.TransactionEnrollment && txn.Enrollment != nil && txn.Enrollment.DriftedReason == "" {
			dto.State = "enrolling"
			// F09：持久化 enrollment 可续接——DTO 返回 TxnID（安全：不含凭据）。
			dto.EnrollmentID = txn.Enrollment.TxnID
		} else {
			dto.State = "needs-reconcile"
		}
		dto.PauseReason = "存在未结事务（" + string(txn.Kind) + "）"
	} else {
		dto.State = "enabled"
	}
	dto.Revision = poolRevisionOf(pool)
	dto.ActiveGeneration = poolGenerationOf(pool)
	dto.ActiveID = poolActiveIDOf(pool)
	if cfg := a.Config.GetClaudeAccountRotation(); cfg != nil {
		dto.RotationEnabled = cfg.EffectiveEnabled()
		dto.RotationConfig = rotationDTOFromDomain(cfg)
	}
	return dto, nil
}

// claudeAccountDTOFromDomain 把 1a 的 credential-free AccountDTO 投影为
// facade DTO（时间统一 RFC3339 字符串，不携带凭据字段）。
func claudeAccountDTOFromDomain(in claudeaccounts.AccountDTO) ClaudeAccountDTO {
	out := ClaudeAccountDTO{
		ID: in.ID, Alias: in.Alias,
		CreatedAt:   in.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   in.UpdatedAt.Format(time.RFC3339),
		EmailMasked: in.EmailMasked, SeatTier: in.SeatTier, Active: in.Active,
		Health: ClaudeAccountHealthDTO{
			ClaudeGrant: string(in.Health.ClaudeGrant), PiGrant: string(in.Health.PiGrant),
			IdentityStatus: in.Health.IdentityStatus, ReasonCode: in.Health.ReasonCode,
		},
	}
	if in.ClaudeExpiresAt != nil {
		expires := in.ClaudeExpiresAt.Format(time.RFC3339)
		out.ClaudeExpiresAt = &expires
	}
	return out
}

// rotationDTOFromDomain 把轮换配置镜像为 facade DTO。
func rotationDTOFromDomain(in *claudeaccounts.ClaudeAccountRotationConfig) *ClaudeAccountRotationConfigDTO {
	if in == nil {
		return nil
	}
	out := &ClaudeAccountRotationConfigDTO{
		SchemaVersion: in.SchemaVersion, Enabled: in.Enabled, Strategy: in.Strategy,
		ThresholdPercent: in.ThresholdPercent, CooldownSeconds: in.CooldownSeconds,
		HysteresisPoints: in.HysteresisPoints, ManualRespectSeconds: in.ManualRespectSeconds,
	}
	if in.AcknowledgedRiskVersion != nil {
		ack := *in.AcknowledgedRiskVersion
		out.AcknowledgedRiskVersion = &ack
	}
	return out
}

func poolRevisionOf(pool *claudeaccounts.AccountPool) uint64 {
	if pool == nil {
		return 0
	}
	return pool.Revision
}

func poolGenerationOf(pool *claudeaccounts.AccountPool) uint64 {
	if pool == nil {
		return 0
	}
	return pool.ActiveGeneration
}

func poolActiveIDOf(pool *claudeaccounts.AccountPool) string {
	if pool == nil {
		return ""
	}
	return pool.ActiveAccountID
}

// BeginClaudeAccountEnrollment 开始录入第二个账号：先 harvest 现有活动账号
// 并持久化，再进入 enrolling（design §4.1；enrolling 期间受影响启动暂停）。
func (a *App) BeginClaudeAccountEnrollment(expectedRevision uint64) (string, error) {
	svc, err := a.claudeAccountsServiceOrErr()
	if err != nil {
		return "", err
	}
	enrollmentID, err := svc.BeginEnrollment(context.Background(), expectedRevision)
	a.refreshClaudeAccountsCache(context.Background())
	return enrollmentID, err
}

// CaptureClaudeAccount 采集当前双端登录（后端读取秘密，前端不传 token）：
// enrollmentID 为空表示首次录入（CaptureFirstAccount），否则完成 enrollment
// 的第二账号采集（CaptureEnrolledAccount）。长 I/O 走后台 operation。
// F10：曾初始化的池已消失（POOL_MISSING）时拒绝首次录入——不能按空池
// 重建并丢失对旧池/事务的认知；恢复/重建须显式确认，v1 fail closed。
func (a *App) CaptureClaudeAccount(enrollmentID, alias string, expectedRevision uint64) (string, error) {
	svc, err := a.claudeAccountsServiceOrErr()
	if err != nil {
		return "", err
	}
	if enrollmentID == "" {
		a.claudeAccountsMu.RLock()
		loadErr := a.claudeAccountsLoadErr
		a.claudeAccountsMu.RUnlock()
		if domain, ok := claudeaccounts.AsError(loadErr); ok && domain.Reason == claudeaccounts.ReasonPoolMissing {
			return "", claudeAccountsPoolMissingErr()
		}
	}
	digest := "capture|" + enrollmentID + "|" + alias + "|" + strconv.FormatUint(expectedRevision, 10)
	return a.runClaudeAccountOperation("capture", "", digest, a.claudeAccountsGeneration(), func(ctx context.Context, op *claudeAccountOperation) error {
		if enrollmentID == "" {
			_, err := svc.CaptureFirstAccount(ctx, alias, expectedRevision)
			return err
		}
		_, err := svc.CaptureEnrolledAccount(ctx, enrollmentID, alias, expectedRevision)
		return err
	})
}

// CancelClaudeAccountEnrollment 取消录入：只清 enrollment journal；原生槽位
// 在录入期间被改变时保持 needs-reconcile 由 reconcile 分类（design §4.1）。
func (a *App) CancelClaudeAccountEnrollment(expectedRevision uint64) error {
	svc, err := a.claudeAccountsServiceOrErr()
	if err != nil {
		return err
	}
	err = svc.CancelEnrollment(context.Background(), expectedRevision)
	a.refreshClaudeAccountsCache(context.Background())
	return err
}

// SwitchClaudeAccount 手动切换到目标账号：reason 固定 manual（前端不可伪造
// automatic），requestId 幂等（同参数返回原操作，不同参数冲突）。
func (a *App) SwitchClaudeAccount(targetID string, expectedGeneration uint64, requestID string) (string, error) {
	svc, err := a.claudeAccountsServiceOrErr()
	if err != nil {
		return "", err
	}
	digest := "switch|" + targetID + "|" + strconv.FormatUint(expectedGeneration, 10)
	return a.runClaudeAccountOperation("switch", requestID, digest, expectedGeneration, func(ctx context.Context, op *claudeAccountOperation) error {
		res, err := svc.Swap(ctx, claudeaccounts.SwapRequest{
			TargetID:           targetID,
			Reason:             claudeaccounts.SwapReasonManual,
			ExpectedGeneration: expectedGeneration,
		})
		if err == nil {
			op.mu.Lock()
			op.dto.ActiveGeneration = res.Generation
			op.dto.Changed = !res.NoOp
			op.mu.Unlock()
		}
		return err
	})
}

// GetClaudeAccountOperation 查询 operation（stage/state/code/warnings）。
// 未知 ID（含重启后丢失的内存结果）返回稳定错误：请重读当前状态。
func (a *App) GetClaudeAccountOperation(operationID string) (*ClaudeAccountOperationDTO, error) {
	a.claudeAccountsOpsMu.Lock()
	op := a.claudeAccountsOps[operationID]
	a.claudeAccountsOpsMu.Unlock()
	if op == nil {
		return nil, errors.New("OPERATION_NOT_FOUND: 操作不存在或已随重启清除，请重读当前账号状态")
	}
	op.mu.Lock()
	defer op.mu.Unlock()
	dto := op.dto
	if dto.Warnings != nil {
		dto.Warnings = append([]string(nil), dto.Warnings...)
	}
	return &dto, nil
}

// SetClaudeAccountRotation 启停自动轮换（经 ConfigService 写 models.json，
// 账号服务不自行写配置，design §3.2）。启用需要：池存在且活动账号非空、
// expectedRevision 匹配池 revision、riskAckVersion 匹配当前声明版本；停用
// 永远允许（安全出口）。不自动开始首笔 swap。
func (a *App) SetClaudeAccountRotation(enabled bool, expectedRevision uint64, riskAckVersion string) error {
	if a.Config == nil {
		return errors.New("NEEDS_RECONCILE: 配置服务未装配")
	}
	if !enabled {
		cfg := a.Config.GetClaudeAccountRotation()
		if cfg == nil || !cfg.EffectiveEnabled() {
			return nil
		}
		cfg.Enabled = false
		return a.Config.SetClaudeAccountRotation(cfg)
	}
	svc, err := a.claudeAccountsServiceOrErr()
	if err != nil {
		return err
	}
	pool, err := svc.LoadPool(context.Background())
	if err != nil {
		return err
	}
	if pool == nil || pool.ActiveAccountID == "" {
		return claudeaccounts.NewErr(claudeaccounts.ReasonPoolMissing, "启用自动轮换前需先录入账号")
	}
	if pool.Revision != expectedRevision {
		return claudeaccounts.NewErr(claudeaccounts.ReasonRevisionConflict, "池 revision 期望 %d，实际 %d", expectedRevision, pool.Revision)
	}
	if riskAckVersion != claudeAccountsRiskAckVersion {
		return claudeaccounts.NewErr(claudeaccounts.ReasonRevisionConflict,
			"风险确认版本不匹配（期望 %s）", claudeAccountsRiskAckVersion)
	}
	cfg := a.Config.GetClaudeAccountRotation()
	if cfg == nil {
		cfg = claudeaccounts.DefaultRotationConfig()
	}
	cfg.Enabled = true
	ack := claudeAccountsRiskAckVersion
	cfg.AcknowledgedRiskVersion = &ack
	return a.Config.SetClaudeAccountRotation(cfg)
}

// ReconcileClaudeAccounts 恢复入口（design §7.2）：inspect 零写同步返回报告；
// complete-target / adopt-current / restore-source 走 mutation 门的后台
// operation；不允许任意路径/JSON 参数。
func (a *App) ReconcileClaudeAccounts(action string, expectedRevision uint64) (*ClaudeAccountReconcileResultDTO, error) {
	svc, err := a.claudeAccountsServiceOrErr()
	if err != nil {
		return nil, err
	}
	switch action {
	case "inspect":
		report, err := svc.InspectNativeState(context.Background())
		if err != nil {
			return nil, err
		}
		dto := &ClaudeAccountReconcileResultDTO{Inspect: nativeStateToDTO(report)}
		a.refreshClaudeAccountsCache(context.Background())
		return dto, nil
	case "complete-target":
		opDigest := "reconcile|complete-target|" + strconv.FormatUint(expectedRevision, 10)
		id, err := a.runClaudeAccountOperation("reconcile-complete-target", "", opDigest, a.claudeAccountsGeneration(), func(ctx context.Context, op *claudeAccountOperation) error {
			// F06：恢复动作在同一锁语义内校验 expectedRevision——陈旧恢复命令
			// 不得作用于已变化的事务。
			pool, perr := svc.LoadPool(ctx)
			if perr != nil {
				return perr
			}
			if pool != nil && pool.Revision != expectedRevision {
				return claudeaccounts.NewErr(claudeaccounts.ReasonRevisionConflict, "池 revision 期望 %d，实际 %d", expectedRevision, pool.Revision)
			}
			outcome, cerr := svc.RecoverAtStartup(ctx)
			if cerr != nil {
				return cerr
			}
			// F06：消费 outcome——未完成（roll-forward-pending/needs-reconcile/
			// blocked）是 blocked 不是 done；done 必须以 journal 已清为依据。
			switch outcome.Action {
			case claudeaccounts.ReconcileRollForwardPending:
				return a.claudeOpMarkBlocked(op, outcome.ReasonCode, "恢复等待会话退出/外部条件，未完成")
			case claudeaccounts.ReconcileNeedsReconcile:
				return a.claudeOpMarkBlocked(op, string(claudeaccounts.ReasonNeedsReconcile), "存在无法自动分类的投影，需人工恢复")
			case claudeaccounts.ReconcileBlocked:
				return a.claudeOpMarkBlocked(op, outcome.ReasonCode, "原生槽位不可读，恢复被阻塞")
			}
			fresh, ferr := svc.LoadPool(ctx)
			if ferr != nil {
				return ferr
			}
			if fresh != nil && fresh.Transaction != nil {
				return a.claudeOpMarkBlocked(op, string(claudeaccounts.ReasonNeedsReconcile), "恢复后仍存在未结事务")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return &ClaudeAccountReconcileResultDTO{OperationID: id}, nil
	case "adopt-current":
		opDigest := "reconcile|adopt-current|" + strconv.FormatUint(expectedRevision, 10)
		id, err := a.runClaudeAccountOperation("reconcile-adopt-current", "", opDigest, a.claudeAccountsGeneration(), func(ctx context.Context, op *claudeAccountOperation) error {
			_, err := svc.AdoptCurrentLogin(ctx, expectedRevision)
			return err
		})
		if err != nil {
			return nil, err
		}
		return &ClaudeAccountReconcileResultDTO{OperationID: id}, nil
	case "restore-source":
		opDigest := "reconcile|restore-source|" + strconv.FormatUint(expectedRevision, 10)
		id, err := a.runClaudeAccountOperation("reconcile-restore-source", "", opDigest, a.claudeAccountsGeneration(), func(ctx context.Context, op *claudeAccountOperation) error {
			pool, err := svc.LoadPool(ctx)
			if err != nil {
				return err
			}
			// F06：陈旧恢复命令（stale expectedRevision）不得作用于已变化事务。
			if pool != nil && pool.Revision != expectedRevision {
				return claudeaccounts.NewErr(claudeaccounts.ReasonRevisionConflict, "池 revision 期望 %d，实际 %d", expectedRevision, pool.Revision)
			}
			if pool == nil || pool.Transaction == nil || pool.Transaction.Kind != claudeaccounts.TransactionSwap || pool.Transaction.Swap == nil {
				return claudeaccounts.NewErr(claudeaccounts.ReasonNeedsReconcile, "无未结 swap 事务，无源账号可恢复")
			}
			sourceID := pool.Transaction.Swap.SourceID
			if sourceID == "" || pool.Account(sourceID) == nil {
				return claudeaccounts.NewErr(claudeaccounts.ReasonNeedsReconcile, "journal 源账号不在池中")
			}
			res, serr := svc.Swap(ctx, claudeaccounts.SwapRequest{
				TargetID:           sourceID,
				Reason:             claudeaccounts.SwapReasonManual,
				ExpectedGeneration: pool.ActiveGeneration,
			})
			if serr != nil {
				return serr
			}
			// F06：Swap 内部已先处理未结事务（含全 before 清 journal）再 no-op；
			// done 以 journal 已清为依据复核。
			fresh, ferr := svc.LoadPool(ctx)
			if ferr != nil {
				return ferr
			}
			if fresh != nil && fresh.Transaction != nil {
				return a.claudeOpMarkBlocked(op, string(claudeaccounts.ReasonNeedsReconcile), "恢复后仍存在未结事务")
			}
			_ = res
			return nil
		})
		if err != nil {
			return nil, err
		}
		return &ClaudeAccountReconcileResultDTO{OperationID: id}, nil
	default:
		return nil, errors.New("REVISION_CONFLICT: 未知恢复动作（白名单：inspect/complete-target/adopt-current/restore-source）")
	}
}

func nativeStateToDTO(report *claudeaccounts.NativeStateReport) *ClaudeAccountNativeStateDTO {
	if report == nil {
		return nil
	}
	dto := &ClaudeAccountNativeStateDTO{
		HasJournal:              report.HasJournal,
		JournalPhase:            string(report.JournalPhase),
		MatchesActiveSnapshot:   report.MatchesActiveSnapshot,
		ExternalChangeSuspected: report.ExternalChangeSuspected,
		ActiveID:                report.ActiveID,
		Generation:              report.Generation,
	}
	if report.Classifications != nil {
		dto.Classifications = make(map[string]string, len(report.Classifications))
		for k, v := range report.Classifications {
			dto.Classifications[string(k)] = v
		}
	}
	return dto
}

// RemoveClaudeAccount 移除保存的账号（design §7.1：活动账号须先切换或停止
// 管理；未清 journal 禁止移除——由 1a RemoveAccount 强制）。
func (a *App) RemoveClaudeAccount(id string, expectedRevision uint64) error {
	svc, err := a.claudeAccountsServiceOrErr()
	if err != nil {
		return err
	}
	err = svc.RemoveAccount(context.Background(), id, expectedRevision)
	a.refreshClaudeAccountsCache(context.Background())
	return err
}

// RenameClaudeAccount 修改账号别名（长度 1..64，1a 强制）。
func (a *App) RenameClaudeAccount(id, alias string, expectedRevision uint64) error {
	svc, err := a.claudeAccountsServiceOrErr()
	if err != nil {
		return err
	}
	err = svc.RenameAccount(context.Background(), id, alias, expectedRevision)
	a.refreshClaudeAccountsCache(context.Background())
	return err
}

// ---------------------------------------------------------------------------
// 服务装配
// ---------------------------------------------------------------------------

// newClaudeAccountsService 装配 1a 领域服务：Deps 全注入生产实现，路径用
// canonical 标准路径与 ~/.amagi-codebox 锁文件（design §2/§3）。非 darwin
// cgo 平台服务仍构造（facade 返回 UNSUPPORTED_PLATFORM），启动门禁按
// unsupported 短路为 absent（零行为变化）。装配失败返回 nil（facade 稳定
// 报错，不阻断应用启动）。
func newClaudeAccountsService(app *App) *claudeaccounts.Service {
	if app.claudeAccountsUnsupportedPlatform() {
		app.claudeAccountsUnsupported = true
	}
	paths, err := claudeaccounts.DefaultFileCredentialPaths()
	if err != nil {
		if app.Log != nil {
			app.Log.Warn("claude-accounts", "解析 canonical 凭据路径失败，账号轮换保持未装配", err.Error())
		}
		return nil
	}
	lockPath, err := claudeaccounts.DefaultPoolLockPath()
	if err != nil {
		if app.Log != nil {
			app.Log.Warn("claude-accounts", "解析账号池锁路径失败，账号轮换保持未装配", err.Error())
		}
		return nil
	}
	markerPath, err := claudeaccounts.DefaultPoolMarkerPath()
	if err != nil {
		if app.Log != nil {
			app.Log.Warn("claude-accounts", "解析账号池标记路径失败，标记不生效", err.Error())
		}
		markerPath = ""
	}
	app.claudeAccountsPoolMarkerPath = markerPath
	poolStore := claudeaccounts.NewKeychainPoolStore()
	// F01：mutation 释放前发布权威状态需直接读池存储（不经 Service 串行锁）；
	// F10：标记判定同源。
	app.claudeAccountsPoolStore = poolStore
	client := claudeaccounts.NewConstrainedHTTPClient(nil)
	svc, err := claudeaccounts.NewService(claudeaccounts.Deps{
		Pool: poolStore,
		Native: claudeaccounts.NewCombinedNativeStore(
			claudeaccounts.NewFileCredentialStore(paths, nil),
			claudeaccounts.NewClaudeKeychainAccess(),
		),
		LockPath:   lockPath,
		PiAuthPath: paths.PiAuthPath,
		Gate:       claudeCredentialsMutationGate{app: app},
		Quiescence: claudeProcessQuiescence{
			app:         app,
			lister:      app.claudeAccountsProcessSamples,
			attribution: app.claudeQuiescenceAttribution,
		},
		Identity:   claudeaccounts.NewHTTPIdentityProvider(client),
		HTTPClient: client,
		Logf: func(format string, args ...any) {
			if app.Log != nil {
				app.Log.Info("claudeaccounts", fmt.Sprintf(format, args...))
			}
		},
		EmitEvent: func(ev claudeaccounts.Event) {
			// 领域完成事件只转发 kind/generation（design §7.2：事件禁夹带
			// 邮箱/token/hash；txn/account ID 不出进程）。
			if app.ctx == nil {
				return
			}
			wailsRuntime.EventsEmit(app.ctx, "claude-accounts:event", map[string]any{
				"kind":       ev.Kind,
				"generation": ev.Generation,
			})
		},
	})
	if err != nil {
		if app.Log != nil {
			app.Log.Warn("claude-accounts", "账号轮换服务装配失败", err.Error())
		}
		return nil
	}
	return svc
}
