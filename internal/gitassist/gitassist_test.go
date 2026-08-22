package gitassist

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/settings"
)

// gitRun 在 dir 内执行 git 命令，失败即 Fatal（测试夹具自身不允许失败）。
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("fixture git %v: %v: %s", args, err, stderr.String())
	}
	return stdout.String()
}

// newGitRepo 建一个带身份配置的真实临时 git 仓库。
func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "Test User")
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// newTestServices 构造落在同一临时目录的 config + settings 服务。
func newTestServices(t *testing.T) (*config.ConfigService, *settings.Service) {
	t.Helper()
	dir := t.TempDir()
	cfgSvc := config.NewConfigService(dir)
	if err := cfgSvc.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	st := settings.NewService(dir)
	if err := st.Load(); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	return cfgSvc, st
}

// ============================================================================
// RepoInfo
// ============================================================================

func TestRepoInfo_NotGitDir(t *testing.T) {
	svc := New(nil, nil, nil)
	plain := t.TempDir() // 空目录，非 git 仓库

	info, err := svc.RepoInfo(plain)
	if err != nil {
		t.Fatalf("RepoInfo on non-git dir should not error, got: %v", err)
	}
	if info.IsGitRepo {
		t.Fatalf("IsGitRepo = true on plain dir, want false")
	}
}

func TestRepoInfo_GitRepoCounts(t *testing.T) {
	svc := New(nil, nil, nil)
	dir := newGitRepo(t)
	writeFile(t, dir, "committed.txt", "v1\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")

	// staged：新文件已 add；unstaged：已提交文件再修改；untracked：新文件未 add。
	writeFile(t, dir, "staged.txt", "s\n")
	gitRun(t, dir, "add", "staged.txt")
	writeFile(t, dir, "committed.txt", "v2\n")
	writeFile(t, dir, "untracked.txt", "u\n")

	info, err := svc.RepoInfo(dir)
	if err != nil {
		t.Fatalf("RepoInfo: %v", err)
	}
	if !info.IsGitRepo {
		t.Fatal("IsGitRepo = false, want true")
	}
	if info.Branch != "main" {
		t.Fatalf("Branch = %q, want main", info.Branch)
	}
	if info.Staged != 1 {
		t.Fatalf("Staged = %d, want 1", info.Staged)
	}
	if info.Unstaged != 1 {
		t.Fatalf("Unstaged = %d, want 1", info.Unstaged)
	}
	if info.Untracked != 1 {
		t.Fatalf("Untracked = %d, want 1", info.Untracked)
	}
	// 无远端、无上游：均为零值而非报错。
	if info.Upstream != "" || info.RemoteURL != "" {
		t.Fatalf("Upstream/RemoteURL = %q/%q, want empty", info.Upstream, info.RemoteURL)
	}
}

// ============================================================================
// ListBranches / SwitchBranch
// ============================================================================

func TestListBranches(t *testing.T) {
	svc := New(nil, nil, nil)
	dir := newGitRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	gitRun(t, dir, "branch", "feature-x")

	branches, err := svc.ListBranches(dir)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	byName := map[string]bool{}
	currents := 0
	for _, b := range branches {
		byName[b.Name] = true
		if b.Current {
			currents++
		}
	}
	if !byName["main"] || !byName["feature-x"] {
		t.Fatalf("branches = %v, want main + feature-x", branches)
	}
	if currents != 1 {
		t.Fatalf("current branch count = %d, want exactly 1", currents)
	}
}

func TestSwitchBranch_RejectsIllegalNames(t *testing.T) {
	svc := New(nil, nil, nil)
	dir := newGitRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	for _, bad := range []string{"- injected", "has space", "a~b", "a^b", "a:b", "a..b", "a@{b", "ends.lock", "ends/", ""} {
		if err := svc.SwitchBranch(dir, bad); err == nil {
			t.Fatalf("SwitchBranch(%q) should be rejected", bad)
		}
	}
	// 非法名在执行前被拒：分支列表应仍只有 main，且仍在 main 上。
	branches, err := svc.ListBranches(dir)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 1 || branches[0].Name != "main" || !branches[0].Current {
		t.Fatalf("illegal switch mutated branch list: %+v", branches)
	}
}

