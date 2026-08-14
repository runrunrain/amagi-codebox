package remote

// Server-held M1-A security surface: SecurityOptions factory, LoadSecurityState,
// the per-run stopInternal lifecycle, and the desktop/public security APIs
// (design §5.2/§5.3/§5.4/§6.2/§6.6). When constructed via the legacy NewServer
// the security surface is zero and v1 paths fail closed; legacy routes are
// unaffected.

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/remote/contract"
)

// SecurityOptions holds the validated security construction parameters. Fields
// are private after factory validation. sinkFactory is invoked with the live
// health register so the durable sink can record closed health codes.
type SecurityOptions struct {
	configDir   string
	hostSummary HostSummaryFunc
	clock       Clock
	random      interface{ Read([]byte) (int, error) }
	sinkFactory func(*securityHealthRegister) SecurityEventSink
}

// NewProductionSecurityOptions validates and freezes production security
// options. configDir and hostSummary are required (programmer error → panic);
// production fixes crypto/rand.Reader, systemClock and the DURABLE event sink
// (M1-B remote-events.log, finding 7).
func NewProductionSecurityOptions(configDir string, hostSummary HostSummaryFunc) SecurityOptions {
	if configDir == "" {
		panic("security: configDir is required")
	}
	if hostSummary == nil {
		panic("security: hostSummary provider is required")
	}
	return SecurityOptions{
		configDir:   configDir,
		hostSummary: hostSummary,
		clock:       systemClock{},
		random:      rand.Reader,
		sinkFactory: func(h *securityHealthRegister) SecurityEventSink {
			return NewDurableSecurityEventSink(configDir, h)
		},
	}
}

// newSecurityOptions is the private constructor for deterministic test deps.
func newSecurityOptions(configDir string, hostSummary HostSummaryFunc, clock Clock, random interface{ Read([]byte) (int, error) }, sink SecurityEventSink) SecurityOptions {
	if configDir == "" || hostSummary == nil || clock == nil || random == nil || sink == nil {
		panic("security: all options are required")
	}
	return SecurityOptions{configDir: configDir, hostSummary: hostSummary, clock: clock, random: random, sinkFactory: func(_ *securityHealthRegister) SecurityEventSink {
		return sink
	}}
}

// randomReader adapts the injected random source to io.Reader.
type randomReader struct {
	r interface{ Read([]byte) (int, error) }
}

func (rr randomReader) Read(b []byte) (int, error) { return rr.r.Read(b) }

// NewServerWithSecurity constructs a Server with the M1-A security surface
// wired. The durable sink is created (unopened) and scanned during
// LoadSecurityState. The store starts NOT ready; Startup must call
// LoadSecurityState.
func NewServerWithSecurity(port int, app AppInterface, log *logging.Service, mobileAssets embed.FS, security SecurityOptions) *Server {
	s := NewServer(port, app, log, mobileAssets)
	s.secOpts = security
	s.gate = newSecurityMaintenanceGate()
	s.store = newFileDeviceStore(security.configDir, security.clock, randomReader{security.random}, s.gate)
	s.registry = newConnectionRegistry(security.clock)
	health := newSecurityHealthRegister()
	sink := security.sinkFactory(health)
	s.sink = sink
	if ds, ok := sink.(*durableSecurityEventSink); ok {
		s.durableSink = ds
	}
	var err error
	s.pairing, err = newDeviceService(s.gate, s.store, s.registry, randomReader{security.random}, security.clock, sink, health, DefaultPairingPolicy())
	if err != nil {
		panic("security: failed to init device service: " + err.Error())
	}
	s.v1sec = &v1Security{
		pairing:    s.pairing,
		deviceAuth: newDeviceAuthenticator(s.store, s.gate, security.clock),
		store:      s.store,
		health:     health,
		hostCache:  newHostSummaryCache(security.hostSummary),
	}
	return s
}

