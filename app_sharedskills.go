package main

// app_sharedskills.go — 共用技能（~/.agents/skills ⇄ ~/.agents/skills-disabled）
// 的 App 接线与前端门面（扩展中心重构 spec §2）。枚举/启停/删除机制本体在
// internal/sharedskills；本文件只负责：
//   - 持有 Service（userHome=用户主目录，构造模式照抄 buildAgentCLIInstallManager）；
//   - managed 标记组装（amagi-codebox 自带技能，联动 internal/agentcli/install
//     的 SkillStatus 五态，spec §2.4；sharedskills 包不反向 import install，
//     联动在本层完成）；
//   - Wails 门面方法（List/SetEnabled/Delete/OpenFolder/OpenRoot/Refresh 共
//     6 个，spec §2.5），全部不含令牌。
//
// sharedskills.Service 不进 bind_list.go：目录枚举面小、且 managed 组装本就
// 要过 App 层（与 agentcli install 同模式）。

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"amagi-codebox/internal/agentcli/install"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/sharedmarket"
	"amagi-codebox/internal/sharedskills"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// SharedSkillView 是前端看到的共用技能条目：sharedskills.Skill 加 managed
// 组装（spec §2.4）。
type SharedSkillView struct {
	sharedskills.Skill
	MarketOrigin  *sharedmarket.SkillOrigin `json:"marketOrigin,omitempty"`
	Managed       bool                      `json:"managed"`                 // amagi-codebox 自带技能
	ManagedStatus string                    `json:"managedStatus,omitempty"` // install 五态（not-installed/up-to-date/outdated/modified-by-user/invalid），经 a.agentCLIInstaller().SkillStatus(install.SkillTargetAgents)
}

// managedSharedSkillMessage 是 managed 技能启停/删除的统一拒绝文案
// （spec §2.4 行为裁定：与 InstallDefaults 启动自动装回冲突，移动/删除会在
// 下次启动被静默恢复）。
const managedSharedSkillMessage = "amagi-codebox 为应用托管技能，请在 设置 → 命令行访问 中管理"

// constructSharedSkills 在 NewApp 构造共用技能服务（与 constructAgentCLIInstall
// 同时机）。纯构造不做文件 IO。
func (a *App) constructSharedSkills() {
	if a.SharedSkills != nil {
		return
	}
	a.SharedSkills = buildSharedSkillsService(a)
}

// buildSharedSkillsService 构造共用技能服务：userHome=用户主目录（~/.agents
// 所在）。os.UserHomeDir 失败时回退 configDir（模式照抄
// buildAgentCLIInstallManager，app_agentcli_install.go——降级时 ~/.agents
// 不可达，List 以空态/错误如实呈现，不影响其余服务）。
func buildSharedSkillsService(a *App) *sharedskills.Service {
	home := defaultConfigDir()
	var log *logging.Service
	if a != nil {
		if a.configDir != "" {
			home = a.configDir
		}
		log = a.Log
	}
	userHome := home
	if resolved, err := os.UserHomeDir(); err == nil {
		userHome = resolved
	}
	return sharedskills.New(userHome, log)
}

// sharedSkillsService 返回共用技能服务。生产由 NewApp 构造；未构造的 App
// （测试夹具直接拼 struct）现场兜底构造，不写回字段（与 agentCLIInstaller
// 同模式，避免与生产构造路径竞态）。
func (a *App) sharedSkillsService() *sharedskills.Service {
	if a != nil && a.SharedSkills != nil {
		return a.SharedSkills
	}
	return buildSharedSkillsService(a)
}

// ---- Wails 门面（spec §2.5，共 6 个） ----

// sharedSkillViews 扫描并组装前端视图：managed 联动在 App 层完成（spec §2.4
// ——Name == amagi-codebox 的条目附加 Managed 与 install 五态，来自
// SkillStatus(SkillTargetAgents)，只读检查）。
func (a *App) sharedSkillViews() ([]SharedSkillView, error) {
	skills, err := a.sharedSkillsService().List()
	if err != nil {
		return nil, err
	}
	views := make([]SharedSkillView, 0, len(skills))
	for _, sk := range skills {
		v := SharedSkillView{Skill: sk, MarketOrigin: sharedmarket.OriginForSkill(sk.Path, sk.Enabled)}
		if sk.Name == sharedskills.ManagedSkillName {
			v.Managed = true
			v.ManagedStatus = string(a.agentCLIInstaller().SkillStatus(install.SkillTargetAgents).State)
		}
		views = append(views, v)
	}
	return views, nil
}

