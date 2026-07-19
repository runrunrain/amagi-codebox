package opencode

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestQuerySessionsOpenCodeSchema 用真实 OpenCode schema 构造临时 db。
//
// 实测真相：表名是单数 session；time_* 是 13 位毫秒；model 是 JSON 字符串；
// cost 是 real；币种按 providerID 推断（zhipuai → CNY，DeepSeek → USD）。
func TestQuerySessionsOpenCodeSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	createOpenCodeTestDB(t, dbPath)

	// 全量查询：sinceTimeUpdated=0
	stubs, maxUpdated, err := QuerySessions(dbPath, 0)
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	if len(stubs) != 2 {
		t.Fatalf("expected 2 stubs, got %d", len(stubs))
	}

	// stubs 按 time_updated ASC 排序：
	// stubs[0] = ses_gpt（time_updated=1783051351176，更早；gpt-4o/openai/USD）
	// stubs[1] = ses_glm（time_updated=1783051798442，更晚；glm-5.2/zhipuai/CNY）
	s0 := stubs[0]
	if s0.Model != "gpt-4o" {
		t.Errorf("model0 = %q, want gpt-4o", s0.Model)
	}
	if s0.Provider != "openai" {
		t.Errorf("provider0 = %q, want openai", s0.Provider)
	}
	if s0.CurrencyCode != "USD" {
		t.Errorf("currency0 = %q, want USD", s0.CurrencyCode)
	}
	if s0.SessionID != "ses_gpt" {
		t.Errorf("session_id0 = %q, want ses_gpt", s0.SessionID)
	}
	if !s0.CostProvided {
		t.Errorf("cost_provided0 should be true")
	}
	if s0.NativeCost != 5000 { // 0.005 × 1e6
		t.Errorf("native_cost0 = %d, want 5000", s0.NativeCost)
	}

	// 第二条：glm-5.2 + zhipuai → CNY
	s1 := stubs[1]
	if s1.Model != "glm-5.2" {
		t.Errorf("model1 = %q, want glm-5.2", s1.Model)
	}
	if s1.Provider != "zhipuai" {
		t.Errorf("provider1 = %q, want zhipuai", s1.Provider)
	}
	if s1.CurrencyCode != "CNY" {
		t.Errorf("currency1 = %q, want CNY", s1.CurrencyCode)
	}
	if s1.NativeCost != 193251 { // 0.193251 × 1e6
		t.Errorf("native_cost1 = %d, want 193251", s1.NativeCost)
	}
	// reasoning 归 output：tokens_output(460) + tokens_reasoning(11682) = 12142
	if s1.OutputTokens != 460+11682 {
		t.Errorf("output1 = %d, want %d (reasoning folded)", s1.OutputTokens, 460+11682)
	}
	if s1.InputTokens != 58989 {
		t.Errorf("input1 = %d, want 58989", s1.InputTokens)
	}
	if s1.CacheReadInputTokens != 220160 {
		t.Errorf("cache_read1 = %d, want 220160", s1.CacheReadInputTokens)
	}

	// maxTimeUpdated 应是两条中的最大值
	if maxUpdated != 1783051798442 {
		t.Errorf("maxUpdated = %d, want 1783051798442", maxUpdated)
	}

	// 增量查询：sinceTimeUpdated = 1783051351176（gpt 的 time_updated），应只返回 glm
	stubs2, _, err := QuerySessions(dbPath, 1783051351176)
	if err != nil {
		t.Fatalf("incremental QuerySessions: %v", err)
	}
	if len(stubs2) != 1 {
		t.Errorf("incremental: expected 1 stub (time_updated > 1783051351176), got %d", len(stubs2))
	}
	if stubs2[0].SessionID != "ses_glm" {
		t.Errorf("incremental session = %q, want ses_glm", stubs2[0].SessionID)
	}
}