// LoadSecurityState opens+scans the durable event sink, then loads the device
// store. It must be called at Startup before any Remote.Start. The sink scan
// runs exactly once (before the store load). A sink failure is health-visible
// + leaves the sink in PreAccept, but it MUST NOT block the device store load:
// existing revoke/auth authority remains usable (design C-5 / §E). Sink errors
// are mapped to a closed health code — integrity/corruption →
// HealthEventIntegrityFailed; IO/unavailable → HealthEventAppendFailed — but the
// store is NEVER latched due to an audit-sink failure.
func (s *Server) LoadSecurityState() error {
	if s.store == nil {
		return nil // legacy construction: no security surface.
	}
	if s.durableSink != nil {
		if err := s.durableSink.OpenAndScan(); err != nil {
			if s.v1sec != nil {
				code := HealthEventAppendFailed
				if isDurableIntegrityError(err) {
					code = HealthEventIntegrityFailed
				}
				s.v1sec.health.Record(code, "", s.now())
			}
			// fall through to store load; the sink stays unavailable for Append.
		}
	}
	permit, ok := s.gate.issueNormalPermit()
	if !ok {
		return fmt.Errorf("security state unavailable")
	}
	err := s.store.LoadOrInitialize(permit)
	s.gate.returnNormalPermit(permit)
	return err
}

// now returns the security clock's current time (or wall time if unset).
func (s *Server) now() time.Time {
	if s.secOpts.clock != nil {
		return s.secOpts.clock.Now()
	}
	return time.Now()
}

// CloseSecurityState idempotently closes the durable sink (App shutdown). It
// performs no store/Terminate work.
func (s *Server) CloseSecurityState() error {
	if s.durableSink != nil {
		return s.durableSink.Close()
	}
	return nil
}

// ListRemoteSecurityEvents returns the sanitized newest-first durable projection
// and a status error (delegates to the durable sink).
// On a non-durable (test volatile) sink it returns nil.
func (s *Server) ListRemoteSecurityEvents(limit int) ([]SecurityEventRecord, error) {
	if s.durableSink != nil {
		return s.durableSink.ListSecurityEvents(limit)
	}
	return nil, errSinkNotOpen
}

// securityReady reports the store latch.
func (s *Server) securityReady() bool {
	return s.store != nil && s.store.Ready()
}

// ---------------------------------------------------------------------------
// Per-run lifecycle (design §5.4)
// ---------------------------------------------------------------------------

type serverStopCause uint8

const (
	stopCauseExplicit serverStopCause = iota + 1
	stopCauseParentCancel
	stopCauseServeFail
	stopCauseServeReturn
)

// serverRun holds the per-run shutdown serialization.
type serverRun struct {
	shutdownOnce *sync.Once
	done         chan struct{}
	listener     net.Listener
	httpServer   *http.Server
	generation   uint64 // monotonic run identity assigned at publish (R2-Major-03 fence diagnostic)

	// started-event ownership handshake (R4-Major). startedPending is set under
	// the Server's s.mu by publishLifecycleAcceptance when it commits acceptance
	// for this run (curRun==run && !stopping). Start then appends the started
	// event OUTSIDE s.mu and closes startedDone (always — even if the append
	// failed, which emitServiceEvent records as closed health). stopInternal,
	// for a run whose startedPending was set, waits on <-startedDone before
	// appending stopped, so stopped can never precede started. A PRE-acceptance
	// Stop (startedPending false) does NOT wait — it cannot deadlock with the
	// synchronous publish→acceptance barrier. startedPending is a one-way flag
	// (false→true, never reset); it is read by stopInternal under s.mu in
	// section-1 (which happens-after the acceptance that may set it).
	startedPending bool
	startedDone    chan struct{}
}