func TestSwitchBranch_Switches(t *testing.T) {
	svc := New(nil, nil, nil)
	dir := newGitRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	gitRun(t, dir, "branch", "feature-x")

	if err := svc.SwitchBranch(dir, "feature-x"); err != nil {
		t.Fatalf("SwitchBranch: %v", err)
	}
	info, err := svc.RepoInfo(dir)
	if err != nil {
		t.Fatalf("RepoInfo: %v", err)
	}
	if info.Branch != "feature-x" {
		t.Fatalf("Branch after switch = %q, want feature-x", info.Branch)
	}
}

// ============================================================================
// CommitAll / CommitStaged
// ============================================================================

func TestCommitAll_MultiLineMessage(t *testing.T) {
	svc := New(nil, nil, nil)
	dir := newGitRepo(t)
	writeFile(t, dir, "base.txt", "v1\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")

	// 一个修改 + 一个 untracked：CommitAll 都要收进去。
	writeFile(t, dir, "base.txt", "v2\n")
	writeFile(t, dir, "new.txt", "n\n")
	message := "feat(git): 新增提交总结\n\n- description: 补充 AI 提交信息生成\n- 版本同步: 1.5.162 -> 1.5.163"
	if err := svc.CommitAll(dir, message); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}

	got := gitRun(t, dir, "log", "-1", "--pretty=%B")
	if strings.TrimSpace(got) != strings.TrimSpace(message) {
		t.Fatalf("commit message = %q, want %q", got, message)
	}
	// 两个文件都进入该提交。
	names := gitRun(t, dir, "show", "-1", "--name-only", "--pretty=format:")
	for _, want := range []string{"base.txt", "new.txt"} {
		if !strings.Contains(names, want) {
			t.Fatalf("commit should contain %s; got %q", want, names)
		}
	}
	// 工作区应干净（无 staged/unstaged/untracked）。
	info, err := svc.RepoInfo(dir)
	if err != nil {
		t.Fatalf("RepoInfo: %v", err)
	}
	if info.Staged != 0 || info.Unstaged != 0 || info.Untracked != 0 {
		t.Fatalf("working tree not clean after CommitAll: %+v", info)
	}
}

func TestCommitAll_EmptyMessage(t *testing.T) {
	svc := New(nil, nil, nil)
	dir := newGitRepo(t)
	if err := svc.CommitAll(dir, "   \n  "); err == nil {
		t.Fatal("CommitAll with blank message should error")
	}
}

func TestCommitStaged_OnlyStaged(t *testing.T) {
	svc := New(nil, nil, nil)
	dir := newGitRepo(t)
	writeFile(t, dir, "base.txt", "v1\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")

	// 只暂存 f1，f2 保持 untracked。
	writeFile(t, dir, "f1.txt", "1\n")
	writeFile(t, dir, "f2.txt", "2\n")
	gitRun(t, dir, "add", "f1.txt")

	if err := svc.CommitStaged(dir, "chore: only staged"); err != nil {
		t.Fatalf("CommitStaged: %v", err)
	}
	names := gitRun(t, dir, "show", "-1", "--name-only", "--pretty=format:")
	if strings.TrimSpace(names) != "f1.txt" {
		t.Fatalf("last commit files = %q, want only f1.txt", names)
	}
	info, err := svc.RepoInfo(dir)
	if err != nil {
		t.Fatalf("RepoInfo: %v", err)
	}
	if info.Untracked != 1 {
		t.Fatalf("f2.txt should remain untracked, got %+v", info)
	}
}

// ============================================================================
// SummarizeDiff
// ============================================================================

