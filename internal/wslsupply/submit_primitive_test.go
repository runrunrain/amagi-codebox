package wslsupply

// submit_primitive_test.go — S0 diting 前置门槛（M1/M2/M4）的针对性测试：
//   - M1：Compensate 与 Apply 的写路径共用 applyMu 串行化（探测重叠检测）；
//   - M2：普通投影单元 chmod 失败后前像恢复（敏感单元维持删除 fail-closed）；
//   - M4：submitFile 原语在绕开 applyMu 的真并发下唯一临时名成立（直接并发
//     调用私有提交原语 + 注入 chmod 故障验证清理路径亦不撕裂目标）。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/platform"
)

func assertNoStagingLeftover(t *testing.T, dir string) {
	t.Helper()
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(filepath.Base(path), ".amagi-supply-") {
			t.Errorf("staging leftover: %s", path)
		}
		return nil
	})
}

// overlapProbes 在 Chmod 入口检测并发进入（宽窗口 sleep 提高无锁时的可观测性）。
type overlapProbes struct {
	fake    fakeProbes
	active  int32
	overlap int32
}

func (op *overlapProbes) probes() Probes {
	inner := op.fake.probes()
	return Probes{
		Identity: inner.Identity,
		ToUNC:    inner.ToUNC,
		Chmod: func(_ context.Context, _, _ string, _ ...string) error {
			n := atomic.AddInt32(&op.active, 1)
			if n > 1 {
				atomic.StoreInt32(&op.overlap, 1)
			}
			// 宽窗口：无锁时两个写路径在此交错的可观测性最大化。
			time.Sleep(30 * time.Millisecond)
			atomic.AddInt32(&op.active, -1)
			return nil
		},
	}
}

// TestCompensateApplySerialized（M1）：并发 Apply 与 Compensate 不得交错执行
// 写路径——chmod 探测的重叠检测为 0，且终态一致（无撕裂、无 staging 残留）。
func TestCompensateApplySerialized(t *testing.T) {
	op := &overlapProbes{fake: fakeProbes{
		uncRoot:  t.TempDir(),
		identity: platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "m1", OK: true},
	}}
	c := NewCoordinator(t.TempDir(), nil, op.probes())
	c.SetA2Adapter(&stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})

	var content atomic.Value // []byte
	content.Store([]byte(`{"rev":1}`))
	rel := "prefs.json"
	c.RegisterProjection(&stubProjection{unit: "pi/prefs", rel: rel, source: func() []byte {
		return content.Load().([]byte)
	}})

	// 第一次运行建立登记与可撤销句柄（首建：prevExisted=false）。
	res := runUnit(t, c, CLIPi, "pi/prefs")
	if res.Results[0].Outcome != UnitApplied || res.Results[0].Undo == nil {
		t.Fatalf("first run must apply with undo: %+v", res.Results)
	}
	undo := res.Results[0].Undo

	// 真并发：N 轮「Apply 更新 + Compensate 恢复」在栅栏后同时开跑。
	const rounds = 4
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < rounds; i++ {
		wg.Add(2)
		go func(i int) { // Apply 侧：持续更新内容
			defer wg.Done()
			<-start
			content.Store([]byte(fmt.Sprintf(`{"rev":%d}`, i+2)))
			_, _ = c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, []UnitID{"pi/prefs"})
		}(i)
		go func() { // Compensate 侧：CAS 命中时删除首建文件/恢复前像
			defer wg.Done()
			<-start
			_ = c.Compensate(context.Background(), undo)
		}()
	}
	close(start)
	wg.Wait()

	if atomic.LoadInt32(&op.overlap) != 0 {
		t.Fatal("Apply and Compensate write paths must be serialized by applyMu (probe overlap observed)")
	}
	// 终态一致性：撤销句柄来自首建（prevExisted=false），其 CAS 命中会删除
	// 目标——「在场且为某个完整修订」或「不在场」都是一致终态；撕裂内容与
	// staging 残留才是失败。
	target := filepath.Join(op.fake.uncRoot, "home/u/.pi/agent", rel)
	if got, err := os.ReadFile(target); err == nil {
		if !strings.Contains(string(got), `{"rev":`) {
			t.Fatalf("target must hold a complete revision, got %s", got)
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected target read error: %v", err)
	}
	assertNoStagingLeftover(t, filepath.Dir(target))
}

