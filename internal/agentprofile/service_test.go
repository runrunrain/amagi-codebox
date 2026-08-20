package agentprofile

// agent 配置档服务的回归测试：capture→apply 往返、备份保留一份、
// 非法 JSON（pi 侧）/非法或非映射根 YAML（omp 侧）拒绝、omp 缺失时的
// 降级、delete 与名字校验。全部走临时目录（HOME + PI_CODING_AGENT_DIR
// 重定向），不碰真实文件。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// redirectEnv 把 HOME（及 Windows 的 USERPROFILE）与 PI_CODING_AGENT_DIR
// 重定向到临时目录，使 pi/omp/存储三处路径全部落在测试沙箱内。
// PI_CODING_AGENT_DIR 置空串 => TrimSpace 后为空，走 HOME 回退分支。
func redirectEnv(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir 在 Windows 读 USERPROFILE
	t.Setenv("PI_CODING_AGENT_DIR", "")
}

// writeFixture 写入测试 fixture 文件（自动建目录）。
func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const companyPi = `{"profile":"tiered","agents":{"coder":{"model":"company-model"}}}`

// companyOmp 是真实形状的 omp config.yml（modelRoles 映射等）。
const companyOmp = `modelRoles:
  main: company-main-model
  compact: company-compact-model
task:
  agentModelOverrides:
    coder: company-main-model
theme: dark
`
const homePi = `{"profile":"home","agents":{"coder":{"model":"home-model"}}}`

// TestCaptureApplyRoundTrip 覆盖：capture 快照 → 修改 live → apply 恢复，
// pi/omp 两侧均为字节级往返，lastApplied 更新。
func TestCaptureApplyRoundTrip(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	piPath := filepath.Join(home, ".pi", "agent", "amagi.json")
	ompPath := filepath.Join(home, ".omp", "agent", "config.yml")
	writeFixture(t, piPath, companyPi)
	writeFixture(t, ompPath, companyOmp)

	s := NewService()
	if err := s.CaptureAgentProfile("company"); err != nil {
		t.Fatalf("CaptureAgentProfile: %v", err)
	}

	// 预览：单档 JSON 含两侧内容（内嵌内容被转义，解析后比较）
	got, err := s.GetAgentProfile("company")
	if err != nil {
		t.Fatalf("GetAgentProfile: %v", err)
	}
	var preview struct {
		Pi  string `json:"pi"`
		Omp string `json:"omp"`
	}
	if err := json.Unmarshal([]byte(got), &preview); err != nil {
		t.Fatalf("parse preview: %v\n%s", err, got)
	}
	if preview.Pi != companyPi || preview.Omp != companyOmp {
		t.Fatalf("profile preview = pi %q / omp %q", preview.Pi, preview.Omp)
	}

	// 修改 live 后应用，应恢复为快照内容
	writeFixture(t, piPath, homePi)
	writeFixture(t, ompPath, "modelRoles:\n  main: omp-edited\n")
	if err := s.ApplyAgentProfile("company"); err != nil {
		t.Fatalf("ApplyAgentProfile: %v", err)
	}
	for path, want := range map[string]string{piPath: companyPi, ompPath: companyOmp} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read live %s: %v", path, err)
		}
		if string(data) != want {
			t.Fatalf("live %s = %q, want %q", path, string(data), want)
		}
	}

	// lastApplied 更新
	raw, err := os.ReadFile(filepath.Join(home, ".amagi-codebox", "agent-profiles.json"))
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	var store struct {
		Version     int    `json:"version"`
		LastApplied string `json:"lastApplied"`
	}
	if err := json.Unmarshal(raw, &store); err != nil {
		t.Fatalf("parse store: %v", err)
	}
	if store.Version != 1 || store.LastApplied != "company" {
		t.Fatalf("store version/lastApplied = %d/%q", store.Version, store.LastApplied)
	}
}