// stopInternal is the SINGLE stop entry. Order: Suspend pairing → registry
// Stop/detach → Terminate OUTSIDE the registry lock → HTTP shutdown. It never
// fsyncs/terminates/sinks inside a registry lock.
func (s *Server) stopInternal(run *serverRun, cause serverStopCause) {
	if run == nil {
		return
	}
	run.shutdownOnce.Do(func() {
		s.mu.Lock()
		sameRun := s.curRun == run
		if sameRun {
			s.curRun = nil
			s.stopping = true   // commit the stop for this run BEFORE Suspend so a
			s.stoppingRun = run // racing Start's entry gate observes it and waits (R3-Major-03).
		}
		// Read startedPending under s.mu (R4-Major): one-way flag set by
		// publishLifecycleAcceptance under this same lock. Once section-1 has
		// committed (curRun nilled) acceptance can never set it, so the value is
		// frozen. Only an ACCEPTED run (startedPending) owes a started event, so
		// only such a run's Stop waits on startedDone — a pre-acceptance Stop does
		// not wait and cannot deadlock with the synchronous publish→acceptance
		// barrier.
		startedPending := run.startedPending
		s.mu.Unlock()
		_ = sameRun

		// H2/§4A.3: FenceAllRemote is the FIRST lock-free action after Stop admission,
		// BEFORE pairing.Suspend / registry Stop / Terminate / HTTP shutdown /
		// startedDone wait / stopped event. This ensures no new device launch/write/
		// lifecycle intent can succeed while the Suspend sink is blocked (design §4A.3,
		// T41). The caller holds no server/pairing/registry/store/hub/journal lock.
		s.lifecycleHook.FenceAllRemote(ControlCauseServerStopped, time.Now())

		if s.pairing != nil {
			s.pairing.Suspend()
		}
		var detached []ManagedV1Connection
		if s.registry != nil {
			detached = s.registry.Stop(time.Now())
		}
		for _, c := range detached {
			c.Terminate(ConnectionTermination{Cause: TerminationServerStopped, OccurredAt: time.Now()})
		}
		// M-002: release all remote device holders immediately (no grace) after
		// registry terminate, so a restart leaves no stale device holder (design
		// §4A.3 authority order: Fence → Suspend → registry Stop → Terminate →
		// Release(server_stopped) → HTTP shutdown). Device holders go to ownerNone
		// with a typed transition; desktop holders are preserved.
		s.lifecycleHook.ReleaseAllRemote(ControlCauseServerStopped, time.Now())
		if run.httpServer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := run.httpServer.Shutdown(ctx); err != nil {
				_ = run.httpServer.Close()
			}
			cancel()
		}
		if run.listener != nil {
			_ = run.listener.Close()
		}
		s.mu.Lock()
		s.running = false
		// explicit Stop（stopCauseExplicit）已通过 s.cancel 取消，此处无需重复 cancel。
		isSameRun := sameRun
		s.mu.Unlock()

		// R3-Major-03 tail-window seam (nil in production): fired AFTER
		// running=false, BEFORE the stopped event / clearing stopping / done. A
		// test injects a concurrent Start here to prove the entry gate blocks it.
		// The test is responsible for one-shot semantics (R4-N01) so cleanup Stops
		// do not re-trigger it.
		if testStopTailBarrier != nil {
			testStopTailBarrier()
		}

		// Event order (R4-Major): if this run was accepted (took started-event
		// ownership), wait for Start to finish appending started (Start closes
		// startedDone right after the append, even on failure) BEFORE appending
		// stopped. This wait is OUTSIDE s.mu and AFTER Suspend/fence above, so a
		// blocked durable append in Start's emit cannot stall the functional stop.
		// For a run whose Serve/ctx triggered this stop, startedDone is already
		// closed. A pre-acceptance Stop (startedPending false) skips the wait.
		if isSameRun && startedPending {
			<-run.startedDone
		}
		// One stopped event per run (inside shutdownOnce), AFTER Suspend→fence→
		// Terminate→HTTP Shutdown/listener close/running=false (so the event fsync
		// never delays the real stop), still before the App closes the security
		// state. Emitted OUTSIDE s.mu (R3-N01). Only emitted for the run that was
		// actually published. Event order vs started is guaranteed by the
		// startedDone handshake above (R4-Major).
		if isSameRun {
			s.emitServiceEvent(RemoteServiceStopped)
		}
		// Clear the stopping gate by run identity (R3-Major-03): only the run
		// that set stopping/stoppingRun clears it, so a newer run's Stop does not
		// clobber an in-flight one. This happens BEFORE close(done) so a Start
		// waiting on stoppingRun.done observes stopping==false when it wakes.
		s.mu.Lock()
		if s.stoppingRun == run {
			s.stopping = false
			s.stoppingRun = nil
		}
		s.mu.Unlock()
		close(run.done)
	})
}

