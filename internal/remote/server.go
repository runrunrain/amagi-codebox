package remote

import (
	"context"
	"crypto/rand"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/remote/contract"
)

// Server 远程 API HTTP 服务器，允许移动端通过 HTTP/WebSocket 操作 Amagi CodeBox 的全部功能。
type Server struct {
	host               string
	port               int
	auth               *Auth
	app                AppInterface
	ptyBridge          any // optional PtyBridge/ObserverAttachProvider/DimensionsProvider (unbound; design §8.6.3)
	log                *logging.Service
	httpSrv            *http.Server
	cancel             context.CancelFunc
	running            bool
	starting           bool       // CAS reserve under s.mu to serialize concurrent Start
	stopping           bool       // set under s.mu when a Stop has committed to the current run (R2-Major-03 fence)
	stoppingRun        *serverRun // the run whose Stop tail window owns `stopping`; Start waits on its done (R3-Major-03)
	runSeq             uint64     // diagnostic: # of published runs (atomic)
	mu                 sync.RWMutex
	webRoot            string   // 移动端 Web 前端的 dist 目录路径，为空则不提供静态文件服务
	mobileAssets       embed.FS // 构建时嵌入的移动端 Web 资源（mobile/dist）
	mobileAssetsPrefix string   // mobileAssets 中的路径前缀，默认 "mobile/dist"
	// interfaceAddrs is the LAN-address discovery seam used only when the
	// server listens on a wildcard address and needs to advertise a concrete
	// address in the desktop pairing QR code.
	interfaceAddrs func() ([]net.Addr, error)

	// M1-A security surface (zero when constructed via the legacy NewServer).
	secOpts     SecurityOptions
	v1sec       *v1Security
	store       *fileDeviceStore
	gate        *securityMaintenanceGate
	registry    *connectionRegistry
	pairing     *deviceService
	sink        SecurityEventSink
	durableSink *durableSecurityEventSink
	curRun      *serverRun
	runOnce     sync.Once
	runDone     chan struct{}

	// M1-B2c1 internal service-event emission (NFR-17).
	serverEventScope string // process scope for stable service EventIDs
	serverEventSeq   uint64 // monotonic per-process sequence (atomic)

	// M1-B2c2 legacy deprecation event dedup (NFR-17): per-process tuple set;
	// each (carrier,routeClass) is recorded at most once.
	legacySeenMu sync.Mutex
	legacySeen   map[legacyAuthTupleKey]bool

	// H2: control lifecycle hook (design §4A.3). Injected by the App's
	// control_wiring before Server.Start. When nil, a no-op hook is used (legacy
	// Server without M3-A). The hook is synchronous, idempotent, and performs NO
	// network/file I/O.
	lifecycleHook ControlLifecycleHook

	// M2-A session REST adapter (design §4.2). Injected by the App after
	// control_wiring + security setup. When nil, session routes stay 404
	// (design §4A hardening gate: routes activate only after H0-H3 PASS).
	sessionAdapter *RemoteSessionAdapter

	// CG-03 per-session canonical input ledger registry (contract-addendum-cg03
	// §5). Lazy-created per session; destroyed on session remove.
	inputLedgers *SessionInputLedgerRegistry

	// M0-06/M3-B timing collector (design §6): server-side attach/resync duration.
	// Fixed-schema, no payload/ID; in-process test/report harness only.
	metrics *Metrics
}

// SetSessionAdapter injects the M2-A session REST adapter (design §4.2).
// MUST be called before Start. When not called, session routes stay 404.
func (s *Server) SetSessionAdapter(adapter *RemoteSessionAdapter) {
	s.sessionAdapter = adapter
	s.inputLedgers = NewSessionInputLedgerRegistry()
	s.metrics = NewMetrics(nil)
	// M3-005：权威 remove 提交时销毁该 session 的 CG-03 ledger（remote REST remove
	// 路径）。desktop remove/clear 跨包路径由 App 经 DestroySessionInputLedger 接线。
	adapter.destroyLedger = func(sessionID contract.SessionID) {
		s.inputLedgers.Destroy(sessionID)
	}
}

