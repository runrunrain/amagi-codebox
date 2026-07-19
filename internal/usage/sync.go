package usage

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/appmeta/claude"
	"amagi-codebox/internal/appmeta/codex"
	"amagi-codebox/internal/appmeta/opencode"
)

// 并发同步上限（设计 8.4：限制 4 并发文件处理）。
const syncConcurrency = 4

// SyncAll 触发三类源全量+增量同步，串行化（mu 互斥）。
//
// 流程：
//  1. Claude Code：枚举 ~/.claude/projects/*/*.jsonl
//  2. Codex：枚举 ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
//  3. OpenCode：读 ~/.local/share/opencode/opencode.db
//  4. 刷新 daily_rollup（分区刷新：仅重算受影响日期）
//
// 统计语义（M5）：
//   - RecordsAdded：真正新增行（INSERT 生效）。
//   - ProcessedCount：处理过的 stub 总数（含去重命中 / REPLACE 更新）。
//
// rollup 刷新（M3）：
//   - affectedDays 收集本轮 sync 中实际改写过 DB 的行对应的 UTC 日期。
//   - 始终把"今天"加入集合，以兜底 proxy 实时路径直接写入主表的记录（proxy 绕过 sync）。
//   - 若 affectedDays 为空（sync 无任何变化），跳过刷新——rollup 已一致。
func (s *Service) SyncAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return errServiceNotLoaded
	}

	started := time.Now().UTC()
	syncMeta := SyncRunMeta{StartedAt: started, Errors: []string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	home, _ := os.UserHomeDir()
	if home == "" {
		// 无 homeDir 时仅跳过同步（不报错）
		return nil
	}

	var (
		filesScanned   int
		recordsAdded   int64
		processedCount int64
		wg             sync.WaitGroup
		sem            = make(chan struct{}, syncConcurrency)
		errorsMu       sync.Mutex
		affectedMu     sync.Mutex
		affectedDays   = make(map[string]struct{})
	)

	appendErr := func(src, msg string) {
		errorsMu.Lock()
		syncMeta.Errors = append(syncMeta.Errors, "["+src+"] "+msg)
		errorsMu.Unlock()
	}

	// === 1. Claude Code jsonl ===
	claudeFiles := enumerateJSONLs(filepath.Join(home, ".claude", "projects"), ".jsonl")
	filesScanned += len(claudeFiles)
	for _, f := range claudeFiles {
		f := f
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out := s.syncClaudeJSONL(ctx, f)
			if out.err != nil {
				appendErr("claude", f+": "+out.err.Error())
				return
			}
			errorsMu.Lock()
			recordsAdded += out.added
			processedCount += out.processed
			errorsMu.Unlock()
			affectedMu.Lock()
			for _, d := range out.days {
				affectedDays[d] = struct{}{}
			}
			affectedMu.Unlock()
		}()
	}
	wg.Wait()

	// === 2. Codex jsonl ===
	codexFiles := enumerateJSONLs(filepath.Join(home, ".codex", "sessions"), ".jsonl")
	filesScanned += len(codexFiles)
	for _, f := range codexFiles {
		f := f
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out := s.syncCodexJSONL(ctx, f)
			if out.err != nil {
				appendErr("codex", f+": "+out.err.Error())
				return
			}
			errorsMu.Lock()
			recordsAdded += out.added
			processedCount += out.processed
			errorsMu.Unlock()
			affectedMu.Lock()
			for _, d := range out.days {
				affectedDays[d] = struct{}{}
			}
			affectedMu.Unlock()
		}()
	}
	wg.Wait()

	// === 3. OpenCode SQLite ===
	ocPath := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if _, statErr := os.Stat(ocPath); statErr == nil {
		filesScanned++
		out := s.syncOpenCode(ctx, ocPath)
		if out.err != nil {
			appendErr("opencode", out.err.Error())
		} else {
			errorsMu.Lock()
			recordsAdded += out.added
			processedCount += out.processed
			errorsMu.Unlock()
			affectedMu.Lock()
			for _, d := range out.days {
				affectedDays[d] = struct{}{}
			}
			affectedMu.Unlock()
		}
	}

	// === 4. 刷新 daily_rollup（分区刷新） ===
	// 始终加入"今天"以兜底 proxy 实时路径（proxy 绕过 sync 直接写主表，
	// 仅靠分区刷新会漏；多刷新一天的代价极小）。
	affectedDays[time.Now().UTC().Format("2006-01-02")] = struct{}{}
	days := make([]string, 0, len(affectedDays))
	for d := range affectedDays {
		days = append(days, d)
	}
	if err := refreshDailyRollup(ctx, s.db, days); err != nil {
		appendErr("rollup", err.Error())
	}

	syncMeta.FilesScanned = filesScanned
	syncMeta.RecordsAdded = recordsAdded
	syncMeta.ProcessedCount = processedCount
	syncMeta.FinishedAt = time.Now().UTC()
	s.syncMeta = syncMeta
	s.logInfo("usage", "同步完成",
		"files="+strconv.Itoa(filesScanned)+
			" added="+strconv.FormatInt(recordsAdded, 10)+
			" processed="+strconv.FormatInt(processedCount, 10)+
			" days="+strconv.Itoa(len(days))+
			" errors="+strconv.Itoa(len(syncMeta.Errors)))
	return nil
}