// TestListAgentProfilesShape 覆盖：List 返回存储全文 JSON；缺失时返回空骨架。
func TestListAgentProfilesShape(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	s := NewService()

	// 文件缺失：空骨架
	out, err := s.ListAgentProfiles()
	if err != nil {
		t.Fatalf("ListAgentProfiles empty: %v", err)
	}
	var empty struct {
		Version     int            `json:"version"`
		Profiles    map[string]any `json:"profiles"`
		LastApplied string         `json:"lastApplied"`
	}
	if err := json.Unmarshal([]byte(out), &empty); err != nil {
		t.Fatalf("parse empty list: %v\n%s", err, out)
	}
	if empty.Version != 1 || empty.Profiles == nil || len(empty.Profiles) != 0 || empty.LastApplied != "" {
		t.Fatalf("empty store skeleton unexpected:\n%s", out)
	}

	writeFixture(t, filepath.Join(home, ".pi", "agent", "amagi.json"), companyPi)
	if err := s.CaptureAgentProfile("company"); err != nil {
		t.Fatalf("CaptureAgentProfile: %v", err)
	}
	out, err = s.ListAgentProfiles()
	if err != nil {
		t.Fatalf("ListAgentProfiles: %v", err)
	}
	var listed struct {
		Profiles map[string]struct {
			Pi        string `json:"pi"`
			Omp       string `json:"omp"`
			UpdatedAt int64  `json:"updatedAt"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("parse list: %v\n%s", err, out)
	}
	if listed.Profiles["company"].Pi != companyPi {
		t.Fatalf("captured pi content mismatch:\n%s", out)
	}
}

// TestApplyCreatesBackup 覆盖：覆盖已有 live 文件前生成 .bak-<epochms> 备份，
// 且仅保留一份（新备份覆盖旧）。
func TestApplyCreatesBackup(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	piPath := filepath.Join(home, ".pi", "agent", "amagi.json")
	ompPath := filepath.Join(home, ".omp", "agent", "config.yml")
	writeFixture(t, piPath, companyPi)
	writeFixture(t, ompPath, companyOmp)

	s := NewService()
	if err := s.CaptureAgentProfile("company"); err != nil {
		t.Fatalf("CaptureAgentProfile: %v", err)
	}

	// 第一次 apply：live 已被改写，旧内容应进备份
	writeFixture(t, piPath, homePi)
	writeFixture(t, ompPath, "modelRoles:\n  main: omp-v2\n")
	if err := s.ApplyAgentProfile("company"); err != nil {
		t.Fatalf("ApplyAgentProfile #1: %v", err)
	}
	assertSingleBackup(t, piPath, homePi)
	assertSingleBackup(t, ompPath, "modelRoles:\n  main: omp-v2\n")

	// 第二次 apply：再换 live，新备份应替换旧备份（仍只有一份）
	writeFixture(t, piPath, `{"profile":"v3"}`)
	if err := s.ApplyAgentProfile("company"); err != nil {
		t.Fatalf("ApplyAgentProfile #2: %v", err)
	}
	assertSingleBackup(t, piPath, `{"profile":"v3"}`)
}

// assertSingleBackup 断言 <path>.bak-* 恰好一份且内容等于 want。
func assertSingleBackup(t *testing.T, path, want string) {
	t.Helper()
	matches, err := filepath.Glob(path + ".bak-*")
	if err != nil {
		t.Fatalf("glob backups for %s: %v", path, err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 backup for %s, got %v", path, matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read backup %s: %v", matches[0], err)
	}
	if string(data) != want {
		t.Fatalf("backup %s = %q, want %q", matches[0], string(data), want)
	}
}

// TestApplyNoBackupWhenLiveMissing 覆盖：live 文件不存在时 apply 不产生备份，
// 直接写入快照内容（pi 侧目录不存在则创建）。
func TestApplyNoBackupWhenLiveMissing(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	// 仅建 pi live（用于 capture），omp 目录完全不存在
	piPath := filepath.Join(home, ".pi", "agent", "amagi.json")
	writeFixture(t, piPath, companyPi)

	s := NewService()
	if err := s.CaptureAgentProfile("company"); err != nil {
		t.Fatalf("CaptureAgentProfile: %v", err)
	}

	// 删掉 pi live 再 apply：无备份，直接落档
	if err := os.Remove(piPath); err != nil {
		t.Fatalf("remove pi live: %v", err)
	}
	if err := s.ApplyAgentProfile("company"); err != nil {
		t.Fatalf("ApplyAgentProfile: %v", err)
	}
	matches, _ := filepath.Glob(piPath + ".bak-*")
	if len(matches) != 0 {
		t.Fatalf("unexpected backups when live missing: %v", matches)
	}
	data, err := os.ReadFile(piPath)
	if err != nil {
		t.Fatalf("read restored pi live: %v", err)
	}
	if string(data) != companyPi {
		t.Fatalf("restored = %q, want %q", string(data), companyPi)
	}
}

// TestInvalidJSONRejected 覆盖：Save 拒绝非 JSON 的 pi 内容；存储被手工污染
// 后 Apply 同样拒绝且不动 live 文件（omp 侧 YAML 校验见
// TestInvalidOmpYAMLRejected）。
func TestInvalidJSONRejected(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	piPath := filepath.Join(home, ".pi", "agent", "amagi.json")
	writeFixture(t, piPath, companyPi)

	s := NewService()
	if err := s.SaveAgentProfile("bad", "{not json", ""); err == nil {
		t.Fatal("SaveAgentProfile should reject invalid pi JSON")
	}

	// 手工写入含非法 JSON 的存储，Apply 必须拒绝且不写 live
	store := `{"version":1,"profiles":{"bad":{"pi":"{broken","omp":"","updatedAt":1}},"lastApplied":""}`
	writeFixture(t, filepath.Join(home, ".amagi-codebox", "agent-profiles.json"), store)
	if err := s.ApplyAgentProfile("bad"); err == nil {
		t.Fatal("ApplyAgentProfile should reject invalid stored JSON")
	}
	data, err := os.ReadFile(piPath)
	if err != nil {
		t.Fatalf("read pi live: %v", err)
	}
	if string(data) != companyPi {
		t.Fatalf("live must be untouched on reject, got %q", string(data))
	}
	matches, _ := filepath.Glob(piPath + ".bak-*")
	if len(matches) != 0 {
		t.Fatalf("unexpected backup on reject: %v", matches)
	}
}

// TestInvalidOmpYAMLRejected 覆盖：omp 侧内容必须是合法 YAML 且根为映射——
// 非法 YAML、纯标量、列表根均被 Save 拒绝；存储被手工污染后 Apply 同样
// 拒绝且不动 live 文件。
func TestInvalidOmpYAMLRejected(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	piPath := filepath.Join(home, ".pi", "agent", "amagi.json")
	writeFixture(t, piPath, companyPi)
	ompPath := filepath.Join(home, ".omp", "agent", "config.yml")
	writeFixture(t, ompPath, companyOmp)

	s := NewService()
	// 非法 YAML（未闭合的 flow 序列）
	if err := s.SaveAgentProfile("bad", "", "modelRoles:\n  main: [unclosed"); err == nil {
		t.Fatal("SaveAgentProfile should reject invalid omp YAML")
	}
	// 合法 YAML 但根为纯标量
	if err := s.SaveAgentProfile("bad", "", "just-a-scalar"); err == nil {
		t.Fatal("SaveAgentProfile should reject scalar-root omp YAML")
	}
	// 合法 YAML 但根为列表
	if err := s.SaveAgentProfile("bad", "", "- one\n- two\n"); err == nil {
		t.Fatal("SaveAgentProfile should reject list-root omp YAML")
	}

	// 手工写入 omp 为非法 YAML / 标量根的存储，Apply 必须拒绝且不写 live
	badStores := []string{
		`{"version":1,"profiles":{"bad":{"pi":"","omp":"modelRoles: [unclosed","updatedAt":1}},"lastApplied":""}`,
		`{"version":1,"profiles":{"bad":{"pi":"","omp":"just-a-scalar","updatedAt":1}},"lastApplied":""}`,
	}
	for i, store := range badStores {
		writeFixture(t, filepath.Join(home, ".amagi-codebox", "agent-profiles.json"), store)
		if err := s.ApplyAgentProfile("bad"); err == nil {
			t.Fatalf("ApplyAgentProfile should reject invalid stored omp YAML (case %d)", i)
		}
		got, err := os.ReadFile(ompPath)
		if err != nil {
			t.Fatalf("read omp live (case %d): %v", i, err)
		}
		if string(got) != companyOmp {
			t.Fatalf("omp live must be untouched on reject (case %d), got %q", i, string(got))
		}
		matches, _ := filepath.Glob(ompPath + ".bak-*")
		if len(matches) != 0 {
			t.Fatalf("unexpected omp backup on reject (case %d): %v", i, matches)
		}
	}
}

// TestOmpMissingCaptureAndApply 覆盖：omp 目录/文件缺失时 capture 记空串、
// apply 跳过 omp 写入且不报错、不创建 omp 目录。
func TestOmpMissingCaptureAndApply(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	writeFixture(t, filepath.Join(home, ".pi", "agent", "amagi.json"), companyPi)

	s := NewService()
	if err := s.CaptureAgentProfile("company"); err != nil {
		t.Fatalf("CaptureAgentProfile without omp: %v", err)
	}
	got, err := s.GetAgentProfile("company")
	if err != nil {
		t.Fatalf("GetAgentProfile: %v", err)
	}
	var p struct {
		Pi  string `json:"pi"`
		Omp string `json:"omp"`
	}
	if err := json.Unmarshal([]byte(got), &p); err != nil {
		t.Fatalf("parse profile: %v", err)
	}
	if p.Pi != companyPi || p.Omp != "" {
		t.Fatalf("capture without omp = pi %q / omp %q", p.Pi, p.Omp)
	}

	// apply：omp 侧跳过，不创建 ~/.omp
	if err := s.ApplyAgentProfile("company"); err != nil {
		t.Fatalf("ApplyAgentProfile without omp dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".omp")); !os.IsNotExist(err) {
		t.Fatalf("~/.omp should not be created, stat err=%v", err)
	}

	// omp 目录存在但快照 omp 为空：同样跳过写入
	writeFixture(t, filepath.Join(home, ".omp", "agent", "keep.yml"), "modelRoles: {}\n")
	if err := s.ApplyAgentProfile("company"); err != nil {
		t.Fatalf("ApplyAgentProfile with empty omp content: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".omp", "agent", "config.yml")); !os.IsNotExist(err) {
		t.Fatalf("omp config.yml must stay absent when content empty, stat err=%v", err)
	}
}

// TestDeleteAgentProfile 覆盖：delete 后 List 不再包含、重复 delete 报错、
// 删除 lastApplied 时清空。
func TestDeleteAgentProfile(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	writeFixture(t, filepath.Join(home, ".pi", "agent", "amagi.json"), companyPi)

	s := NewService()
	if err := s.CaptureAgentProfile("company"); err != nil {
		t.Fatalf("CaptureAgentProfile: %v", err)
	}
	if err := s.ApplyAgentProfile("company"); err != nil {
		t.Fatalf("ApplyAgentProfile: %v", err)
	}
	if err := s.DeleteAgentProfile("company"); err != nil {
		t.Fatalf("DeleteAgentProfile: %v", err)
	}

	out, err := s.ListAgentProfiles()
	if err != nil {
		t.Fatalf("ListAgentProfiles: %v", err)
	}
	if strings.Contains(out, "company") {
		t.Fatalf("deleted profile still listed:\n%s", out)
	}
	var store struct {
		LastApplied string `json:"lastApplied"`
	}
	if err := json.Unmarshal([]byte(out), &store); err != nil {
		t.Fatalf("parse list: %v", err)
	}
	if store.LastApplied != "" {
		t.Fatalf("lastApplied must be cleared on delete, got %q", store.LastApplied)
	}
	if err := s.DeleteAgentProfile("company"); err == nil {
		t.Fatal("deleting a missing profile must error")
	}
}

// TestNameValidation 覆盖：空名/超长名拒绝，64 字符（含中文）合法，名字去空白。
func TestNameValidation(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	writeFixture(t, filepath.Join(home, ".pi", "agent", "amagi.json"), companyPi)

	s := NewService()
	if err := s.CaptureAgentProfile("   "); err == nil {
		t.Fatal("blank name must be rejected")
	}
	long := strings.Repeat("公司", 32) + "x" // 32*2+1 = 65 字符
	if len([]rune(long)) != 65 {
		t.Fatalf("fixture length = %d", len([]rune(long)))
	}
	if err := s.CaptureAgentProfile(long); err == nil {
		t.Fatal("65-char name must be rejected")
	}

	ok64 := strings.Repeat("公", 63) + "x" // 64 字符
	if len([]rune(ok64)) != 64 {
		t.Fatalf("fixture length = %d", len([]rune(ok64)))
	}
	if err := s.CaptureAgentProfile(ok64); err != nil {
		t.Fatalf("64-char name must be accepted: %v", err)
	}

	// 名字 trim：前后空白折叠到规范名
	if err := s.CaptureAgentProfile("  home  "); err != nil {
		t.Fatalf("CaptureAgentProfile with padded name: %v", err)
	}
	if _, err := s.GetAgentProfile("home"); err != nil {
		t.Fatalf("profile should be stored under trimmed name: %v", err)
	}
}

// TestGetAgentProfileMissing 覆盖：预览不存在的配置档报错。
func TestGetAgentProfileMissing(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	s := NewService()
	if _, err := s.GetAgentProfile("nope"); err == nil {
		t.Fatal("GetAgentProfile on missing profile must error")
	}
}

// TestApplyMissingProfile 覆盖：应用不存在的配置档报错。
func TestApplyMissingProfile(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	s := NewService()
	if err := s.ApplyAgentProfile("nope"); err == nil {
		t.Fatal("ApplyAgentProfile on missing profile must error")
	}
}

// TestStorePermissions 覆盖：存储文件 0600、目录 0700（Unix）。
func TestStorePermissions(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	writeFixture(t, filepath.Join(home, ".pi", "agent", "amagi.json"), companyPi)

	s := NewService()
	if err := s.CaptureAgentProfile("company"); err != nil {
		t.Fatalf("CaptureAgentProfile: %v", err)
	}
	info, err := os.Stat(filepath.Join(home, ".amagi-codebox"))
	if err != nil {
		t.Fatalf("stat store dir: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("store dir perm = %o, want 700", info.Mode().Perm())
	}
	f, err := os.Stat(filepath.Join(home, ".amagi-codebox", "agent-profiles.json"))
	if err != nil {
		t.Fatalf("stat store file: %v", err)
	}
	if f.Mode().Perm() != 0o600 {
		t.Fatalf("store file perm = %o, want 600", f.Mode().Perm())
	}
}

// TestEnvAgentDirOverride 覆盖：PI_CODING_AGENT_DIR 优先（pi/omp 同变量，
// 复刻 piconfig/ompconfig 语义：两侧均落在该目录下）。
func TestEnvAgentDirOverride(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", root)

	// 现有语义：env 覆盖目录下 pi 读 amagi.json、omp 读 config.yml
	writeFixture(t, filepath.Join(root, "amagi.json"), companyPi)
	writeFixture(t, filepath.Join(root, "config.yml"), companyOmp)

	s := NewService()
	if err := s.CaptureAgentProfile("company"); err != nil {
		t.Fatalf("CaptureAgentProfile via env override: %v", err)
	}
	got, err := s.GetAgentProfile("company")
	if err != nil {
		t.Fatalf("GetAgentProfile: %v", err)
	}
	var p struct {
		Pi  string `json:"pi"`
		Omp string `json:"omp"`
	}
	if err := json.Unmarshal([]byte(got), &p); err != nil {
		t.Fatalf("parse profile: %v", err)
	}
	if p.Pi != companyPi || p.Omp != companyOmp {
		t.Fatalf("env override capture = pi %q / omp %q", p.Pi, p.Omp)
	}

	// 存储仍落在 HOME 下的 ~/.amagi-codebox
	if _, err := os.Stat(filepath.Join(home, ".amagi-codebox", "agent-profiles.json")); err != nil {
		t.Fatalf("store must live under HOME: %v", err)
	}
}

// TestSaveAgentProfileExplicitContent 覆盖：显式保存（前端编辑后落档）+
// apply 使用显式内容；omp 内容非空且目录存在时写入。
func TestSaveAgentProfileExplicitContent(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)

	s := NewService()
	if err := s.SaveAgentProfile("edited", companyPi, companyOmp); err != nil {
		t.Fatalf("SaveAgentProfile: %v", err)
	}
	// 预建 omp agentDir：omp 写入仅在目录存在时生效
	writeFixture(t, filepath.Join(home, ".omp", "agent", "keep.yml"), "modelRoles: {}\n")
	if err := s.ApplyAgentProfile("edited"); err != nil {
		t.Fatalf("ApplyAgentProfile: %v", err)
	}
	piData, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "amagi.json"))
	if err != nil {
		t.Fatalf("read pi live: %v", err)
	}
	if string(piData) != companyPi {
		t.Fatalf("pi live = %q, want %q", string(piData), companyPi)
	}
	ompData, err := os.ReadFile(filepath.Join(home, ".omp", "agent", "config.yml"))
	if err != nil {
		t.Fatalf("read omp live: %v", err)
	}
	if string(ompData) != companyOmp {
		t.Fatalf("omp live = %q, want %q", string(ompData), companyOmp)
	}
}

// TestCaptureOverwritesSameName 覆盖：同名 capture 覆盖旧快照。
func TestCaptureOverwritesSameName(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	piPath := filepath.Join(home, ".pi", "agent", "amagi.json")
	writeFixture(t, piPath, companyPi)

	s := NewService()
	if err := s.CaptureAgentProfile("work"); err != nil {
		t.Fatalf("capture #1: %v", err)
	}
	writeFixture(t, piPath, homePi)
	if err := s.CaptureAgentProfile("work"); err != nil {
		t.Fatalf("capture #2: %v", err)
	}
	out, err := s.ListAgentProfiles()
	if err != nil {
		t.Fatalf("ListAgentProfiles: %v", err)
	}
	// 存储内嵌 JSON 会被转义，解析后比较字段
	var listed struct {
		Profiles map[string]struct {
			Pi string `json:"pi"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("parse list: %v", err)
	}
	if got := listed.Profiles["work"].Pi; got != homePi {
		t.Fatalf("same-name capture must overwrite: pi = %q", got)
	}
}

// TestUpdatedAtIsEpochMs 覆盖：updatedAt 为毫秒级 epoch（捕获时刻附近）。
func TestUpdatedAtIsEpochMs(t *testing.T) {
	home := t.TempDir()
	redirectEnv(t, home)
	writeFixture(t, filepath.Join(home, ".pi", "agent", "amagi.json"), companyPi)

	s := NewService()
	if err := s.CaptureAgentProfile("company"); err != nil {
		t.Fatalf("CaptureAgentProfile: %v", err)
	}
	got, err := s.GetAgentProfile("company")
	if err != nil {
		t.Fatalf("GetAgentProfile: %v", err)
	}
	var p struct {
		UpdatedAt int64 `json:"updatedAt"`
	}
	if err := json.Unmarshal([]byte(got), &p); err != nil {
		t.Fatalf("parse profile: %v", err)
	}
	now := time.Now().UnixMilli()
	if p.UpdatedAt <= now-time.Hour.Milliseconds() || p.UpdatedAt > now+time.Minute.Milliseconds() {
		t.Fatalf("updatedAt %d not in epoch-ms range near now %d", p.UpdatedAt, now)
	}
}