// DestroySessionInputLedger releases the CG-03 per-session input ledger for
// sessionID (M3-005). Called by the App after an authoritative desktop
// remove/clear commit so the registry has no unbounded per-session residual
// window. Idempotent; nil-safe when the ledger registry is not wired (legacy
// Server without SetSessionAdapter) and nil-receiver-safe (test App without a
// wired Remote). Only the server owning the registry is the authoritative
// destroy point; failed/retained IDs are never destroyed by the caller (the
// App only calls this after a confirmed remove commit).
func (s *Server) DestroySessionInputLedger(sessionID contract.SessionID) {
	if s == nil || s.inputLedgers == nil {
		return
	}
	s.inputLedgers.Destroy(sessionID)
}

// LedgerForSession returns the per-session canonical input ledger for
// sessionID, lazily creating it on first use (same semantics as the WS
// canonical-input path). Exposed for resource-lifecycle tests that must
// populate a ledger before asserting DestroySessionInputLedger releases it.
// Returns nil when the registry is not wired (legacy Server without
// SetSessionAdapter).
func (s *Server) LedgerForSession(sessionID contract.SessionID) *SessionInputLedger {
	if s == nil || s.inputLedgers == nil {
		return nil
	}
	return s.inputLedgers.Ledger(sessionID)
}

// TimingSnapshot returns the server-side attach/resync timing snapshot (design
// §6). Fixed-schema, no payload/ID; for in-process test/report harness only.
func (s *Server) TimingSnapshot() MetricsSnapshot {
	if s.metrics == nil {
		return MetricsSnapshot{SchemaVersion: 1, Unit: "ms"}
	}
	return s.metrics.Snapshot()
}

// NewServer 创建远程服务器实例，不启动监听。
// mobileAssets 为构建时嵌入的移动端 Web 资源（mobile/dist），可为空 embed.FS。
func NewServer(port int, app AppInterface, log *logging.Service, mobileAssets embed.FS) *Server {
	// Event-scope entropy failure does NOT panic: an empty scope disables service
	// events (with closed health) rather than crashing startup.
	scopeBytes := make([]byte, 16)
	defer zeroBytes(scopeBytes) // wipe the temporary scope entropy on every path
	scope := ""
	if _, err := io.ReadFull(rand.Reader, scopeBytes); err == nil {
		scope = rawURLBase64(scopeBytes)
	}
	return &Server{
		port:               port,
		auth:               newAuth(),
		app:                app,
		log:                log,
		mobileAssets:       mobileAssets,
		mobileAssetsPrefix: "mobile/dist",
		interfaceAddrs:     net.InterfaceAddrs,
		serverEventScope:   scope,
		legacySeen:         make(map[legacyAuthTupleKey]bool),
		lifecycleHook:      noopLifecycleHook{},
	}
}

// SetControlLifecycleHook injects the H2 control lifecycle hook (design §4A.3).
// MUST be called before Start. When not called, a no-op hook is used (legacy).
// It also propagates the hook to the deviceService (pairing) for revoke/latch
// wiring (design §4A.3).
func (s *Server) SetControlLifecycleHook(hook ControlLifecycleHook) {
	if hook == nil {
		hook = noopLifecycleHook{}
	}
	s.lifecycleHook = hook
	if s.pairing != nil {
		s.pairing.SetControlLifecycleHook(hook)
	}
}

// controlHook returns the lifecycle hook (never nil).

