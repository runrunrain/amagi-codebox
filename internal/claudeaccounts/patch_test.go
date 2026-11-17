package claudeaccounts

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// 字段 patch 组（design §10.1 矩阵第 2 行）：临时 HOME 中只变指定键，
// 非拥有字段语义和原字节片段保留。

func newPatchStore(t *testing.T) (*FileCredentialStore, string, string) {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	cj := filepath.Join(home, ".claude.json")
	pa := filepath.Join(home, ".pi", "agent", "auth.json")
	if err := os.WriteFile(cj, syntheticClaudeJSON(syntheticUUIDA, "user-a", "a@example.com"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pa, syntheticPiAuth("pi-a-1", futureExpiresMS(time.Now())), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewFileCredentialStore(FileCredentialPaths{ClaudeJSONPath: cj, PiAuthPath: pa}, nil), cj, pa
}

// TestPatchClaudeIdentityOnlyManagedKeys：只变 oauthAccount 与 userID。
func TestPatchClaudeIdentityOnlyManagedKeys(t *testing.T) {
	store, cj, _ := newPatchStore(t)
	before := readJSON(t, cj)
	newOauth := `{"accountUuid":"` + syntheticUUIDB + `","emailAddress":"b@example.com"}`
	_, err := store.PatchClaudeIdentity(context.Background(), ClaudeIdentityFields{
		OauthAccount: []byte(newOauth), UserID: "user-b",
	})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	after := readJSON(t, cj)
	if got := gjson.GetBytes(after, "oauthAccount.accountUuid").String(); got != syntheticUUIDB {
		t.Fatalf("oauthAccount.accountUuid 应为 B，实际 %s", got)
	}
	if got := gjson.GetBytes(after, "userID").String(); got != "user-b" {
		t.Fatalf("userID 应为 user-b，实际 %s", got)
	}
	// 非拥有字段语义保留：projects / firstStartTime / isQualifiedForDataSharing。
	for _, key := range []string{"projects", "firstStartTime", "isQualifiedForDataSharing"} {
		want := gjson.GetBytes(before, key).Raw
		got := gjson.GetBytes(after, key).Raw
		if want != got {
			t.Fatalf("非拥有字段 %s 被改动：%s → %s", key, want, got)
		}
	}
	// 顶层键集合不变。
	if !sameStringSet(topKeyNames(before), topKeyNames(after)) {
		t.Fatalf("顶层键集合变化：%v → %v", topKeyNames(before), topKeyNames(after))
	}
}

// TestPatchPiAnthropicOnlyManagedKey：只变 anthropic 键，其他 provider 保留。
func TestPatchPiAnthropicOnlyManagedKey(t *testing.T) {
	store, _, pa := newPatchStore(t)
	before := readJSON(t, pa)
	newEntry := `{"type":"oauth","access":"pi-b-9","refresh":"pir-b-9","expires":` + strconv.FormatInt(futureExpiresMS(time.Now()), 10) + `}`
	_, err := store.PatchPiAnthropic(context.Background(), []byte(newEntry))
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	after := readJSON(t, pa)
	if got := gjson.GetBytes(after, "anthropic.access").String(); got != "pi-b-9" {
		t.Fatalf("anthropic.access 应为 pi-b-9，实际 %s", got)
	}
	for _, key := range []string{"openai", "google"} {
		if gjson.GetBytes(before, key).Raw != gjson.GetBytes(after, key).Raw {
			t.Fatalf("其他 provider %s 被改动", key)
		}
	}
}

// TestPatchPreservesExternalOtherFieldUpdate：他人更新非拥有字段后 patch
// 保留其新值（design §10.1 配对负向：他人更新非拥有字段）。
func TestPatchPreservesExternalOtherFieldUpdate(t *testing.T) {
	store, cj, _ := newPatchStore(t)
	// 外部（如 Claude Code 自身）在 patch 前更新了 projects。
	external := syntheticClaudeJSON(syntheticUUIDA, "user-a", "a@example.com")
	external, _ = sjsonSetRawBytes(external, "projects", []byte(`{"/tmp/other":{"allowedTools":["Bash"]}}`))
	if err := os.WriteFile(cj, external, 0o644); err != nil {
		t.Fatal(err)
	}
	newOauth := `{"accountUuid":"` + syntheticUUIDB + `"}`
	if _, err := store.PatchClaudeIdentity(context.Background(), ClaudeIdentityFields{OauthAccount: []byte(newOauth), UserID: "user-b"}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	after := readJSON(t, cj)
	if got := gjson.GetBytes(after, "projects./tmp/other.allowedTools.0").String(); got != "Bash" {
		t.Fatalf("外部更新的非拥有字段新值未保留：%s", got)
	}
}

// TestReadStableRejectsSymlink：拒绝符号链接。
func TestReadStableRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	if err := os.WriteFile(real, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("平台不支持符号链接")
	}
	_, _, err := readStableFile(link)
	assertReason(t, err, ReasonNeedsReconcile)
}

// TestReadStableRejectsDirectory：拒绝目录替代文件（F 测试有效性修复：此前
// 用尚不存在的子路径，实际测的是 missing，不是目录）。先真实创建目录。
func TestReadStableRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if fi, err := os.Stat(subdir); err != nil || !fi.IsDir() {
		t.Fatalf("前置失败：%v isDir=%v", err, fi != nil && fi.IsDir())
	}
	_, _, err := readStableFile(subdir)
	assertReason(t, err, ReasonNeedsReconcile)
}

// TestReadStableRejectsMissing：文件缺失。
func TestReadStableRejectsMissing(t *testing.T) {
	_, _, err := readStableFile(filepath.Join(t.TempDir(), "missing.json"))
	assertReason(t, err, ReasonNeedsReconcile)
}

// TestAdapterRejectsBadJSON：坏 JSON 在适配器层被拒。
func TestAdapterRejectsBadJSON(t *testing.T) {
	store, cj, pa := newPatchStore(t)
	if err := os.WriteFile(cj, []byte(`{"oauthAccount": not-json}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := store.ReadClaudeJSON(context.Background())
	assertReason(t, err, ReasonUnsupportedCredentialSchema)

	if err := os.WriteFile(pa, []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = store.ReadPiAuth(context.Background())
	assertReason(t, err, ReasonUnsupportedCredentialSchema)
}

// TestReadRejectsMissingManagedFields：受管字段缺失 → 未登录/未知 schema。
func TestReadRejectsMissingManagedFields(t *testing.T) {
	store, cj, pa := newPatchStore(t)
	if err := os.WriteFile(cj, []byte(`{"other":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := store.ReadClaudeJSON(context.Background())
	assertReason(t, err, ReasonNeedsLogin)

	if err := os.WriteFile(pa, []byte(`{"openai":{"type":"api_key"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = store.ReadPiAuth(context.Background())
	assertReason(t, err, ReasonNeedsLogin)
}

// TestAtomicWriteFailureSteps：按精确步骤注入故障，断言结果事实
// （design §10.1：注入每个 write/fsync 失败；§4.4：记录 renamed 与
// sync 失败的区别）。
func TestAtomicWriteFailureSteps(t *testing.T) {
	cases := []struct {
		step    string
		renamed bool
	}{
		{"create-temp", false},
		{"sync-temp", false},
		{"close-temp", false},
		{"rename", false},
		{"sync-directory", true},
	}
	for _, tc := range cases {
		t.Run(tc.step, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "auth.json")
			if err := os.WriteFile(path, []byte(`{"before":1}`), 0o644); err != nil {
				t.Fatal(err)
			}
			result, err := writeAtomicFile(path, []byte(`{"after":2}`), newFailOps(tc.step, 1))
			if err == nil {
				t.Fatal("期望注入失败")
			}
			if result.Renamed != tc.renamed {
				t.Fatalf("Renamed 期望 %v，实际 %v（step=%s）", tc.renamed, result.Renamed, result.Step)
			}
			if result.Step != tc.step {
				t.Fatalf("Step 期望 %s，实际 %s", tc.step, result.Step)
			}
			if tc.renamed {
				// rename 已完成：内容已替换（恢复不得假定失败表示未改文件）。
				if got := string(readJSON(t, path)); got != `{"after":2}` {
					t.Fatalf("rename 后内容应已替换，实际 %s", got)
				}
			} else {
				if got := string(readJSON(t, path)); got != `{"before":1}` {
					t.Fatalf("rename 前失败不应改变内容，实际 %s", got)
				}
			}
			// 失败后无临时文件残留。
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".auth.json.claudeaccounts-") {
					t.Fatalf("临时文件残留：%s", e.Name())
				}
			}
		})
	}
}

// TestAtomicWriteFileModes：临时文件 0600、目标保持原 mode、不对已存在
// 目录 chmod（design §4.4：勿对 HOME chmod）。
func TestAtomicWriteFileModes(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := writeAtomicFile(path, []byte(`{"v":1}`), osWriteFileOps{}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("目标文件 mode 应保持 0640，实际 %v", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o755 {
		t.Fatalf("已存在目录不应被 chmod，实际 %v", dirInfo.Mode().Perm())
	}
	// 无临时文件残留。
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("目录应只含目标文件，实际 %d 项", len(entries))
	}
}

// TestPatchKeychainItemPreservesSiblings：Keychain 项未知顶层兄弟字段保留
// （design §3.1：不跨账号复制未知字段；缺投影字段拒绝）。
func TestPatchKeychainItemPreservesSiblings(t *testing.T) {
	item := syntheticKeychainItem("cc-a-1")
	next, err := PatchClaudeKeychainItemBytes(item, []byte(`{"accessToken":"cc-b-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(next, "claudeAiOauth.accessToken").String(); got != "cc-b-1" {
		t.Fatalf("投影应已替换，实际 %s", got)
	}
	if !gjson.GetBytes(next, "otherSibling").Exists() {
		t.Fatal("未知兄弟字段丢失")
	}
	// 缺投影字段 → UNSUPPORTED_CREDENTIAL_SCHEMA。
	_, err = PatchClaudeKeychainItemBytes([]byte(`{"unrelated":true}`), []byte(`{}`))
	assertReason(t, err, ReasonUnsupportedCredentialSchema)
}