// TestInferCurrency 覆盖各 provider 到币种映射。
func TestInferCurrency(t *testing.T) {
	cases := map[string]string{
		"zhipuai":    "CNY",
		"ZHIPU":      "CNY",
		"deepseek":   "USD",
		"moonshot":   "CNY",
		"kimi":       "CNY",
		"minimax":    "CNY",
		"doubao":     "CNY",
		"volcengine": "CNY",
		"qwen":       "CNY",
		"aliyun":     "CNY",
		"dashscope":  "CNY",
		"openai":     "USD",
		"anthropic":  "USD",
		"google":     "USD",
		"":           "USD",
		"unknown":    "USD",
	}
	for in, want := range cases {
		if got := inferCurrencyByProvider(in); got != want {
			t.Errorf("inferCurrencyByProvider(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestQuerySessionsUsesAssistantMessageMetadata covers newer OpenCode schemas
// where session.model is NULL and model/provider live on the newest assistant
// message instead.
func TestQuerySessionsUsesAssistantMessageMetadata(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	createOpenCodeTestDB(t, dbPath)
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?", dbPath))
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE message (
		id TEXT PRIMARY KEY, session_id TEXT NOT NULL, data TEXT NOT NULL,
		time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL
	)`); err != nil {
		t.Fatalf("create message table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO session
		(id, project_id, slug, directory, title, version, time_created, time_updated, model, cost,
		 tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"ses_message", "proj1", "slug3", "/work3", "title3", "1",
		1783051800000, 1783051900000, nil, 0.042081436,
		59430, 13548, 0, 618112, 0); err != nil {
		t.Fatalf("insert NULL-model session: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO message (id, session_id, data, time_created, time_updated) VALUES
		(?,?,?,?,?), (?,?,?,?,?)`,
		"msg_old", "ses_message", `{"role":"assistant","providerID":"openai","modelID":"gpt-4o"}`, 1, 1,
		"msg_new", "ses_message", `{"role":"assistant","providerID":"deepseek","modelID":"deepseek-v4-pro"}`, 2, 2); err != nil {
		t.Fatalf("insert assistant messages: %v", err)
	}

	stubs, _, err := QuerySessions(dbPath, 0)
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	for _, stub := range stubs {
		if stub.SessionID != "ses_message" {
			continue
		}
		if stub.Model != "deepseek-v4-pro" || stub.Provider != "deepseek" {
			t.Fatalf("assistant metadata = %s/%s, want deepseek-v4-pro/deepseek", stub.Model, stub.Provider)
		}
		if stub.CurrencyCode != "USD" {
			t.Fatalf("DeepSeek V4 Pro currency = %s, want USD", stub.CurrencyCode)
		}
		return
	}
	t.Fatal("NULL-model session was not returned")
}

// createOpenCodeTestDB 用真实 schema 构造测试 db（两条 session 数据）。
//
// 数据模仿实测样本：第一条 glm-5.2/zhipuai，第二条 gpt-4o/openai。
func createOpenCodeTestDB(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?", dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// 复制实测 schema（关键字段：cost real, time_* integer milliseconds, model text）
	schema := `CREATE TABLE session (
		id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL,
		parent_id TEXT,
		slug TEXT NOT NULL,
		directory TEXT NOT NULL,
		title TEXT NOT NULL,
		version TEXT NOT NULL,
		share_url TEXT,
		summary_additions INTEGER,
		summary_deletions INTEGER,
		summary_files INTEGER,
		summary_diffs TEXT,
		revert TEXT,
		permission TEXT,
		time_created INTEGER NOT NULL,
		time_updated INTEGER NOT NULL,
		time_compacting INTEGER,
		time_archived INTEGER,
		workspace_id TEXT,
		path TEXT,
		agent TEXT,
		model TEXT,
		cost REAL DEFAULT 0 NOT NULL,
		tokens_input INTEGER DEFAULT 0 NOT NULL,
		tokens_output INTEGER DEFAULT 0 NOT NULL,
		tokens_reasoning INTEGER DEFAULT 0 NOT NULL,
		tokens_cache_read INTEGER DEFAULT 0 NOT NULL,
		tokens_cache_write INTEGER DEFAULT 0 NOT NULL,
		metadata TEXT
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	// 第一条：glm-5.2/zhipuai，cost=0.193251，time_updated=1783051798442（实测样本）
	_, err = db.Exec(`INSERT INTO session
		(id, project_id, slug, directory, title, version,
		 time_created, time_updated, model, cost,
		 tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"ses_glm", "proj1", "slug1", "/work", "title1", "1",
		1783051396540, 1783051798442,
		`{"id":"glm-5.2","providerID":"zhipuai","variant":"high"}`,
		0.193251,
		58989, 460, 11682, 220160, 0)
	if err != nil {
		t.Fatalf("insert glm: %v", err)
	}

	// 第二条：gpt-4o/openai，cost=0.005，time_updated=1783051351176（更早，用于增量查询）
	_, err = db.Exec(`INSERT INTO session
		(id, project_id, slug, directory, title, version,
		 time_created, time_updated, model, cost,
		 tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"ses_gpt", "proj1", "slug2", "/work2", "title2", "1",
		1783051350000, 1783051351176,
		`{"id":"gpt-4o","providerID":"openai"}`,
		0.005,
		1000, 200, 0, 50, 0)
	if err != nil {
		t.Fatalf("insert gpt: %v", err)
	}

	// 防止 lint 报 unused
	_ = os.Stat
	_ = time.Now
}