// ListSharedSkills 返回 ~/.agents/skills 与 skills-disabled 下的全部共用技能
// （按名称排序，含 managed 标记与 install 状态）。
func (a *App) ListSharedSkills() ([]SharedSkillView, error) {
	return a.sharedSkillViews()
}

// RefreshSharedSkills 显式刷新：服务层无缓存，语义等同 ListSharedSkills
// （spec §2.5——为契约点名的 Refresh 显式提供，未来加缓存不改前端）。
func (a *App) RefreshSharedSkills() ([]SharedSkillView, error) {
	return a.sharedSkillViews()
}

// SetSharedSkillEnabled 启用/停用共用技能（目录在 ~/.agents/skills ⇄
// skills-disabled 间移动，对读取 ~/.agents/skills 的 CLI 立即生效），返回
// 最新列表（前端免二次请求）。managed 技能拒绝（spec §2.4 行为裁定）。
func (a *App) SetSharedSkillEnabled(name string, enabled bool) ([]SharedSkillView, error) {
	a.sharedSkillWrites.Lock()
	defer a.sharedSkillWrites.Unlock()
	if name == sharedskills.ManagedSkillName {
		return nil, errors.New(managedSharedSkillMessage)
	}
	if _, err := a.sharedSkillsService().SetEnabled(name, enabled); err != nil {
		return nil, err
	}
	return a.sharedSkillViews()
}

// DeleteSharedSkill 永久删除共用技能目录（含禁用态；前端须弹 danger 确认
// 弹窗），返回最新列表。managed 技能拒绝（同 SetSharedSkillEnabled）。
func (a *App) DeleteSharedSkill(name string) ([]SharedSkillView, error) {
	a.sharedSkillWrites.Lock()
	defer a.sharedSkillWrites.Unlock()
	if name == sharedskills.ManagedSkillName {
		return nil, errors.New(managedSharedSkillMessage)
	}
	if sk, err := a.sharedSkillsService().Detail(name); err != nil {
		return nil, err
	} else if origin := sharedmarket.OriginForSkill(sk.Path, sk.Enabled); origin != nil {
		return nil, fmt.Errorf("此技能由共用市场部署，请在「%s」市场卸载插件「%s」", origin.Marketplace, origin.Plugin)
	}
	if err := a.sharedSkillsService().Delete(name); err != nil {
		return nil, err
	}
	return a.sharedSkillViews()
}

// OpenSharedSkillFolder 用系统文件管理器打开指定技能的当前目录（模式照抄
// OpenFileInEditor，app_session_pty.go：先 Stat 校验存在再 Open，绝不凭空调
// 用打开器）。
func (a *App) OpenSharedSkillFolder(name string) error {
	dir, err := a.sharedSkillsService().SkillDir(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		if a.Log != nil {
			a.Log.Debug("sharedskills", "技能目录不存在，跳过打开", dir)
		}
		return fmt.Errorf("技能目录不存在：%s", dir)
	}
	if err := a.fileOpener().Open(dir); err != nil {
		if a.Log != nil {
			a.Log.Warn("sharedskills", "打开技能目录失败", fmt.Sprintf("dir=%s err=%v", dir, err))
		}
		return fmt.Errorf("打开技能目录失败：%s（%v）", dir, err)
	}
	if a.Log != nil {
		a.Log.Info("sharedskills", "系统文件管理器打开技能目录", dir)
	}
	return nil
}

// OpenSharedSkillsRoot 打开 ~/.agents/skills 根目录。根目录不存在时如实报错
// 并提示先安装技能（spec §2.5 裁定：不自动 MkdirAll——「用户可见状态必须
// 真实」，不替用户造目录；导入是唯一例外：导入落盘时会按需创建禁用根）。
func (a *App) OpenSharedSkillsRoot() error {
	root := a.sharedSkillsService().SkillsRoot()
	fi, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("目录不存在：%s。请先安装技能后再打开（技能目录会在首次安装时自动创建）", root)
		}
		return fmt.Errorf("检查目录失败：%s（%v）", root, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s 不是目录", root)
	}
	if err := a.fileOpener().Open(root); err != nil {
		if a.Log != nil {
			a.Log.Warn("sharedskills", "打开技能根目录失败", fmt.Sprintf("dir=%s err=%v", root, err))
		}
		return fmt.Errorf("打开技能根目录失败：%s（%v）", root, err)
	}
	if a.Log != nil {
		a.Log.Info("sharedskills", "系统文件管理器打开技能根目录", root)
	}
	return nil
}

