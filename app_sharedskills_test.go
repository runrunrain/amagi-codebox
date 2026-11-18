package main

// app_sharedskills_test.go — App 门面层测试（spec §2.8）：managed 拒绝
// （SetEnabled/Delete 返回错误且目录未动）、List 的 Managed/ManagedStatus
// 组装（夹具放一个 amagi-codebox 目录）、门面往返与「打开目录」两门面。

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/install"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/sharedskills"
)

// recordingFileOpener 记录 Open 调用（避免测试真的弹出文件管理器窗口）。
type recordingFileOpener struct {
	paths  []string
	failOn string // 命中该路径时返回错误
}

func (r *recordingFileOpener) Open(path string) error {
	if r.failOn == path {
		return errors.New("注入的打开失败")
	}
	r.paths = append(r.paths, path)
	return nil
}

func (r *recordingFileOpener) opened() []string { return r.paths }

// newSharedSkillsFacadeTestApp 构造指向临时 home 的最小 App：
//   - SharedSkills 服务与 install.Manager 共用同一个临时 userHome（managed
//     状态与目录枚举面对同一份夹具）；
//   - FileOpener 用记录器，绝不真开窗口。
func newSharedSkillsFacadeTestApp(t *testing.T) (*App, string, *recordingFileOpener) {
	t.Helper()
	home := t.TempDir()
	cfgDir := t.TempDir()
	logSvc := logging.NewService(cfgDir)
	opener := &recordingFileOpener{}
	app := &App{
		configDir:       cfgDir,
		Log:             logSvc,
		FileOpener:      opener,
		agentCLIInstall: install.New(cfgDir, home, "", nil),
		SharedSkills:    sharedskills.New(home, logSvc),
	}
	t.Cleanup(logSvc.Close)
	return app, home, opener
}