// StartBackgroundSync 启动后台定时同步 goroutine（直到 s.Ctx 取消）。
//
// 设计 8.1：默认 5 分钟一次；调用方传 interval 控制。
// ctx 不再作为参数（M1）：context.Context 不可 JS 序列化，
// 原签名会让 Wails v2 生成前端无法调用的无效绑定。ctx 改由 app.go Startup
// 注入到 s.Ctx（导出字段但非方法，wailsjs 不绑定）。
func (s *Service) StartBackgroundSync(interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ctx := s.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.SyncAll(); err != nil {
					s.logWarn("usage", "后台同步失败", err.Error())
				}
			}
		}
	}()
}

// syncOutcome 是单个源的同步产出（M3/M5 重构后统一返回值）。
//
//   - added：真正新增行数（INSERT 生效）。
//   - processed：处理过的 stub 总数（含去重命中 / REPLACE 更新）。
//   - days：本轮实际改写过 DB 的行对应的 UTC 日期（用于 rollup 分区刷新）。
//   - err：源级错误（解析失败等）；record 级错误不计入此处。
type syncOutcome struct {
	added     int64
	processed int64
	days      []string
	err       error
}

// syncClaudeJSONL 同步单个 Claude jsonl 文件。
//
// 增量策略（设计 8.2）：
//   - mtime + size 未变 → 跳过
//   - 否则从 last_line_offset 起解析，INSERT OR IGNORE（dedup_key="cc:msg_"+message.id）
//   - mtime 变 size 没变 → 保守从头重扫（INSERT OR IGNORE 保证幂等）
func (s *Service) syncClaudeJSONL(ctx context.Context, path string) syncOutcome {
	info, err := os.Stat(path)
	if err != nil {
		return syncOutcome{err: err}
	}
	state, _ := getSyncState(ctx, s.db, "claude_jsonl", path)
	if state.LastMTime == info.ModTime().UnixNano() && state.LastLineOffset == info.Size() {
		return syncOutcome{}
	}

	startOffset := state.LastLineOffset
	// mtime 变但 size 没变：保守从头扫（覆盖更新）
	if state.LastMTime != 0 && state.LastMTime != info.ModTime().UnixNano() && state.LastLineOffset == info.Size() {
		startOffset = 0
	}

	stubs, lastOffset, parseErr := claude.ExtractUsageRecords(path, startOffset)
	if parseErr != nil {
		_ = s.updateSyncStateClaude(ctx, path, info.ModTime().UnixNano(), startOffset, 0, parseErr.Error())
		return syncOutcome{err: parseErr}
	}

	out := syncOutcome{days: make([]string, 0, len(stubs))}
	for _, st := range stubs {
		select {
		case <-ctx.Done():
			out.err = ctx.Err()
			return out
		default:
		}
		evt := UsageEvent{
			AppType:                  appClaudeCode,
			Source:                   SourceSessionLog,
			Model:                    st.Model,
			ProjectDir:               st.ProjectDir,
			SessionID:                st.SessionID,
			InputTokens:              st.InputTokens,
			OutputTokens:             st.OutputTokens,
			CacheReadInputTokens:     st.CacheReadInputTokens,
			CacheCreationInputTokens: st.CacheCreationInputTokens,
			OccurredAt:               st.OccurredAt,
			DedupKey:                 st.DedupKey,
		}
		isNew, err := s.Record(evt)
		if err != nil {
			s.logWarn("usage", "claude record 失败", err.Error())
			continue
		}
		out.processed++
		if isNew {
			out.added++
			out.days = append(out.days, evt.OccurredAt.UTC().Format("2006-01-02"))
		}
	}

	if err := s.updateSyncStateClaude(ctx, path, info.ModTime().UnixNano(), lastOffset, out.added, ""); err != nil {
		// state 写入失败不覆盖业务错误（已写入的记录仍有效）。
		s.logWarn("usage", "claude sync_state 写入失败", err.Error())
	}
	return out
}

func (s *Service) updateSyncStateClaude(ctx context.Context, path string, mtime, offset int64, added int64, lastErr string) error {
	state := SyncState{
		SourceType:     "claude_jsonl",
		SourceKey:      path,
		AppType:        appClaudeCode,
		LastMTime:      mtime,
		LastLineOffset: offset,
		LastSyncedAt:   time.Now().UTC(),
		LastError:      lastErr,
		RecordsAdded:   added,
	}
	return upsertSyncState(ctx, s.db, state)
}

