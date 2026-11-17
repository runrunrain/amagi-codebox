package sharedext

// concurrency_test.go — 覆盖清单 12：registry 读写竞态（go test -race 下
// 并发 Import/SetEnabled/List/GetAggregatedSkills）。

import (
	"sync"
	"testing"
)

// TestConcurrentRegistryAccess 在同一 Service 上并发执行导入、启停、
// targets 变更、列表与聚合读取，验证互斥锁覆盖全部共享状态。
func TestConcurrentRegistryAccess(t *testing.T) {
	env := newTestService(t)

	src := t.TempDir()
	for i := 0; i < 8; i++ {
		name := skillNameFor(i)
		writeSkillDir(t, src, name, "---\nname: "+name+"\ndescription: 并发技能\n---\n\n正文\n")
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)

	// 前置导入必须真正前置：在并发导入者启动前完成，确保 victim 存在。
	// （原实现把主导入放在并发 goroutine 启动之后，并发者先完成时主导入
	// 全部撞同名冲突，导致间歇性失败与 Fatalf 后 goroutine 继续写临时目录。）
	pre, err := env.svc.ImportSkillFromDir(src)
	if err != nil || len(pre) == 0 {
		t.Fatalf("前置导入失败: %v", err)
	}
	victim := pre[0].Name

	// 并发导入同一批 skill（同名冲突是预期行为，错误不视为失败）。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := env.svc.ImportSkillFromDir(src)
			if err != nil {
				errs <- err
			}
		}()
	}

	// 并发导入 MCP server + 启停。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := "srv-" + string(rune('a'+id))
			if _, _, err := env.svc.ImportMCPServers([]SharedMCPServer{
				{Name: name, Config: map[string]any{"command": "x"}},
			}); err != nil {
				errs <- err
			}
			if err := env.svc.SetSharedMCPServerTargets(name, []Engine{EnginePi}); err != nil {
				errs <- err
			}
			if err := env.svc.SetSharedMCPServerEnabled(name, true); err != nil {
				errs <- err
			}
		}(w)
	}

	// 并发启停已导入 skill（victim 由上方前置导入提供）。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			enabled := id%2 == 0
			if err := env.svc.SetSharedSkillTargets(victim, []Engine{EnginePi, EngineOmp}); err != nil {
				errs <- err
				return
			}
			if err := env.svc.SetSharedSkillEnabled(victim, enabled); err != nil {
				errs <- err
			}
		}(w)
	}

	// 并发读：List + 聚合。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := env.svc.ListSharedSkills(); err != nil {
				errs <- err
			}
			if _, err := env.svc.ListSharedMCPServers(); err != nil {
				errs <- err
			}
			if _, err := env.svc.GetAggregatedSkills(nil, false); err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	// 导入/启停的「条目不存在」类错误源于竞态写顺序，属预期；这里只验证
	// 没有非预期系统性失败（计数超过阈值视为异常）。
	nonTransient := 0
	for err := range errs {
		if err == nil {
			continue
		}
		// 竞态下的 NotFound / 同名冲突是预期结果。
		if contains(err.Error(), "不存在") || contains(err.Error(), "同名") || contains(err.Error(), "跳过") {
			continue
		}
		nonTransient++
		t.Logf("非预期错误: %v", err)
	}
	if nonTransient > 0 {
		t.Fatalf("存在 %d 个非预期错误", nonTransient)
	}

	// 终态一致性：registry 可读且聚合缓存可用。
	if _, err := env.svc.GetAggregatedSkills(nil, true); err != nil {
		t.Fatal(err)
	}
}

func skillNameFor(i int) string {
	return "conc-skill-" + string(rune('a'+i))
}
