package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/settings"
)

type transactionPlanner struct {
	shared bool
	config bool
}

func (p transactionPlanner) BuildPlan(_ context.Context, req launchplan.BuildRequest) (*launchplan.Plan, *launchplan.BuildFailure) {
	workdir := req.Workdir
	if workdir == "" {
		workdir = "/transaction-work"
	}
	effects := make([]launchplan.EffectSpec, 0, 4)
	var admissions []launchplan.SharedAdmissionSpec
	if p.shared {
		fingerprint := sharedFingerprintForProxy("https://shared.example/v1", 5280)
		admissions = append(admissions, launchplan.SharedAdmissionSpec{Service: launchplan.SharedClaudeProxy, ConfigFingerprint: fingerprint})
		effects = append(effects, launchplan.EffectSpec{Kind: launchplan.EffectProxyStart, Shared: &launchplan.SharedStartSpec{
			Service: launchplan.SharedClaudeProxy, ConfigFingerprint: fingerprint,
			UpstreamURL: "https://shared.example/v1", ListenPort: 5280,
		}})
	}
	secretBuffers := make([][]byte, 0, 1)
	if p.config {
		secretBuffers = append(secretBuffers, []byte(`{"providers":{"transaction":{}}}`))
		effects = append(effects, launchplan.EffectSpec{Kind: launchplan.EffectConfigMutation, Config: &launchplan.ConfigMutationSpec{
			Target: launchplan.ConfigPi, Candidate: launchplan.SecretBufferRef{Index: 0},
		}})
	}
	effects = append(effects,
		launchplan.EffectSpec{
			Kind: launchplan.EffectPTYStart,
			Process: &launchplan.ProcessStartSpec{
				Mode: launchplan.ModeEmbedded, RequireRunHandle: true,
				Resolved: platform.ResolvedLaunchSpec{
					AppType: string(req.CLIType), LaunchMode: string(session.ModeEmbedded), WorkDir: workdir,
					CLI:           platform.ResolvedCLI{Name: string(req.CLIType), Path: "/fake/" + string(req.CLIType), Args: []string{"--model", "model"}},
					BootstrapMode: platform.BootstrapShellAttach, StartupCommand: string(req.CLIType) + " --model model",
					PTYCols: 120, PTYRows: 40,
				},
			},
		},
		launchplan.EffectSpec{Kind: launchplan.EffectBootstrapWrite, Bootstrap: &launchplan.BootstrapWriteSpec{StartupCommand: string(req.CLIType) + " --model model"}},
	)
	plan := &launchplan.Plan{
		Recipe:     launchplan.StableRecipe{CLIType: req.CLIType, Workdir: workdir, ProviderRef: "transaction", ModelRef: "model", UseProxy: p.shared},
		Admissions: admissions, Effects: effects, Secrets: launchplan.NewEphemeralSecretBundle(secretBuffers...),
	}
	if err := plan.Validate(); err != nil {
		panic(err)
	}
	return plan, nil
}

func (transactionPlanner) Probe(_ context.Context, cli contract.CLIType) (contract.CLIAvailability, *launchplan.BuildFailure) {
	return contract.CLIAvailability{CLIType: cli, Available: true}, nil
}

type confirmedCloseWaiter struct{}

func (confirmedCloseWaiter) Wait(context.Context) error { return nil }
func (confirmedCloseWaiter) Confirmed() bool            { return true }

type transactionBinding struct {
	id       processcap.BindingID
	pty      *transactionPTY
	session  string
	once     sync.Once
	evidence processcap.ExactCloseEvidence
}

func (b *transactionBinding) BindingID() processcap.BindingID { return b.id }
func (b *transactionBinding) CloseExact(context.Context) processcap.ExactCloseEvidence {
	b.once.Do(func() {
		b.pty.mu.Lock()
		if b.pty.current[b.session] == b {
			delete(b.pty.current, b.session)
		}
		b.pty.closeCount++
		b.pty.events = append(b.pty.events, "close:"+b.session)
		b.pty.mu.Unlock()
		receipt := b.pty.receipt.Add(1)
		evidence, err := processcap.NewExactCloseEvidence(b.id, receipt, processcap.CloseConfirmed, confirmedCloseWaiter{})
		if err != nil {
			panic(err)
		}
		b.evidence = evidence
	})
	return b.evidence
}

type transactionPTY struct {
	mu              sync.Mutex
	owner           uint64
	generation      uint64
	pid             int
	current         map[string]*transactionBinding
	events          []string
	writes          [][]byte
	startSpecs      []platform.ResolvedLaunchSpec
	runHandles      []any
	closeCount      int
	failStartAt     int
	failReadyAt     int
	failWriteAt     int
	onReady         func(int)
	validateStart   func(platform.ResolvedLaunchSpec) error
	validatedStarts int
	receipt         atomic.Uint64
}

func newTransactionPTY(t *testing.T) *transactionPTY {
	t.Helper()
	owner, err := processcap.NewOwnerID()
	if err != nil {
		t.Fatal(err)
	}
	return &transactionPTY{owner: owner, current: make(map[string]*transactionBinding), pid: 4100}
}