// Start 在后台 goroutine 中启动 HTTP 服务器。并发 Start 由 s.mu 下的 `starting`
// CAS reserve 串行化：只有一个调用能进入 listen/publish，其余立即返回错误/幂等。
// R3-Major-03: 入口在 s.mu 下检查 `stopping`——若上一 run 的 Stop 尾窗未结束
// （stopping=true），本 Start 等待该 run 的 done 后重试，杜绝 Stop 尾窗内新 Start
// 发布 listener / event 倒序。s.mu 从不跨 store Sync/Terminate 或 durable I/O 持有。
func (s *Server) Start(parentCtx context.Context) error {
	// Stopping gate (R3-Major-03): wait for any in-progress Stop to fully finish
	// before publishing a new run. A Stop in its tail window (running=false but
	// stopping=true, done not yet closed) must complete first so the old stopped
	// event is appended before any new started event, and Stop's caller observes a
	// fully-stopped server. The wait releases s.mu (it blocks on a channel).
	for {
		s.mu.Lock()
		if s.running {
			s.mu.Unlock()
			return nil
		}
		if s.starting {
			s.mu.Unlock()
			return fmt.Errorf("remote server: start already in progress")
		}
		if !s.stopping {
			// No Stop in progress; this Start may proceed.
			s.starting = true
			s.mu.Unlock()
			break
		}
		// A Stop tail window owns `stopping`; wait on that run's done (released s.mu).
		waitRun := s.stoppingRun
		s.mu.Unlock()
		if waitRun != nil {
			select {
			case <-waitRun.done:
			case <-parentCtx.Done():
				return parentCtx.Err()
			}
		}
		// loop: re-check stopping (the just-finished Stop cleared it by identity)
	}
	// Any failure before publish must clear `starting`.
	clearStarting := func() {
		s.mu.Lock()
		s.starting = false
		s.mu.Unlock()
	}

	// Maintenance epoch blocks Start (and poisons an active/entering session).
	if s.gate != nil && s.gate.recordNormalSecurityAttempt() {
		clearStarting()
		return fmt.Errorf("remote server: maintenance epoch active")
	}
	// Hold a normal gate permit across listen + run publish + registry.Start so a
	// maintenance Begin cannot interleave with a live service. Returned on every
	// path; the permit is a gate counter, not server.mu, so no lock is held across
	// listen/Sync/Terminate.
	var permit normalStorePermit
	havePermit := false
	if s.gate != nil {
		p, ok := s.gate.issueNormalPermit()
		if !ok {
			clearStarting()
			return fmt.Errorf("remote server: maintenance epoch active")
		}
		permit = p
		havePermit = true
	}
	returnPermit := func() {
		if havePermit {
			s.gate.returnNormalPermit(permit)
		}
	}

	ctx, cancel := context.WithCancel(parentCtx)

	handler := s.buildHandler()
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		cancel()
		returnPermit()
		clearStarting()
		return fmt.Errorf("remote server listen %s: %w", addr, err)
	}

	run := &serverRun{shutdownOnce: &sync.Once{}, done: make(chan struct{}), startedDone: make(chan struct{}), listener: ln, httpServer: httpSrv}
	s.mu.Lock()
	s.starting = false
	s.running = true
	s.httpSrv = httpSrv
	s.cancel = cancel
	s.curRun = run
	// NOTE: do NOT reset s.stopping here. The entry gate above guarantees
	// stopping==false when we reach publish (we waited for any in-progress Stop
	// to finish). publishLifecycleAcceptance still re-checks it defensively.
	s.mu.Unlock()
	run.generation = atomic.AddUint64(&s.runSeq, 1)

	// Test seam (nil in production): lets tests inject a concurrent Stop into
	// the publish→acceptance window to prove the fence deterministically.
	if testLifecycleBarrier != nil {
		testLifecycleBarrier()
	}

	// State flip (R3-N01/R4-Major): publish registry + pairing acceptance AND
	// take started-event ownership (run.startedPending) under s.mu, but do NOT emit
	// the started event under s.mu — a durable sink append (write / Sync /
	// rotation) must not block a racing Stop from acquiring s.mu to commit stopping
	// + Suspend/fence. The started event is appended OUTSIDE s.mu below; event
	// order vs stopped is guaranteed by the startedDone handshake (stopInternal for
	// an accepted run waits on <-run.startedDone before appending stopped), NOT by
	// holding s.mu across the append.
	accepted := s.publishLifecycleAcceptance(run)
	returnPermit() // publish complete; release the gate permit
	if accepted {
		// H2/§4A.3: tell the control runtime that remote control is accepting
		// again with a new acceptance generation. This is called OUTSIDE s.mu,
		// after the listen/run publish and acceptance commit, so a fresh attach can
		// proceed. The hook is synchronous and idempotent (design §4A.3).
		s.lifecycleHook.RestartRemote(time.Now())
		// Test seam (nil in production): inject a direct Stop/Shutdown in the
		// acceptance→emit window (R4-Major). The handshake makes stopInternal wait
		// on startedDone, so the started append completes before stopped/Close.
		if testAcceptanceEmitBarrier != nil {
			testAcceptanceEmitBarrier()
		}
		// Emit started OUTSIDE s.mu (restores emitServiceEvent's original isolation
		// intent: append outside pair/store/registry/server-state locks).
		// emitServiceEvent records any append failure as closed health itself.
		s.emitServiceEvent(RemoteServiceStarted)
		// Signal started-event completion so an accepted run's stopInternal can
		// append stopped (always close, even if the append degraded — the failure
		// is already recorded as health; Stop observes it and does not reorder).
		close(run.startedDone)
		// R4-Major: a direct Stop/Shutdown may have shut this run down during the
		// acceptance→emit window (stopInternal committed stopping + nilled curRun,
		// then waited on startedDone which we just closed). If so, the run is dead
		// — do NOT spawn Serve/ctx and return an error (a stopped run must not
		// report Start success). The listener was already closed by stopInternal.
		s.mu.Lock()
		stillCurrent := s.curRun == run && !s.stopping
		s.mu.Unlock()
		if !stillCurrent {
			return errServerStoppedDuringStart
		}
	}
	if !accepted {
		// A racing Stop won BEFORE acceptance (startedPending never set, so
		// stopInternal did not wait on startedDone). No started event emitted.
		return nil
	}

	go func() {
		launchHost := s.desktopLaunchHost()
		s.log.Info("remote", "远程服务器启动", fmt.Sprintf("listen_host=%s port=%d desktop_host=%s", s.GetHost(), s.GetPort(), launchHost))
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.log.Error("remote", "远程服务器异常退出", err.Error())
			s.stopInternal(run, stopCauseServeFail)
		} else {
			s.stopInternal(run, stopCauseServeReturn)
		}
	}()

	// 监控父 context 取消
	go func() {
		<-ctx.Done()
		s.stopInternal(run, stopCauseParentCancel)
	}()

	return nil
}

