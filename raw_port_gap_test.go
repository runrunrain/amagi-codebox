package main

// RC1-0 缺口 A（T0-1 完成度矩阵 §4.2）回归：生产 v1 WS 输入/缩放写入端口接线。
//
// v1 WS 数据面（legacy input / canonical input / resize）的写入目标唯一取自
// adapter.sessRaw.(remote.PTYRawPort)（ws_v1_session.go rawPTYPort）：断言失败 →
// raw==nil → handler 内「raw==nil → return nil」静默丢弃（legacy 假成功、canonical
// 不 ACK、resize 无效）。修复前生产 sessRaw=appSessionRaw 只有 SessionRawPort
// 三方法（StopSession/RemoveSession/ResizeSession），生产数据面是死的；e2e
// harness 的 fakeSessionRaw（e2e/harness/remote-server/main.go:241-323）双接口
// 实现掩盖了该缺口。本文件证明：
//  1. 生产装配（newAppSessionRaw，与 app.go NewRemoteSessionAdapter 接线同一
//     构造）同时满足 SessionRawPort + PTYRawPort——rawPTYPort() 不再返回 nil；
//  2. WriteRaw/ResizeRaw/DetachSession 经真实 pty.Service 数据面真实生效
//     （input 字节到达活 PTY、resize 到达内核 winsize、detach 返回确切收据）。

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/pty"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/session"
)

// TestRC1_0_GapA_ProductionSessionRawCarriesPTYRawPort 证明生产 sessRaw 能通过
// wsV1Connection.rawPTYPort() 的同款类型断言（修复前在此失败 → 静默丢弃）。
func TestRC1_0_GapA_ProductionSessionRawCarriesPTYRawPort(t *testing.T) {
	app := &App{Sessions: session.NewManager(), Pty: pty.NewService(nil)}
	// 与 app.go 生产接线相同的赋值：sessRaw 以 remote.SessionRawPort 存储。
	var sessRaw remote.SessionRawPort = newAppSessionRaw(app)
	// 与 wsV1Connection.rawPTYPort()（ws_v1_session.go）相同的类型断言。
	raw, ok := sessRaw.(remote.PTYRawPort)
	if !ok {
		t.Fatal("生产 sessRaw 未实现 remote.PTYRawPort：v1 WS input/resize 帧将静默丢弃（RC1-0 缺口 A 回归）")
	}
	if raw == nil {
		t.Fatal("rawPTYPort() 返回 nil（RC1-0 缺口 A 回归）")
	}
}

// TestRC1_0_GapA_ProductionRawPortWritesAndResizesRealPTY 用真实 pty.Service 后端
// 驱动生产 raw port：输入字节真实到达活 PTY（终端回显）、resize 真实到达内核
// winsize（回调+尺寸断言）、DetachSession 返回确切后端收据并确认。
func TestRC1_0_GapA_ProductionRawPortWritesAndResizesRealPTY(t *testing.T) {
	// 真实 PTY 后端仅存在于 darwin（creack-pty）与 windows（ConPTY）；其他平台
	// pty.Service 是 stub（无法 spawn），跳过（CI 全量 go test 在 macOS 运行）。
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skipf("real PTY backend unavailable on %s（stub 平台无法验证真实写入）", runtime.GOOS)
	}
	shellPath := "/bin/cat"
	if runtime.GOOS == "windows" {
		shellPath = "cmd.exe"
	}
	svc := pty.NewService(nil)
	app := &App{Sessions: session.NewManager(), Pty: svc}
	var sessRaw remote.SessionRawPort = newAppSessionRaw(app)
	raw, ok := sessRaw.(remote.PTYRawPort)
	if !ok || raw == nil {
		t.Fatal("生产 sessRaw 未实现 remote.PTYRawPort（前置条件失败）")
	}

	const sid = "rc1-0-gap-a-real-pty"
	spec := platform.ResolvedLaunchSpec{
		WorkDir:       t.TempDir(),
		CLI:           platform.ResolvedCLI{Path: shellPath},
		BootstrapMode: platform.BootstrapDirectCommand,
		PTYCols:       80,
		PTYRows:       24,
	}
	evidence, err := svc.StartResolvedWithRunEvidence(sid, spec, struct{}{})
	if err != nil {
		t.Fatalf("start real PTY: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close(sid) })

	output := make(chan string, 16)
	svc.RegisterOutputCallback(sid, "rc1-0-gap-a", func(data []byte) { output <- string(data) })
	t.Cleanup(func() { svc.UnregisterOutputCallback(sid, "rc1-0-gap-a") })
	resized := make(chan [2]int, 4)
	svc.RegisterResizeCallback(sid, "rc1-0-gap-a", func(cols, rows int) { resized <- [2]int{cols, rows} })
	t.Cleanup(func() { svc.UnregisterResizeCallback(sid, "rc1-0-gap-a") })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.WaitReadyForBinding(ctx, sid, evidence.Binding.BindingID()); err != nil {
		t.Fatalf("wait PTY ready: %v", err)
	}

	// 1) Input：经生产 raw port 写入的字节必须到达活 PTY（终端回显即证据）。
	if err := raw.WriteRaw(ctx, sid, []byte("rc1-0-gap-a-write-marker\n")); err != nil {
		t.Fatalf("WriteRaw via production sessRaw: %v", err)
	}
	var observed string
	for !strings.Contains(observed, "rc1-0-gap-a-write-marker") {
		select {
		case chunk := <-output:
			observed += chunk
		case <-ctx.Done():
			t.Fatalf("input marker 未回显（写入未到达真实 PTY），output=%q", observed)
		}
	}

	// 2) Resize：变更后的尺寸必须到达内核 winsize 并触发回调。
	if err := raw.ResizeRaw(ctx, sid, 120, 40); err != nil {
		t.Fatalf("ResizeRaw via production sessRaw: %v", err)
	}
	select {
	case dims := <-resized:
		if dims != [2]int{120, 40} {
			t.Fatalf("resize 回调尺寸 = %v, want [120 40]", dims)
		}
	case <-ctx.Done():
		t.Fatal("resize 回调未触发（缩放未到达真实 PTY）")
	}
	cols, rows, err := svc.GetPtyDimensions(sid)
	if err != nil {
		t.Fatalf("GetPtyDimensions: %v", err)
	}
	if cols != 120 || rows != 40 {
		t.Fatalf("PTY 尺寸 = %dx%d, want 120x40", cols, rows)
	}

	// 3) DetachSession：活后端拆离返回确切后端收据并最终确认。
	receipt, err := raw.DetachSession(sid)
	if err != nil {
		t.Fatalf("DetachSession via production sessRaw: %v", err)
	}
	if receipt == nil {
		t.Fatal("DetachSession 返回 nil receipt")
	}
	if werr := receipt.Wait(ctx); werr != nil {
		t.Fatalf("detach receipt wait: %v", werr)
	}
}
