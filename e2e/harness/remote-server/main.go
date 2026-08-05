// e2e/harness/remote-server — M1-D2 真服务器配对 E2E harness（TEST-ONLY）。
// ---------------------------------------------------------------------------
// 本二进制是**测试工具**，不是生产入口：
//
//	· 不参与 wails build / 生产二进制（独立 main 包，仅供 e2e spec 以
//	  `go build ./e2e/harness/remote-server` 编译后由 Playwright 拉起）。
//	· 被测面（数据面）按 app.go 同款生产装配：NewServerWithSecurity +
//	  NewProductionSecurityOptions（真实文件 Device Store + durable
//	  security event sink + LoadSecurityState）+ 真实 CLIResolver 构造
//	  HostSummary provider + SetWebRoot 同源伺服 mobile/dist。
//	  数据面不改写、不绕过任何 production guard（Host/Origin/auth 策略全开）。
//	· 控制面（/control/*）是**测试专用**辅助通道：仅监听 127.0.0.1 随机端口，
//	  调用与桌面端 Wails wrapper 完全相同的 Server 导出 API
//	  （CreatePairingWindow/CancelPairingWindow/ListDevices/RevokeDevice），
//	  作用等同测试代替"桌面端用户点击"。confirmTerminalExposure / revoke
//	  confirm 由控制面固定为 true —— 这是在扮演桌面用户确认动作，不是绕过
//	  服务端校验（服务端仍执行全部校验）。控制面绝不进入生产二进制。
//	· config 根为进程私有 temp dir（退出时清理），不触碰 ~/.amagi-codebox。
//
// 启动后 stdout 打印一行就绪信号供 Playwright 解析：
//
//	HARNESS_READY {"origin":"http://127.0.0.1:P","controlOrigin":"http://127.0.0.1:C"}
//
// ---------------------------------------------------------------------------
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
)

// serverVersion 标识 harness 数据面身份（mobile 大厅占位页会展示它，
// 使真服务器证据与 route-mock 证据在截图/投影上可区分）。
const serverVersion = "1.0.5-e2e-real-harness"

// emptyAssets：harness 以 SetWebRoot 伺服磁盘上的 mobile/dist（优先级 1），
// 不嵌资源（生产二进制走 embed，两者经同一 serveStaticOrSPA 代码路径）。
var emptyAssets embed.FS

// buildHostSummary 复刻 app.go hostSummaryFromResolver 的生产逻辑：
// 遍历 contract.KnownCLITypes，对每项调用真实 CLIResolver.Resolve，仅把
// bool 放入 DTO；不调用 ResolveExecutable、不外露 path/diagnostics。
func buildHostSummary(resolver platform.CLIResolver) remote.HostSummaryFunc {
	return func() (contract.HostSummary, error) {
		avail := make([]contract.CLIAvailability, 0, len(contract.KnownCLITypes))
		for _, cliType := range contract.KnownCLITypes {
			spec, err := resolver.Resolve(platform.ResolveRequest{
				AppType:    string(cliType),
				LaunchMode: string(session.ModeEmbedded),
				Env:        os.Environ(),
			})
			available := err == nil && spec.CLI.Path != ""
			avail = append(avail, contract.CLIAvailability{CLIType: cliType, Available: available})
		}
		return contract.HostSummary{
			APIVersion:      contract.APIVersionV1,
			ServerVersion:   serverVersion,
			CLIAvailability: avail,
		}, nil
	}
}

// pickLoopbackPort 向内核申请一个空闲 loopback 端口（随机关联，避免 spec
// 并行 worker 之间端口冲突）。返回后短暂关闭监听，存在微小竞态，E2E 可接受。
func pickLoopbackPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}

// ---------------------------------------------------------------------------
// M2-INT session 接线（TEST-ONLY）：受控 fake CLI + 真实 adapter 装配
// ---------------------------------------------------------------------------
//
// 本节是 M2-INT 的核心扩展：按 app.go NewApp 同款装配真实 RemoteSessionAdapter
// （ControlRuntime / SessionCatalog / SessionStreamStore / SessionOperationJournal /
// RemoteLaunchResolver / LaunchRawPort / SessionRawPort），并 SetSessionAdapter
// 注册到数据面 Server，使 v1 session REST index 2-9 + /ws/v1 在 harness 内全激活。
//
// 与生产的唯一差异在两个 seam：
//   · RemoteLaunchResolver → fakeRemoteLaunchResolver：不查找真实 CLI 二进制，
//     对四类已知 CLI 一律解析成功，返回指向 fake CLI 的 recipe + sentinel spec。
//   · LaunchRawPort/SessionRawPort → fakeLaunchRaw/fakeSessionRaw：不启动真实
//     进程/PTY，仅记录会话生命周期（Start/Stop/Remove/Resize 全部记账）。
//
// 输出注入走真实 M2/H3 目的地（SessionStreamStore.AppendOutput 分配 v1 Seq +
// 回放环；SessionEventHub.ReserveRunRecordUnderState + PublishReserved 经真实
// 因果账本投递给已 attach 的 WS 订阅者）。这是生产 H1→pump 写入的同一目的地；
// harness 从控制面驱动它，因为远端启动的 PTY 输出尚未经 run-scoped projector
// 路由（control_wiring.go appLaunchRaw 已披露的 wiring 残留）。真实四类 CLI
// 本机端到端（真二进制→真 PTY→完整 run-observation 路径）属 M4/最终验收。