// Stop 优雅关闭服务器，统一走 stopInternal。
func (s *Server) Stop() {
	s.mu.Lock()
	run := s.curRun
	cancel := s.cancel
	adapter := s.sessionAdapter
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if run != nil {
		s.stopInternal(run, stopCauseExplicit)
		<-run.done
	}
	if adapter != nil {
		ctx, cancelFlush := context.WithTimeout(context.Background(), 500*time.Millisecond)
		adapter.FlushPostCommitDebt(ctx)
		cancelFlush()
	}
}

// IsRunning 返回服务器是否正在运行。
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// SetPort 设置监听端口（仅在服务器停止时有效）。
func (s *Server) SetPort(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.port = port
}

// SetHost 设置监听地址（仅在服务器停止时有效）。
func (s *Server) SetHost(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.host = host
}

// GetHost 返回监听地址。
func (s *Server) GetHost() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.host
}

// SetWebRoot 设置移动端 Web 前端的 dist 目录路径。
// 设置后远程服务器将在同一端口同时提供 API 和静态页面服务。
func (s *Server) SetWebRoot(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webRoot = path
}

// SetPtyBridge injects the unbound PTY bridge adapter (design §8.6.3). The
// adapter implements PtyBridge / ObserverAttachProvider / DimensionsProvider
// and is NOT Wails-bound (unlike the old App callback exports). The legacy WS
// handler uses this instead of type-asserting the Wails-bound App.
func (s *Server) SetPtyBridge(b any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ptyBridge = b
}

// SetMobileAssetsPrefix sets the path prefix within the embedded FS where mobile
// assets are located. Defaults to "mobile/dist". Exported for test use.
func (s *Server) SetMobileAssetsPrefix(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mobileAssetsPrefix = prefix
}

// GetMobileWebRootStatus 返回当前生效的移动端静态资源目录状态。
func (s *Server) GetMobileWebRootStatus() (root string, configured bool, exists bool) {
	root = s.getEffectiveWebRoot()
	if root == "" {
		return "", false, false
	}

	indexPath := filepath.Join(root, "index.html")
	if info, err := os.Stat(indexPath); err == nil && !info.IsDir() {
		return root, true, true
	}

	return root, true, false
}

