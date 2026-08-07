package usage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestEnumerateOmpSessionFilesSingleRoot verifies omp enumeration uses the
// single root ~/.omp/agent/sessions and recursively covers nested sub-session
// transcripts (sessions/<project>/<session>/<id>.jsonl).
func TestEnumerateOmpSessionFilesSingleRoot(t *testing.T) {
	home := t.TempDir()

	mk := func(rel string) string {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	top := mk(filepath.Join(".omp", "agent", "sessions", "proj", "1734000000000_uuid.jsonl"))
	nested := mk(filepath.Join(".omp", "agent", "sessions", "proj", "1734000000001_sess-uuid", "1734000000002_subagent.jsonl"))

	// 无关目录（非 .omp 根）不得被枚举。
	mk(filepath.Join(".pi", "agent", "sessions", "other", "x.jsonl"))

	got := enumerateOmpSessionFiles(home)
	want := map[string]bool{top: true, nested: true}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected enumerated file: %s", p)
		}
		delete(want, p)
	}
	if len(want) != 0 {
		t.Fatalf("missing expected files after enumeration: %v; got=%v", want, got)
	}
}

// TestNormalizeOmpProvider verifies the amagi- namespace prefix is stripped
// while unprefixed providers pass through unchanged.
func TestNormalizeOmpProvider(t *testing.T) {
	cases := map[string]string{
		"amagi-glm":    "glm",
		"amagi-kimi":   "kimi",
		"anthropic":    "anthropic", // 内置 provider 原样
		"openai":       "openai",
		"":             "",
		"amagi-":       "amagi-", // 前缀剥除后为空 -> 原样（避免空 bucket）
		"my-provider":  "my-provider",
	}
	for raw, want := range cases {
		if got := normalizeOmpProvider(raw); got != want {
			t.Errorf("normalizeOmpProvider(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestSyncOmpJSONL verifies the full omp jsonl sync path: records land with
// AppType=omp, provider prefix stripped, USD currency preserved, the state key
// is "omp_jsonl", and a second sync is idempotent (added=0).
func TestSyncOmpJSONL(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir, nil)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	home := t.TempDir()
	sessionRoot := filepath.Join(home, ".omp", "agent", "sessions", "proj")
	if err := os.MkdirAll(sessionRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionRoot, "1734000000000_uuid.jsonl")
	content := []byte(`{"type":"session","version":3,"id":"uuid-1","timestamp":"2024-12-12T10:00:00.000Z","cwd":"/proj"}` + "\n" +
		`{"type":"message","id":"aa000001","parentId":null,"timestamp":"2024-12-12T10:00:01.000Z","message":{"role":"assistant","provider":"amagi-glm","model":"glm-5","usage":{"input":120,"output":8,"cacheRead":0,"cacheWrite":0,"totalTokens":128,"cost":{"total":0.0171}},"timestamp":1734000001000}}` + "\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}

	ctx := context.Background()
	out := s.syncOmpJSONL(ctx, path)
	if out.err != nil {
		t.Fatalf("syncOmpJSONL: %v", out.err)
	}
	if out.added != 1 || out.processed != 1 {
		t.Fatalf("first sync added=%d processed=%d, want 1/1", out.added, out.processed)
	}

	// 记录内容：AppType=omp、provider 前缀剥除、USD 原生成本保留。
	rows, err := s.queryDB().Query(`SELECT app_type, provider, model, currency_code, cost_provided FROM usage_records WHERE dedup_key LIKE 'omp:%'`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var gotApp, gotProvider, gotModel, gotCurrency string
	var gotCostProvided bool
	if !rows.Next() {
		t.Fatal("no omp record persisted")
	}
	if err := rows.Scan(&gotApp, &gotProvider, &gotModel, &gotCurrency, &gotCostProvided); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if gotApp != appOmp || gotProvider != "glm" || gotModel != "glm-5" || gotCurrency != "USD" || !gotCostProvided {
		t.Fatalf("record = app:%q provider:%q model:%q currency:%q costProvided:%v, want omp/glm/glm-5/USD/true",
			gotApp, gotProvider, gotModel, gotCurrency, gotCostProvided)
	}

	// state key 是 "omp_jsonl"。
	state, err := getSyncState(ctx, s.db, "omp_jsonl", path)
	if err != nil {
		t.Fatalf("getSyncState: %v", err)
	}
	if state.SourceType != "omp_jsonl" || state.AppType != appOmp || state.LastLineOffset == 0 {
		t.Fatalf("state = %+v, want omp_jsonl/%s with progress", state, appOmp)
	}

	// 幂等：第二次同步无新增。
	out2 := s.syncOmpJSONL(ctx, path)
	if out2.err != nil {
		t.Fatalf("second sync: %v", out2.err)
	}
	if out2.added != 0 {
		t.Fatalf("second sync added=%d, want 0 (idempotent)", out2.added)
	}
}
