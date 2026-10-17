package wslsupply

// intent_store.go — OperationIntent 的持久化与观察登记（攻坚契约「持久化
// 顺序与清理」）：私有目录 → 唯一 tmp → 完整写 → Sync → Close → 权限检查 →
// 内容读回验证 → rename 唯一 intent 名 → 最终读回验证。intent 确认可读取后
// 才允许改变目标。pending 有界扫描本目录，格式损坏记录不自动删除。

import (
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"amagi-codebox/internal/platform"
)

// intentVersion 是 intent 文件格式版本。
const intentVersion = 1

// pendingScanLimit 是 pending 目录的有界扫描上限（本目录 manifest；不扫
// 工程/CLI 根）。
const pendingScanLimit = 512

// formatOpID 生成时间有序 + 随机后缀的操作 ID（并发下唯一；截断纳秒会在
// 高并发时碰撞——两个操作会共享同一 staging/intent 名）。
func formatOpID(t time.Time) string {
	var rnd [4]byte
	if _, err := crand.Read(rnd[:]); err != nil {
		// 降级：进程内原子计数器保证唯一（crypto 不可用时）。
		return fmt.Sprintf("op-%s-%08x-%d", t.UTC().Format("20060102T150405"), uint32(t.Nanosecond()), atomic.AddUint64(&opIDFallback, 1))
	}
	return fmt.Sprintf("op-%s-%08x-%x", t.UTC().Format("20060102T150405"), uint32(t.Nanosecond()), rnd[:])
}

var opIDFallback uint64

// saveIntent 以不可变方式持久化一个操作 intent（全顺序见文件头注释）。
// 已存在同名 intent 时拒绝（不可变；重试必须换新 OpID）。
func (s *stateStore) saveIntent(op OperationIntent) error {
	if op.OpID == "" {
		return errors.New("intent opID is required")
	}
	op.Version = intentVersion
	b, err := json.MarshalIndent(op, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal operation intent: %w", err)
	}
	b = append(b, '\n')
	dir := filepath.Join(s.configDir, pendingDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir pending intent dir: %w", err)
	}
	final := pendingIntentPath(s.configDir, op.OpID)
	if _, serr := os.Lstat(final); serr == nil {
		return fmt.Errorf("operation intent %s already exists (immutable)", op.OpID)
	}
	tmp, err := os.CreateTemp(dir, ".intent-*")
	if err != nil {
		return fmt.Errorf("create intent tmp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(b); err != nil {
		cleanup()
		return fmt.Errorf("write intent tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync intent tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close intent tmp: %w", err)
	}
	// 私有化 + 验证（tmp 层）：走平台原语——POSIX 收紧并验证 0600；Windows
	// 宿主上 Mode().Perm() 恒报 0666，不能作为私有判据，改为真实 owner-only
	// DACL 建立与复验（Windows 真机已证实旧 POSIX 位判据令每次 intent 保存失败）。
	if err := platform.EnsurePrivateFile(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("verify intent tmp perms: %w", err)
	}
	if got, rerr := os.ReadFile(tmpPath); rerr != nil || string(got) != string(b) {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("verify intent tmp readback: err=%v equal=%v", rerr, string(got) == string(b))
	}
	if err := os.Rename(tmpPath, final); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit operation intent: %w", err)
	}
	// 最终读回验证：确认可读取且内容一致。
	got, err := os.ReadFile(final)
	if err != nil || string(got) != string(b) {
		return fmt.Errorf("verify operation intent after commit: err=%v (readback failed; treat as pending)", err)
	}
	return nil
}

