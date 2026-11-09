//go:build ignore

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
 "fmt"
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

var wslSharedSkillsRunMu sync.Mutex
var wslSharedSkillsRunning = map[string]bool{} // distro → 正在执行（单飞）

// runWSLSharedSkillsRoot 执行一轮核验/修复并登记（funnel 与显式动作共用）。
// R1-F01：每 distro 单飞——同 distro 并发的会话漏斗/显式检查串行化，后到者
// 直接复用前者结果（already-linked 幂等收敛）；跨进程安全由 distro 内脚本的
// 原子发布语义保证（wslSharedSkillsScript 注释）。
func (a *App) runWSLSharedSkillsRoot(distro string) platform.WSLSharedSkillsLinkResult {
	wslSharedSkillsRunMu.Lock()
	if wslSharedSkillsRunning[distro] {
		wslSharedSkillsRunMu.Unlock()
		if rec, ok := lookupWSLSharedSkillsRoot(distro); ok {
			return rec.Res
		}
		return platform.WSLSharedSkillsLinkResult{State: platform.WSLSkillsFailed, Link: "~/.agents/skills", Detail: "同 distro 的共享技能根核验正在执行，稍后重试"}
	}
	wslSharedSkillsRunning[distro] = true
	wslSharedSkillsRunMu.Unlock()
	defer func() {
		wslSharedSkillsRunMu.Lock()
		delete(wslSharedSkillsRunning, distro)
		wslSharedSkillsRunMu.Unlock()
	}()
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


// Minimal inert App/log/home stubs; the concurrency/cache functions above are copied verbatim from current production source.
type App struct { Log *probeLog }
type probeLog struct{}
func (*probeLog) Info(string,string,...string) {}
func (*probeLog) Warn(string,string,...string) {}
func wslSkillsHostRoot()string{return `C:\Users\fixture\.agents\skills`}
func main(){
 a:=&App{}; entered:=make(chan struct{}); release:=make(chan struct{}); firstDone:=make(chan struct{})
 wslSharedSkillsLinkFn=func(string,string)platform.WSLSharedSkillsLinkResult{close(entered);<-release;return platform.WSLSharedSkillsLinkResult{State:platform.WSLSkillsLinked}}
 go func(){a.wslEnsureSharedAgentsSkillsFunnel("Ubuntu");close(firstDone)}()
 <-entered
 a.wslEnsureSharedAgentsSkillsFunnel("Ubuntu")
 _,cached:=lookupWSLSharedSkillsRoot("Ubuntu")
 fmt.Printf("SECOND_FUNNEL_RETURNED_BEFORE_LINK_READY cached=%v\n",cached)
 close(release);<-firstDone
 rec,_:=lookupWSLSharedSkillsRoot("Ubuntu");fmt.Printf("FIRST_EVENTUALLY_FINISHED state=%s\n",rec.Res.State)
}