// fakeRemoteLaunchResolver 是 harness 提供的确定性 RemoteLaunchResolver：对四类
// 已知 CLI 一律解析成功，返回 fake CLI recipe + sentinel spec（不查找真实二进制）。
type fakeRemoteLaunchResolver struct {
	homeDir string
}

// fakeCLISpec 是 fake CLI 的 sentinel spec（fakeLaunchRaw 不消费它，仅类型占位）。
type fakeCLISpec struct{}

func knownCLI(cli contract.CLIType) bool {
	for _, k := range contract.KnownCLITypes {
		if k == cli {
			return true
		}
	}
	return false
}

func (r *fakeRemoteLaunchResolver) ResolveCreate(_ context.Context, req contract.CreateSessionRequest) (remote.RemoteLaunchResolution, *remote.LaunchResolveFailure) {
	if !knownCLI(req.CLIType) {
		return remote.RemoteLaunchResolution{}, &remote.LaunchResolveFailure{Kind: remote.LaunchResolveFailureContext, CLIType: req.CLIType}
	}
	workdir := r.homeDir
	if req.Workdir != nil && *req.Workdir != "" {
		workdir = *req.Workdir
	}
	return remote.RemoteLaunchResolution{
		Recipe: remote.RemoteLaunchRecipe{CLIType: req.CLIType, Workdir: workdir},
		Spec:   fakeCLISpec{},
	}, nil
}

func (r *fakeRemoteLaunchResolver) ResolveRestart(_ context.Context, recipe remote.RemoteLaunchRecipe) (remote.RemoteLaunchResolution, *remote.LaunchResolveFailure) {
	if !knownCLI(recipe.CLIType) {
		return remote.RemoteLaunchResolution{}, &remote.LaunchResolveFailure{Kind: remote.LaunchResolveFailureContext, CLIType: recipe.CLIType}
	}
	return remote.RemoteLaunchResolution{Recipe: recipe, Spec: fakeCLISpec{}}, nil
}

func (r *fakeRemoteLaunchResolver) Probe(_ context.Context, cli contract.CLIType) (contract.CLIAvailability, *remote.LaunchResolveFailure) {
	// harness 宣称四类 CLI 全可用，使大厅启动器全部可点（真实 availability 由
	// HostSummary 经 buildHostSummary + 真实 resolver 提供，与本 Probe 解耦）。
	return contract.CLIAvailability{CLIType: cli, Available: knownCLI(cli)}, nil
}

// fakeSessionRegistry 记录 fake CLI 会话生命周期（Start/Stop/Remove/Resize），
// 供控制面/测试观测，不启动任何真实进程。
type fakeSessionRegistry struct {
	mu       sync.Mutex
	sessions map[string]string // sessionID → state（running/stopped/removed）
}

func newFakeSessionRegistry() *fakeSessionRegistry {
	return &fakeSessionRegistry{sessions: make(map[string]string)}
}

func (r *fakeSessionRegistry) start(id string)  { r.set(id, "running") }
func (r *fakeSessionRegistry) stop(id string)   { r.set(id, "stopped") }
func (r *fakeSessionRegistry) remove(id string) { r.set(id, "removed") }
func (r *fakeSessionRegistry) set(id, state string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[id] = state
}
func (r *fakeSessionRegistry) state(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[id]
}

// fakeLaunchRaw 实现 remote.LaunchRawPort：StartProcess 仅在 registry 记账，
// 不启动真实进程（已在 gate DoLaunchEffect 回调内，gate 许可已获取）。
type fakeLaunchRaw struct {
	reg *fakeSessionRegistry
}

func (f fakeLaunchRaw) StartProcess(_ context.Context, sessionID contract.SessionID, _ remote.RemoteLaunchRecipe, _ any, _ *remote.RunObservationPermit) error {
	f.reg.start(string(sessionID))
	return nil
}

