// Package dockerwsl 实现 Docker Desktop ↔ WSL 集成的健康检查与自愈。
//
// 背景（2026-09-25 运维会话复盘 §1.2，X:/WorkSpace/Database/01-AI技术与工具/
// codebox优化/wsl优化/codebox-wsl环境会话摩擦与问题复盘-2026-09-25.md）：
// Docker Desktop 与 Ubuntu 用户发行版同时冷启动初始化期间，/mnt/wsl/docker-desktop/
// 共享挂载可能被竞态挂坏——docker-desktop-user-distro 挂成 0 字节空文件（正常应为
// 二十余 MB 的可执行），对空文件 exec → exit 1 且 stderr 为空 → WSL 集成弹窗
// "unexpectedly stopped" 无限重试。仅重启 GUI 不够（backend 不随 GUI 复活、坏挂载
// 陈旧保留）；实证自愈序列为：
//
//	① 全量退出 Docker Desktop（Docker Desktop.exe / com.docker.backend.exe /
//	  com.docker.build.exe，taskkill /T /F 后轮询 tasklist 计数归零）
//	② wsl.exe --terminate docker-desktop（仅工具发行版；Ubuntu 用户发行版与会话零影响）
//	③ 全新启动 Docker Desktop.exe
//	④ 轮询引擎就绪（terminate 后重启 ≈5s；当天首次冷启动可达 3-4 分钟 → 预算 300s、5s 间隔）
//	⑤ 复核集成挂载字节数 > 0 且发行版内 /usr/bin/docker 已注入
//
// 责任归属：根因在上游（Docker Desktop/WSL 共享挂载竞态）；本包把实证序列固化为
// 一键自愈，供环境系统性规避。跨平台边界：所有 Windows 系统调用经 runner 接缝注入
// （service_windows.go 真实现 / service_other.go 不支持桩 / service_test.go 注入假件），
// 编排与判定逻辑在本文件平台无关可直测。
package dockerwsl

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"amagi-codebox/internal/logging"
)

// 集成共享挂载路径（WSL 侧，经用户默认发行版命名空间可见——/mnt/wsl 为跨发行版共享挂载）。
const (
	// integrationMountPath 是集成代理二进制的共享挂载路径：挂坏时为 0 字节空文件。
	integrationMountPath = "/mnt/wsl/docker-desktop/docker-desktop-user-distro"
	// integrationDockerBin 是集成注入到用户发行版的 docker CLI 路径。
	integrationDockerBin = "/usr/bin/docker"
	// toolDistroName 是 Docker Desktop 的 WSL 工具发行版（terminate 仅针对它）。
	toolDistroName = "docker-desktop"

	// desktopExitPollInterval / desktopExitDeadline：① 全量退出后等待进程计数归零的节奏。
	desktopExitPollInterval = 1 * time.Second
	desktopExitDeadline     = 30 * time.Second
	// enginePollInterval / enginePollBudget：④ 引擎就绪轮询。冷启动实测 3-4 分钟 > 旧 120s
	// 预算；terminate 工具发行版后的重启因 Windows 侧缓存全命中仅需 ≈5s。
	enginePollInterval = 5 * time.Second
	enginePollBudget   = 300 * time.Second

	// stepExitDesktop / stepTerminateToolDistro / stepStartDesktop / stepWaitEngine /
	// stepVerifyIntegration 为固定步骤名（前端按名渲染进度行）。
	stepExitDesktop         = "exit-desktop"
	stepTerminateToolDistro = "terminate-tool-distro"
	stepStartDesktop        = "start-desktop"
	stepWaitEngine          = "wait-engine"
	stepVerifyIntegration   = "verify-integration"
)

// MountState 是集成共享挂载的健康判定（字符串枚举，前端可直接展示）。
const (
	// MountOK：挂载存在且字节数 > 0（健康）。
	MountOK = "ok"
	// MountBroken：挂载存在但为 0 字节空文件——§1.2 竞态挂坏的精确签名。
	MountBroken = "broken"
	// MountAbsent：挂载不可见（Docker 未运行 / 集成未开启 / 发行版未起）。
	MountAbsent = "absent"
	// MountUnknown：无法判定（探测失败）。
	MountUnknown = "unknown"
)