func (p *transactionPTY) StartResolvedWithRunEvidence(sessionID string, spec platform.ResolvedLaunchSpec, runHandle any) (processcap.StartEvidence, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failStartAt > 0 && len(p.startSpecs)+1 == p.failStartAt {
		return processcap.StartEvidence{}, errors.New("injected PTY start failure")
	}
	if p.validateStart != nil {
		if err := p.validateStart(spec); err != nil {
			return processcap.StartEvidence{}, err
		}
		p.validatedStarts++
	}
	if p.current[sessionID] != nil {
		return processcap.StartEvidence{}, errors.New("fake PTY session already active")
	}
	p.generation++
	p.pid++
	binding := &transactionBinding{id: processcap.BindingID{Kind: processcap.BackendPTY, Owner: p.owner, Generation: p.generation}, pty: p, session: sessionID}
	p.current[sessionID] = binding
	p.startSpecs = append(p.startSpecs, spec)
	p.runHandles = append(p.runHandles, runHandle)
	p.events = append(p.events, "start:"+sessionID)
	return processcap.StartEvidence{PID: p.pid, Binding: binding}, nil
}

func (p *transactionPTY) WaitReadyForBinding(_ context.Context, sessionID string, id processcap.BindingID) error {
	p.mu.Lock()
	p.events = append(p.events, "ready:"+sessionID)
	startCount := len(p.startSpecs)
	fail := p.failReadyAt > 0 && startCount == p.failReadyAt
	onReady := p.onReady
	p.mu.Unlock()
	if onReady != nil {
		onReady(startCount)
	}
	if fail {
		return errors.New("injected ready failure")
	}
	p.mu.Lock()
	binding := p.current[sessionID]
	p.mu.Unlock()
	if binding == nil || binding.id != id {
		return errors.New("stale ready binding")
	}
	return nil
}

func (p *transactionPTY) WriteRawForBinding(_ context.Context, sessionID string, id processcap.BindingID, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, "bootstrap:"+sessionID)
	p.writes = append(p.writes, append([]byte(nil), data...))
	if p.failWriteAt > 0 && len(p.writes) == p.failWriteAt {
		return errors.New("injected bootstrap failure")
	}
	binding := p.current[sessionID]
	if binding == nil || binding.id != id {
		return errors.New("stale bootstrap binding")
	}
	return nil
}

type transactionHarness struct {
	adapter           *remote.RemoteSessionAdapter
	runtime           *remote.ControlRuntime
	manager           *session.Manager
	registry          *processcap.Registry
	coord             *remote.SharedServiceCoordinator
	streams           *remote.SessionStreamStore
	pty               *transactionPTY
	ownersMu          sync.Mutex
	owners            map[remote.SharedLeaseOwnerKey]*remote.SharedDependencyLease
	onPrepareTransfer func()
}

func newTransactionHarness(t *testing.T, pty *transactionPTY) *transactionHarness {
	return newTransactionHarnessWithProxy(t, pty, nil)
}

func newTransactionHarnessWithProxy(t *testing.T, pty *transactionPTY, proxyService *fakeProxyService) *transactionHarness {
	return newTransactionHarnessWithPlanner(t, pty, transactionPlanner{shared: proxyService != nil}, proxyService)
}

func newTransactionHarnessWithPlanner(t *testing.T, pty *transactionPTY, planner launchplan.Planner, proxyService *fakeProxyService) *transactionHarness {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	runtime := remote.NewControlRuntime(remote.NewSystemClock(), nil)
	runtime.MarkReady()
	manager := session.NewManager()
	registry := processcap.NewRegistry()
	coord := remote.NewSharedServiceCoordinator()
	streams := remote.NewSessionStreamStore()
	adapter := remote.NewRemoteSessionAdapter(
		runtime.Gate(), runtime, nil, streams, remote.NewSessionOperationJournal(t.TempDir()),
		remote.NewNoopRemoteLaunchResolver(), nil, nil, remote.NewSystemClock(), t.TempDir(),
	)
	adapter.SetSessionAuthority(manager, registry, planner)
	executor := newAppLaunchExecutor(launchExecutorDeps{
		pty: pty, proxy: proxyService, sharedCoord: coord, debts: launchplan.NewCompensationDebtRegistry(),
	})
	adapter.SetLaunchExecutor(executor, coord)
	h := &transactionHarness{
		adapter: adapter, runtime: runtime, manager: manager, registry: registry,
		coord: coord, streams: streams, pty: pty, owners: make(map[remote.SharedLeaseOwnerKey]*remote.SharedDependencyLease),
	}
	adapter.SetSharedLeaseTransfer(h.prepareTransfer, h.releaseExact)
	runtime.Projector().SetRunTerminalCleanup(h.releaseExact)
	return h
}

func (h *transactionHarness) prepareTransfer(sessionID string, oldEpoch, newEpoch uint64, newLeases []*remote.SharedDependencyLease) (func(), func(), func(), error) {
	h.ownersMu.Lock()
	old := make([]*remote.SharedDependencyLease, 0, 3)
	for key, lease := range h.owners {
		if string(key.SessionID) == sessionID && key.RunEpoch == oldEpoch {
			old = append(old, lease)
		}
	}
	h.ownersMu.Unlock()
	token, err := h.coord.PrepareLeaseTransfer(contract.SessionID(sessionID), oldEpoch, newEpoch, old, newLeases)
	if err != nil {
		return nil, nil, nil, err
	}
	if h.onPrepareTransfer != nil {
		h.onPrepareTransfer()
	}
	commit := func() {
		h.ownersMu.Lock()
		h.coord.CommitLeaseTransferNoFail(token, func() {
			for _, lease := range old {
				delete(h.owners, lease.OwnerKey())
			}
			for _, lease := range newLeases {
				h.owners[lease.OwnerKey()] = lease
			}
		})
		h.ownersMu.Unlock()
	}
	return commit, func() { h.coord.FinishLeaseTransfer(token) }, func() { h.coord.AbortLeaseTransfer(token) }, nil
}

