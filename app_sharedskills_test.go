package main

// app_sharedskills_test.go — App 门面层测试（spec §2.8）：managed 拒绝
// （SetEnabled/Delete 返回错误且目录未动）、List 的 Managed/ManagedStatus
// 组装（夹具放一个 amagi-codebox 目录）、门面往返与「打开目录」两门面。

import (
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