func mkSkillDir(t *testing.T, home, root, name string) string {
	t.Helper()
	dir := filepath.Join(home, ".agents", root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeAppSkillDir 在 parent 下创建带 SKILL.md 的技能目录（导入夹具用）。
func writeAppSkillDir(t *testing.T, parent, name, skillMD string) string {
	t.Helper()
	dir := mkSkillDirFor(t, parent, name)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func mkSkillDirFor(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAppSharedSkills_ManagedRefusedAndDirUntouched(t *testing.T) {
	app, home, _ := newSharedSkillsFacadeTestApp(t)
	dir := mkSkillDir(t, home, "skills", sharedskills.ManagedSkillName)

	if views, err := app.SetSharedSkillEnabled(sharedskills.ManagedSkillName, false); err == nil {
		t.Fatalf("managed 技能停用应被拒绝，got views=%v", views)
	} else if !strings.Contains(err.Error(), "应用托管技能") || !strings.Contains(err.Error(), "设置 → 命令行访问") {
		t.Fatalf("拒绝文案应引导到 设置 → 命令行访问，got %v", err)
	}
	if views, err := app.DeleteSharedSkill(sharedskills.ManagedSkillName); err == nil {
		t.Fatalf("managed 技能删除应被拒绝，got views=%v", views)
	}

	// 目录未动：仍在启用根，禁用根未出现。
	if _, err := os.Lstat(dir); err != nil {
		t.Fatalf("managed 目录应原样保留在启用根：%v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills-disabled", sharedskills.ManagedSkillName)); !os.IsNotExist(err) {
		t.Fatalf("managed 目录不应被移动，got err=%v", err)
	}
}

func TestAppSharedSkills_ListAssemblesManagedFields(t *testing.T) {
	app, home, _ := newSharedSkillsFacadeTestApp(t)
	// 夹具放一个 amagi-codebox 目录：经 install.Manager 正式安装 → 五态
	// up-to-date（确定性断言）；另放一个普通技能作对照。
	if err := app.agentCLIInstaller().InstallSkill(install.SkillTargetAgents); err != nil {
		t.Fatal(err)
	}
	plain := mkSkillDir(t, home, "skills", "plain-skill")
	if err := os.WriteFile(filepath.Join(plain, "SKILL.md"), []byte("---\nname: Plain\ndescription: 对照技能\n---\n正文"), 0o644); err != nil {
		t.Fatal(err)
	}

	views, err := app.ListSharedSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatalf("应列出 2 个技能，got %+v", views)
	}
	byName := map[string]SharedSkillView{}
	for _, v := range views {
		byName[v.Name] = v
	}
	mg := byName[sharedskills.ManagedSkillName]
	if !mg.Managed || mg.ManagedStatus != string(install.StateUpToDate) {
		t.Fatalf("managed 组装 = Managed:%v ManagedStatus:%q（want true/up-to-date）", mg.Managed, mg.ManagedStatus)
	}
	pl := byName["plain-skill"]
	if pl.Managed || pl.ManagedStatus != "" || pl.DisplayName != "Plain" || !pl.Enabled {
		t.Fatalf("非 managed 条目不应带 managed 字段值：%+v", pl)
	}

	// Refresh 语义别名：与 List 同源同值。
	rv, err := app.RefreshSharedSkills()
	if err != nil || len(rv) != len(views) {
		t.Fatalf("RefreshSharedSkills 应与 List 同源：len=%d err=%v", len(rv), err)
	}
}

func TestAppSharedSkills_SetEnabledReturnsFreshList(t *testing.T) {
	app, home, _ := newSharedSkillsFacadeTestApp(t)
	mkSkillDir(t, home, "skills", "ordinary")

	views, err := app.SetSharedSkillEnabled("ordinary", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Enabled || views[0].Name != "ordinary" {
		t.Fatalf("停用后应返回最新列表（已禁用条目），got %+v", views)
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills-disabled", "ordinary")); err != nil {
		t.Fatalf("停用应真实移动目录：%v", err)
	}

	views, err = app.DeleteSharedSkill("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 0 {
		t.Fatalf("删除后应返回空列表，got %+v", views)
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills-disabled", "ordinary")); !os.IsNotExist(err) {
		t.Fatalf("删除后目录应不存在，got err=%v", err)
	}
}

func TestAppSharedSkills_OpenFolderAndRoot(t *testing.T) {
	app, home, opener := newSharedSkillsFacadeTestApp(t)
	dir := mkSkillDir(t, home, "skills", "openable")

	if err := app.OpenSharedSkillFolder("openable"); err != nil {
		t.Fatal(err)
	}
	if got := opener.opened(); len(got) != 1 || got[0] != dir {
		t.Fatalf("OpenSharedSkillFolder 应经 fileOpener 打开技能目录，got %v", got)
	}

	// 不存在的技能：SkillDir 先行报错，不触碰打开器。
	if err := app.OpenSharedSkillFolder("missing"); err == nil {
		t.Fatal("不存在的技能应报错")
	}
	if got := opener.opened(); len(got) != 1 {
		t.Fatalf("失败路径不应额外打开目录，got %v", got)
	}

	// 根目录存在 → 打开；不存在（全新 home）→ 如实报错、不建目录。
	if err := app.OpenSharedSkillsRoot(); err != nil {
		t.Fatal(err)
	}
	freshHome := t.TempDir()
	app2 := &App{SharedSkills: sharedskills.New(freshHome, nil)}
	if err := app2.OpenSharedSkillsRoot(); err == nil || !strings.Contains(err.Error(), "目录不存在") {
		t.Fatalf("技能根不存在应如实报错（不自动创建），got %v", err)
	}
	if _, serr := os.Lstat(filepath.Join(freshHome, ".agents", "skills")); !os.IsNotExist(serr) {
		t.Fatalf("OpenSharedSkillsRoot 不得创建目录，got err=%v", serr)
	}
}

// 接口断言：recordingFileOpener 满足 platform.FileOpener（编译期守卫）。
var _ platform.FileOpener = (*recordingFileOpener)(nil)

// ---- 导入门面（canonical 共用技能纵切）：信封组装与整体失败语义 ----

// makeSharedSkillsZip 构造测试 zip（entry 名 → 内容）。
func makeSharedSkillsZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w := zip.NewWriter(f)
	for name, content := range files {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAppSharedSkills_ImportZipEnvelopeAndFreshViews(t *testing.T) {
	app, home, _ := newSharedSkillsFacadeTestApp(t)
	zipPath := filepath.Join(t.TempDir(), "demo.zip")
	makeSharedSkillsZip(t, zipPath, map[string]string{
		"demo/SKILL.md": "---\nname: Demo\ndescription: 演示\n---\n正文",
	})

	res, err := app.ImportSharedSkillsZip(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != 1 || res.Imported[0].Name != "demo" || res.Imported[0].Enabled {
		t.Fatalf("信封 Imported 应含默认停用的 demo: %+v", res.Imported)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("无跳过项: %v", res.Skipped)
	}
	// Views 为导入后的最新列表（与 List 同源，含落盘路径）。
	if len(res.Views) != 1 || res.Views[0].Name != "demo" || res.Views[0].Enabled {
		t.Fatalf("Views 应为最新列表（demo 默认停用）: %+v", res.Views)
	}
	if res.Views[0].Path != filepath.Join(home, ".agents", "skills-disabled", "demo") {
		t.Fatalf("Views 落点应在禁用根: %s", res.Views[0].Path)
	}

	// List 独立复核：导入条目真实可见且默认停用。
	views, err := app.ListSharedSkills()
	if err != nil || len(views) != 1 || views[0].Enabled {
		t.Fatalf("List 应看到停用条目: err=%v views=%+v", err, views)
	}
}

func TestAppSharedSkills_ImportDirPartialSuccessAndManagedAssembly(t *testing.T) {
	app, home, _ := newSharedSkillsFacadeTestApp(t)
	// 夹具：托管技能正式安装（Managed=true）+ 既有同名占位。
	if err := app.agentCLIInstaller().InstallSkill(install.SkillTargetAgents); err != nil {
		t.Fatal(err)
	}
	writeAppSkillDir(t, filepath.Join(home, ".agents", "skills"), "taken", "---\nname: Taken\n---\n")
	src := t.TempDir()
	writeAppSkillDir(t, src, "taken", "---\nname: NewTaken\n---\n")
	writeAppSkillDir(t, src, "fresh", "---\nname: Fresh\ndescription: 新技能\n---\n正文")

	res, err := app.ImportSharedSkillsDir(src)
	if err != nil {
		t.Fatalf("部分成功不应返回 error: %v", err)
	}
	if len(res.Imported) != 1 || res.Imported[0].Name != "fresh" {
		t.Fatalf("仅 fresh 导入: %+v", res.Imported)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "拒绝覆盖") {
		t.Fatalf("taken 应以同名拒绝覆盖跳过: %v", res.Skipped)
	}
	// Views 组装 managed 联动（amagi-codebox 条目带 Managed 标记）。
	byName := map[string]SharedSkillView{}
	for _, v := range res.Views {
		byName[v.Name] = v
	}
	if v := byName[sharedskills.ManagedSkillName]; !v.Managed || v.ManagedStatus == "" {
		t.Fatalf("托管条目应带 Managed/ManagedStatus: %+v", v)
	}
	if v := byName["fresh"]; v.Managed || v.Enabled {
		t.Fatalf("fresh 应为非托管且默认停用: %+v", v)
	}
}

func TestAppSharedSkills_ImportZipTotalFailureReturnsError(t *testing.T) {
	app, _, _ := newSharedSkillsFacadeTestApp(t)
	zipPath := filepath.Join(t.TempDir(), "attack.zip")
	makeSharedSkillsZip(t, zipPath, map[string]string{"../evil.txt": "boom"})

	res, err := app.ImportSharedSkillsZip(zipPath)
	if err == nil {
		t.Fatal("整体失败应返回 error")
	}
	if res.Imported == nil || res.Skipped == nil {
		t.Fatalf("信封切片应非 nil（JS 免判空）: %+v", res)
	}
	if len(res.Imported) != 0 || len(res.Views) != 0 {
		t.Fatalf("整体失败不应有导入结果: %+v", res)
	}
}