func (h *transactionHarness) releaseExact(sessionID string, epoch uint64) {
	h.ownersMu.Lock()
	var leases []*remote.SharedDependencyLease
	for key, lease := range h.owners {
		if string(key.SessionID) == sessionID && key.RunEpoch == epoch {
			leases = append(leases, lease)
			delete(h.owners, key)
		}
	}
	h.ownersMu.Unlock()
	for _, lease := range leases {
		_ = h.coord.ReleaseExact(context.Background(), lease)
	}
}

func TestProductionPiOmpCreateExecutesCredentialConfigArgvAndBootstrap(t *testing.T) {
	for _, cli := range []contract.CLIType{contract.CLITypePi, contract.CLITypeOmp} {
		t.Run(string(cli), func(t *testing.T) {
			configDir := t.TempDir()
			configService := config.NewConfigService(configDir)
			if err := configService.Load(); err != nil {
				t.Fatal(err)
			}
			secretService := secrets.NewSecretsService(configDir)
			settingsService := settings.NewService(configDir)
			if err := settingsService.Load(); err != nil {
				t.Fatal(err)
			}
			defaults, err := launchplan.NewDefaultStore(settingsService)
			if err != nil {
				t.Fatal(err)
			}
			providerID := "production-joint"
			secret := "production-secret-123456789"
			provider := config.Provider{
				Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://production.example/v1", AuthKey: config.AuthTypeAPIKey},
				DefaultModel: "production-model",
				Presets: map[string]config.Preset{
					"production-preset": {Name: "production-preset", Model: "production-model", Parameters: config.Parameters{ReasoningEffort: "high"}},
				},
			}
			if err := configService.SaveProvider(providerID, provider); err != nil {
				t.Fatal(err)
			}
			if err := secretService.SetAPIKey(providerID, secret); err != nil {
				t.Fatal(err)
			}
			workdir := t.TempDir()
			if err := defaults.RecordDesktopActivation(launchplan.StableRecipe{
				CLIType: cli, Workdir: workdir, ProviderRef: providerID,
				PresetRef: "production-preset", ModelRef: "production-preset", ShellRef: "/bin/sh",
			}); err != nil {
				t.Fatal(err)
			}
			planner := newAppLaunchPlanner(
				configService, secretService, defaults, fakeCLIResolver{}, fakePlatformCaps(),
				paths.NewPathsService(configDir), envvars.NewEnvVarsService(configDir), configDir,
			)
			pty := newTransactionPTY(t)
			wantProvider := "amagi-" + providerID
			wantArgs := []string{"--provider", wantProvider, "--model", "production-model", "--thinking", "high"}
			pty.validateStart = func(spec platform.ResolvedLaunchSpec) error {
				if strings.Join(spec.CLI.Args, "|") != strings.Join(wantArgs, "|") {
					return fmt.Errorf("argv = %v, want %v", spec.CLI.Args, wantArgs)
				}
				if !strings.Contains(spec.StartupCommand, "--provider "+wantProvider) || !strings.Contains(spec.StartupCommand, "--model production-model") || !strings.Contains(spec.StartupCommand, "--thinking high") {
					return fmt.Errorf("startup command = %q", spec.StartupCommand)
				}
				if !envContainsExact(spec.Env.Variables, "ANTHROPIC_API_KEY="+secret) {
					return errors.New("resolved PTY environment lacks exact credential")
				}
				return nil
			}
			h := newTransactionHarnessWithPlanner(t, pty, planner, nil)
			principal := remote.DevicePrincipal{DeviceID: contract.DeviceID("device-production-" + string(cli)), DeviceName: "phone"}
			created, adapterErr := h.adapter.CreateSession(context.Background(), contract.RequestID("request-production-"+string(cli)), principal, contract.CreateSessionRequest{CLIType: cli})
			if adapterErr != nil {
				t.Fatal(adapterErr)
			}
			if created.Detail.State != contract.SessionStateRunning || pty.validatedStarts != 1 {
				t.Fatalf("create state/validated starts = %s/%d", created.Detail.State, pty.validatedStarts)
			}
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(home, ".pi", "agent", "models.json")
			if cli == contract.CLITypeOmp {
				configPath = filepath.Join(home, ".omp", "agent", "models.yml")
			}
			written, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("read executed config mutation: %v", err)
			}
			if !strings.Contains(string(written), wantProvider) || !strings.Contains(string(written), secret) || !strings.Contains(string(written), "production-model") {
				t.Fatalf("executed config lacks joint provider/key/model evidence: %s", written)
			}
			before, err := h.manager.RemoteSnapshotByID(string(created.Detail.ID))
			if err != nil {
				t.Fatal(err)
			}
			acquireTransactionControl(t, h, principal, created.Detail.ID, "production-restart-"+string(cli))
			restarted, adapterErr := h.adapter.RestartSession(context.Background(), contract.RequestID("request-production-restart-"+string(cli)), principal, created.Detail.ID)
			if adapterErr != nil {
				t.Fatalf("production restart: %v", adapterErr)
			}
			after, err := h.manager.RemoteSnapshotByID(string(created.Detail.ID))
			if err != nil {
				t.Fatal(err)
			}
			if restarted.Detail.State != contract.SessionStateRunning || after.Revisions.Run <= before.Revisions.Run || h.registry.Count() != 1 || pty.validatedStarts != 2 {
				t.Fatalf("restart state/run/registry/validated = %s/%d→%d/%d/%d", restarted.Detail.State, before.Revisions.Run, after.Revisions.Run, h.registry.Count(), pty.validatedStarts)
			}
			written, err = os.ReadFile(configPath)
			if err != nil || !strings.Contains(string(written), secret) {
				t.Fatalf("restart config mutation result = %q, err=%v", written, err)
			}
			pty.mu.Lock()
			writes := append([][]byte(nil), pty.writes...)
			pty.mu.Unlock()
			if len(writes) != 2 {
				t.Fatalf("bootstrap write count = %d, want create+restart", len(writes))
			}
			for _, write := range writes {
				if !strings.Contains(string(write), "--provider "+wantProvider) || !strings.HasSuffix(string(write), "\r\n") {
					t.Fatalf("bootstrap writes = %q", writes)
				}
			}
		})
	}
}

func TestProductionCreateExactReadyBootstrapBeforePublish(t *testing.T) {
	pty := newTransactionPTY(t)
	h := newTransactionHarness(t, pty)
	principal := remote.DevicePrincipal{DeviceID: "device-create", DeviceName: "phone"}
	result, adapterErr := h.adapter.CreateSession(context.Background(), "request-create", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
	if adapterErr != nil {
		t.Fatalf("CreateSession: %v", adapterErr)
	}
	if result.Detail.ID == "" || result.Detail.State != contract.SessionStateRunning {
		t.Fatalf("published detail = %#v", result.Detail)
	}
	pty.mu.Lock()
	events := append([]string(nil), pty.events...)
	writes := append([][]byte(nil), pty.writes...)
	pty.mu.Unlock()
	if len(events) < 3 || events[0][:6] != "start:" || events[1][:6] != "ready:" || events[2][:10] != "bootstrap:" {
		t.Fatalf("effect order = %v, want start→ready→bootstrap", events)
	}
	if len(writes) != 1 || string(writes[0]) != "claudecode --model model\r\n" {
		t.Fatalf("bootstrap payloads = %q", writes)
	}
	if h.registry.Count() != 1 || len(h.manager.ListRemoteSafeSnapshots()) != 1 {
		t.Fatalf("published registry/authority counts = %d/%d", h.registry.Count(), len(h.manager.ListRemoteSafeSnapshots()))
	}
}

func TestProductionCreateReadyOrBootstrapFailurePublishesZero(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failReady bool
	}{
		{name: "ready-timeout", failReady: true},
		{name: "bootstrap-write"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pty := newTransactionPTY(t)
			if tc.failReady {
				pty.failReadyAt = 1
			} else {
				pty.failWriteAt = 1
			}
			h := newTransactionHarness(t, pty)
			principal := remote.DevicePrincipal{DeviceID: "device-fail", DeviceName: "phone"}
			_, adapterErr := h.adapter.CreateSession(context.Background(), "request-fail", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
			if adapterErr == nil {
				t.Fatal("injected PTY failure published success")
			}
			if h.registry.Count() != 0 || len(h.manager.ListRemoteSafeSnapshots()) != 0 {
				t.Fatalf("failure published registry/authority = %d/%d", h.registry.Count(), len(h.manager.ListRemoteSafeSnapshots()))
			}
			pty.mu.Lock()
			closeCount, active := pty.closeCount, len(pty.current)
			pty.mu.Unlock()
			if closeCount != 1 || active != 0 {
				t.Fatalf("failure exact cleanup close/active = %d/%d", closeCount, active)
			}
		})
	}
}

func TestProductionRestartReusesExecutorAndReplacesExactBinding(t *testing.T) {
	pty := newTransactionPTY(t)
	h := newTransactionHarness(t, pty)
	principal := remote.DevicePrincipal{DeviceID: "device-restart", DeviceName: "phone"}
	created, adapterErr := h.adapter.CreateSession(context.Background(), "request-create", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
	if adapterErr != nil {
		t.Fatal(adapterErr)
	}
	sid := created.Detail.ID
	lease, _ := h.runtime.Directory().Attach(principal.DeviceID, principal.DeviceName, remote.ConnectionID("restart-connection"), sid)
	if lease == nil {
		t.Fatal("attach returned nil lease")
	}
	if _, err := h.runtime.Gate().Acquire(context.Background(), principal, lease, sid); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	before, err := h.manager.RemoteSnapshotByID(string(sid))
	if err != nil {
		t.Fatal(err)
	}
	restarted, adapterErr := h.adapter.RestartSession(context.Background(), "request-restart", principal, sid)
	if adapterErr != nil {
		t.Fatalf("RestartSession: %v", adapterErr)
	}
	after, err := h.manager.RemoteSnapshotByID(string(sid))
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Detail.ID != sid || after.Revisions.Membership != before.Revisions.Membership || after.Revisions.Run <= before.Revisions.Run {
		t.Fatalf("restart invariants before=%#v after=%#v detail=%#v", before.Revisions, after.Revisions, restarted.Detail)
	}
	if h.registry.Count() != 1 {
		t.Fatalf("registry count = %d, want exact replacement only", h.registry.Count())
	}
	pty.mu.Lock()
	starts, writes, closes := len(pty.startSpecs), len(pty.writes), pty.closeCount
	pty.mu.Unlock()
	if starts != 2 || writes != 2 || closes != 1 {
		t.Fatalf("restart effects start/write/close = %d/%d/%d, want 2/2/1", starts, writes, closes)
	}
	stopped, stopErr := h.adapter.StopSession(context.Background(), "request-post-restart-stop", principal, sid)
	if stopErr != nil {
		t.Fatalf("post-restart lifecycle lane remained drained: %v", stopErr)
	}
	if stopped.Detail.State != contract.SessionStateStopped || h.registry.Count() != 1 {
		t.Fatalf("post-restart stop detail/retained closed registry = %#v/%d", stopped.Detail, h.registry.Count())
	}
}

func TestProductionRestartCommitsBoundaryBeforeStagedOutput(t *testing.T) {
	pty := newTransactionPTY(t)
	h := newTransactionHarness(t, pty)
	principal := remote.DevicePrincipal{DeviceID: "device-restart-stage", DeviceName: "phone"}
	created, adapterErr := h.adapter.CreateSession(context.Background(), "request-stage-create", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
	if adapterErr != nil {
		t.Fatal(adapterErr)
	}
	sid := created.Detail.ID
	acquireTransactionControl(t, h, principal, sid, "stage-restart-connection")
	pty.onReady = func(startCount int) {
		if startCount != 2 {
			return
		}
		pty.mu.Lock()
		handle := pty.runHandles[1]
		pty.mu.Unlock()
		h.runtime.Projector().OfferOutput(handle, 73, []byte("staged-before-commit"))
	}
	if _, adapterErr = h.adapter.RestartSession(context.Background(), "request-stage-restart", principal, sid); adapterErr != nil {
		t.Fatal(adapterErr)
	}
	snapshot, _, err := h.runtime.Feed().SnapshotAndSubscribe(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 2 || snapshot.Records[0].Kind != remote.LiveRecordRestartBoundary || snapshot.Records[0].SourceOrdinal != 1 || snapshot.Records[1].Kind != remote.LiveRecordOutput || snapshot.Records[1].SourceOrdinal != 2 || string(snapshot.Records[1].Output) != "staged-before-commit" {
		t.Fatalf("restart continuity records = %#v", snapshot.Records)
	}
	earliest, latest := h.streams.SeqBounds(sid)
	if earliest != 1 || latest != 2 {
		t.Fatalf("stream bounds = %d..%d, want boundary→staged output 1..2", earliest, latest)
	}
}

func TestProductionRestartPreCommitExitPublishesZeroAndClosesNewBinding(t *testing.T) {
	pty := newTransactionPTY(t)
	h := newTransactionHarness(t, pty)
	principal := remote.DevicePrincipal{DeviceID: "device-restart-terminal", DeviceName: "phone"}
	created, adapterErr := h.adapter.CreateSession(context.Background(), "request-terminal-create", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
	if adapterErr != nil {
		t.Fatal(adapterErr)
	}
	sid := created.Detail.ID
	acquireTransactionControl(t, h, principal, sid, "terminal-restart-connection")
	before, _ := h.manager.RemoteSnapshotByID(string(sid))
	pty.onReady = func(startCount int) {
		if startCount != 2 {
			return
		}
		pty.mu.Lock()
		handle := pty.runHandles[1]
		pty.mu.Unlock()
		h.runtime.Projector().OfferExit(handle, 41, true)
	}
	if _, adapterErr = h.adapter.RestartSession(context.Background(), "request-terminal-restart", principal, sid); adapterErr == nil {
		t.Fatal("pre-commit new-process exit returned restart success")
	}
	after, err := h.manager.RemoteSnapshotByID(string(sid))
	if err != nil {
		t.Fatal(err)
	}
	pty.mu.Lock()
	active, closes := len(pty.current), pty.closeCount
	pty.mu.Unlock()
	if after.Revisions.Run != before.Revisions.Run || after.State != session.AuthorityUnavailable || h.registry.Count() != 0 || active != 0 || closes != 2 {
		t.Fatalf("terminal stage leaked/published: before=%#v after=%#v registry=%d active/closes=%d/%d", before, after, h.registry.Count(), active, closes)
	}
	if earliest, latest := h.streams.SeqBounds(sid); earliest != 0 || latest != 0 {
		t.Fatalf("failed terminal stage published stream frames %d..%d", earliest, latest)
	}
}

func acquireTransactionControl(t *testing.T, h *transactionHarness, principal remote.DevicePrincipal, sid contract.SessionID, suffix string) {
	t.Helper()
	lease, _ := h.runtime.Directory().Attach(principal.DeviceID, principal.DeviceName, remote.ConnectionID(suffix), sid)
	if lease == nil {
		t.Fatal("attach returned nil lease")
	}
	if _, err := h.runtime.Gate().Acquire(context.Background(), principal, lease, sid); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
}

func TestProductionRestartProcessEffectFailuresPublishNoNewReceipt(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configure  func(*transactionPTY)
		wantCloses int
	}{
		{name: "pty-start", configure: func(p *transactionPTY) { p.failStartAt = 2 }, wantCloses: 1},
		{name: "pty-ready", configure: func(p *transactionPTY) { p.failReadyAt = 2 }, wantCloses: 2},
		{name: "bootstrap", configure: func(p *transactionPTY) { p.failWriteAt = 2 }, wantCloses: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pty := newTransactionPTY(t)
			tc.configure(pty)
			h := newTransactionHarness(t, pty)
			principal := remote.DevicePrincipal{DeviceID: contract.DeviceID("device-restart-" + tc.name), DeviceName: "phone"}
			created, adapterErr := h.adapter.CreateSession(context.Background(), "request-create", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
			if adapterErr != nil {
				t.Fatal(adapterErr)
			}
			sid := created.Detail.ID
			acquireTransactionControl(t, h, principal, sid, tc.name+"-connection")
			before, _ := h.manager.RemoteSnapshotByID(string(sid))
			_, adapterErr = h.adapter.RestartSession(context.Background(), contract.RequestID("request-restart-"+tc.name), principal, sid)
			if adapterErr == nil {
				t.Fatal("injected restart effect failure returned success")
			}
			after, err := h.manager.RemoteSnapshotByID(string(sid))
			if err != nil {
				t.Fatal(err)
			}
			if after.Revisions.Run != before.Revisions.Run || after.State != session.AuthorityUnavailable || h.registry.Count() != 0 {
				t.Fatalf("failure published receipt/state/registry: before=%#v after=%#v registry=%d", before, after, h.registry.Count())
			}
			pty.mu.Lock()
			active, closes := len(pty.current), pty.closeCount
			pty.mu.Unlock()
			if active != 0 || closes != tc.wantCloses {
				t.Fatalf("failed restart active/close = %d/%d, want 0/%d", active, closes, tc.wantCloses)
			}
		})
	}
}

func TestProductionRestartConfigFailurePublishesNoNewReceipt(t *testing.T) {
	pty := newTransactionPTY(t)
	h := newTransactionHarnessWithPlanner(t, pty, transactionPlanner{config: true}, nil)
	principal := remote.DevicePrincipal{DeviceID: "device-restart-config", DeviceName: "phone"}
	created, adapterErr := h.adapter.CreateSession(context.Background(), "request-config-create", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
	if adapterErr != nil {
		t.Fatal(adapterErr)
	}
	sid := created.Detail.ID
	acquireTransactionControl(t, h, principal, sid, "config-restart-connection")
	before, _ := h.manager.RemoteSnapshotByID(string(sid))
	if _, adapterErr = h.adapter.RestartSession(context.Background(), "request-config-restart", principal, sid); adapterErr == nil {
		t.Fatal("restart config preimage failure returned success")
	}
	after, err := h.manager.RemoteSnapshotByID(string(sid))
	if err != nil {
		t.Fatal(err)
	}
	pty.mu.Lock()
	starts, active, closes := len(pty.startSpecs), len(pty.current), pty.closeCount
	pty.mu.Unlock()
	if after.Revisions.Run != before.Revisions.Run || after.State != session.AuthorityUnavailable || h.registry.Count() != 0 || starts != 1 || active != 0 || closes != 1 {
		t.Fatalf("config failure leaked/published: before=%#v after=%#v registry=%d starts/active/closes=%d/%d/%d", before, after, h.registry.Count(), starts, active, closes)
	}
}

func TestProductionRestartSharedVerificationFailureReleasesOldGeneration(t *testing.T) {
	pty := newTransactionPTY(t)
	proxyService := &fakeProxyService{}
	h := newTransactionHarnessWithProxy(t, pty, proxyService)
	principal := remote.DevicePrincipal{DeviceID: "device-restart-shared-fail", DeviceName: "phone"}
	created, adapterErr := h.adapter.CreateSession(context.Background(), "request-shared-fail-create", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
	if adapterErr != nil {
		t.Fatal(adapterErr)
	}
	sid := created.Detail.ID
	acquireTransactionControl(t, h, principal, sid, "shared-fail-connection")
	before, _ := h.manager.RemoteSnapshotByID(string(sid))
	proxyService.mu.Lock()
	proxyService.startCalls[len(proxyService.startCalls)-1].BackendURL = "https://externally-mutated.example/v1"
	proxyService.mu.Unlock()
	if _, adapterErr = h.adapter.RestartSession(context.Background(), "request-shared-fail-restart", principal, sid); adapterErr == nil {
		t.Fatal("restart shared verification failure returned success")
	}
	after, err := h.manager.RemoteSnapshotByID(string(sid))
	if err != nil {
		t.Fatal(err)
	}
	h.ownersMu.Lock()
	ownerCount := len(h.owners)
	h.ownersMu.Unlock()
	pty.mu.Lock()
	starts, active, closes := len(pty.startSpecs), len(pty.current), pty.closeCount
	pty.mu.Unlock()
	if after.Revisions.Run != before.Revisions.Run || after.State != session.AuthorityUnavailable || h.registry.Count() != 0 || h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy) != 0 || ownerCount != 0 || starts != 1 || active != 0 || closes != 1 {
		t.Fatalf("shared failure leaked/published: before=%#v after=%#v registry=%d promoted=%d owners=%d starts/active/closes=%d/%d/%d", before, after, h.registry.Count(), h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy), ownerCount, starts, active, closes)
	}
}

func TestProductionSharedLeaseExactRestartAndStaleExit(t *testing.T) {
	pty := newTransactionPTY(t)
	h := newTransactionHarnessWithProxy(t, pty, &fakeProxyService{})
	appOwner := &App{sharedCoord: h.coord, sharedLeases: make(map[remote.SharedLeaseOwnerKey]*remote.SharedDependencyLease)}
	h.adapter.SetSharedLeaseTransfer(appOwner.prepareSharedLeaseTransfer, appOwner.releaseSharedLeasesExact)
	h.runtime.Projector().SetRunTerminalCleanup(appOwner.releaseSharedLeasesExact)
	principal := remote.DevicePrincipal{DeviceID: "device-shared-restart", DeviceName: "phone"}
	created, adapterErr := h.adapter.CreateSession(context.Background(), "request-shared-create", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
	if adapterErr != nil {
		t.Fatal(adapterErr)
	}
	sid := created.Detail.ID
	before, _ := h.manager.RemoteSnapshotByID(string(sid))
	if h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy) != 1 {
		t.Fatal("create did not promote exactly one shared lease")
	}
	acquireTransactionControl(t, h, principal, sid, "shared-restart-connection")
	if _, adapterErr = h.adapter.RestartSession(context.Background(), "request-shared-restart", principal, sid); adapterErr != nil {
		t.Fatal(adapterErr)
	}
	after, _ := h.manager.RemoteSnapshotByID(string(sid))
	if after.Revisions.Run <= before.Revisions.Run || h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy) != 1 {
		t.Fatalf("restart shared invariant: run %d→%d, promoted=%d", before.Revisions.Run, after.Revisions.Run, h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy))
	}
	appOwner.sharedLeaseMu.Lock()
	ownerCount := len(appOwner.sharedLeases)
	var ownerEpoch uint64
	for key := range appOwner.sharedLeases {
		ownerEpoch = key.RunEpoch
	}
	appOwner.sharedLeaseMu.Unlock()
	if ownerCount != 1 || ownerEpoch != after.Revisions.Run {
		t.Fatalf("owner count/epoch = %d/%d, want 1/%d", ownerCount, ownerEpoch, after.Revisions.Run)
	}
	pty.mu.Lock()
	oldHandle, newHandle := pty.runHandles[0], pty.runHandles[1]
	pty.mu.Unlock()
	h.runtime.Projector().OfferExit(oldHandle, 23, true)
	if h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy) != 1 {
		t.Fatal("stale old-run exit released the replacement generation")
	}
	h.runtime.Projector().OfferExit(newHandle, 0, false)
	if h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy) != 0 {
		t.Fatal("current natural exit retained the exact shared lease")
	}
	appOwner.sharedLeaseMu.Lock()
	ownerCount = len(appOwner.sharedLeases)
	appOwner.sharedLeaseMu.Unlock()
	if ownerCount != 0 {
		t.Fatalf("natural exit owner count = %d, want 0", ownerCount)
	}
}

func TestProductionSharedLeaseConcurrentRestartAndOldNaturalExit(t *testing.T) {
	pty := newTransactionPTY(t)
	h := newTransactionHarnessWithProxy(t, pty, &fakeProxyService{})
	principal := remote.DevicePrincipal{DeviceID: "device-shared-concurrent", DeviceName: "phone"}
	created, adapterErr := h.adapter.CreateSession(context.Background(), "request-concurrent-create", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
	if adapterErr != nil {
		t.Fatal(adapterErr)
	}
	sid := created.Detail.ID
	acquireTransactionControl(t, h, principal, sid, "shared-concurrent-connection")
	pty.mu.Lock()
	oldHandle := pty.runHandles[0]
	pty.mu.Unlock()
	readyEntered := make(chan struct{})
	continueReady := make(chan struct{})
	pty.onReady = func(startCount int) {
		if startCount == 2 {
			close(readyEntered)
			<-continueReady
		}
	}
	restartDone := make(chan *remote.AdapterError, 1)
	go func() {
		_, restartErr := h.adapter.RestartSession(context.Background(), "request-concurrent-restart", principal, sid)
		restartDone <- restartErr
	}()
	select {
	case <-readyEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("restart did not reach staged PTY ready barrier")
	}
	h.runtime.Projector().OfferExit(oldHandle, 29, true)
	close(continueReady)
	select {
	case restartErr := <-restartDone:
		if restartErr != nil {
			t.Fatalf("restart lost race to stale old exit: %v", restartErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("restart did not resolve after ready barrier")
	}
	after, err := h.manager.RemoteSnapshotByID(string(sid))
	if err != nil {
		t.Fatal(err)
	}
	h.ownersMu.Lock()
	ownerCount := len(h.owners)
	var ownerEpoch uint64
	for key := range h.owners {
		ownerEpoch = key.RunEpoch
	}
	h.ownersMu.Unlock()
	if ownerCount != 1 || ownerEpoch != after.Revisions.Run || h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy) != 1 || h.registry.Count() != 1 {
		t.Fatalf("concurrent transfer state owners/epoch/promoted/registry=%d/%d/%d/%d, run=%d", ownerCount, ownerEpoch, h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy), h.registry.Count(), after.Revisions.Run)
	}
}

func TestProductionSharedLeaseStopThenRestartReacquiresNewGeneration(t *testing.T) {
	pty := newTransactionPTY(t)
	h := newTransactionHarnessWithProxy(t, pty, &fakeProxyService{})
	appOwner := &App{sharedCoord: h.coord, sharedLeases: make(map[remote.SharedLeaseOwnerKey]*remote.SharedDependencyLease)}
	h.adapter.SetSharedLeaseTransfer(appOwner.prepareSharedLeaseTransfer, appOwner.releaseSharedLeasesExact)
	h.runtime.Projector().SetRunTerminalCleanup(appOwner.releaseSharedLeasesExact)
	principal := remote.DevicePrincipal{DeviceID: "device-shared-stop-restart", DeviceName: "phone"}
	created, adapterErr := h.adapter.CreateSession(context.Background(), "request-stop-restart-create", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
	if adapterErr != nil {
		t.Fatal(adapterErr)
	}
	sid := created.Detail.ID
	acquireTransactionControl(t, h, principal, sid, "stop-restart-connection")
	before, _ := h.manager.RemoteSnapshotByID(string(sid))
	if _, adapterErr = h.adapter.StopSession(context.Background(), "request-stop-before-restart", principal, sid); adapterErr != nil {
		t.Fatal(adapterErr)
	}
	if h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy) != 0 || h.registry.Count() != 1 {
		t.Fatalf("stop promoted/closed-capability registry = %d/%d, want 0/1", h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy), h.registry.Count())
	}
	restarted, adapterErr := h.adapter.RestartSession(context.Background(), "request-restart-after-stop", principal, sid)
	if adapterErr != nil {
		t.Fatalf("Restart after Stop: %v", adapterErr)
	}
	after, err := h.manager.RemoteSnapshotByID(string(sid))
	if err != nil {
		t.Fatal(err)
	}
	appOwner.sharedLeaseMu.Lock()
	ownerCount := len(appOwner.sharedLeases)
	var ownerEpoch uint64
	for key := range appOwner.sharedLeases {
		ownerEpoch = key.RunEpoch
	}
	appOwner.sharedLeaseMu.Unlock()
	if restarted.Detail.State != contract.SessionStateRunning || after.Revisions.Run <= before.Revisions.Run || h.registry.Count() != 1 || h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy) != 1 || ownerCount != 1 || ownerEpoch != after.Revisions.Run {
		t.Fatalf("stop→restart state/run/registry/promoted/owner=%s/%d→%d/%d/%d/%d@%d", restarted.Detail.State, before.Revisions.Run, after.Revisions.Run, h.registry.Count(), h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy), ownerCount, ownerEpoch)
	}
}

func TestProductionSharedLeaseExactStopAndRemove(t *testing.T) {
	for _, operation := range []string{"stop", "remove"} {
		t.Run(operation, func(t *testing.T) {
			pty := newTransactionPTY(t)
			h := newTransactionHarnessWithProxy(t, pty, &fakeProxyService{})
			appOwner := &App{sharedCoord: h.coord, sharedLeases: make(map[remote.SharedLeaseOwnerKey]*remote.SharedDependencyLease)}
			h.adapter.SetSharedLeaseTransfer(appOwner.prepareSharedLeaseTransfer, appOwner.releaseSharedLeasesExact)
			h.runtime.Projector().SetRunTerminalCleanup(appOwner.releaseSharedLeasesExact)
			principal := remote.DevicePrincipal{DeviceID: contract.DeviceID("device-shared-" + operation), DeviceName: "phone"}
			created, adapterErr := h.adapter.CreateSession(context.Background(), contract.RequestID("request-shared-create-"+operation), principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
			if adapterErr != nil {
				t.Fatal(adapterErr)
			}
			sid := created.Detail.ID
			acquireTransactionControl(t, h, principal, sid, operation+"-connection")
			if operation == "stop" {
				if _, adapterErr = h.adapter.StopSession(context.Background(), "request-stop", principal, sid); adapterErr != nil {
					t.Fatal(adapterErr)
				}
			} else if adapterErr = h.adapter.RemoveSession(context.Background(), "request-remove", principal, sid); adapterErr != nil {
				t.Fatal(adapterErr)
			}
			wantRegistry := 0
			if operation == "stop" {
				wantRegistry = 1 // closed exact capability remains for Remove/Restart
			}
			if h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy) != 0 || h.registry.Count() != wantRegistry {
				t.Fatalf("%s promoted lease/registry = %d/%d, want 0/%d", operation, h.coord.PromotedLeaseCount(remote.SharedServiceClaudeProxy), h.registry.Count(), wantRegistry)
			}
			appOwner.sharedLeaseMu.Lock()
			ownerCount := len(appOwner.sharedLeases)
			appOwner.sharedLeaseMu.Unlock()
			if ownerCount != 0 {
				t.Fatalf("%s retained %d exact owners", operation, ownerCount)
			}
			if operation == "stop" {
				if removeErr := h.adapter.RemoveSession(context.Background(), "request-remove-after-stop", principal, sid); removeErr != nil {
					t.Fatalf("Remove after Stop lost the retained exact capability: %v", removeErr)
				}
				if h.registry.Count() != 0 {
					t.Fatalf("Remove after Stop retained registry=%d", h.registry.Count())
				}
			}
		})
	}
}