// desktopProcessImages 是自愈第①步需要全量退出的进程清单（复盘实证集合）。
var desktopProcessImages = []string{"Docker Desktop.exe", "com.docker.backend.exe", "com.docker.build.exe"}

// HealthReport 是一次健康检查的结构化快照（前端按 Issues 逐行展示 + 徽标）。
type HealthReport struct {
	// Available：Docker Desktop 安装路径存在。
	Available bool `json:"available"`
	// DockerDesktopPath / EngineExePath：安装路径（空串 = 未找到）。
	DockerDesktopPath string `json:"dockerDesktopPath"`
	EngineExePath     string `json:"engineExePath"`
	// DefaultDistro：WSL 默认用户发行版名（探测失败为空）。
	DefaultDistro string `json:"defaultDistro"`
	// ToolDistroPresent / ToolDistroRunning：docker-desktop 工具发行版注册/运行状态。
	ToolDistroPresent  bool `json:"toolDistroPresent"`
	ToolDistroRunning  bool `json:"toolDistroRunning"`
	// DesktopProcessesRunning：三个 Desktop 相关进程的存活计数（0 = 全退）。
	DesktopProcessesRunning int `json:"desktopProcessesRunning"`
	// IntegrationMountBytes：docker-desktop-user-distro 字节数（-1 = 不可见/未知）。
	IntegrationMountBytes int64 `json:"integrationMountBytes"`
	// IntegrationMountState：ok / broken / absent / unknown。
	IntegrationMountState string `json:"integrationMountState"`
	// DistroDockerBinInjected：用户发行版内 /usr/bin/docker 是否已注入。
	DistroDockerBinInjected bool `json:"distroDockerBinInjected"`
	// EngineReady / EngineVersion：引擎 npipe 就绪与版本。
	EngineReady   bool   `json:"engineReady"`
	EngineVersion string `json:"engineVersion"`
	// RecommendSelfHeal：结构化判定——挂载挂坏、或 Desktop 运行中但引擎未就绪、
	// 或引擎就绪但集成挂载/注入缺失。为 true 时前端高亮「一键自愈」。
	RecommendSelfHeal bool `json:"recommendSelfHeal"`
	// Issues：人读问题清单（中文，按严重度排序）。
	Issues []string `json:"issues"`
	// CheckedAt：快照时间。
	CheckedAt time.Time `json:"checkedAt"`
}

// SelfHealStep 是自愈序列中的一步（名称固定，Ok/Detail 供前端渲染）。
type SelfHealStep struct {
	Name      string `json:"name"`
	Ok        bool   `json:"ok"`
	Detail    string `json:"detail"`
	DurationMs int64 `json:"durationMs"`
}

// SelfHealReport 是一次自愈执行的完整回执（步骤表 + 前后健康对照）。
type SelfHealReport struct {
	Success      bool         `json:"success"`
	Summary      string       `json:"summary"`
	Steps        []SelfHealStep `json:"steps"`
	HealthBefore HealthReport `json:"healthBefore"`
	HealthAfter  HealthReport `json:"healthAfter"`
	StartedAt    time.Time    `json:"startedAt"`
	FinishedAt   time.Time    `json:"finishedAt"`
}

// runner 收集全部平台相关系统调用（Windows 真实现 / 测试假件注入）。
type runner struct {
	desktopExePath func() string                              // Docker Desktop.exe 安装路径；"" = 未安装
	engineExePath  func() string                              // docker.exe 路径；"" = 未找到
	defaultDistro  func() string                              // WSL 默认用户发行版；"" = 未知
	wslStates      func() map[string]string                   // 发行版名 → Running/Stopped（含 docker-desktop）
	wsl            func(args ...string) (string, error)       // wsl.exe 合并输出（UTF-16 已解码）
	tasklistCount  func(image string) (int, error)            // 指定镜像名存活进程数
	taskkillTree   func(image string) error                   // taskkill /IM <image> /T /F（容忍 ENOENT）
	startDesktop   func(path string) error                    // 分离启动 Docker Desktop.exe
	engineReady    func(engineExe string) (bool, string, error) // (就绪, ServerVersion, 错误)
	sleep          func(d time.Duration)                      // 轮询间隔（测试 no-op）
	now            func() time.Time
}

