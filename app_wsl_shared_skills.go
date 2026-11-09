package main

// app_wsl_shared_skills.go — WSL 内共享 Windows ~/.agents/skills（受管符号链接）。
//
// 1.3.126 裁定的延伸：WSL 只提供运行环境，用户资产全部来自 Windows。pi 原生
// 按 Agent Skills 规范扫描 $HOME/.agents/skills——本模块在 WSL pi 会话启动漏斗
// 与状态页「检查」动作两处核验（并按需修复）distro 内
// ~/.agents/skills → Windows ~/.agents/skills 的符号链接：
//   - 会话漏斗（resolveEmbeddedLaunchSpec 的 WSL pi 分支）：每进程每 distro
//     一次成功后缓存，不阻塞每次启动（重连/修复走显式动作）；
//   - EnsureWSLSharedSkillsRoot（wails 绑定）：状态页显式「检查/修复」，强制
//     重跑一轮并返回结果（真实目录被备份迁移等情况在此可见）。
// 旧 P3-S3「共享技能」同意制投影已随本机制退役（见 internal/wslsupply/skills.go
// 头注与 pruneRetiredUnits）——同一棵树不允许两套写入通道。

import (
	"sync"
	"time"

	"amagi-codebox/internal/platform"
)

// wslSharedSkillsRootRecord 是一次受管符号链接核验/修复的记录（进程内缓存）。
type wslSharedSkillsRootRecord struct {
	At  time.Time
	Res platform.WSLSharedSkillsLinkResult
}

var wslSharedSkillsRootMu sync.Mutex
var wslSharedSkillsRoots = map[string]wslSharedSkillsRootRecord{}

func recordWSLSharedSkillsRoot(distro string, res platform.WSLSharedSkillsLinkResult) {
	wslSharedSkillsRootMu.Lock()
	defer wslSharedSkillsRootMu.Unlock()
	wslSharedSkillsRoots[distro] = wslSharedSkillsRootRecord{At: time.Now(), Res: res}
}

func lookupWSLSharedSkillsRoot(distro string) (wslSharedSkillsRootRecord, bool) {
	wslSharedSkillsRootMu.Lock()
	defer wslSharedSkillsRootMu.Unlock()
	rec, ok := wslSharedSkillsRoots[distro]
	return rec, ok
}

// WSLSharedSkillsRootStatus 是状态页「共享技能根」的结果面。
type WSLSharedSkillsRootStatus struct {
	Distro  string `json:"distro"`
	State   string `json:"state"` // linked / already-linked / backed-up / replaced-link / host-missing / failed / unchecked
	Target  string `json:"target,omitempty"`
	Backup  string `json:"backup,omitempty"`
	Detail  string `json:"detail"`
	Checked bool   `json:"checked"`
}

// wslSharedSkillsLinkFn 是共享技能根供给的接缝（生产 platform 实现；测试注入）。
var wslSharedSkillsLinkFn = platform.WSLEnsureSharedAgentsSkillsLink

// runWSLSharedSkillsRoot 执行一轮核验/修复并登记（funnel 与显式动作共用）。
func (a *App) runWSLSharedSkillsRoot(distro string) platform.WSLSharedSkillsLinkResult {
	res := wslSharedSkillsLinkFn(distro, wslSkillsHostRoot())
	recordWSLSharedSkillsRoot(distro, res)
	if a != nil && a.Log != nil {
		switch res.State {
		case platform.WSLSkillsAlreadyLinked:
			a.Log.Info("wslsupply", "WSL 内 ~/.agents/skills 已指向 Windows 技能根", "distro="+distro)
		case platform.WSLSkillsHostMissing:
			a.Log.Info("wslsupply", "Windows 技能根在 distro 内不可达，未建符号链接", "distro="+distro)
		case platform.WSLSkillsFailed:
			a.Log.Warn("wslsupply", "WSL 共享技能根核验失败", "distro="+distro, res.Detail)
		default:
			a.Log.Info("wslsupply", "WSL 内 ~/.agents/skills 已共享 Windows 技能根", "distro="+distro, string(res.State)+" "+res.Detail)
		}
	}
	return res
}

// wslEnsureSharedAgentsSkillsFunnel 是会话启动漏斗的缓存入口：每进程每 distro
// 仅首轮真实执行（成功后幂等跳过；失败每 distro 记一次，不重试刷 wsl.exe）。
// 修复入口走 EnsureWSLSharedSkillsRoot（显式动作）。
func (a *App) wslEnsureSharedAgentsSkillsFunnel(distro string) {
	if distro == "" {
		return
	}
	if _, ok := lookupWSLSharedSkillsRoot(distro); ok {
		return
	}
	a.runWSLSharedSkillsRoot(distro)
}

// EnsureWSLSharedSkillsRoot 状态页显式「检查/修复共享技能根」：强制重跑一轮
// （不过缓存）并返回结果。distro 不可用或宿主根缺失时如实返回，不报错。
func (a *App) EnsureWSLSharedSkillsRoot() WSLSharedSkillsRootStatus {
	out := WSLSharedSkillsRootStatus{State: "unchecked", Detail: "尚未检查"}
	if a == nil {
		return out
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		out.Detail = "未找到可用的 WSL 发行版"
		return out
	}
	if wslSkillsHostRoot() == "" {
		out.Detail = "宿主用户目录不可解析"
		return out
	}
	res := a.runWSLSharedSkillsRoot(distro)
	out.Distro = distro
	out.Checked = true
	out.State = string(res.State)
	out.Target = res.Target
	out.Backup = res.Backup
	out.Detail = res.Detail
	return out
}

// wslSharedSkillsFace 聚合「共享技能根」卡片（配置状态区第五面；替代已退役
// 的 P3-S3 技能投影面）。只读缓存，不触发探测。
func wslSharedSkillsFace() WSLConfigSupplyFace {
	face := WSLConfigSupplyFace{Key: "skills", Label: "共享技能（Skills）", State: "unchecked", Detail: "尚未检查（启动 WSL pi 会话或点检查后核验符号链接）"}
	wslSharedSkillsRootMu.Lock()
	latest := wslSharedSkillsRootRecord{}
	found := false
	for _, rec := range wslSharedSkillsRoots {
		if !found || rec.At.After(latest.At) {
			latest = rec
			found = true
		}
	}
	wslSharedSkillsRootMu.Unlock()
	if !found {
		return face
	}
	face.LastCheck = latest.At.Format(time.RFC3339)
	switch latest.Res.State {
	case platform.WSLSkillsAlreadyLinked, platform.WSLSkillsLinked, platform.WSLSkillsBackedUp, platform.WSLSkillsReplacedLink:
		face.State = "synced"
		face.Detail = "WSL ~/.agents/skills → Windows 技能根（符号链接）"
		if latest.Res.Backup != "" {
			face.Detail += "；旧目录已备份"
		}
	case platform.WSLSkillsHostMissing:
		face.State = "unchecked"
		face.Detail = "Windows 技能根在 distro 内不可达（不存在或未挂载）"
	case platform.WSLSkillsFailed:
		face.State = "failed"
		face.Detail = "核验失败：" + latest.Res.Detail
	}
	return face
}
