package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/aiproxy/subscription"
	_ "modernc.org/sqlite"
)

// TrafficPolicy is frozen for each core process, including rollback processes.
type TrafficPolicy struct {
	Settings  Settings
	Nodes     map[string]subscription.Node
	Providers map[string][]string
}
type TrafficRecord struct {
	ID          string `json:"id"`
	Time        string `json:"time"`
	Revision    uint64 `json:"revision"`
	Provider    string `json:"provider"`
	Domain      string `json:"domain"`
	Destination string `json:"destination"`
	Network     string `json:"network"`
	Inbound     string `json:"inbound"`
	Expected    string `json:"expected"`
	Actual      string `json:"actual"`
	Verdict     string `json:"verdict"`
}
type TrafficQuery struct {
	Search   string `json:"search"`
	Verdict  string `json:"verdict"`
	Provider string `json:"provider"`
	Exit     string `json:"exit"`
	Since    string `json:"since"`
	Until    string `json:"until"`
	Offset   int    `json:"offset"`
}
type TrafficGroup struct {
	Provider string `json:"provider"`
	Domain   string `json:"domain"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Verdict  string `json:"verdict"`
	Count    int    `json:"count"`
}
type TrafficPage struct {
	Records []TrafficRecord `json:"records"`
	Groups  []TrafficGroup  `json:"groups"`
	Total   int             `json:"total"`
	Error   string          `json:"error"`
}

const trafficTimeFormat = "2006-01-02T15:04:05.000000000Z"

type trafficStore struct {
	mu  sync.Mutex
	db  *sql.DB
	err string
}

func (t *trafficStore) open(dir string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.db != nil {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.err = err.Error()
		return
	}
	path := filepath.Join(dir, "traffic.sqlite")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.err = err.Error()
		return
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err == nil {
		db.SetMaxOpenConns(1)
		_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;
 CREATE TABLE IF NOT EXISTS traffic (id TEXT PRIMARY KEY, time TEXT NOT NULL, provider TEXT NOT NULL, domain TEXT NOT NULL, expected TEXT NOT NULL, actual TEXT NOT NULL, verdict TEXT NOT NULL, record TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS traffic_time ON traffic(time);
 CREATE INDEX IF NOT EXISTS traffic_verdict ON traffic(verdict);
 CREATE TABLE IF NOT EXISTS traffic_diagnostics (key TEXT PRIMARY KEY, count INTEGER NOT NULL);`)
	}
	if err != nil {
		if db != nil {
			db.Close()
		}
		t.err = err.Error()
		return
	}
	t.db = db
}
func (t *trafficStore) put(r TrafficRecord) {
	if t.db == nil {
		return
	}
	data, _ := json.Marshal(r)
	_, err := t.db.Exec(`INSERT INTO traffic VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET actual=excluded.actual, verdict=excluded.verdict, record=excluded.record`, r.ID, r.Time, r.Provider, r.Domain, r.Expected, r.Actual, r.Verdict, string(data))
	if err != nil {
		t.mu.Lock()
		t.err = "连接记录写入失败：" + err.Error()
		t.mu.Unlock()
	}
}
func (t *trafficStore) query(q TrafficQuery) (TrafficPage, error) {
	t.mu.Lock()
	db, problem := t.db, t.err
	t.mu.Unlock()
	p := TrafficPage{Records: []TrafficRecord{}, Groups: []TrafficGroup{}, Error: problem}
	if db == nil {
		return p, nil
	}
	where := " WHERE 1=1"
	args := []any{}
	for _, filter := range []struct{ column, value string }{{"verdict", q.Verdict}, {"provider", q.Provider}, {"actual", q.Exit}} {
		if filter.value != "" {
			where += " AND " + filter.column + "=?"
			args = append(args, filter.value)
		}
	}
	if q.Search != "" {
		where += " AND (instr(lower(domain),lower(?))>0 OR instr(lower(provider),lower(?))>0 OR instr(lower(actual),lower(?))>0)"
		args = append(args, q.Search, q.Search, q.Search)
	}
	for _, filter := range []struct{ op, value string }{{">=", q.Since}, {"<=", q.Until}} {
		if filter.value != "" {
			parsed, err := time.Parse(time.RFC3339, filter.value)
			if err != nil {
				return p, fmt.Errorf("时间筛选无效")
			}
			where += " AND time" + filter.op + "?"
			args = append(args, parsed.UTC().Format(trafficTimeFormat))
		}
	}
	// A read transaction keeps totals, summaries and the page on the same snapshot.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	var unparsed int
	if err = tx.QueryRowContext(ctx, "SELECT coalesce(sum(count),0) FROM traffic_diagnostics").Scan(&unparsed); err != nil {
		return p, err
	}
	if unparsed > 0 {
		p.Error += fmt.Sprintf(" 有 %d 条连接日志无法解析或缺少目标，不能确认完整覆盖。", unparsed)
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM traffic"+where, args...).Scan(&p.Total); err != nil {
		return p, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT provider,domain,expected,actual,verdict,count(*) FROM traffic"+where+" GROUP BY provider,domain,expected,actual,verdict ORDER BY CASE verdict WHEN 'mismatch' THEN 0 WHEN 'unknown' THEN 1 ELSE 2 END,provider,domain,actual LIMIT 500", args...)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var g TrafficGroup
		if err = rows.Scan(&g.Provider, &g.Domain, &g.Expected, &g.Actual, &g.Verdict, &g.Count); err != nil {
			rows.Close()
			return p, err
		}
		p.Groups = append(p.Groups, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	pageArgs := append(append([]any{}, args...), q.Offset)
	rows, err = tx.QueryContext(ctx, "SELECT record FROM traffic"+where+" ORDER BY time DESC,id DESC LIMIT 100 OFFSET ?", pageArgs...)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var raw string
		var r TrafficRecord
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return p, err
		}
		if err = json.Unmarshal([]byte(raw), &r); err != nil {
			rows.Close()
			return p, err
		}
		p.Records = append(p.Records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}

var connectionID = regexp.MustCompile(`\[(\d+) [^\]]+\]`)
var trafficLine = regexp.MustCompile(`(inbound|outbound|endpoint)/[^\[]+\[([^\]]+)\]: (?:inbound|outbound) (packet |multiplex )?connection(?: to ([^\s]+))?`)

type trafficWriter struct {
	mu       sync.Mutex
	buffer   string
	logs     *logBuffer
	store    *trafficStore
	policy   TrafficPolicy
	revision uint64
	session  string
}

func (w *trafficWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer += string(p)
	for {
		i := strings.IndexByte(w.buffer, '\n')
		if i < 0 {
			break
		}
		line := w.buffer[:i]
		w.buffer = w.buffer[i+1:]
		_, _ = w.logs.Write([]byte(line + "\n"))
		w.consume(line)
	}
	return len(p), nil
}
func (w *trafficWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buffer != "" {
		_, _ = w.logs.Write([]byte(w.buffer))
		w.consume(w.buffer)
		w.buffer = ""
	}
}
func (w *trafficWriter) consume(line string) {
	id := connectionID.FindStringSubmatch(line)
	m := trafficLine.FindStringSubmatch(line)
	if len(id) == 0 || len(m) == 0 || (m[4] == "" && m[1] == "inbound") {
		if strings.Contains(line, "connection") && !strings.Contains(line, "connection from ") && (strings.Contains(line, "inbound/") || strings.Contains(line, "outbound/")) && !strings.Contains(line, "ERROR") {
			if w.store.db != nil {
				_, err := w.store.db.Exec("INSERT INTO traffic_diagnostics VALUES('unparsed',1) ON CONFLICT(key) DO UPDATE SET count=count+1")
				if err != nil {
					w.store.mu.Lock()
					w.store.err = err.Error()
					w.store.mu.Unlock()
				}
			}
		}
		return
	}
	key := id[1]
	var r TrafficRecord
	var raw string
	exists := false
	if w.store.db != nil {
		if err := w.store.db.QueryRow("SELECT record FROM traffic WHERE id=?", w.session+":"+key).Scan(&raw); err == nil {
			exists = json.Unmarshal([]byte(raw), &r) == nil
		}
	}
	if !exists {
		if m[4] == "" {
			if w.store.db != nil {
				_, err := w.store.db.Exec("INSERT INTO traffic_diagnostics VALUES('unparsed',1) ON CONFLICT(key) DO UPDATE SET count=count+1")
				if err != nil {
					w.store.mu.Lock()
					w.store.err = err.Error()
					w.store.mu.Unlock()
				}
			}
			return
		}
		host, _, err := net.SplitHostPort(m[4])
		if err != nil {
			host = m[4]
		}
		r = TrafficRecord{ID: w.session + ":" + key, Time: time.Now().UTC().Format(trafficTimeFormat), Revision: w.revision, Domain: strings.ToLower(strings.TrimSuffix(host, ".")), Destination: m[4], Network: "TCP", Verdict: "unknown"}
		if m[3] == "packet " {
			r.Network = "UDP"
		}
		r.Provider, r.Expected = w.expected(r.Domain)
	}
	if m[1] == "inbound" {
		r.Inbound = m[2]
	} else {
		r.Actual = w.exit(m[2])
		r.Verdict = "unknown"
		if r.Expected != "" && r.Actual != "" && !strings.HasPrefix(r.Actual, "未识别出口") {
			if r.Expected == r.Actual {
				r.Verdict = "match"
			} else {
				r.Verdict = "mismatch"
			}
		}
	}
	w.store.put(r)
}
func (w *trafficWriter) exit(tag string) string {
	switch tag {
	case "direct", "local-direct":
		return "直连"
	case "fallback":
		return "其他域名上游"
	}
	for _, r := range w.policy.Settings.Rules {
		if tag == "rule-"+r.ID+"-out" {
			n := w.policy.Nodes[r.Node]
			return n.Name + " [" + r.Node + "]"
		}
	}
	return "未识别出口 [" + tag + "]"
}
func (w *trafficWriter) expected(host string) (string, string) {
	// Match domains independently of dedicated inbound routing, so wrong-entry
	// traffic remains visible as a mismatch. Overlapping provider rules are
	// ambiguous and must never be reported as verified normal.
	names := []string{}
	targets := []string{}
	for _, r := range w.policy.Settings.Rules {
		if !r.Enabled {
			continue
		}
		for _, d := range r.Domains {
			if host == d || (net.ParseIP(d) == nil && strings.HasSuffix(host, "."+d)) {
				name := r.Name
				if r.Provider != "" {
					name = r.Provider
				}
				names = append(names, name)
				target := "直连"
				if r.Mode == "proxy" {
					target = w.exit("rule-" + r.ID + "-out")
				}
				targets = append(targets, target)
				break
			}
		}
	}
	if len(names) == 0 {
		for name, domains := range w.policy.Providers {
			for _, d := range domains {
				if host == d || (net.ParseIP(d) == nil && strings.HasSuffix(host, "."+d)) {
					names = append(names, name)
					break
				}
			}
		}
		if len(names) > 0 {
			sort.Strings(names)
			return strings.Join(names, " / ") + "（未指定出口）", ""
		}
		return "未归属域名", ""
	}
	if len(names) > 1 {
		return strings.Join(names, " / ") + "（域名重叠）", ""
	}
	// Loopback destinations are intentionally exempt from provider egress.
	if host == "localhost" || net.ParseIP(host).IsLoopback() {
		return names[0], "直连"
	}
	return names[0], targets[0]
}