// Service 提供健康检查与自愈（App facade 经字段持有；非 Windows 平台方法返回不支持）。
type Service struct {
	log *logging.Service
	r   runner
}

// NewService 构造真实实现（runner 由平台文件提供）。
func NewService(log *logging.Service) *Service {
	return &Service{log: log, r: newRealRunner()}
}

// newServiceWithRunner 供测试注入假件。
func newServiceWithRunner(log *logging.Service, r runner) *Service {
	return &Service{log: log, r: r}
}

func (s *Service) logInfo(scope, msg, detail string) {
	if s.log != nil {
		s.log.Info(scope, msg, detail)
	}
}

// GetHealth 执行一次全量健康检查（各探测独立容错：单项失败不阻断其余项）。
func (s *Service) GetHealth() HealthReport {
	r := s.r
	rep := HealthReport{CheckedAt: r.now()}
	rep.DockerDesktopPath = r.desktopExePath()
	rep.Available = rep.DockerDesktopPath != ""
	rep.EngineExePath = r.engineExePath()
	rep.DefaultDistro = r.defaultDistro()

	if states := r.wslStates(); states != nil {
		if state, ok := states[toolDistroName]; ok {
			rep.ToolDistroPresent = true
			rep.ToolDistroRunning = strings.EqualFold(state, "Running")
		}
	}

	for _, img := range desktopProcessImages {
		if n, err := r.tasklistCount(img); err == nil {
			rep.DesktopProcessesRunning += n
		}
	}

	// 引擎就绪（docker.exe info；未安装/引擎未起均为 not ready，不视为探测失败）。
	if rep.EngineExePath != "" {
		if ok, ver, err := r.engineReady(rep.EngineExePath); err == nil && ok {
			rep.EngineReady = true
			rep.EngineVersion = ver
		}
	}

	// 集成挂载：经默认用户发行版 stat（/mnt/wsl 跨发行版共享；发行版未运行会自动拉起，
	// 与 CodeBox 会话宿主一致，可接受）。distro 未知 → unknown。
	if rep.DefaultDistro != "" {
		if out, err := r.wsl("-d", rep.DefaultDistro, "--", "stat", "-c", "%s", integrationMountPath); err == nil {
			if n, perr := strconv.ParseInt(strings.TrimSpace(out), 10, 64); perr == nil {
				rep.IntegrationMountBytes = n
				if n > 0 {
					rep.IntegrationMountState = MountOK
				} else {
					rep.IntegrationMountState = MountBroken
				}
			} else {
				rep.IntegrationMountState = MountUnknown
			}
		} else {
			rep.IntegrationMountBytes = -1
			rep.IntegrationMountState = MountAbsent
		}
		if _, err := r.wsl("-d", rep.DefaultDistro, "--", "test", "-e", integrationDockerBin); err == nil {
			rep.DistroDockerBinInjected = true
		}
	} else {
		rep.IntegrationMountBytes = -1
		rep.IntegrationMountState = MountUnknown
	}

	rep.Issues = buildIssues(&rep)
	rep.RecommendSelfHeal = shouldSelfHeal(&rep)
	return rep
}

// buildIssues 依据快照字段生成人读问题清单（纯函数，测试直测）。
func buildIssues(rep *HealthReport) []string {
	var issues []string
	if !rep.Available {
		issues = append(issues, "未检测到 Docker Desktop 安装（常见路径缺失）——无法检查/自愈")
		return issues
	}
	switch rep.IntegrationMountState {
	case MountBroken:
		issues = append(issues, "集成共享挂载已挂坏：docker-desktop-user-distro 为 0 字节空文件（应为二十余 MB 可执行）——WSL 集成将无限弹窗报错，需自愈（全退 Desktop → terminate 工具发行版 → 重启）")
	case MountAbsent:
		if rep.ToolDistroPresent && rep.DesktopProcessesRunning > 0 {
			issues = append(issues, "集成挂载不可见但 Desktop 进程运行中——引擎可能未就绪或集成未建立，可自愈重建")
		}
	case MountUnknown:
		issues = append(issues, "集成挂载状态未知（无默认发行版或探测失败）")
	}
	if !rep.DistroDockerBinInjected && rep.ToolDistroPresent && rep.IntegrationMountState == MountOK {
		issues = append(issues, "发行版内 /usr/bin/docker 未注入——集成代理未运行完成")
	}
	if rep.DesktopProcessesRunning > 0 && !rep.EngineReady {
		issues = append(issues, "Docker Desktop 进程运行中但引擎未就绪（\"GUI 活着\"≠\"引擎活着\"）")
	}
	return issues
}