// fakeSessionRaw 实现 remote.SessionRawPort：Stop/Remove/Resize 仅记账。
// M3-C：同时实现 remote.PTYRawPort（WriteRaw 计数 + ResizeRaw 记账 +
// DetachSession 桩），使 canonical input 路径真实走 ledger→raw→ACK，
// 并让整形 E2E 能断言 rawInput 调用次数（输入 0 重复的机器 oracle）。
//
// M3-007 C5b（R2 证据修复）：ResizeRaw 支持 test-only barrier——当 barrier armed
// 时阻塞，直到 release 或 ctx 取消（fence/timeout）。用于证明 A 的 resize 真实
// 到达 server、进入 gate（lane+permit+checkpoint 通过）、停在 raw port in-flight，
// 再被 desktop take fence 取消 ctx → raw port 见 ctx.Done() 返回 error → 不 commit。
// barrier 是 TEST-ONLY（不进生产）；hit 计数供 E2E 断言 resize 确实到达 raw port。
// WriteRaw 不需 barrier（C5b 只针对 resize checkpoint 场景）。
type fakeSessionRaw struct {
	reg *fakeSessionRegistry

	ioMu           sync.Mutex
	writeCount     map[string]int
	writeBytes     map[string]int
	resizeCount    map[string]int
	lastResizeCols map[string]int // TEST-ONLY：最后一次 ResizeRaw 的 cols（C5b rawResize=[desktop-dims] oracle）
	lastResizeRows map[string]int // TEST-ONLY：最后一次 ResizeRaw 的 rows
	// M3-007 C2b（R3 冻结 oracle）：不可逆 FIFO 顺序摘要。每次 WriteRaw 以
	// rolling SHA-256 链累积（chain_i = sha256(chain_{i-1} || payload_i)），只存
	// hex 摘要不存原文（隐私：与 network.ts HMAC 摘要 posture 一致；生产 raw port
	// 不存任何 payload）。测试侧据已知 FIFO 序列独立复算同一链比对，证明 raw port
	// 见到 32 项的顺序与客户端入队一致（不乱序、不丢）。per-process 全局（单用例独占）。
	writeOrderChain string

	// C5b test-only resize barrier（per-process 全局；E2E 单用例独占 harness 进程）。
	barrierMu         sync.Mutex
	resizeBarrier     chan struct{} // non-nil = armed；close 释放
	resizeBarrierHits atomic.Int32
}

func newFakeSessionRaw(reg *fakeSessionRegistry) *fakeSessionRaw {
	return &fakeSessionRaw{
		reg:            reg,
		writeCount:     make(map[string]int),
		writeBytes:     make(map[string]int),
		resizeCount:    make(map[string]int),
		lastResizeCols: make(map[string]int),
		lastResizeRows: make(map[string]int),
	}
}

func (f *fakeSessionRaw) StopSession(_ context.Context, sessionID contract.SessionID) error {
	f.reg.stop(string(sessionID))
	return nil
}
func (f *fakeSessionRaw) RemoveSession(_ context.Context, sessionID contract.SessionID) error {
	f.reg.remove(string(sessionID))
	return nil
}
func (f *fakeSessionRaw) ResizeSession(_ context.Context, sessionID contract.SessionID, _, _ int) error {
	return nil // fake CLI 无真实 PTY 尺寸
}

// WriteRaw 实现 remote.PTYRawPort：不启动真实进程，仅按会话计数（幂等断言 oracle）。
// M3-007 C2b（R3 冻结 oracle）：同步累积不可逆 FIFO 顺序摘要（rolling SHA-256），
// 证明 raw port 见到 N 项的顺序与客户端 FIFO 入队一致（不依赖客户端自报顺序）。
func (f *fakeSessionRaw) WriteRaw(_ context.Context, sessionID string, data []byte) error {
	f.ioMu.Lock()
	f.writeCount[sessionID]++
	f.writeBytes[sessionID] += len(data)
	sum := sha256.Sum256([]byte(f.writeOrderChain + string(data)))
	f.writeOrderChain = hex.EncodeToString(sum[:])
	f.ioMu.Unlock()
	return nil
}

// ResizeRaw 实现 remote.PTYRawPort：计数并记录最后一次尺寸（不产生真实 PTY 副作用）。
// C5b test-only barrier：当 armed 时阻塞，直到 release 或 ctx 取消。ctx 取消
// （fence/timeout）时返回 ctx.Err() 且 **不计数**——证明 in-flight resize 被
// fence 阻止 commit。这模拟一个 ctx-aware raw port（真实 PTY ioctl 极快且
// 不可中断，但 test fake 以 barrier + ctx 检查呈现「in-flight 被 fence」语义）。
func (f *fakeSessionRaw) ResizeRaw(ctx context.Context, sessionID string, cols, rows int) error {
	f.barrierMu.Lock()
	ch := f.resizeBarrier
	f.barrierMu.Unlock()
	if ch != nil {
		f.resizeBarrierHits.Add(1)
		select {
		case <-ch:
			// barrier 释放：继续记录（未被 fence）。
		case <-ctx.Done():
			// fence/timeout 取消了 operation ctx：raw port 中止，不 commit。
			return ctx.Err()
		}
	}
	f.ioMu.Lock()
	f.resizeCount[sessionID]++
	f.lastResizeCols[sessionID] = cols
	f.lastResizeRows[sessionID] = rows
	f.ioMu.Unlock()
	return nil
}

// armResizeBarrier 装载 test-only resize barrier（TEST-ONLY，C5b）。
// 幂等：重复 arm 不覆盖既有 barrier。
func (f *fakeSessionRaw) armResizeBarrier() {
	f.barrierMu.Lock()
	defer f.barrierMu.Unlock()
	if f.resizeBarrier == nil {
		f.resizeBarrier = make(chan struct{})
	}
}

// releaseResizeBarrier 释放 test-only resize barrier（TEST-ONLY，C5b）。
// 幂等：未 armed 时为 no-op。释放后 ResizeRaw 不再阻塞（直到再次 arm）。
func (f *fakeSessionRaw) releaseResizeBarrier() {
	f.barrierMu.Lock()
	defer f.barrierMu.Unlock()
	if f.resizeBarrier != nil {
		close(f.resizeBarrier)
		f.resizeBarrier = nil
	}
}