// publishLifecycleAcceptance is the R2-Major-03 / R3-N01 / R4-Major fence: it
// atomically publishes registry + pairing acceptance for run under s.mu AND
// takes started-event ownership (sets run.startedPending). Returns whether the
// run was still current (acceptance published). It does NOT emit the started
// event — that is done OUTSIDE s.mu by Start so a blocked durable sink append
// cannot stall a racing Stop's state flip / Suspend / fence (R3-N01).
//
// Event order (R4-Major): by setting startedPending under s.mu, this guarantees
// stopInternal for this run will wait on <-run.startedDone before appending
// stopped. Start closes startedDone right after the started append (or records
// the failure and still closes it), so stopped can never be appended before
// started. A pre-acceptance Stop observes startedPending==false and skips the
// wait (no deadlock with the synchronous publish→acceptance barrier).
//
// A concurrent Stop that committed (set stopping + nilled curRun under s.mu)
// makes this observe curRun != run || stopping and skip acceptance (startedPending
// stays false). Lock safety: registry.Start (registry.mu) and pairing.Resume
// (pairMu) are non-blocking flag flips and neither re-enters s.mu.
func (s *Server) publishLifecycleAcceptance(run *serverRun) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.curRun != run || s.stopping {
		return false
	}
	if s.registry != nil && s.store != nil && s.store.Ready() {
		s.registry.Start()
	}
	if s.pairing != nil {
		s.pairing.Resume()
	}
	// Take started-event ownership: stopInternal for this run will wait on
	// startedDone before appending stopped (R4-Major).
	run.startedPending = true
	return true
}

// testLifecycleBarrier (test-only; nil in production) is invoked synchronously
// AFTER a run is published (running=true, curRun set, s.mu released) and BEFORE
// the fenced lifecycle acceptance. Tests use it to inject a concurrent Stop
// into the publish→acceptance window to prove the generation/stopping fence
// deterministically (R2-Major-03).
var testLifecycleBarrier func()

// testAcceptanceEmitBarrier (test-only; nil in production) is invoked AFTER
// publishLifecycleAcceptance commits acceptance (startedPending set) and BEFORE
// Start emits the started event / closes startedDone — the R4-Major window. A
// test injects a direct Stop/Shutdown here; the handshake guarantees stopInternal
// waits on startedDone so the started append completes before stopped/Close.
var testAcceptanceEmitBarrier func()

// testStopTailBarrier (test-only; nil in production) is invoked inside
// stopInternal AFTER running=false is set but BEFORE the stopped event is
// appended / stopping is cleared / done is closed — i.e. the R3-Major-03 tail
// window. Tests use it to inject a concurrent Start and prove the stopping gate
// blocks it until Stop fully finishes. Each test MUST wrap its barrier in a
// one-shot (sync.Once) so its own cleanup Stops do not re-trigger it (R4-N01).
var testStopTailBarrier func()

// ---------------------------------------------------------------------------
// Desktop Wails-facing security APIs (design §6.2)
// ---------------------------------------------------------------------------

func (s *Server) requireSecurity() error {
	if s.v1sec == nil || !s.securityReady() {
		return errSecurityNotReady
	}
	if s.gate.recordNormalSecurityAttempt() {
		return errSecurityNotReady
	}
	return nil
}