// TestSubmitFileChmodFailureRestoresPreimage（M2）：普通单元更新场景 chmod 失败
// → 前像恢复（用户旧配置可读）；首建场景 → 删除新增（不落半提交）；敏感单元
// 维持删除 fail-closed（宽松权限明文不留盘）。
func TestSubmitFileChmodFailureRestoresPreimage(t *testing.T) {
	prefsPath := func(fp *fakeProbes) string {
		return filepath.Join(fp.uncRoot, "home/u/.pi/agent/prefs.json")
	}

	t.Run("update restores preimage", func(t *testing.T) {
		fp := newFakeProbes(t)
		c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
		src := `{"rev":1}`
		c.RegisterProjection(&stubProjection{unit: "pi/prefs", rel: "prefs.json",
			source: func() []byte { return []byte(src) }, sensitive: false})
		if res := runUnit(t, c, CLIPi, "pi/prefs"); res.Results[0].Outcome != UnitApplied {
			t.Fatalf("setup run must apply: %+v", res.Results)
		}
		src = `{"rev":2}`
		// 目标存在旧前像 rev1，chmod 失败 → 恢复前像 rev1（M2）。
		fp.chmodErr = fmt.Errorf("wsl.exe dead")
		res := runUnit(t, c, CLIPi, "pi/prefs")
		if res.Results[0].Outcome != UnitFailed {
			t.Fatalf("chmod failure must fail the unit, got %+v", res.Results)
		}
		got, readErr := os.ReadFile(prefsPath(fp))
		if readErr != nil || string(got) != `{"rev":1}` {
			t.Fatalf("preimage must be restored on chmod failure, got %q err=%v", got, readErr)
		}
	})

	t.Run("first create removes half-committed file", func(t *testing.T) {
		fp := newFakeProbes(t)
		fp.chmodErr = fmt.Errorf("wsl.exe dead")
		c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
		c.RegisterProjection(&stubProjection{unit: "pi/prefs", rel: "prefs.json",
			source: func() []byte { return []byte(`{"a":1}`) }, sensitive: false})
		res := runUnit(t, c, CLIPi, "pi/prefs")
		if res.Results[0].Outcome != UnitFailed {
			t.Fatalf("chmod failure must fail, got %+v", res.Results)
		}
		if _, statErr := os.Stat(prefsPath(fp)); !os.IsNotExist(statErr) {
			t.Fatal("first-create chmod failure must not leave a half-committed file")
		}
	})

	t.Run("sensitive unit fails before rename keeps preimage", func(t *testing.T) {
		fp := newFakeProbes(t)
		c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
		src := `{"rev":1}`
		c.RegisterProjection(&stubProjection{unit: "pi/prefs", rel: "prefs.json",
			source: func() []byte { return []byte(src) }, sensitive: true})
		if res := runUnit(t, c, CLIPi, "pi/prefs"); res.Results[0].Outcome != UnitApplied {
			t.Fatalf("setup: %+v", res.Results)
		}
		src = `{"rev":2}`
		fp.chmodErr = fmt.Errorf("wsl.exe dead")
		res := runUnit(t, c, CLIPi, "pi/prefs")
		if res.Results[0].Outcome != UnitFailed {
			t.Fatalf("sensitive chmod failure must fail: %+v", res.Results)
		}
		// SA-07（wsl-goal-closure）：敏感 chmod 失败发生在 rename 之前（staging
		// 内收紧 0600 失败即中止）——原有内容（含用户自有字段）原样保留，
		// 不再以删整文件充当 fail-closed；新内容不得落盘。
		got, readErr := os.ReadFile(prefsPath(fp))
		if readErr != nil || string(got) != `{"rev":1}` {
			t.Fatalf("sensitive chmod failure must preserve the preimage untouched, got %q err=%v", got, readErr)
		}
		assertNoStagingLeftover(t, filepath.Join(fp.uncRoot, "home"))
	})
}