// fakeDetachReceipt 是 DetachSession 的立即确认收据（fake CLI 无真实后端）。
type fakeDetachReceipt struct{ id uint64 }

func (r fakeDetachReceipt) Identity() uint64             { return r.id }
func (r fakeDetachReceipt) Confirmed() bool              { return true }
func (r fakeDetachReceipt) LastError() error             { return nil }
func (r fakeDetachReceipt) Wait(_ context.Context) error { return nil }

// DetachSession 实现 remote.PTYRawPort：fake CLI 无后端可拆，返回立即确认收据。
func (f *fakeSessionRaw) DetachSession(_ string) (remote.BackendDetachReceipt, error) {
	return fakeDetachReceipt{id: 1}, nil
}

// rawIOSnapshot 返回会话的 raw IO 计数（TEST-ONLY 控制面断言用）。
// M3-007 C5b：含 resizeBarrierHits（全局，证明 resize 到达 raw port in-flight）。
// M3-007 C2b（R3）：含 writeOrderChain（不可逆 FIFO 顺序摘要，证明 N 项 drain 顺序）。
func (f *fakeSessionRaw) rawIOSnapshot(sessionID string) map[string]any {
	f.ioMu.Lock()
	defer f.ioMu.Unlock()
	return map[string]any{
		"writeCount":        f.writeCount[sessionID],
		"writeBytes":        f.writeBytes[sessionID],
		"resizeCount":       f.resizeCount[sessionID],
		"lastResizeCols":    f.lastResizeCols[sessionID],
		"lastResizeRows":    f.lastResizeRows[sessionID],
		"resizeBarrierHits": int(f.resizeBarrierHits.Load()),
		"writeOrderChain":   f.writeOrderChain,
	}
}

// controlState 汇集控制面（TEST-ONLY）所需的全部句柄：数据面 Server、注入用
// adapter/runtime、数据面 origin（控制面以真实 HTTP 调用数据面 pairing/create），
// 以及懒装配的“控制设备”凭据 + 每会话因果源序号。
type controlState struct {
	srv        *remote.Server
	adapter    *remote.RemoteSessionAdapter
	dataOrigin string
	reg        *fakeSessionRegistry
	sessRaw    *fakeSessionRaw // M3-C：raw IO 计数（输入幂等 oracle）

	devMu       sync.Mutex
	ctlCookie   string // 控制设备的 device cookie 值（同源 loopback HTTP 携带）
	ctlDeviceID string

	srcMu       sync.Mutex
	srcCounters map[string]int // sessionID → 已注入因果源序号（单调）
}

// injectOutput 经真实 M2/H3 路径注入一条输出：SessionStreamStore 分配 v1 Seq +
// 回放环；SessionEventHub 预留因果票据并 PublishReserved 投递给已 attach 的
// WS 订阅者（真实因果 drain loop → socket）。
func (st *controlState) injectOutput(sessionID contract.SessionID, data []byte) contract.Seq {
	rt := st.adapter.Runtime()
	seq := st.adapter.Streams().AppendOutput(sessionID, data)
	if rt == nil {
		return seq
	}
	st.srcMu.Lock()
	st.srcCounters[string(sessionID)]++
	src := st.srcCounters[string(sessionID)]
	st.srcMu.Unlock()
	pos := remote.RunCausalPosition{SegmentID: 1, Source: remote.RunSourceOrdinal(src)}
	// SetRunPos 使 stream.runPos 与因果水位 watermark.Run 同步推进：attach 的
	// syncFeedAndAttachCausal 比较 expectedPos(=stream.runPos) 与 watermark.Run，
	// 两者必须一致（生产中由 pump 从 feed 同步；harness 直接注入须手动同步）。
	st.adapter.Streams().SetRunPos(sessionID, pos)
	ticket, err := rt.Hub().ReserveRunRecordUnderState(sessionID, pos, remote.CausalReplay)
	if err != nil {
		return seq // 预留失败：回放环仍已更新（attach/backfill 可见），best-effort
	}
	rt.Hub().PublishReserved(ticket, contract.OutputEvent{
		Type:      contract.ServerEventTypeOutput,
		SessionID: sessionID,
		Seq:       seq,
		Chunk:     base64.StdEncoding.EncodeToString(data),
	})
	st.adapter.Catalog().TouchActivity(sessionID, time.Now())
	return seq
}

// injectBoundary 经真实 M2/H3 路径注入一条重启边界：AppendBoundary 分配 Seq +
// 回放环；因果预留 + PublishReserved 投递 session.state(restartBoundary) 帧。
func (st *controlState) injectBoundary(sessionID contract.SessionID) contract.Seq {
	rt := st.adapter.Runtime()
	seq := st.adapter.Streams().AppendBoundary(sessionID)
	if rt == nil {
		return seq
	}
	st.srcMu.Lock()
	st.srcCounters[string(sessionID)]++
	src := st.srcCounters[string(sessionID)]
	st.srcMu.Unlock()
	pos := remote.RunCausalPosition{SegmentID: 1, Source: remote.RunSourceOrdinal(src)}
	st.adapter.Streams().SetRunPos(sessionID, pos)
	ticket, err := rt.Hub().ReserveRunRecordUnderState(sessionID, pos, remote.CausalReplay)
	if err != nil {
		return seq
	}
	rt.Hub().PublishReserved(ticket, contract.SessionRestartBoundaryEvent{
		Type:            contract.ServerEventTypeSessionState,
		SessionID:       sessionID,
		State:           contract.SessionStateRunning,
		RestartBoundary: true,
		Seq:             seq,
		OccurredAt:      time.Now().UTC().Format(time.RFC3339Nano),
	})
	return seq
}

// ensureControlDevice 懒装配一个“控制设备”：开窗 → 以真实 loopback HTTP
// POST /pairing/complete 完成配对 → 捕获 Set-Cookie。后续 create/stop 等经
// 真实 REST 调用数据面时携带该 Cookie（与桌面 Wails 边界同级真实 auth 路径）。
func (st *controlState) ensureControlDevice() error {
	st.devMu.Lock()
	defer st.devMu.Unlock()
	if st.ctlCookie != "" {
		return nil
	}
	info, err := st.srv.CreatePairingWindow(true)
	if err != nil {
		return fmt.Errorf("open pairing window: %w", err)
	}
	body, _ := json.Marshal(contract.PairingCompleteRequest{
		Code:       info.Code,
		DeviceName: "E2E 控制面·控制设备",
	})
	req, _ := http.NewRequest(http.MethodPost, st.dataOrigin+contract.RESTBasePath+"/pairing/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// 控制面是同源 loopback 调用者（harness 数据面调用自身），如实声明同源 Origin；
	// 与桌面 Wails 同源边界同级，服务端仍执行全部 Origin/Host/auth 校验。
	req.Header.Set("Origin", st.dataOrigin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("pairing complete: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pairing complete -> %d: %s", resp.StatusCode, string(raw))
	}
	for _, c := range resp.Cookies() {
		if c.Name == "amagi_codebox_device" {
			st.ctlCookie = c.Value
			break
		}
	}
	if st.ctlCookie == "" {
		return errors.New("pairing complete: no device cookie set")
	}
	var pairResp contract.PairingCompleteResponse
	if err := json.NewDecoder(resp.Body).Decode(&pairResp); err != nil {
		// 响应体已读（cookies 已取）；decode 失败不致命，deviceID 从 list 推导。
		st.ctlDeviceID = string(pairResp.Device.ID)
	} else {
		st.ctlDeviceID = string(pairResp.Device.ID)
	}
	return nil
}

// createSessionViaREST 以控制设备凭据经真实 REST POST /sessions 创建会话，
// 返回 SessionDetail（真实 gate/catalog/stream 全路径）。
func (st *controlState) createSessionViaREST(cliType contract.CLIType) (contract.SessionDetail, error) {
	if err := st.ensureControlDevice(); err != nil {
		return contract.SessionDetail{}, err
	}
	body, _ := json.Marshal(contract.CreateSessionRequest{CLIType: cliType})
	req, _ := http.NewRequest(http.MethodPost, st.dataOrigin+contract.RESTBasePath+"/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", st.dataOrigin)
	req.AddCookie(&http.Cookie{Name: "amagi_codebox_device", Value: st.ctlCookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return contract.SessionDetail{}, fmt.Errorf("create session: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return contract.SessionDetail{}, fmt.Errorf("create session -> %d: %s", resp.StatusCode, string(raw))
	}
	var detail contract.SessionDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		return contract.SessionDetail{}, fmt.Errorf("create session decode: %w", err)
	}
	return detail, nil
}

// ---------------------------------------------------------------------------
// 控制面（TEST-ONLY，仅 loopback）
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeCtlError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// controlMux 只组装"桌面用户动作等价物" + M2-INT fake CLI 注入通道。所有端点
// 都要求 loopback peer（监听本身已绑定 127.0.0.1，双重保险）。
func controlMux(st *controlState) *http.ServeMux {
	srv := st.srv
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "running": srv.IsRunning()})
	})

	// 开启配对窗口：等同桌面端「设置 › 远程访问 → 发起配对」（confirm=true
	// 扮演用户确认）。返回一次性 code 与真实过期时间（code 只经此测试通道
	// 传出，与桌面 Wails 边界同级；不入日志、不落盘）。
	mux.HandleFunc("POST /pairing-window", func(w http.ResponseWriter, _ *http.Request) {
		info, err := srv.CreatePairingWindow(true)
		if err != nil {
			writeCtlError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusCreated, info)
	})

	// 取消配对窗口：等同桌面端用户取消（CAS by generation）。
	mux.HandleFunc("POST /pairing-window/cancel", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Generation uint64 `json:"generation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeCtlError(w, http.StatusBadRequest, err)
			return
		}
		cancelled, err := srv.CancelPairingWindow(body.Generation)
		if err != nil {
			writeCtlError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"cancelled": cancelled})
	})

	// 查询已配对设备（桌面设备列表等价物；投影不含任何凭据材料）。
	mux.HandleFunc("GET /devices", func(w http.ResponseWriter, _ *http.Request) {
		devices, err := srv.ListDevices()
		if err != nil {
			writeCtlError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, devices)
	})

	// 撤销指定设备：等同桌面端设备列表的撤销动作（confirm=true 扮演用户确认）。
	mux.HandleFunc("POST /devices/revoke", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DeviceID string `json:"deviceId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeCtlError(w, http.StatusBadRequest, err)
			return
		}
		res, err := srv.RevokeDevice(body.DeviceID, true)
		if err != nil {
			writeCtlError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	// --- M2-INT fake CLI 通道（TEST-ONLY）：控制面造会话 / 注入输出 / 查 journal ---

	// 创建受控 fake CLI 会话：控制设备经真实 REST POST /sessions 创建，走完整
	// gate/catalog/stream 路径。返回 SessionDetail（含真实 sessionId）。
	mux.HandleFunc("POST /control/session", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CLIType string `json:"cliType"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeCtlError(w, http.StatusBadRequest, err)
			return
		}
		detail, err := st.createSessionViaREST(contract.CLIType(body.CLIType))
		if err != nil {
			writeCtlError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"sessionId": detail.ID,
			"title":     detail.Title,
			"state":     detail.State,
			"cliType":   detail.CLIType,
		})
	})

	// 向会话注入输出（fake CLI 回输出）：经真实 M2/H3 路径（Seq + 因果账本）。
	mux.HandleFunc("POST /control/session/{id}/output", func(w http.ResponseWriter, r *http.Request) {
		sessionID := contract.SessionID(r.PathValue("id"))
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeCtlError(w, http.StatusBadRequest, err)
			return
		}
		if sessionID == "" || body.Text == "" {
			writeCtlError(w, http.StatusBadRequest, errors.New("id and text required"))
			return
		}
		seq := st.injectOutput(sessionID, []byte(body.Text))
		writeJSON(w, http.StatusOK, map[string]any{"seq": seq})
	})

	// 批量注入 N 帧输出（C6a eviction：一次注入 >4096 帧使 origin 仅保留 tail）。
	// 服务端循环调真实 AppendOutput + 因果 publish；返回首末 Seq。
	mux.HandleFunc("POST /control/session/{id}/output-many", func(w http.ResponseWriter, r *http.Request) {
		sessionID := contract.SessionID(r.PathValue("id"))
		var body struct {
			Count  int    `json:"count"`
			Prefix string `json:"prefix"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeCtlError(w, http.StatusBadRequest, err)
			return
		}
		if sessionID == "" || body.Count <= 0 {
			writeCtlError(w, http.StatusBadRequest, errors.New("id and count>0 required"))
			return
		}
		prefix := body.Prefix
		if prefix == "" {
			prefix = "f"
		}
		var first, last contract.Seq
		for i := 0; i < body.Count; i++ {
			seq := st.injectOutput(sessionID, []byte(fmt.Sprintf("%s-%d\n", prefix, i+1)))
			if i == 0 {
				first = seq
			}
			last = seq
		}
		writeJSON(w, http.StatusOK, map[string]any{"firstSeq": first, "lastSeq": last, "count": body.Count})
	})

	// 向会话注入重启边界（stop/restart 边界渲染）：经真实 M2/H3 路径。
	mux.HandleFunc("POST /control/session/{id}/boundary", func(w http.ResponseWriter, r *http.Request) {
		sessionID := contract.SessionID(r.PathValue("id"))
		if sessionID == "" {
			writeCtlError(w, http.StatusBadRequest, errors.New("id required"))
			return
		}
		seq := st.injectBoundary(sessionID)
		writeJSON(w, http.StatusOK, map[string]any{"seq": seq})
	})

	// 查询会话危险操作 journal（真实文件后端；最新优先）。
	mux.HandleFunc("GET /control/session/{id}/journal", func(w http.ResponseWriter, r *http.Request) {
		sessionID := contract.SessionID(r.PathValue("id"))
		records, err := st.adapter.Journal().ListRecent(r.Context(), 50)
		if err != nil {
			writeCtlError(w, http.StatusServiceUnavailable, err)
			return
		}
		// 过滤本会话（journal 不含凭据；全量返回亦可，这里按会话过滤便于断言）。
		filtered := make([]remote.SessionOperationRecord, 0, len(records))
		for _, rec := range records {
			if rec.SessionID == sessionID {
				filtered = append(filtered, rec)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"records": filtered})
	})

	// 查询会话 raw IO 计数（M3-C 整形 E2E 输入幂等 oracle：rawInput 调用次数）。
	mux.HandleFunc("GET /control/session/{id}/raw-io", func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.PathValue("id")
		if sessionID == "" {
			writeCtlError(w, http.StatusBadRequest, errors.New("id required"))
			return
		}
		writeJSON(w, http.StatusOK, st.sessRaw.rawIOSnapshot(sessionID))
	})

	// --- M3-INT 多设备/几何/grace 控制端点（TEST-ONLY）---
	// 以下三个端点扮演「桌面用户动作等价物」（与桌面 Wails 边界同级），调用与
	// 桌面端相同的 ControlRuntime 导出 API；服务端仍执行全部 gate/arbiter 校验。
	// 控制面绝不进入生产二进制。

	// desktop 收回控制权：等同桌面端「收回控制」（TakeDesktop）。
	// 经真实 gate.TakeDesktop → arbiter commitTransition(reasonTakeover) →
	// SessionEventHub 广播 control.state(takeover) 给已 attach 的 WS 订阅者。
	mux.HandleFunc("POST /control/session/{id}/desktop-take", func(w http.ResponseWriter, r *http.Request) {
		sessionID := contract.SessionID(r.PathValue("id"))
		if sessionID == "" {
			writeCtlError(w, http.StatusBadRequest, errors.New("id required"))
			return
		}
		rt := st.adapter.Runtime()
		if err := rt.Gate().TakeDesktop(r.Context(), rt.DesktopAuthority(), sessionID); err != nil {
			writeCtlError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"taken": true})
	})

	// desktop 释放控制权：等同桌面端「释放控制」（ReleaseDesktop）。
	// 使多设备编排中「设备重新申请」可在 desktop 收回后成功。
	mux.HandleFunc("POST /control/session/{id}/desktop-release", func(w http.ResponseWriter, r *http.Request) {
		sessionID := contract.SessionID(r.PathValue("id"))
		if sessionID == "" {
			writeCtlError(w, http.StatusBadRequest, errors.New("id required"))
			return
		}
		rt := st.adapter.Runtime()
		if err := rt.Gate().ReleaseDesktop(r.Context(), rt.DesktopAuthority(), sessionID); err != nil {
			writeCtlError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"released": true})
	})

	// desktop 被动 resize（不取控制权；design §6.2 R-06：device 持权时被拒）。
	// 经真实 ControlRuntime.DesktopPassiveResize → gate.DoDesktopPassiveResize →
	// fakeSessionRaw.ResizeRaw 计数（几何冲突 oracle）。
	mux.HandleFunc("POST /control/session/{id}/desktop-passive-resize", func(w http.ResponseWriter, r *http.Request) {
		sessionID := contract.SessionID(r.PathValue("id"))
		if sessionID == "" {
			writeCtlError(w, http.StatusBadRequest, errors.New("id required"))
			return
		}
		var body struct {
			Cols int `json:"cols"`
			Rows int `json:"rows"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeCtlError(w, http.StatusBadRequest, err)
			return
		}
		rt := st.adapter.Runtime()
		if err := rt.DesktopPassiveResize(r.Context(), sessionID, body.Cols, body.Rows); err != nil {
			writeCtlError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"resized": true})
	})

	// M3-007 C5b（R2 证据修复）test-only resize barrier：arm/release。barrier 使
	// A 的 resize 到达 server 后阻塞在 raw port（已过 lane+permit+checkpoint），
	// 让 desktop take 先 fence，再释放 → raw port 见 ctx.Done() → 不 commit。
	// 这证明 resize **真实到达 server 并 in-flight**（非 relay/pre-admission）。
	// 端点 TEST-ONLY，不进生产。
	mux.HandleFunc("POST /control/session/{id}/arm-resize-barrier", func(w http.ResponseWriter, r *http.Request) {
		st.sessRaw.armResizeBarrier()
		writeJSON(w, http.StatusOK, map[string]any{"armed": true})
	})
	mux.HandleFunc("POST /control/session/{id}/release-resize-barrier", func(w http.ResponseWriter, r *http.Request) {
		st.sessRaw.releaseResizeBarrier()
		writeJSON(w, http.StatusOK, map[string]any{"released": true})
	})

	// 覆盖 grace 时长（design §7.2 / C-004）：E2E 把 30s 缩短到可观测窗口，
	// 使「grace 内拒绝 / grace 过期可申请」可在 wall-clock 内确定性验证。
	// 只影响 SetGraceDuration 之后 arm 的 grace 定时器（system clock）。
	mux.HandleFunc("POST /control/grace-duration", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Seconds float64 `json:"seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeCtlError(w, http.StatusBadRequest, err)
			return
		}
		if body.Seconds <= 0 {
			writeCtlError(w, http.StatusBadRequest, errors.New("seconds must be > 0"))
			return
		}
		st.adapter.Runtime().Arbiter().SetGraceDuration(time.Duration(body.Seconds * float64(time.Second)))
		writeJSON(w, http.StatusOK, map[string]any{"seconds": body.Seconds})
	})

	return mux
}

func main() {
	webRoot := flag.String("web-root", "", "absolute path to mobile/dist served same-origin by the harness server")
	keepConfig := flag.Bool("keep-config", false, "do not remove the temp config dir on shutdown (debug)")
	flag.Parse()

	if *webRoot == "" {
		fmt.Fprintln(os.Stderr, "harness: -web-root is required (mobile/dist absolute path)")
		os.Exit(2)
	}
	absWebRoot, err := filepath.Abs(*webRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: resolve web-root:", err)
		os.Exit(2)
	}
	if st, err := os.Stat(filepath.Join(absWebRoot, "index.html")); err != nil || st.IsDir() {
		fmt.Fprintf(os.Stderr, "harness: web-root %s has no index.html; run `npm --prefix mobile run build` first\n", absWebRoot)
		os.Exit(2)
	}

	// 进程私有 config 根：真实文件 Device Store + durable event sink 落在这里，
	// 绝不触碰真实 ~/.amagi-codebox。
	configDir, err := os.MkdirTemp("", "amagi-e2e-harness-config-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: temp config dir:", err)
		os.Exit(1)
	}
	cleanup := func() {
		if !*keepConfig {
			_ = os.RemoveAll(configDir)
		}
	}

	log := logging.NewService(configDir)

	// --- 生产装配（与 app.go NewApp 同款顺序） ---
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	srv := remote.NewServerWithSecurity(0, nil, log, emptyAssets,
		remote.NewProductionSecurityOptions(configDir, buildHostSummary(resolver)))
	// AppInterface 传 nil：v1 数据面（pairing/complete、host/summary）与静态
	// 伺服均不触达 App（server.go 对 s.app 做 nil 守卫）；legacy /api/* 面
	// 不属于本 E2E 范围，harness 也不向它发请求。

	// --- M2-INT session 接线：真实 RemoteSessionAdapter 装配（TEST-ONLY） ---
	// 与 app.go NewApp 同款依赖（design §4.2）：ControlRuntime + catalog +
	// streams + journal + resolver + LaunchRawPort + SessionRawPort。唯一差异：
	// resolver/launchRaw/sessRaw 指向 harness 提供的确定性 fake CLI（见本文件
	// M2-INT 节），不查找真实二进制/不启动真实进程。装配后 v1 session REST
	// index 2-9 + /ws/v1 全激活（design §4A hardening gate）。
	homeDir, _ := os.UserHomeDir()
	control := remote.NewControlRuntime(remote.NewSystemClock(), log)
	m2Catalog := remote.NewSessionCatalog()
	m2Streams := remote.NewSessionStreamStore()
	m2Journal := remote.NewSessionOperationJournal(configDir)
	fakeReg := newFakeSessionRegistry()
	fakeRaw := newFakeSessionRaw(fakeReg)
	m2Adapter := remote.NewRemoteSessionAdapter(
		control.Gate(), control, m2Catalog, m2Streams, m2Journal,
		&fakeRemoteLaunchResolver{homeDir: homeDir},
		fakeLaunchRaw{reg: fakeReg},
		fakeRaw,
		remote.NewSystemClock(), configDir,
	)
	// 与生产 app.go:1230 同款：把同一 raw port 注入 ControlRuntime 自身（供
	// DesktopPassiveResize/DesktopWrite 等桌面路径使用），否则 runtime.ptyRaw 为
	// nil → DesktopPassiveResize 误报 "runtime not ready"（test-only 接线缺口）。
	control.SetPTYRawPort(fakeRaw)
	srv.SetSessionAdapter(m2Adapter)
	// MarkReady 必须在 srv.Start 前完成：gate 在接受任何请求前需进入 ready
	// （CreateSession/AttachControl 等均 checkReady；未 ready → service.down）。
	control.MarkReady()

	if err := srv.LoadSecurityState(); err != nil {
		fmt.Fprintln(os.Stderr, "harness: LoadSecurityState:", err)
		cleanup()
		os.Exit(1)
	}

	dataPort, err := pickLoopbackPort()
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: pick data port:", err)
		cleanup()
		os.Exit(1)
	}
	srv.SetHost("127.0.0.1")
	srv.SetPort(dataPort)
	srv.SetWebRoot(absWebRoot)

	if err := srv.Start(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "harness: start remote server:", err)
		cleanup()
		os.Exit(1)
	}

	// --- 控制面（TEST-ONLY，独立 loopback 端口） ---
	ctlPort, err := pickLoopbackPort()
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: pick control port:", err)
		srv.Stop()
		cleanup()
		os.Exit(1)
	}
	ctlSrv := &http.Server{
		Addr: fmt.Sprintf("127.0.0.1:%d", ctlPort),
		Handler: controlMux(&controlState{
			srv:         srv,
			adapter:     m2Adapter,
			dataOrigin:  fmt.Sprintf("http://127.0.0.1:%d", dataPort),
			reg:         fakeReg,
			sessRaw:     fakeRaw,
			srcCounters: make(map[string]int),
		}),
	}
	ctlLn, err := net.Listen("tcp", ctlSrv.Addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: listen control:", err)
		srv.Stop()
		cleanup()
		os.Exit(1)
	}
	go func() {
		if err := ctlSrv.Serve(ctlLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "harness: control plane exited:", err)
		}
	}()

	ready := map[string]string{
		"origin":        fmt.Sprintf("http://127.0.0.1:%d", dataPort),
		"controlOrigin": fmt.Sprintf("http://127.0.0.1:%d", ctlPort),
	}
	readyJSON, _ := json.Marshal(ready)
	fmt.Printf("HARNESS_READY %s\n", readyJSON)

	// 等待终止信号（Playwright afterEach 发 SIGTERM）。
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	_ = ctlSrv.Close()
	srv.Stop()
	if err := srv.CloseSecurityState(); err != nil {
		fmt.Fprintln(os.Stderr, "harness: CloseSecurityState:", err)
	}
	cleanup()
}