// ---- Wails 门面：导入（canonical 共用技能纵切，替代 v1.3.145 sharedext ----
// ---- 重复导入面）                                                          ----

// SharedSkillsImportResult 是 ImportSharedSkillsZip/Dir 的单值返回信封
// （Wails v2 对多返回值支持有限，服务层 (imported, skipped, error) 在此折叠）。
// 部分成功（同名/非法名/超限跳过）不返回 error，明细在 Skipped 供前端双
// toast；仅整体失败才返回 error。Views 为导入后的最新列表（前端免二次请求）。
type SharedSkillsImportResult struct {
	Imported []sharedskills.Skill `json:"imported"`
	Skipped  []string             `json:"skipped"`
	Views    []SharedSkillView    `json:"views"`
}

// wrapSharedSkillsImport 折叠服务层导入结果为信封：非 nil 切片保证（JS 侧
// 免判空）；error 原样透传（整体失败时 Skipped 一并带回供前端展示明细）；
// 成功后组装最新列表（managed 联动与 List 同源）。
func (a *App) wrapSharedSkillsImport(imported []sharedskills.Skill, skipped []string, err error) (SharedSkillsImportResult, error) {
	if imported == nil {
		imported = []sharedskills.Skill{}
	}
	if skipped == nil {
		skipped = []string{}
	}
	env := SharedSkillsImportResult{Imported: imported, Skipped: skipped}
	if err != nil {
		return env, err
	}
	views, verr := a.sharedSkillViews()
	if verr != nil {
		return env, verr
	}
	env.Views = views
	return env, nil
}

// ImportSharedSkillsZip 从 zip 压缩包导入共用技能：安全解包（拒绝路径穿越
// 与链接条目，总量/文件数/单文件上限）后统一落盘到 ~/.agents/skills-disabled
// （默认停用为保守策略：不自动分发到各 CLI 私有目录）；同名（启用/停用/托管
// 名 amagi-codebox）一律跳过、绝不覆盖。名称与 ImportSharedSkillFromZip
// （已随 v1.3.145 sharedext 重复模块退役移除）无关。
func (a *App) ImportSharedSkillsZip(zipPath string) (SharedSkillsImportResult, error) {
	a.sharedSkillWrites.Lock()
	defer a.sharedSkillWrites.Unlock()
	imported, skipped, err := a.sharedSkillsService().ImportFromZip(zipPath)
	return a.wrapSharedSkillsImport(imported, skipped, err)
}

// ImportSharedSkillsDir 从本地文件夹导入共用技能（文件夹自身含 SKILL.md 视为
// 单个技能，否则扫描子目录；链接与越界拒绝、上限与 zip 导入一致），落盘与
// 同名语义同 ImportSharedSkillsZip。
func (a *App) ImportSharedSkillsDir(dirPath string) (SharedSkillsImportResult, error) {
	a.sharedSkillWrites.Lock()
	defer a.sharedSkillWrites.Unlock()
	imported, skipped, err := a.sharedSkillsService().ImportFromDir(dirPath)
	return a.wrapSharedSkillsImport(imported, skipped, err)
}

// PickSharedSkillsZip 弹出系统文件选择对话框选择技能 zip 压缩包（导入入口）。
// 用户取消返回空串（无错误）；仅返回路径，不读文件内容。
func (a *App) PickSharedSkillsZip() (string, error) {
	path, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择技能压缩包",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "ZIP 压缩包 (*.zip)", Pattern: "*.zip"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("打开文件选择对话框失败：%w", err)
	}
	return path, nil
}

// PickSharedSkillsDir 弹出系统目录选择对话框选择技能文件夹（导入入口）。
// 用户取消返回空串（无错误）。
func (a *App) PickSharedSkillsDir() (string, error) {
	dir, err := wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择技能文件夹",
	})
	if err != nil {
		return "", fmt.Errorf("打开目录选择对话框失败：%w", err)
	}
	return dir, nil
}
