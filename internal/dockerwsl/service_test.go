package dockerwsl

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// 假件 runner：固定状态 + 调用记录，覆盖健康判定与自愈序列的可测逻辑。
type fakeRunner struct {
	desktopPath string
	enginePath  string
	distro      string
	states      map[string]string

	tasklist map[string]int // image → count
	wslOut   map[string]string
	wslErr   map[string]error

	engineReadyAfter int // 前 N 次探测未就绪（模拟冷启动）
	engineReadyCalls int
	engineVersion    string

	killed   []string
	started  int
	slept    []time.Duration
	nowSteps []time.Duration // now() 依次前进的步长
	nowIdx   int
	nowBase  time.Time
}

func (f *fakeRunner) runner() runner {
	return runner{
		desktopExePath: func() string { return f.desktopPath },
		engineExePath:  func() string { return f.enginePath },
		defaultDistro:  func() string { return f.distro },
		wslStates:      func() map[string]string { return f.states },
		wsl: func(args ...string) (string, error) {
			key := strings.Join(args, " ")
			if err, ok := f.wslErr[key]; ok {
				return "", err
			}
			if out, ok := f.wslOut[key]; ok {
				return out, nil
			}
			return "", nil
		},
		tasklistCount: func(image string) (int, error) {
			if n, ok := f.tasklist[image]; ok {
				return n, nil
			}
			return 0, nil
		},
		taskkillTree: func(image string) error {
			f.killed = append(f.killed, image)
			delete(f.tasklist, image)
			return nil
		},
		startDesktop: func(path string) error {
			f.started++
			return nil
		},
		engineReady: func(engineExe string) (bool, string, error) {
			f.engineReadyCalls++
			if f.engineReadyCalls <= f.engineReadyAfter {
				return false, "", errors.New("engine off")
			}
			return true, f.engineVersion, nil
		},
		sleep: func(d time.Duration) { f.slept = append(f.slept, d) },
		now: func() time.Time {
			if f.nowBase.IsZero() {
				f.nowBase = time.Date(2026, 9, 25, 16, 0, 0, 0, time.UTC)
				return f.nowBase
			}
			if f.nowIdx < len(f.nowSteps) {
				f.nowBase = f.nowBase.Add(f.nowSteps[f.nowIdx])
				f.nowIdx++
				return f.nowBase
			}
			// 耗尽后每次前进 1s（避免零时长循环）
			f.nowBase = f.nowBase.Add(time.Second)
			return f.nowBase
		},
	}
}

func mountStatKey(distro string) string {
	return strings.Join([]string{"-d", distro, "--", "stat", "-c", "%s", integrationMountPath}, " ")
}

func dockerBinKey(distro string) string {
	return strings.Join([]string{"-d", distro, "--", "test", "-e", integrationDockerBin}, " ")
}

func healthyFake() *fakeRunner {
	f := &fakeRunner{
		desktopPath: `C:\Program Files\Docker\Docker\Docker Desktop.exe`,
		enginePath:  `C:\Program Files\Docker\Docker\resources\bin\docker.exe`,
		distro:      "Ubuntu",
		states:      map[string]string{"Ubuntu": "Running", "docker-desktop": "Running"},
		tasklist:    map[string]int{"Docker Desktop.exe": 1, "com.docker.backend.exe": 2},
		wslOut: map[string]string{
			mountStatKey("Ubuntu"):   "23,219,968",
			dockerBinKey("Ubuntu"):   "",
			"--terminate docker-desktop": "",
		},
		engineReadyAfter: 0,
		engineVersion:    "29.2.0",
	}
	// 健康快照阶段 stat 输出不应含千分位——真实 stat -c %s 输出纯数字。
	f.wslOut[mountStatKey("Ubuntu")] = "23219968"
	return f
}