// summarizeFixture 构造带一次变更的仓库 + config/settings 服务组合。
func summarizeFixture(t *testing.T) (string, *config.ConfigService, *settings.Service) {
	t.Helper()
	dir := newGitRepo(t)
	writeFile(t, dir, "docs.md", "v1\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "docs(amagi): 初始化")
	writeFile(t, dir, "docs.md", "v2 更多内容\n")
	writeFile(t, dir, "new.md", "新文件\n")
	cfgSvc, st := newTestServices(t)
	return dir, cfgSvc, st
}

func TestSummarizeDiff_NoPresetSelected(t *testing.T) {
	dir, cfgSvc, st := summarizeFixture(t)
	svc := New(cfgSvc, st, nil)

	_, err := svc.SummarizeDiff(dir)
	if err == nil || !strings.Contains(err.Error(), "请先在设置页选择提交总结模型") {
		t.Fatalf("want no-preset error, got: %v", err)
	}
}

func TestSummarizeDiff_PresetMissing(t *testing.T) {
	dir, cfgSvc, st := summarizeFixture(t)
	if err := cfgSvc.SaveProvider("p", config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://unused.example.com/v1"},
		DefaultModel: "provider-default",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := st.SetCommitSummaryPreset("p/missing"); err != nil {
		t.Fatalf("SetCommitSummaryPreset: %v", err)
	}
	svc := New(cfgSvc, st, nil)

	_, err := svc.SummarizeDiff(dir)
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("want preset-missing error, got: %v", err)
	}
}

func TestSummarizeDiff_AnthropicOnlyProvider(t *testing.T) {
	dir, cfgSvc, st := summarizeFixture(t)
	if err := cfgSvc.SaveProvider("ap", config.Provider{
		Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://unused.example.com"},
		DefaultModel: "claude-x",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := cfgSvc.SaveTerminalPreset("anthropic", "ap/sum", config.TerminalPreset{
		Name: "sum", Provider: "ap", Model: "claude-x",
	}); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}
	if err := st.SetCommitSummaryPreset("ap/sum"); err != nil {
		t.Fatalf("SetCommitSummaryPreset: %v", err)
	}
	svc := New(cfgSvc, st, nil)

	_, err := svc.SummarizeDiff(dir)
	if err == nil || !strings.Contains(err.Error(), "当前仅支持 OpenAI 兼容") {
		t.Fatalf("want anthropic-only error, got: %v", err)
	}
}

func TestSummarizeDiff_HappyPath(t *testing.T) {
	dir, cfgSvc, st := summarizeFixture(t)

	var gotReq struct {
		Method string
		Path   string
		Auth   string
		Body   map[string]any
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotReq.Method, gotReq.Path, gotReq.Auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		_ = json.Unmarshal(body, &gotReq.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "docs(amagi): 更新文档\n\n- description: 补充更多内容\n- 新增: new.md 文件"}},
			},
		})
	}))
	t.Cleanup(server.Close)

	if err := cfgSvc.SaveProvider("p", config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: server.URL + "/v1"},
		DefaultModel: "provider-default",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := cfgSvc.SaveTerminalPreset("openai", "p/sum", config.TerminalPreset{
		Name:     "sum",
		Provider: "p",
		Model:    "preset-model",
		Parameters: config.Parameters{
			ReasoningEffort: "high",
			MaxTokens:       2048,
			Temperature:     0.3,
			TopP:            0.8,
		},
	}); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}
	if err := st.SetCommitSummaryPreset("p/sum"); err != nil {
		t.Fatalf("SetCommitSummaryPreset: %v", err)
	}
	svc := New(cfgSvc, st, func(provider string) string {
		if provider != "p" {
			t.Errorf("apiKeyResolver provider = %q, want p", provider)
		}
		return "sk-test-key"
	})

	got, err := svc.SummarizeDiff(dir)
	if err != nil {
		t.Fatalf("SummarizeDiff: %v", err)
	}
	const want = "docs(amagi): 更新文档\n\n- description: 补充更多内容\n- 新增: new.md 文件"
	if got != want {
		t.Fatalf("SummarizeDiff = %q, want %q", got, want)
	}

	// 请求契约断言：POST {base}/chat/completions + Bearer key + 参数透传。
	if gotReq.Method != http.MethodPost || gotReq.Path != "/v1/chat/completions" {
		t.Fatalf("request = %s %s, want POST /v1/chat/completions", gotReq.Method, gotReq.Path)
	}
	if gotReq.Auth != "Bearer sk-test-key" {
		t.Fatalf("Authorization = %q", gotReq.Auth)
	}
	if gotReq.Body["model"] != "preset-model" {
		t.Fatalf("model = %v, want preset-model", gotReq.Body["model"])
	}
	messages, _ := gotReq.Body["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2 (system+user)", len(messages))
	}
	// user prompt 应包含变更材料与风格参考。
	userMsg, _ := messages[1].(map[string]any)
	userContent, _ := userMsg["content"].(string)
	for _, want := range []string{"【最近提交风格】", "【本次变更文件清单】", "new.md", "【完整 diff】"} {
		if !strings.Contains(userContent, want) {
			t.Fatalf("user prompt missing %q", want)
		}
	}
	if gotReq.Body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v, want high", gotReq.Body["reasoning_effort"])
	}
	if gotReq.Body["max_tokens"] != float64(2048) {
		t.Fatalf("max_tokens = %v, want 2048", gotReq.Body["max_tokens"])
	}
	if gotReq.Body["temperature"] != 0.3 {
		t.Fatalf("temperature = %v, want 0.3", gotReq.Body["temperature"])
	}
	if gotReq.Body["top_p"] != 0.8 {
		t.Fatalf("top_p = %v, want 0.8", gotReq.Body["top_p"])
	}
}