// syncCodexJSONL 同步单个 Codex rollout 文件。
//
// Codex 实测真相（设计 17.1 / 17.2 已确认）：
//   - usage 在 type=="event_msg" && payload.type=="token_count" 行的
//     payload.info.last_token_usage（每 turn 增量）
//   - 字段名是 cached_input_tokens（非设计假设的 cache_read_input_tokens）
//   - model 在 turn_context.payload.model（session_meta.payload.model 通常为 null）
//   - 时间戳在根级 timestamp（ISO8601）
//
// 每个 token_count event 一条记录；dedup_key 含 timestamp 避免同文件多 event 冲突。
func (s *Service) syncCodexJSONL(ctx context.Context, path string) syncOutcome {
	info, err := os.Stat(path)
	if err != nil {
		return syncOutcome{err: err}
	}
	state, _ := getSyncState(ctx, s.db, "codex_jsonl", path)
	if state.LastMTime == info.ModTime().UnixNano() && state.LastLineOffset == info.Size() {
		return syncOutcome{}
	}

	startOffset := state.LastLineOffset
	if state.LastMTime != 0 && state.LastMTime != info.ModTime().UnixNano() && state.LastLineOffset == info.Size() {
		startOffset = 0
	}

	parseContext := codex.UsageContext{
		Provider:   state.LastProvider,
		Model:      state.LastModel,
		SessionID:  state.LastSessionID,
		ProjectDir: state.LastProjectDir,
	}
	// Sync state created before context persistence has an offset but no
	// session_meta / turn_context information. Recover that prefix once without
	// re-inserting its token events.
	if startOffset > 0 && (parseContext.Provider == "" || parseContext.Model == "") {
		if recovered, contextErr := codex.ReadUsageContext(path, startOffset); contextErr == nil {
			parseContext = recovered
		} else {
			s.logWarn("usage", "codex 增量上下文恢复失败", contextErr.Error())
		}
	}

	stubs, lastOffset, nextContext, parseErr := codex.ExtractUsageRecordsWithContext(path, startOffset, parseContext)
	if parseErr != nil {
		_ = s.updateSyncStateCodex(ctx, path, info.ModTime().UnixNano(), startOffset, parseContext, 0, parseErr.Error())
		return syncOutcome{err: parseErr}
	}

	out := syncOutcome{days: make([]string, 0, len(stubs))}
	for _, st := range stubs {
		select {
		case <-ctx.Done():
			out.err = ctx.Err()
			return out
		default:
		}
		evt := UsageEvent{
			AppType:                  appCodex,
			Source:                   SourceSessionLog,
			Provider:                 st.Provider,
			Model:                    st.Model,
			ProjectDir:               st.ProjectDir,
			SessionID:                st.SessionID,
			InputTokens:              st.InputTokens,
			OutputTokens:             st.OutputTokens,
			CacheReadInputTokens:     st.CacheReadInputTokens,
			CacheCreationInputTokens: st.CacheCreationInputTokens,
			OccurredAt:               st.OccurredAt,
			DedupKey:                 st.DedupKey,
		}
		isNew, err := s.Record(evt)
		if err != nil {
			s.logWarn("usage", "codex record 失败", err.Error())
			continue
		}
		out.processed++
		if isNew {
			out.added++
			out.days = append(out.days, evt.OccurredAt.UTC().Format("2006-01-02"))
		}
	}

	if err := s.updateSyncStateCodex(ctx, path, info.ModTime().UnixNano(), lastOffset, nextContext, out.added, ""); err != nil {
		s.logWarn("usage", "codex sync_state 写入失败", err.Error())
	}
	return out
}

func (s *Service) updateSyncStateCodex(ctx context.Context, path string, mtime, offset int64, usageContext codex.UsageContext, added int64, lastErr string) error {
	state := SyncState{
		SourceType:     "codex_jsonl",
		SourceKey:      path,
		AppType:        appCodex,
		LastMTime:      mtime,
		LastLineOffset: offset,
		LastProvider:   usageContext.Provider,
		LastModel:      usageContext.Model,
		LastSessionID:  usageContext.SessionID,
		LastProjectDir: usageContext.ProjectDir,
		LastSyncedAt:   time.Now().UTC(),
		LastError:      lastErr,
		RecordsAdded:   added,
	}
	return upsertSyncState(ctx, s.db, state)
}