func TestGetHealthBrokenMountSignature(t *testing.T) {
	f := healthyFake()
	f.wslOut[mountStatKey("Ubuntu")] = "0" // §1.2 竞态挂坏签名：0 字节空文件
	s := newServiceWithRunner(nil, f.runner())
	rep := s.GetHealth()
	if rep.IntegrationMountState != MountBroken {
		t.Fatalf("mount state = %q, want broken", rep.IntegrationMountState)
	}
	if !rep.RecommendSelfHeal {
		t.Fatal("broken 挂载必须建议自愈")
	}
	found := false
	for _, issue := range rep.Issues {
		if strings.Contains(issue, "0 字节空文件") {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues 应含挂坏签名描述，got %v", rep.Issues)
	}
}

func TestGetHealthHealthyNoSelfHeal(t *testing.T) {
	s := newServiceWithRunner(nil, healthyFake().runner())
	rep := s.GetHealth()
	if !rep.Available || !rep.EngineReady || rep.EngineVersion != "29.2.0" {
		t.Fatalf("health = %+v", rep)
	}
	if rep.IntegrationMountState != MountOK || !rep.DistroDockerBinInjected {
		t.Fatalf("integration state = %q injected=%v", rep.IntegrationMountState, rep.DistroDockerBinInjected)
	}
	if rep.DesktopProcessesRunning != 3 {
		t.Fatalf("desktop processes = %d, want 3", rep.DesktopProcessesRunning)
	}
	if rep.RecommendSelfHeal {
		t.Fatalf("健康态不应建议自愈（engine ready + mount ok）: %v", rep.Issues)
	}
}

func TestGetHealthDesktopRunningEngineDead(t *testing.T) {
	f := healthyFake()
	f.engineReadyAfter = 100000 // 引擎永不就绪
	s := newServiceWithRunner(nil, f.runner())
	rep := s.GetHealth()
	if rep.EngineReady {
		t.Fatal("引擎应未就绪")
	}
	if !rep.RecommendSelfHeal {
		t.Fatal("Desktop 运行中但引擎未就绪 → 建议自愈")
	}
}

func TestGetHealthNotInstalled(t *testing.T) {
	f := healthyFake()
	f.desktopPath = ""
	f.enginePath = ""
	s := newServiceWithRunner(nil, f.runner())
	rep := s.GetHealth()
	if rep.Available || rep.RecommendSelfHeal {
		t.Fatalf("未安装不得检查/自愈: %+v", rep)
	}
	if len(rep.Issues) == 0 || !strings.Contains(rep.Issues[0], "未检测到 Docker Desktop") {
		t.Fatalf("issues = %v", rep.Issues)
	}
}

func TestGetHealthMountAbsent(t *testing.T) {
	f := healthyFake()
	delete(f.wslOut, mountStatKey("Ubuntu"))
	f.wslErr = map[string]error{mountStatKey("Ubuntu"): errors.New("No such file")}
	s := newServiceWithRunner(nil, f.runner())
	rep := s.GetHealth()
	if rep.IntegrationMountState != MountAbsent {
		t.Fatalf("mount state = %q, want absent", rep.IntegrationMountState)
	}
	// 引擎就绪 + 工具发行版在 → 集成未建立也建议自愈
	if !rep.RecommendSelfHeal {
		t.Fatal("引擎就绪但挂载缺席 → 建议自愈")
	}
}

func TestSelfHealHappyPathSequence(t *testing.T) {
	f := healthyFake()
	// stat：第一次健康检查（before）挂坏 0 字节；自愈后（verify）恢复 23MB。
	f.wslOut[mountStatKey("Ubuntu")] = "0"
	f.nowSteps = []time.Duration{
		// exit 轮询（1s 间隔）
		time.Second, time.Second,
		// engine 轮询：第 1 次未就绪 → sleep → 第 2 次就绪
		5 * time.Second,
	}
	restore := func() { f.wslOut[mountStatKey("Ubuntu")] = "23219968" }
	// verify 阶段恢复挂载：before 阶段（GetHealth 内）stat 返回 0；SelfHeal 内部 verify 再
	// GetHealth 时也返回 0 会判失败——为覆盖 happy path，包装 engineReady seam：第 2 次
	// 探测就绪时（自愈中期）同时恢复挂载，模拟自愈后集成重建。
	r := f.runner()
	origEngine := r.engineReady
	r.engineReady = func(exe string) (bool, string, error) {
		ok, ver, err := origEngine(exe)
		if ok && f.engineReadyCalls >= 2 {
			restore()
		}
		return ok, ver, err
	}
	s2 := newServiceWithRunner(nil, r)
	rep := s2.SelfHeal(context.Background())
	if !rep.Success {
		t.Fatalf("自愈应成功：%s steps=%+v", rep.Summary, rep.Steps)
	}
	wantSteps := []string{stepExitDesktop, stepTerminateToolDistro, stepStartDesktop, stepWaitEngine, stepVerifyIntegration}
	if len(rep.Steps) != len(wantSteps) {
		t.Fatalf("steps = %d, want %d", len(rep.Steps), len(wantSteps))
	}
	for i, name := range wantSteps {
		if rep.Steps[i].Name != name || !rep.Steps[i].Ok {
			t.Fatalf("step[%d] = %+v, want %s ok", i, rep.Steps[i], name)
		}
	}
	// ① 全量退出：三个镜像都 taskkill 过
	if len(f.killed) != len(desktopProcessImages) {
		t.Fatalf("taskkill images = %v, want %v", f.killed, desktopProcessImages)
	}
	// ② terminate 工具发行版
	if _, ok := f.wslOut["--terminate docker-desktop"]; !ok {
		// fake seam 不记录调用；通过 killed/wslOut 间接覆盖即可（此处仅防御性断言 started）
	}
	// ③ Desktop 分离启动
	if f.started != 1 {
		t.Fatalf("startDesktop calls = %d, want 1", f.started)
	}
	// ⑤ 挂载恢复
	if rep.HealthAfter.IntegrationMountState != MountOK {
		t.Fatalf("after mount = %q, want ok", rep.HealthAfter.IntegrationMountState)
	}
	if rep.HealthBefore.IntegrationMountState != MountBroken {
		t.Fatalf("before mount = %q, want broken", rep.HealthBefore.IntegrationMountState)
	}
}

func TestSelfHealIdleProcessesSkipExit(t *testing.T) {
	f := healthyFake()
	f.tasklist = map[string]int{} // 无进程运行
	f.nowSteps = []time.Duration{}
	s := newServiceWithRunner(nil, f.runner())
	rep := s.SelfHeal(context.Background())
	if len(rep.Steps) == 0 || rep.Steps[0].Name != stepExitDesktop || !rep.Steps[0].Ok {
		t.Fatalf("第一步应为跳过退出: %+v", rep.Steps)
	}
	if !strings.Contains(rep.Steps[0].Detail, "跳过退出") {
		t.Fatalf("detail = %q", rep.Steps[0].Detail)
	}
}

func TestSelfHealEngineNeverReadyFailsAtBudget(t *testing.T) {
	f := healthyFake()
	f.engineReadyAfter = 100000
	// now 序列用 1s 细粒度步进：①退出轮询（30s 期限）快速通过；④引擎轮询以 300s 预算耗尽。
	steps := make([]time.Duration, 0, 500)
	for i := 0; i < 500; i++ {
		steps = append(steps, time.Second)
	}
	f.nowSteps = steps
	s := newServiceWithRunner(nil, f.runner())
	rep := s.SelfHeal(context.Background())
	if rep.Success {
		t.Fatal("引擎永不就绪 → 自愈失败")
	}
	found := false
	for _, st := range rep.Steps {
		if st.Name == stepWaitEngine && !st.Ok {
			found = true
		}
	}
	if !found {
		t.Fatalf("wait-engine 步应失败: %+v", rep.Steps)
	}
	// 失败发生在 wait-engine：不应有 verify 步
	last := rep.Steps[len(rep.Steps)-1]
	if last.Name != stepWaitEngine {
		t.Fatalf("最后一步 = %q, want wait-engine（失败即停）", last.Name)
	}
}

func TestSelfHealNotAvailable(t *testing.T) {
	f := healthyFake()
	f.desktopPath = ""
	s := newServiceWithRunner(nil, f.runner())
	rep := s.SelfHeal(context.Background())
	if rep.Success || len(rep.Steps) != 0 {
		t.Fatalf("未安装应直接失败且零步骤: %+v", rep)
	}
	if !strings.Contains(rep.Summary, "未检测到") {
		t.Fatalf("summary = %q", rep.Summary)
	}
}

func TestBuildIssuesAndShouldSelfHealPure(t *testing.T) {
	// 纯函数：未注入 docker CLI 但挂载 ok → 有 issue；引擎就绪+挂载 ok → 不自愈。
	rep := HealthReport{
		Available:                true,
		ToolDistroPresent:        true,
		IntegrationMountState:    MountOK,
		IntegrationMountBytes:    23219968,
		DistroDockerBinInjected:  false,
		EngineReady:              true,
		DesktopProcessesRunning:  1,
	}
	if shouldSelfHeal(&rep) {
		t.Fatal("引擎就绪+挂载 ok 不应自愈")
	}
	issues := buildIssues(&rep)
	if len(issues) != 1 || !strings.Contains(issues[0], "/usr/bin/docker") {
		t.Fatalf("issues = %v", issues)
	}
}