// loadIntents 有界扫描 pending 目录返回全部 intent（损坏文件记录不删）。
func (s *stateStore) loadIntents() (map[string]OperationIntent, error) {
	dir := filepath.Join(s.configDir, pendingDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.mu.Lock()
			s.health.Pending = StoreOK
			s.health.PendingErr = ""
			s.mu.Unlock()
			return map[string]OperationIntent{}, nil
		}
		s.mu.Lock()
		s.health.Pending = StoreUnreadable
		s.health.PendingErr = err.Error()
		s.mu.Unlock()
		return nil, fmt.Errorf("scan pending intents: %w", err)
	}
	out := map[string]OperationIntent{}
	corrupt := 0
	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".intent-") {
			continue
		}
		scanned++
		if scanned > pendingScanLimit {
			break // 有界：超限停止（已见部分仍返回；health 标记不可判全量）
		}
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			corrupt++
			continue
		}
		var op OperationIntent
		if jerr := json.Unmarshal(data, &op); jerr != nil || op.OpID == "" || op.Version > intentVersion {
			corrupt++ // 格式损坏记录不自动删除（冻结处置交 reconcile）
			continue
		}
		out[op.OpID] = op
	}
	s.mu.Lock()
	if corrupt > 0 {
		s.health.Pending = StoreCorrupt
		s.health.PendingErr = fmt.Sprintf("%d unreadable/corrupt intent files kept in place", corrupt)
	} else {
		s.health.Pending = StoreOK
		s.health.PendingErr = ""
	}
	s.mu.Unlock()
	return out, nil
}

// readIntent 读回单个 intent（存在性/可读性判断用）。
func (s *stateStore) readIntent(opID string) (OperationIntent, bool, error) {
	data, err := os.ReadFile(pendingIntentPath(s.configDir, opID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return OperationIntent{}, false, nil
		}
		return OperationIntent{}, false, err
	}
	var op OperationIntent
	if jerr := json.Unmarshal(data, &op); jerr != nil {
		return OperationIntent{}, true, fmt.Errorf("parse intent %s: %w", opID, jerr)
	}
	return op, true, nil
}

// deleteIntent 删除一个已终结的 intent 文件（清理顺序的靠后步骤）。
func (s *stateStore) deleteIntent(opID string) error {
	if err := os.Remove(pendingIntentPath(s.configDir, opID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete intent %s: %w", opID, err)
	}
	return nil
}

// observationRegistry 是进程内操作观察的锁内注册表（独立于 durable 清单；
// 深复制读写；重启由 intent+目标重建）。
type observationRegistry struct {
	mu     sync.Mutex
	latest map[string]OperationObservation // targetKey|unit → 最新观察
	byOp   map[string]OperationObservation // opID → 最新观察
}

func newObservationRegistry() *observationRegistry {
	return &observationRegistry{latest: map[string]OperationObservation{}, byOp: map[string]OperationObservation{}}
}

// record 深复制写入一条观察。
func (r *observationRegistry) record(ob OperationObservation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byOp[ob.OpID] = ob
	if ob.Unit != "" && ob.TargetKey != "" {
		key := obUnitKey(ob.TargetKey, ob.Unit)
		if prev, ok := r.latest[key]; !ok || !prev.At.After(ob.At) {
			r.latest[key] = ob
		}
	}
}

// latestForUnit 返回目标+单元的最新观察（深复制）。
func (r *observationRegistry) latestForUnit(targetKey string, unit UnitID) (OperationObservation, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ob, ok := r.latest[obUnitKey(targetKey, unit)]
	return ob, ok
}

// forOp 返回某操作的观察（深复制）。
func (r *observationRegistry) forOp(opID string) (OperationObservation, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ob, ok := r.byOp[opID]
	return ob, ok
}

// snapshot 返回全部观察的深复制（GetStatus 合并用）。
func (r *observationRegistry) snapshot() []OperationObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]OperationObservation, 0, len(r.byOp))
	for _, ob := range r.byOp {
		out = append(out, ob)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

func obUnitKey(targetKey string, unit UnitID) string { return targetKey + "|" + string(unit) }

// recordObservation 是协调器侧入口（同步写注册表）。
func (c *Coordinator) recordObservation(targetKey string, opID string, unit UnitID, effect OpEffect, reg OpRegistration, recovery OpRecovery, errCode, detail string) {
	c.observations.record(OperationObservation{
		OpID: opID, TargetKey: targetKey, Unit: unit, Effect: effect, Registration: reg, Recovery: recovery,
		ErrorCode: errCode, Detail: detail, At: time.Now(),
	})
}