func TestSummarizeDiff_HTTPErrorSurfacesMessage(t *testing.T) {
	dir, cfgSvc, st := summarizeFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "Invalid API key"},
		})
	}))
	t.Cleanup(server.Close)

	if err := cfgSvc.SaveProvider("p", config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: server.URL},
		DefaultModel: "provider-default",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := cfgSvc.SaveTerminalPreset("openai", "p/sum", config.TerminalPreset{
		Name: "sum", Provider: "p", Model: "preset-model",
	}); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}
	if err := st.SetCommitSummaryPreset("p/sum"); err != nil {
		t.Fatalf("SetCommitSummaryPreset: %v", err)
	}
	svc := New(cfgSvc, st, func(string) string { return "sk-bad" })

	_, err := svc.SummarizeDiff(dir)
	if err == nil {
		t.Fatal("want HTTP error")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Invalid API key") {
		t.Fatalf("error should contain status code and API message, got: %v", err)
	}
}

func TestSummarizeDiff_MissingAPIKey(t *testing.T) {
	dir, cfgSvc, st := summarizeFixture(t)
	if err := cfgSvc.SaveProvider("p", config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://unused.example.com/v1"},
		DefaultModel: "provider-default",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := cfgSvc.SaveTerminalPreset("openai", "p/sum", config.TerminalPreset{
		Name: "sum", Provider: "p", Model: "preset-model",
	}); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}
	if err := st.SetCommitSummaryPreset("p/sum"); err != nil {
		t.Fatalf("SetCommitSummaryPreset: %v", err)
	}
	svc := New(cfgSvc, st, func(string) string { return "" })

	_, err := svc.SummarizeDiff(dir)
	if err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Fatalf("want missing-api-key error, got: %v", err)
	}
}

// ============================================================================
// truncateBytes
// ============================================================================

func TestTruncateBytes_UTF8Boundary(t *testing.T) {
	// 中文占 3 字节：截断必须落在完整 rune 边界上。
	s := strings.Repeat("中", 100) // 300 字节
	got := truncateBytes(s, 100)
	if !strings.HasSuffix(got, truncationMarker) {
		t.Fatalf("truncated string should end with marker: %q", got[len(got)-20:])
	}
	body := strings.TrimSuffix(got, truncationMarker)
	if len(body)%3 != 0 {
		t.Fatalf("cut at non-rune boundary: body len = %d", len(body))
	}
	if s2 := truncateBytes(s, 1000); s2 != s {
		t.Fatalf("under-limit string should be unchanged")
	}
}
