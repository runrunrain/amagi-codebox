package remote

// remote_launch_resolver.go — M2 RemoteLaunchResolver seam (design §5.4).
//
// {cliType, workdir?} does not carry provider/preset/model/key, so a host-only
// resolver resolves the launch context from the host's own config. The final
// executable/shell/env/PTY spec is delegated to the injected platform.CLIResolver.
//
// The resolver NEVER does exec.LookPath, NEVER copies candidate literals from
// resolver.go, and NEVER logs/persists the ResolvedLaunchSpec. It only assembles
// profile refs + workdir + mode + args + per-call env, then calls
// platform.CLIResolver.Resolve.

import (
	"context"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// RemoteLaunchRecipe — immutable frozen launch recipe (design §4.4, §5.4)
// ---------------------------------------------------------------------------

// RemoteLaunchRecipe holds the stable refs needed to re-resolve a launch on
// restart. It contains NO secret/env. Stored in the catalog entry.
type RemoteLaunchRecipe struct {
	CLIType contract.CLIType
	Workdir string // canonicalized host path
	// Stable host-default refs (populated by the resolver from settings).
	// These are opaque string refs, not secrets.
	ProviderRef string
	PresetRef   string
	ModelRef    string
	Mode        string
	ShellPath   string
	UseProxy    bool
	UseHeadroom bool
}

// RemoteLaunchResolution is the resolver output: the recipe + the ephemeral spec.
type RemoteLaunchResolution struct {
	Recipe RemoteLaunchRecipe
	// Spec is the ephemeral resolved launch spec (never persisted/logged).
	// In production this is platform.ResolvedLaunchSpec; here it is an opaque
	// interface so the resolver does not depend on platform concrete types.
	Spec any
}

// ---------------------------------------------------------------------------
// RemoteLaunchResolver interface (design §5.4 CLIResolver seam)
// ---------------------------------------------------------------------------

// RemoteLaunchResolver resolves host launch context for create/restart/probe.
// The final spec is delegated to the injected platform.CLIResolver.
type RemoteLaunchResolver interface {
	// ResolveCreate resolves a new-session launch from the client request.
	ResolveCreate(ctx context.Context, req contract.CreateSessionRequest) (RemoteLaunchResolution, *LaunchResolveFailure)
	// ResolveRestart resolves a launch from a stored recipe.
	ResolveRestart(ctx context.Context, recipe RemoteLaunchRecipe) (RemoteLaunchResolution, *LaunchResolveFailure)
	// Probe checks availability of one CLI type (zero side-effect).
	Probe(ctx context.Context, cli contract.CLIType) (contract.CLIAvailability, *LaunchResolveFailure)
}

// ---------------------------------------------------------------------------
// noopRemoteLaunchResolver — stub for environments without platform resolver
// ---------------------------------------------------------------------------

// noopRemoteLaunchResolver always returns capability-unavailable. It is the
// default when no platform.CLIResolver is injected. This ensures routes stay
// fail-closed until real wiring (design §4A hardening gate).
type noopRemoteLaunchResolver struct{}

// NewNoopRemoteLaunchResolver returns a resolver that always fails (capability
// unavailable for all CLIs).
func NewNoopRemoteLaunchResolver() RemoteLaunchResolver {
	return noopRemoteLaunchResolver{}
}

func (noopRemoteLaunchResolver) ResolveCreate(ctx context.Context, req contract.CreateSessionRequest) (RemoteLaunchResolution, *LaunchResolveFailure) {
	return RemoteLaunchResolution{}, newLaunchResolveFailure(LaunchResolveFailureCapability, req.CLIType)
}

func (noopRemoteLaunchResolver) ResolveRestart(ctx context.Context, recipe RemoteLaunchRecipe) (RemoteLaunchResolution, *LaunchResolveFailure) {
	return RemoteLaunchResolution{}, newLaunchResolveFailure(LaunchResolveFailureCapability, recipe.CLIType)
}

func (noopRemoteLaunchResolver) Probe(ctx context.Context, cli contract.CLIType) (contract.CLIAvailability, *LaunchResolveFailure) {
	return contract.CLIAvailability{CLIType: cli, Available: false}, newLaunchResolveFailure(LaunchResolveFailureCapability, cli)
}

// ---------------------------------------------------------------------------
// LaunchRawPort — the raw launch effect port (design §4.4)
// ---------------------------------------------------------------------------

// LaunchRawPort accepts a recipe + resolved spec and executes one bounded
// launch effect. It is ONLY called from within a gated callback (design §4.2).
// It MUST NOT re-enter the gate, registry, Server, or do network broadcast.
type LaunchRawPort interface {
	// StartProcess starts the CLI process run-scoped (M-003): the obsPermit is
	// injected into the raw PTY so its output/exit flows through the H1 committer.
	// A nil obsPermit is a non-run-scoped start (legacy/diagnostic).
	StartProcess(ctx context.Context, sessionID contract.SessionID, recipe RemoteLaunchRecipe, spec any, obsPermit *RunObservationPermit) error
}

// SessionRawPort is the raw mutation port for stop/restart/remove (design §4.2).
type SessionRawPort interface {
	StopSession(ctx context.Context, sessionID contract.SessionID) error
	RemoveSession(ctx context.Context, sessionID contract.SessionID) error
	ResizeSession(ctx context.Context, sessionID contract.SessionID, cols, rows int) error
}

// noopLaunchRawPort is a raw port that records calls but does nothing (tests).
type noopLaunchRawPort struct{}

func (noopLaunchRawPort) StartProcess(ctx context.Context, sessionID contract.SessionID, recipe RemoteLaunchRecipe, spec any, obsPermit *RunObservationPermit) error {
	return nil
}

// noopSessionRawPort records nothing (tests inject fakes).
type noopSessionRawPort struct{}

func (noopSessionRawPort) StopSession(ctx context.Context, sessionID contract.SessionID) error {
	return nil
}
func (noopSessionRawPort) RemoveSession(ctx context.Context, sessionID contract.SessionID) error {
	return nil
}
func (noopSessionRawPort) ResizeSession(ctx context.Context, sessionID contract.SessionID, cols, rows int) error {
	return nil
}