// HasEmbeddedMobileWeb 报告是否包含内置的移动端 Web 资源。
func (s *Server) HasEmbeddedMobileWeb() bool {
	indexPath := s.mobileAssetsPrefix + "/index.html"
	f, err := s.mobileAssets.Open(indexPath)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// BuildDesktopLaunchURL 返回桌面入口 Web UI 地址。随机源失败（grant 为空）→ 返回 ""
// 失败闭合，不打开浏览器。
// 本地通配/回环地址统一映射到 127.0.0.1，其余具体 host 保留原值。
func (s *Server) BuildDesktopLaunchURL() string {
	host := s.desktopLaunchHost()
	if host == "" {
		return "" // concrete non-loopback listen host → no LAN desktop launch URL
	}
	grant := s.auth.IssueLaunchGrant(host)
	if grant == "" {
		return "" // rand failure → fail-closed, no URL
	}
	query := url.Values{}
	query.Set("autoconnect", "1")
	query.Set("launch", grant)
	return fmt.Sprintf("http://%s/?%s", net.JoinHostPort(host, strconv.Itoa(s.GetPort())), query.Encode())
}

func (s *Server) desktopLaunchHost() string {
	return desktopLaunchHostForListenHost(s.GetHost())
}

// desktopLaunchHostForListenHost maps a listen host to the desktop-launch
// target host. Only loopback/localhost/wildcard listeners yield a usable
// loopback target; a CONCRETE non-loopback IP/hostname yields "" — the desktop
// launch URL must never target a LAN address (it would hit the loopback-only
// legacy guard). IPv6 wildcard "::" prefers "::1" (no assumed dual-stack).
func desktopLaunchHostForListenHost(host string) string {
	trimmed := strings.TrimSpace(host)
	canonical := strings.TrimPrefix(strings.TrimSuffix(trimmed, "]"), "[")
	if canonical == "" {
		return "127.0.0.1"
	}
	if strings.EqualFold(canonical, "localhost") {
		return "127.0.0.1"
	}
	ip := net.ParseIP(canonical)
	if ip == nil {
		return "" // hostname → concrete non-loopback
	}
	if ip.IsLoopback() {
		return ip.String()
	}
	if ip.IsUnspecified() {
		if ip.To4() != nil {
			return "127.0.0.1"
		}
		return "::1" // IPv6 wildcard → ::1 (do not assume dual-stack)
	}
	return "" // concrete non-loopback IP
}

func (s *Server) getEffectiveWebRoot() string {
	if s.app != nil && s.app.GetSettingsService() != nil {
		if root := strings.TrimSpace(s.app.GetSettingsService().GetMobileWebRoot()); root != "" {
			return root
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.webRoot)
}

// ---------------------------------------------------------------------------
// M1-B2a legacy loopback guard (design §A / §D, Leader C-1/C-3)
// ---------------------------------------------------------------------------

const (
	legacyCompatibilityEpoch = 1
	legacyRemovalEpoch       = 3
)

// setLegacyCompatibilityHeaders writes the fixed deprecation/no-store headers
// every epoch-1/2 legacy response (success/error/OPTIONS) carries (design §D.2).
func setLegacyCompatibilityHeaders(h http.Header) {
	h.Set("Deprecation", "true")
	h.Set("X-Amagi-Compatibility-Epoch", strconv.Itoa(legacyCompatibilityEpoch))
	h.Set("X-Amagi-Removal-Epoch", strconv.Itoa(legacyRemovalEpoch))
	h.Set("Warning", `299 Amagi-CodeBox "legacy remote API is loopback-only and will be removed at compatibility epoch 3"`)
	h.Set("Cache-Control", "no-store")
}

// isLoopbackPeer is the SOLE algorithm for trusting a legacy peer: it reads
// only r.RemoteAddr (never Host/Origin/Forwarded/X-Forwarded-*). Requires a
// successful SplitHostPort with a numeric port 1..65535, a ParseIP-able host
// (no zone), and IP.IsLoopback().
func isLoopbackPeer(r *http.Request) bool {
	host, portStr, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	if portStr == "" {
		return false
	}
	port, perr := strconv.Atoi(portStr)
	if perr != nil || port < 1 || port > 65535 {
		return false
	}
	if strings.ContainsAny(host, "%") { // reject IPv6 zone
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// isLegacyAPIPath reports whether path is in the legacy bootstrap/API/WS
// namespace (the surface the loopback guard restricts).
func isLegacyAPIPath(path string) bool {
	if path == "/api/bootstrap/consume" {
		return true
	}
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/ws/")
}

// isAllowedLegacyTokenCarrier reports the exact frozen ?token= carrier: a real
// loopback GET /ws/terminal/{nonempty} with a WebSocket Upgrade whose query is
// only `token` plus an optional single known `mode` (design §D.1 / C-3).
func isAllowedLegacyTokenCarrier(r *http.Request) bool {
	if !isLoopbackPeer(r) {
		return false
	}
	if r.Method != http.MethodGet {
		return false
	}
	if !strings.HasPrefix(r.URL.Path, "/ws/terminal/") {
		return false
	}
	sessionID := strings.TrimPrefix(r.URL.Path, "/ws/terminal/")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		return false
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	// Connection header must carry a case-insensitive "upgrade" token (RFC 6455).
	if !headerHasToken(r.Header.Get("Connection"), "upgrade") {
		return false
	}
	q := r.URL.Query()
	for k, v := range q {
		if k == "token" {
			if len(v) != 1 {
				return false
			}
			continue
		}
		if k == "mode" {
			if len(v) != 1 || (v[0] != "controller" && v[0] != "observer") {
				return false
			}
			continue
		}
		return false
	}
	return q.Has("token")
}

// headerHasToken reports whether a comma-separated header value contains the
// given token case-insensitively (per RFC 7230 token-list semantics).
func headerHasToken(headerVal, want string) bool {
	for _, part := range strings.Split(headerVal, ",") {
		if strings.EqualFold(strings.TrimSpace(part), want) {
			return true
		}
	}
	return false
}

// writeLegacyReject writes a fixed legacy rejection (no credential oracle) with
// the deprecation/no-store headers.
func writeLegacyReject(w http.ResponseWriter, status int) {
	setLegacyCompatibilityHeaders(w.Header())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	msg := "forbidden"
	if status == http.StatusUnauthorized {
		msg = "unauthorized"
	}
	w.Write([]byte(`{"error":"` + msg + `"}`))
}

// requireLoopbackPeer is the defense-in-depth inner guard wrapped around
// sensitive handlers; it blocks a non-loopback peer before body decode / App
// call. The global legacy guard remains authoritative.
func (s *Server) requireLoopbackPeer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackPeer(r) {
			writeLegacyReject(w, http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) buildHandler() http.Handler {
	protectedMux := http.NewServeMux()
	s.registerRoutes(protectedMux)

	bootstrapMux := http.NewServeMux()
	// Inner loopback guard on the bootstrap handler itself (defense-in-depth;
	// the global guard remains authoritative).
	bootstrapMux.HandleFunc("POST /api/bootstrap/consume", s.requireLoopbackPeer(s.handleConsumeLaunchGrant))

	// Legacy auth observer: derive routeClass from the request and record the
	// legacy_auth_deprecated event (deduped per tuple). Only fires on successful
	// auth; invalid/LAN attempts never reach here (the loopback guard rejects
	// non-loopback before the auth handler).
	legacyObserver := func(carrier LegacyAuthCarrier, r *http.Request) {
		s.recordLegacyAuthEvent(carrier, deriveLegacyRouteClass(r))
	}
	apiHandler := corsMiddleware(s.auth.MiddlewareWithObserver(protectedMux, legacyObserver))
	bootstrapHandler := corsMiddleware(bootstrapMux)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// v1 security routes are dispatched OUTSIDE legacy auth (design O-6) and
		// BEFORE the legacy loopback guard.
		if strings.HasPrefix(r.URL.Path, contract.RESTBasePath+"/") || r.URL.Path == contract.RESTBasePath {
			if s.v1sec != nil {
				s.buildV1Handler().ServeHTTP(w, r)
				return
			}
		}

		// v1 WebSocket upgrade path (design §6.1: single /ws/v1 consumer).
		// Dispatched BEFORE legacy auth; only active when sessionAdapter is wired.
		if r.URL.Path == contract.WebSocketV1Path && s.sessionAdapter != nil {
			s.handleV1WebSocket(w, r)
			return
		}

		legacyPath := isLegacyAPIPath(r.URL.Path)
		// Strict query parse: url.ParseQuery decodes percent-encoding (so a
		// percent-encoded `token` key is recognized) and surfaces parse errors. A
		// parse error fails closed (401) before static/legacy — token-like /
		// malformed queries must never be served.
		parsedQuery, qerr := url.ParseQuery(r.URL.RawQuery)
		if qerr != nil {
			writeLegacyReject(w, http.StatusUnauthorized)
			return
		}
		hasTokenQuery := len(parsedQuery["token"]) > 0

		if legacyPath {
			// Legacy namespace: non-loopback is 403 BEFORE auth/token (no oracle).
			if !isLoopbackPeer(r) {
				writeLegacyReject(w, http.StatusForbidden)
				return
			}
			// Loopback legacy: the ?token= carrier must be the exact frozen WS
			// carrier; any other ?token= (REST/query/extra/duplicate/non-upgrade)
			// is 401 before auth/handler.
			if hasTokenQuery && !isAllowedLegacyTokenCarrier(r) {
				writeLegacyReject(w, http.StatusUnauthorized)
				return
			}
			// Loopback legacy: all responses carry the fixed deprecation headers.
			setLegacyCompatibilityHeaders(w.Header())
			if r.URL.Path == "/api/bootstrap/consume" {
				bootstrapHandler.ServeHTTP(w, r)
				return
			}
			apiHandler.ServeHTTP(w, r)
			return
		}

		// Non-legacy (static-eligible): a top-level ?token= is never a carrier
		// here (the carrier is /ws/terminal, a legacy path above), so reject
		// before static — `/?token=` must never serve/redirect (C-3).
		if hasTokenQuery {
			writeLegacyReject(w, http.StatusUnauthorized)
			return
		}

		// 静态文件请求：从 Settings 动态读取 webRoot（保存设置后无需重启即可生效）
		webRoot, configured, exists := s.GetMobileWebRootStatus()

		// 优先级 1：用户配置的 MobileWebRoot 且 index.html 存在
		if configured && exists {
			fileSystem := http.Dir(webRoot)
			fileServer := http.FileServer(fileSystem)
			s.serveStaticOrSPA(w, r, fileServer, func(p string) (fs.FileInfo, error) {
				return fs.Stat(os.DirFS(webRoot), p)
			})
			return
		}

		// 优先级 2：内置 embedded mobile dist
		if s.HasEmbeddedMobileWeb() {
			subFS, err := fs.Sub(s.mobileAssets, s.mobileAssetsPrefix)
			if err == nil {
				fileServer := http.FileServer(http.FS(subFS))
				s.serveStaticOrSPA(w, r, fileServer, func(p string) (fs.FileInfo, error) {
					return fs.Stat(subFS, p)
				})
				return
			}
		}

		// 优先级 3：都不可用 -> 回退 API handler（需要认证）
		apiHandler.ServeHTTP(w, r)
	})
}

// serveStaticOrSPA 提供静态文件服务，对未知路径执行 SPA fallback（返回 index.html）。
func (s *Server) serveStaticOrSPA(w http.ResponseWriter, r *http.Request, fileServer http.Handler, statFunc func(string) (fs.FileInfo, error)) {
	path := r.URL.Path
	if path == "/" {
		fileServer.ServeHTTP(w, r)
		return
	}

	// 检查文件是否存在
	cleanPath := strings.TrimPrefix(path, "/")
	f, err := statFunc(cleanPath)
	if err == nil && !f.IsDir() {
		fileServer.ServeHTTP(w, r)
		return
	}

	// SPA fallback：非文件路径返回 index.html
	r2 := new(http.Request)
	*r2 = *r
	r2.URL = new(url.URL)
	*r2.URL = *r.URL
	r2.URL.Path = "/"
	fileServer.ServeHTTP(w, r2)
}

// RegenerateToken 重新生成 Token 并返回新值。
func (s *Server) RegenerateToken() string {
	tok := s.auth.RegenerateToken()
	// Successful rotation of a non-empty token emits a closed internal event;
	// failure (empty token) emits nothing.
	if tok != "" {
		s.emitServiceEvent(LegacyTokenRotated)
	}
	return tok
}

// emitServiceEvent derives a stable EventID (processScope+kind+occurredAt+seq)
// and appends a closed internal service event to the sink. Append happens
// outside all pairMu/store/registry locks; failure only records closed health
// (it never rolls back a business transition).
func (s *Server) emitServiceEvent(kind ServiceSecurityEventKind) {
	at := s.now().UTC()
	// Empty scope (event-scope entropy failure at construction) disables service
	// events; record closed health if a security face exists.
	if s.serverEventScope == "" {
		if s.v1sec != nil {
			s.v1sec.health.Record(HealthEventDurabilityDegraded, "", at)
		}
		return
	}
	if s.sink == nil {
		return
	}
	seq := atomic.AddUint64(&s.serverEventSeq, 1)
	eid := deriveServiceEventID(s.serverEventScope, kind, at, seq)
	res, _ := s.sink.AppendSecurityEvent(ServiceSecurityEvent{EventID: eid, Kind: kind, OccurredAt: at})
	var h *securityHealthRegister
	if s.v1sec != nil {
		h = s.v1sec.health
	}
	recordEventAppendHealth(h, res, eid, at)
}

// EmitListenConfigurationChanged records a closed internal event after a
// successful user-initiated listen configuration change (host/port). The kind
// is closed; App cannot pass an arbitrary string.
func (s *Server) EmitListenConfigurationChanged() {
	s.emitServiceEvent(RemoteListenConfigurationChanged)
}

// legacyAuthTupleKey is the per-process dedup key for legacy deprecation events.
type legacyAuthTupleKey struct {
	carrier    LegacyAuthCarrier
	routeClass LegacyAuthRouteClass
}

// deriveLegacyRouteClass maps a request path+method to the closed legacy route
// class (0 if not a legacy route).
func deriveLegacyRouteClass(r *http.Request) LegacyAuthRouteClass {
	if strings.HasPrefix(r.URL.Path, "/ws/") {
		return RouteWebSocket
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if r.Method == http.MethodGet {
			return RouteAPIRead
		}
		return RouteAPIWrite
	}
	return 0
}

// recordLegacyAuthEvent records a legacy_auth_deprecated event for the given
// (carrier, routeClass) tuple at most once per process. The EventID is stable
// from serverEventScope+carrier+routeClass+outcome (no occurredAt). The tuple is
// marked seen BEFORE the append so a PreAccept/degraded failure never causes a
// retry; the append outcome is mapped to closed health only and never changes
// the auth/handler response.
func (s *Server) recordLegacyAuthEvent(carrier LegacyAuthCarrier, routeClass LegacyAuthRouteClass) {
	if carrier == 0 || routeClass == 0 || s.sink == nil || s.serverEventScope == "" {
		return
	}
	key := legacyAuthTupleKey{carrier: carrier, routeClass: routeClass}
	s.legacySeenMu.Lock()
	if s.legacySeen[key] {
		s.legacySeenMu.Unlock()
		return // already recorded this tuple
	}
	if len(s.legacySeen) >= maxLegacyAuthTuples {
		s.legacySeenMu.Unlock()
		return // bounded; never grows unbounded
	}
	s.legacySeen[key] = true // mark seen BEFORE append (no retry on failure)
	s.legacySeenMu.Unlock()

	at := s.now().UTC()
	eid := deriveLegacyAuthEventID(s.serverEventScope, carrier, routeClass, LegacyAuthAccepted)
	res, _ := s.sink.AppendSecurityEvent(LegacyAuthSecurityEvent{
		EventID: eid, OccurredAt: at, Carrier: carrier, RouteClass: routeClass, Outcome: LegacyAuthAccepted,
	})
	var h *securityHealthRegister
	if s.v1sec != nil {
		h = s.v1sec.health
	}
	recordEventAppendHealth(h, res, eid, at)
}

// maxLegacyAuthTuples bounds the legacy deprecation tuple set (4 carriers × 4
// routes = 16 absolute max).
const maxLegacyAuthTuples = 16

// GetToken 返回认证 token（供前端 Wails 展示）。
func (s *Server) GetToken() string {
	return s.auth.GetToken()
}

// GetPort 返回监听端口。
func (s *Server) GetPort() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

// corsMiddleware 仅为同源浏览器请求回显 CORS 头，拒绝跨源页面借宿主浏览器访问本地 API。
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if !isAllowedCORSOrigin(r, origin) {
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusForbidden)
					return
				}
			} else {
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			}
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
