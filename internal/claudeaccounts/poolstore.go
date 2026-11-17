package claudeaccounts

import (
	"context"
	"encoding/json"
	"fmt"
)

// PoolStore 是账号池 envelope 的持久化边界（design §3.1/§3.3）。
// 生产实现是 macOS Keychain 专用 service；测试注入 fake，绝不实例化真实
// Keychain backend。
type PoolStore interface {
	// Load 读取池 envelope。从未创建返回 (nil, nil)——「正常未启用」；
	// 被删除/损坏必须以稳定 reason 报错，不得返回空池让上层覆盖原生认证。
	Load(ctx context.Context) (*AccountPool, error)
	// Save 原子写入池 envelope，并在写入后立即回读验证 revision 与关键
	// 字段（design §3.3：池写入结果必须回读验证 revision）。回读不一致
	// 视为保存失败。
	Save(ctx context.Context, pool *AccountPool) error
	// Kind 返回后端标识（"keychain" / "memory" 等，仅用于日志与测试）。
	Kind() string
}

// encodePoolEnvelope 序列化池 envelope（存 Keychain 的唯一 JSON）。
func encodePoolEnvelope(pool *AccountPool) ([]byte, error) {
	if pool == nil {
		return nil, fmt.Errorf("encode pool envelope: nil pool")
	}
	data, err := json.Marshal(pool)
	if err != nil {
		return nil, fmt.Errorf("encode pool envelope: %w", err)
	}
	return data, nil
}

// DecodePoolEnvelope 解析并校验池 envelope。校验失败返回 *Error：
// schemaVersion 未知 → POOL_CORRUPT；JSON 损坏 → POOL_CORRUPT。
func DecodePoolEnvelope(data []byte) (*AccountPool, error) {
	var pool AccountPool
	if err := json.Unmarshal(data, &pool); err != nil {
		return nil, NewErr(ReasonPoolCorrupt, "池 envelope JSON 不可解析")
	}
	if pool.SchemaVersion == 0 && len(pool.Accounts) == 0 && pool.Revision == 0 {
		// 空对象视为损坏：真实空池也有 schemaVersion=1。
		return nil, NewErr(ReasonPoolCorrupt, "池 envelope 缺少 schemaVersion")
	}
	if pool.SchemaVersion != PoolSchemaVersion {
		return nil, NewErr(ReasonPoolCorrupt, "池 schema 版本未知（期望 %d，实际 %d）", PoolSchemaVersion, pool.SchemaVersion)
	}
	if pool.Accounts == nil {
		pool.Accounts = map[string]*AccountSnapshot{}
	}
	if pool.ActiveAccountID != "" && pool.Accounts[pool.ActiveAccountID] == nil {
		return nil, NewErr(ReasonPoolCorrupt, "活动账号指向不存在的账号")
	}
	if txn := pool.Transaction; txn != nil {
		switch txn.Kind {
		case TransactionSwap:
			if txn.Swap == nil {
				return nil, NewErr(ReasonPoolCorrupt, "swap 事务缺少 journal")
			}
		case TransactionEnrollment:
			if txn.Enrollment == nil {
				return nil, NewErr(ReasonPoolCorrupt, "enrollment 事务缺少 journal")
			}
		default:
			return nil, NewErr(ReasonPoolCorrupt, "未知事务类型 %q", string(txn.Kind))
		}
	}
	return &pool, nil
}

// verifySavedPool 是 Save 后的回读验证：解析并比对 revision 与关键身份字段
// （design §3.3：写入结果必须回读验证 revision）。
func verifySavedPool(reloaded *AccountPool, expected *AccountPool) error {
	if reloaded == nil {
		return NewErr(ReasonPoolCorrupt, "池保存后回读为空")
	}
	if reloaded.Revision != expected.Revision {
		return NewErr(ReasonPoolCorrupt, "池保存后回读 revision 不一致（期望 %d，实际 %d）", expected.Revision, reloaded.Revision)
	}
	if reloaded.ActiveAccountID != expected.ActiveAccountID {
		return NewErr(ReasonPoolCorrupt, "池保存后回读活动账号不一致")
	}
	if reloaded.ActiveGeneration != expected.ActiveGeneration {
		return NewErr(ReasonPoolCorrupt, "池保存后回读代际不一致")
	}
	if len(reloaded.Accounts) != len(expected.Accounts) {
		return NewErr(ReasonPoolCorrupt, "池保存后回读账号数量不一致")
	}
	for id, want := range expected.Accounts {
		got, ok := reloaded.Accounts[id]
		if !ok || got == nil {
			return NewErr(ReasonPoolCorrupt, "池保存后回读缺少账号 %s", id)
		}
		if got.Alias != want.Alias || !got.UpdatedAt.Equal(want.UpdatedAt) {
			return NewErr(ReasonPoolCorrupt, "池保存后回读账号 %s 元数据不一致", id)
		}
	}
	return nil
}