// syncOpenCode 同步 OpenCode SQLite（设计 5.3 / 8.3）。
//
// 实测真相（已确认，非设计猜测）：
//   - 表名是单数 session（非 sessions）
//   - time_created/time_updated 是 13 位 Unix 毫秒
//   - model 字段是 JSON 字符串 {"id":"glm-5.2","providerID":"zhipuai",...}，取 .id 作模型名
//   - 币种按 providerID 推断（zhipuai/deepseek/moonshot/minimax/doubao/qwen → CNY，其他 → USD）
//
// 增量策略：基于 sessions.time_updated > last_sync。同 session 更新用 INSERT OR REPLACE。
//
// recordsAdded 语义（M5）：
//   - 走 recordForceInternal，只有 dedup_key 原本不存在（真正新增）才计入 added。
//   - 处理过的 stub 总数（含更新已有 session）计入 processed。
//   - 无论新增还是更新，session 当日都加入 days（rollup 需要刷新该日聚合值）。
func (s *Service) syncOpenCode(ctx context.Context, dbPath string) syncOutcome {
	state, _ := getSyncState(ctx, s.db, "opencode_db", "opencode_default")

	stubs, maxUpdated, parseErr := opencode.QuerySessions(dbPath, state.LastTimeUpdated)
	if parseErr != nil {
		_ = s.updateSyncStateOpenCode(ctx, state.LastTimeUpdated, 0, parseErr.Error())
		return syncOutcome{err: parseErr}
	}

	out := syncOutcome{days: make([]string, 0, len(stubs))}
	for _, st := range stubs {
		select {
		case <-ctx.Done():
			out.err = ctx.Err()
			return out
		default:
		}
		evt := UsageEvent{
			AppType:                  appOpenCode,
			Source:                   SourceSessionLog,
			Provider:                 st.Provider,
			Model:                    st.Model,
			ProjectDir:               st.ProjectDir,
			SessionID:                st.SessionID,
			InputTokens:              st.InputTokens,
			OutputTokens:             st.OutputTokens,
			CacheReadInputTokens:     st.CacheReadInputTokens,
			CacheCreationInputTokens: st.CacheCreationInputTokens,
			OccurredAt:               st.OccurredAt,
			DedupKey:                 st.DedupKey,
			CostProvided:             st.CostProvided,
			NativeCost:               st.NativeCost,
			CurrencyCode:             st.CurrencyCode,
		}
		// OpenCode 同 session 可能更新（cost/tokens 增长），用 upsert；
		// 返回 isNew 区分真正新增 vs 更新已有（M5）。
		isNew, err := s.recordForceInternal(evt)
		if err != nil {
			s.logWarn("usage", "opencode record 失败", err.Error())
			continue
		}
		out.processed++
		if isNew {
			out.added++
		}
		// REPLACE 也改写了行数据，必须刷新该日 rollup。
		out.days = append(out.days, evt.OccurredAt.UTC().Format("2006-01-02"))
	}

	if err := s.updateSyncStateOpenCode(ctx, maxUpdated, out.added, ""); err != nil {
		s.logWarn("usage", "opencode sync_state 写入失败", err.Error())
	}
	return out
}

func (s *Service) updateSyncStateOpenCode(ctx context.Context, lastUpdated int64, added int64, lastErr string) error {
	state := SyncState{
		SourceType:      "opencode_db",
		SourceKey:       "opencode_default",
		AppType:         appOpenCode,
		LastTimeUpdated: lastUpdated,
		LastSyncedAt:    time.Now().UTC(),
		LastError:       lastErr,
		RecordsAdded:    added,
	}
	return upsertSyncState(ctx, s.db, state)
}

// enumerateJSONLs 递归枚举 root 下所有后缀为 suffix 的文件（按路径字典序，便于稳定测试）。
func enumerateJSONLs(root, suffix string) []string {
	return walkFiles(root, suffix)
}

// walkFiles 递归遍历目录，收集所有以 suffix 结尾的文件（按字典序正向排序）。
//
// 出错（目录不存在、权限）静默跳过；调用方对无目录场景视为空集。
//
// 第一期排除规则（设计 1.2 / 5.1 明确不扫）：
//   - Claude Code 的 subagents/ 子目录（嵌套 agent jsonl，第二期才扫）
//   - archived_sessions（Codex 旧归档）
func walkFiles(root, suffix string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 容错：单个目录不可读不中断整体扫描
		}
		if d.IsDir() {
			// 跳过 subagents / archived_sessions 子目录（剪枝）
			lower := strings.ToLower(d.Name())
			if lower == "subagents" || lower == "archived_sessions" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, suffix) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	// 插入排序（文件数通常不大；用标准库 sort 也行，这里避免新增 import）
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j-1] > files[j]; j-- {
			files[j-1], files[j] = files[j], files[j-1]
		}
	}
	return files
}

var errServiceNotLoaded = newErrServiceNotLoaded()

func newErrServiceNotLoaded() error {
	return &simpleErr{msg: "usage service not loaded"}
}

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }
