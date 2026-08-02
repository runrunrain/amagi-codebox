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
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

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

// controlMux 只组装"桌面用户动作等价物"。所有端点都要求 loopback peer
// （监听本身已绑定 127.0.0.1，双重保险）。
func controlMux(srv *remote.Server) *http.ServeMux {
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
		Addr:    fmt.Sprintf("127.0.0.1:%d", ctlPort),
		Handler: controlMux(srv),
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