// CreatePairingWindow opens a pairing window (code returned ONCE). The BaseURL
// is built from the real listen host/port. A concrete routable LAN IP is used
// directly; a wildcard listen address is resolved to a concrete local interface
// address so the desktop can render a QR code that another device can open.
// Loopback/hostname/non-routable addresses still fail closed with
// AddressRequired. The code is NEVER placed in a server-observed path/query.
func (s *Server) CreatePairingWindow(confirmTerminalExposure bool) (PairingWindowInfo, error) {
	if !confirmTerminalExposure {
		return PairingWindowInfo{}, errSecurityNotReady
	}
	if err := s.requireSecurity(); err != nil {
		return PairingWindowInfo{}, err
	}
	base, addressRequired := s.buildPairingAdvertiseBaseURL()
	info, err := s.pairing.CreateWindow(base)
	if err != nil {
		return PairingWindowInfo{}, err
	}
	info.AddressRequired = addressRequired
	return info, nil
}

// buildPairingAdvertiseBaseURL resolves wildcard listeners to one concrete
// interface address. The QR destination must be reachable from another LAN
// device; advertising 0.0.0.0/[::] or loopback would produce an unusable code.
func (s *Server) buildPairingAdvertiseBaseURL() (string, bool) {
	host := s.GetHost()
	port := s.GetPort()
	if base, required := buildPairingBaseURL(host, port); !required {
		return base, false
	}
	if port < 1 || port > 65535 || !isWildcardListenHost(host) {
		return "", true
	}

	resolver := s.interfaceAddrs
	if resolver == nil {
		resolver = net.InterfaceAddrs
	}
	addrs, err := resolver()
	if err != nil {
		return "", true
	}
	family := 0
	if canonicalListenHost(host) == "::" {
		family = 6
	} else {
		family = 4
	}
	ip := selectPairingAdvertiseIP(addrs, family)
	if ip == nil {
		return "", true
	}
	return "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(port)), false
}

func canonicalListenHost(host string) string {
	h := strings.TrimSpace(host)
	return strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
}

func isWildcardListenHost(host string) bool {
	switch canonicalListenHost(host) {
	case "", "0.0.0.0", "::":
		return true
	default:
		return false
	}
}

// selectPairingAdvertiseIP picks a stable, routable LAN address. Private
// addresses are preferred over public ones and IPv4 is preferred when the
// listener is IPv4. family is 4 or 6.
func selectPairingAdvertiseIP(addrs []net.Addr, family int) net.IP {
	type candidate struct {
		ip    net.IP
		score int
	}
	var candidates []candidate
	for _, addr := range addrs {
		var ip net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		default:
			raw := addr.String()
			if host, _, err := net.SplitHostPort(raw); err == nil {
				raw = host
			} else if slash := strings.IndexByte(raw, '/'); slash >= 0 {
				raw = raw[:slash]
			}
			ip = net.ParseIP(strings.Trim(raw, "[]"))
		}
		if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsMulticast() || !ip.IsGlobalUnicast() {
			continue
		}
		isV4 := ip.To4() != nil
		if (family == 4 && !isV4) || (family == 6 && isV4) {
			continue
		}
		canon := ip
		if isV4 {
			canon = ip.To4()
		} else {
			canon = ip.To16()
		}
		score := 1
		if canon.IsPrivate() {
			score = 0
		}
		candidates = append(candidates, candidate{ip: append(net.IP(nil), canon...), score: score})
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score < candidates[j].score
		}
		return bytes.Compare(candidates[i].ip, candidates[j].ip) < 0
	})
	return candidates[0].ip
}

// buildPairingBaseURL returns a strict http://host:port base for a concrete
// global-unicast IP and a valid port (1..65535). Empty/wildcard/loopback/
// link-local/multicast/non-global-unicast IPs, IPv6 zones, hostnames, and
// out-of-range ports yield an empty base with addressRequired.
func buildPairingBaseURL(host string, port int) (string, bool) {
	h := strings.TrimSpace(host)
	if h == "" {
		return "", true
	}
	if port < 1 || port > 65535 {
		return "", true
	}
	canon := h
	if strings.HasPrefix(canon, "[") {
		canon = strings.TrimPrefix(strings.TrimSuffix(canon, "]"), "[")
	}
	// IPv6 zone present → not a usable base.
	if i := strings.IndexByte(canon, '%'); i >= 0 {
		return "", true
	}
	ip := net.ParseIP(canon)
	if ip == nil || !ip.IsGlobalUnicast() {
		return "", true
	}
	return "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(port)), false
}