// TestSubmitFilePrimitiveTrueConcurrency（M4）：绕开 applyMu 直接并发调用
// submitFile 原语——唯一临时名保证不碰撞、无残留、目标为完整候选之一。
// 同文件并发在 Windows 下允许瞬态共享冲突（SHARING VIOLATION）读回失败：
// 生产同目标写入由 applyMu 串行化；本测钉「唯一名 + 不撕裂 + 无残留」。
func TestSubmitFilePrimitiveTrueConcurrency(t *testing.T) {
	const writers = 8

	t.Run("distinct files never collide", func(t *testing.T) {
		fp := newFakeProbes(t)
		c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
		agentDir := filepath.Join(fp.uncRoot, "home/u/.pi/agent")
		if err := os.MkdirAll(agentDir, 0o700); err != nil {
			t.Fatal(err)
		}
		target := TargetRef{
			Distro: "Ubuntu", CLI: CLIPi, UID: "1000", Home: "/home/u",
			LinuxRoot: "/home/u/.pi/agent", UNCRoot: agentDir,
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make(chan error, writers)
		for i := 0; i < writers; i++ {
			rel := fmt.Sprintf("dir%d/prefs.json", i)
			content := fmt.Sprintf(`{"writer":%d}`, i)
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := c.submitFile(target, rel, []byte(content), false); err != nil {
					errs <- err
				}
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("concurrent submitFile to distinct files (no lock): %v", err)
		}
		for i := 0; i < writers; i++ {
			got, err := os.ReadFile(filepath.Join(agentDir, fmt.Sprintf("dir%d/prefs.json", i)))
			if err != nil || string(got) != fmt.Sprintf(`{"writer":%d}`, i) {
				t.Fatalf("writer %d file missing/corrupt: %q err=%v", i, got, err)
			}
		}
		assertNoStagingLeftover(t, agentDir)
	})

	t.Run("same file unique staging tolerates transient sharing violation", func(t *testing.T) {
		fp := newFakeProbes(t)
		c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
		agentDir := filepath.Join(fp.uncRoot, "home/u/.pi/agent")
		if err := os.MkdirAll(agentDir, 0o700); err != nil {
			t.Fatal(err)
		}
		target := TargetRef{
			Distro: "Ubuntu", CLI: CLIPi, UID: "1000", Home: "/home/u",
			LinuxRoot: "/home/u/.pi/agent", UNCRoot: agentDir,
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make(chan error, writers)
		for i := 0; i < writers; i++ {
			content := fmt.Sprintf(`{"writer":%d}`, i)
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := c.submitFile(target, "prefs.json", []byte(content), false); err != nil {
					errs <- err
				}
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			msg := err.Error()
			// 唯一名被破坏的形态是 CreateTemp/Rename 的 already-exists 类错误。
			if strings.Contains(msg, "already exists") {
				t.Fatalf("unique staging name violated: %v", err)
			}
			t.Logf("tolerated transient under unlocked same-target concurrency: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(agentDir, "prefs.json"))
		if err != nil || !strings.Contains(string(got), `"writer":`) {
			t.Fatalf("target must hold one complete candidate: %q err=%v", got, err)
		}
		assertNoStagingLeftover(t, agentDir)
	})

	t.Run("chmod failure under concurrency stays consistent", func(t *testing.T) {
		fp := newFakeProbes(t)
		fp.chmodErr = fmt.Errorf("wsl.exe dead")
		c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
		agentDir := filepath.Join(fp.uncRoot, "home/u/.pi/agent")
		if err := os.MkdirAll(agentDir, 0o700); err != nil {
			t.Fatal(err)
		}
		target := TargetRef{
			Distro: "Ubuntu", CLI: CLIPi, UID: "1000", Home: "/home/u",
			LinuxRoot: "/home/u/.pi/agent", UNCRoot: agentDir,
		}
		// 预置共享前像：并发失败路径要么恢复它、要么删目标，绝不撕裂。
		if err := os.WriteFile(filepath.Join(agentDir, "prefs.json"), []byte(`{"pre":"image"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 6; i++ {
			content := fmt.Sprintf(`{"broken":%d}`, i)
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, _ = c.submitFile(target, "prefs.json", []byte(content), false)
			}()
		}
		close(start)
		wg.Wait()
		got, err := os.ReadFile(filepath.Join(agentDir, "prefs.json"))
		if err != nil {
			if !os.IsNotExist(err) {
				t.Fatalf("unexpected read error: %v", err)
			}
		} else if string(got) != `{"pre":"image"}` && !strings.Contains(string(got), `"broken":`) {
			t.Fatalf("torn content after concurrent chmod failures: %s", got)
		}
		assertNoStagingLeftover(t, agentDir)
	})
}