// shouldSelfHeal 结构化判定是否建议自愈（纯函数）。
func shouldSelfHeal(rep *HealthReport) bool {
	if !rep.Available {
		return false
	}
	if rep.IntegrationMountState == MountBroken {
		return true
	}
	if rep.DesktopProcessesRunning > 0 && !rep.EngineReady {
		return true
	}
	if rep.EngineReady && rep.IntegrationMountState == MountAbsent && rep.ToolDistroPresent {
		return true
	}
	return false
}

// SelfHeal 执行实证自愈序列（§1.2 五步）。幂等可重入；任一关键步失败即停（后续步
// 依赖前者），Success=false 时 Summary 给出失败步与原因。ctx 取消会在轮询边界中止。
func (s *Service) SelfHeal(ctx context.Context) SelfHealReport {
	started := s.r.now()
	report := SelfHealReport{
		StartedAt:    started,
		HealthBefore: s.GetHealth(),
	}
	if !report.HealthBefore.Available {
		report.FinishedAt = s.r.now()
		report.Summary = "未检测到 Docker Desktop 安装，无法自愈"
		return report
	}

	addStep := func(step *SelfHealStep, name, detail string, ok bool, d time.Duration) {
		step.Name = name
		step.Detail = detail
		step.Ok = ok
		step.DurationMs = d.Milliseconds()
		report.Steps = append(report.Steps, *step)
	}
	fail := func(name, detail string, d time.Duration) {
		addStep(&SelfHealStep{}, name, detail, false, d)
		report.FinishedAt = s.r.now()
		report.Success = false
		report.Summary = fmt.Sprintf("自愈在步骤 %s 失败：%s", name, detail)
		s.logInfo("dockerwsl", "自愈失败", report.Summary)
	}

	// ① 全量退出（进程未运行时直接跳过——已是退出态，容忍幂等重入）。
	t0 := s.r.now()
	runningBefore := 0
	for _, img := range desktopProcessImages {
		if n, err := s.r.tasklistCount(img); err == nil {
			runningBefore += n
		}
	}
	exitOK := true
	detail := "进程未运行，跳过退出"
	if runningBefore > 0 {
		for _, img := range desktopProcessImages {
			_ = s.r.taskkillTree(img) // 容忍 ENOENT：进程可能已在退出中
		}
		exited := false
		detail = fmt.Sprintf("已 taskkill %d 个进程", runningBefore)
		deadline := t0.Add(desktopExitDeadline)
		for s.r.now().Before(deadline) {
			if ctx.Err() != nil {
				fail(stepExitDesktop, "等待进程退出时被取消", s.r.now().Sub(t0))
				return report
			}
			n := 0
			for _, img := range desktopProcessImages {
				if c, err := s.r.tasklistCount(img); err == nil {
					n += c
				}
			}
			if n == 0 {
				exited = true
				detail += fmt.Sprintf("，全部退出（耗时 %.1fs）", s.r.now().Sub(t0).Seconds())
				break
			}
			s.r.sleep(desktopExitPollInterval)
		}
		exitOK = exited
		if !exitOK {
			detail += fmt.Sprintf("，%.0fs 内未全部退出（复检请看任务管理器）", desktopExitDeadline.Seconds())
		}
	}
	addStep(&SelfHealStep{}, stepExitDesktop, detail, exitOK, s.r.now().Sub(t0))
	if !exitOK {
		report.FinishedAt = s.r.now()
		report.Success = false
		report.Summary = "自愈失败：Docker Desktop 进程未能全量退出（taskkill 后仍有存活）"
		s.logInfo("dockerwsl", "自愈失败", report.Summary)
		return report
	}

	// ② terminate 工具发行版（仅 docker-desktop；容忍发行版不存在/未运行）。
	t1 := s.r.now()
	_, err := s.r.wsl("--terminate", toolDistroName)
	tdDetail := fmt.Sprintf("wsl.exe --terminate %s 完成（仅工具发行版，用户发行版不受影响）", toolDistroName)
	tdOK := true
	if err != nil {
		// 发行版不存在或本就停止时 wsl.exe 报错——此形态下该步目的已达成。
		tdDetail = fmt.Sprintf("wsl.exe --terminate %s 返回（%v）——发行版不存在或本就停止，视为完成", toolDistroName, err)
	}
	addStep(&SelfHealStep{}, stepTerminateToolDistro, tdDetail, tdOK, s.r.now().Sub(t1))

	// ③ 全新启动 Docker Desktop.exe。
	t2 := s.r.now()
	if err := s.r.startDesktop(report.HealthBefore.DockerDesktopPath); err != nil {
		fail(stepStartDesktop, fmt.Sprintf("启动失败：%v", err), s.r.now().Sub(t2))
		return report
	}
	addStep(&SelfHealStep{}, stepStartDesktop, "已分离启动 Docker Desktop.exe（全新实例）", true, s.r.now().Sub(t2))

	// ④ 轮询引擎就绪（预算 300s/间隔 5s：冷启动 3-4 分钟；terminate 后重启 ≈5s）。
	t3 := s.r.now()
	engineOK := false
	version := ""
	engineDetail := ""
	deadline := t3.Add(enginePollBudget)
	for {
		if ctx.Err() != nil {
			fail(stepWaitEngine, "等待引擎时被取消", s.r.now().Sub(t3))
			return report
		}
		if ok, ver, err := s.r.engineReady(report.HealthBefore.EngineExePath); err == nil && ok {
			engineOK = true
			version = ver
			engineDetail = fmt.Sprintf("引擎就绪（Server %s，耗时 %.1fs）", version, s.r.now().Sub(t3).Seconds())
			break
		}
		if !s.r.now().Before(deadline) {
			engineDetail = fmt.Sprintf("等待 %.0fs 引擎未就绪（冷启动预算耗尽；复检 docker info）", enginePollBudget.Seconds())
			break
		}
		s.r.sleep(enginePollInterval)
	}
	addStep(&SelfHealStep{}, stepWaitEngine, engineDetail, engineOK, s.r.now().Sub(t3))
	if !engineOK {
		report.FinishedAt = s.r.now()
		report.Success = false
		report.Summary = "自愈失败：引擎在冷启动预算内未就绪——请查看 Docker Desktop 界面报错后重试"
		s.logInfo("dockerwsl", "自愈失败", report.Summary)
		return report
	}

	// ⑤ 复核集成：挂载字节数 > 0 + /usr/bin/docker 注入（注入缺失不判失败，但记入 Detail）。
	t4 := s.r.now()
	verify := s.GetHealth()
	verifyDetail := fmt.Sprintf("挂载 %s 字节（状态 %s）", formatBytes(verify.IntegrationMountBytes), verify.IntegrationMountState)
	verifyOK := verify.IntegrationMountState == MountOK
	if !verify.DistroDockerBinInjected {
		verifyDetail += "；/usr/bin/docker 未注入（集成代理仍在建立，可稍后复检）"
	} else {
		verifyDetail += "；/usr/bin/docker 已注入"
	}
	addStep(&SelfHealStep{}, stepVerifyIntegration, verifyDetail, verifyOK, s.r.now().Sub(t4))

	report.FinishedAt = s.r.now()
	report.HealthAfter = verify
	report.Success = verifyOK
	if verifyOK {
		report.Summary = fmt.Sprintf("自愈完成：引擎就绪（Server %s），集成挂载重建健康（%s 字节）", version, formatBytes(verify.IntegrationMountBytes))
	} else {
		report.Summary = "自愈未完全成功：引擎就绪但集成挂载仍未重建——请重试一次或升级 Docker Desktop/WSL"
	}
	s.logInfo("dockerwsl", "自愈结束", report.Summary)
	return report
}

// formatBytes 把字节数格式化为千分位字符串（-1 → "不可见"）。
func formatBytes(n int64) string {
	if n < 0 {
		return "不可见"
	}
	s := strconv.FormatInt(n, 10)
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	if s != "" {
		parts = append([]string{s}, parts...)
	}
	return strings.Join(parts, ",")
}