// GetPairingWindow returns the non-code status.
func (s *Server) GetPairingWindow() (PairingWindowStatus, error) {
	if err := s.requireSecurity(); err != nil {
		return PairingWindowStatus{}, err
	}
	return s.pairing.WindowStatus()
}

// CancelPairingWindow CAS-cancels by generation.
func (s *Server) CancelPairingWindow(expectedGeneration uint64) (bool, error) {
	if err := s.requireSecurity(); err != nil {
		return false, err
	}
	return s.pairing.CancelWindow(expectedGeneration)
}

// ListDevices returns the device list.
func (s *Server) ListDevices() ([]DeviceInfo, error) {
	if err := s.requireSecurity(); err != nil {
		return nil, err
	}
	return s.pairing.ListDevices()
}

// RevokeDevice persists + fences + terminates + events. The device ID must be a
// canonical 22-char RawURL encoding of 16 bytes; invalid/whitespace IDs are
// rejected BEFORE the store/registry is touched.
func (s *Server) RevokeDevice(deviceID string, confirm bool) (RevokeDeviceResult, error) {
	if !confirm {
		return RevokeDeviceResult{}, errSecurityNotReady
	}
	// The id must already be trimmed AND canonical; padded whitespace around a
	// valid id is rejected before the store/registry is touched.
	if deviceID != strings.TrimSpace(deviceID) || !validRawURLID(deviceID) {
		return RevokeDeviceResult{}, errSecurityNotReady
	}
	if err := s.requireSecurity(); err != nil {
		return RevokeDeviceResult{}, err
	}
	return s.pairing.RevokeDevice(contract.DeviceID(deviceID))
}

// RecordDeviceSeen is called by M1-B/future WS after a successful auth.
func (s *Server) RecordDeviceSeen(principal DevicePrincipal) (DeviceSeenResult, error) {
	if s.v1sec == nil {
		return DeviceSeenResult{}, errSecurityNotReady
	}
	return s.pairing.RecordDeviceSeen(principal)
}

// GetSecurityHealth returns the bounded health snapshot.
func (s *Server) GetSecurityHealth() SecurityHealthSnapshot {
	if s.v1sec == nil {
		return SecurityHealthSnapshot{SecurityReady: false}
	}
	return s.v1sec.health.Snapshot(s.securityReady())
}

// AcknowledgeSecurityHealth acknowledges a closed code (never resolves/retries).
// The code is trimmed and must be a known closed code; unknown/blank is rejected
// with a fixed error and does not mutate health.
func (s *Server) AcknowledgeSecurityHealth(code string) (SecurityHealthSnapshot, error) {
	if s.v1sec == nil {
		return SecurityHealthSnapshot{}, errSecurityNotReady
	}
	trimmed := strings.TrimSpace(code)
	if !isKnownHealthCode(SecurityHealthCode(trimmed)) {
		return SecurityHealthSnapshot{}, errSecurityNotReady
	}
	s.v1sec.health.Acknowledge(SecurityHealthCode(trimmed))
	return s.v1sec.health.Snapshot(s.securityReady()), nil
}

// RegisterV1Connection registers a future WS connection (M2/M3 consumer).
func (s *Server) RegisterV1Connection(principal DevicePrincipal, connectionID ConnectionID, conn ManagedV1Connection) (ConnectionRegistrationResult, error) {
	if s.registry == nil {
		return ConnectionRegistrationResult{Outcome: RegistrationRejectedNotAccepting}, nil
	}
	return s.registry.Register(principal, connectionID, conn)
}

// UnregisterV1Connection removes a registration (epoch-guarded).
func (s *Server) UnregisterV1Connection(reg ConnectionRegistration) {
	if s.registry != nil {
		s.registry.Unregister(reg)
	}
}

// ---------------------------------------------------------------------------
// Maintenance Server wrappers (design §6.2) — fully connectable
// ---------------------------------------------------------------------------

func (s *Server) BeginDeviceStoreMaintenance() (MaintenanceSession, error) {
	if s.store == nil {
		return MaintenanceSession{}, errSecurityNotReady
	}
	// Precheck: server stopped/not-starting, pairing inactive, registry stopped+empty.
	if s.maintenanceLiveConflict() {
		return MaintenanceSession{}, errSecurityNotReady
	}
	if s.gate.isActive() {
		return MaintenanceSession{}, errSecurityNotReady
	}
	// H2/§4A.3: idempotent Fence+Release(maintenance) before store Begin. This
	// ensures no device launch/write/lifecycle intent is in flight during the
	// maintenance epoch (design §4A.3: "stopped precheck → idempotent
	// Fence+Release(maintenance) → store Begin→postcheck").
	s.lifecycleHook.FenceAllRemote(ControlCauseMaintenance, time.Now())
	s.lifecycleHook.ReleaseAllRemote(ControlCauseMaintenance, time.Now())
	sess, err := s.store.BeginMaintenance()
	if err != nil {
		return MaintenanceSession{}, err
	}
	// Post-acquire recheck: a racing Start/pairing change between the precheck and
	// session acquisition must invalidate the session immediately (Abort + closed
	// error). No capability is returned; the gate returns to normal so subsequent
	// normal ops remain usable.
	if s.maintenanceLiveConflict() {
		_ = s.store.AbortMaintenance(sess)
		return MaintenanceSession{}, errSecurityNotReady
	}
	return sess, nil
}

// maintenanceLiveConflict reports whether a live service state conflicts with an
// exclusive maintenance epoch: server running/starting, an active pairing
// window, or a registry that is accepting or has live connections. It performs
// NO store Sync / Terminate (read-only state probes).
func (s *Server) maintenanceLiveConflict() bool {
	s.mu.Lock()
	live := s.running || s.starting
	s.mu.Unlock()
	if live {
		return true
	}
	if s.pairing != nil && s.pairing.WindowActive() {
		return true
	}
	if s.registry != nil && (s.registry.IsAccepting() || s.registry.LiveCount() != 0) {
		return true
	}
	return false
}

func (s *Server) BackupDeviceStoreForMigration(sess MaintenanceSession) (DeviceStoreBackup, error) {
	return s.store.BackupForMigration(sess)
}

func (s *Server) RestoreDeviceStoreMigrationBackup(sess MaintenanceSession, b DeviceStoreBackup) error {
	return s.store.RestoreMigrationBackup(sess, b)
}

func (s *Server) ValidateDeviceStoreMaintenance(sess MaintenanceSession) error {
	return s.store.ValidateMaintenanceStore(sess)
}

func (s *Server) EndDeviceStoreMaintenance(sess MaintenanceSession) error {
	return s.store.EndMaintenance(sess)
}

func (s *Server) AbortDeviceStoreMaintenance(sess MaintenanceSession) error {
	return s.store.AbortMaintenance(sess)
}

// CleanupMigrationBackup deletes exactly the validated backup directory for the
// given opaque token. Stale/copied/cross-process handles fail closed.
func (s *Server) CleanupMigrationBackup(backup DeviceStoreBackup) error {
	return s.store.CleanupMigrationBackup(backup)
}

// MigrationBackupInfo returns the sanitized backup info (BackupID + manifest
// SHA-256) for a live or post-End backup handle. The backup is strictly
// revalidated before info is returned.
func (s *Server) MigrationBackupInfo(backup DeviceStoreBackup) (MigrationBackupInfo, error) {
	if s.store == nil {
		return MigrationBackupInfo{}, errSecurityNotReady
	}
	return s.store.MigrationBackupInfo(backup)
}
