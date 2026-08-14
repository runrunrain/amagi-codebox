package launchplan

import (
	"context"
	"errors"
	"fmt"

	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote/contract"
)

type CLIType = contract.CLIType

type Mode uint8

const (
	ModeEmbedded Mode = iota + 1
	ModeExternal
)

type Origin uint8

const (
	OriginDesktop Origin = iota + 1
	OriginRemote
	OriginRestart
)

type StableLaunchRefs struct {
	ProviderRef string `json:"providerRef,omitempty"`
	PresetRef   string `json:"presetRef,omitempty"`
	ModelRef    string `json:"modelRef,omitempty"`
	ShellRef    string `json:"shellRef,omitempty"`
	UseHeadroom *bool  `json:"useHeadroom,omitempty"`
}

type StableRecipe struct {
	CLIType     contract.CLIType `json:"cliType"`
	Workdir     string           `json:"workdir"`
	ProviderRef string           `json:"providerRef,omitempty"`
	PresetRef   string           `json:"presetRef,omitempty"`
	ModelRef    string           `json:"modelRef,omitempty"`
	ShellRef    string           `json:"shellRef,omitempty"`
	UseHeadroom bool             `json:"useHeadroom,omitempty"`
}

type BuildRequest struct {
	CLIType    contract.CLIType
	Origin     Origin
	Mode       Mode
	Workdir    string
	StableRefs *StableLaunchRefs
}

type SharedServiceKind uint8

const (
	SharedClaudeHeadroom SharedServiceKind = iota + 1
	SharedCodexHeadroom
)

type ConfigTarget uint8

const (
	ConfigOpenCode ConfigTarget = iota + 1
	ConfigCodex
	ConfigPi
	ConfigOmp
)

type EffectKind uint8

const (
	EffectHeadroomStart EffectKind = iota + 1
	EffectConfigMutation
	EffectPTYStart
	EffectExternalProcessStart
	EffectBootstrapWrite
)

type SecretBufferRef struct{ Index uint16 }

type SharedAdmissionSpec struct {
	Service           SharedServiceKind
	ConfigFingerprint [32]byte
}

type SharedStartSpec struct {
	Service           SharedServiceKind
	ConfigFingerprint [32]byte
	UpstreamURL       string // non-secret real backend URL the shared service forwards to
	ListenPort        int    // canonical Headroom listen port (8787 Claude / 8788 Codex)
}

type ConfigMutationSpec struct {
	Target                 ConfigTarget
	Candidate              SecretBufferRef
	ExpectedPreimageDigest [32]byte
}

type ProcessStartSpec struct {
	Mode             Mode
	Resolved         platform.ResolvedLaunchSpec
	RequireRunHandle bool
}

type BootstrapWriteSpec struct {
	Payload        SecretBufferRef
	StartupCommand string // canonical startup command (e.g. "claude", "codex")
}

type EffectSpec struct {
	Kind      EffectKind
	Shared    *SharedStartSpec
	Config    *ConfigMutationSpec
	Process   *ProcessStartSpec
	Bootstrap *BootstrapWriteSpec
}

type DependencyRevision struct {
	Settings uint64
	Config   uint64
	Secrets  uint64
}

type BuildFailureKind uint8

const (
	FailureCapability BuildFailureKind = iota + 1
	FailureLaunchContext
	FailureWorkdir
)

type BuildFailure struct {
	Kind    BuildFailureKind
	CLIType contract.CLIType
}

// EphemeralSecretBundle owns secret buffers only for plan preparation and
// execution. Callers must Dispose on every return path.
type EphemeralSecretBundle struct {
	buffers  [][]byte
	disposed bool
}

func NewEphemeralSecretBundle(buffers ...[]byte) *EphemeralSecretBundle {
	owned := make([][]byte, len(buffers))
	for i := range buffers {
		owned[i] = append([]byte(nil), buffers[i]...)
	}
	return &EphemeralSecretBundle{buffers: owned}
}

func (b *EphemeralSecretBundle) Dispose() {
	if b == nil || b.disposed {
		return
	}
	for i := range b.buffers {
		for j := range b.buffers[i] {
			b.buffers[i][j] = 0
		}
		b.buffers[i] = nil
	}
	b.buffers = nil
	b.disposed = true
}

func (b *EphemeralSecretBundle) Buffer(ref SecretBufferRef) ([]byte, bool) {
	if b == nil || b.disposed || int(ref.Index) >= len(b.buffers) {
		return nil, false
	}
	return b.buffers[ref.Index], true
}

type Plan struct {
	Recipe     StableRecipe
	Admissions []SharedAdmissionSpec
	Effects    []EffectSpec
	Secrets    *EphemeralSecretBundle
	Dependency DependencyRevision
}

func (p *Plan) Validate() error {
	if p == nil || !KnownCLIType(p.Recipe.CLIType) || p.Recipe.Workdir == "" {
		return ErrInvalidPlan
	}
	seenAdmissions := make(map[SharedServiceKind]struct{}, len(p.Admissions))
	for _, admission := range p.Admissions {
		if admission.Service == 0 {
			return ErrInvalidPlan
		}
		if _, duplicate := seenAdmissions[admission.Service]; duplicate {
			return ErrInvalidPlan
		}
		seenAdmissions[admission.Service] = struct{}{}
	}
	processCount := 0
	lastRank := -1
	for _, effect := range p.Effects {
		nonNil := 0
		if effect.Shared != nil {
			nonNil++
		}
		if effect.Config != nil {
			nonNil++
		}
		if effect.Process != nil {
			nonNil++
		}
		if effect.Bootstrap != nil {
			nonNil++
		}
		if nonNil != 1 || !effectMatchesUnion(effect) {
			return ErrInvalidPlan
		}
		rank := CanonicalEffectRank(effect)
		if rank < lastRank {
			return ErrInvalidEffectOrder
		}
		lastRank = rank
		if effect.Process != nil {
			processCount++
		}
	}
	if processCount != 1 {
		return ErrInvalidPlan
	}
	return nil
}

func effectMatchesUnion(effect EffectSpec) bool {
	switch effect.Kind {
	case EffectHeadroomStart:
		return effect.Shared != nil
	case EffectConfigMutation:
		return effect.Config != nil
	case EffectPTYStart, EffectExternalProcessStart:
		return effect.Process != nil
	case EffectBootstrapWrite:
		return effect.Bootstrap != nil
	default:
		return false
	}
}

func CanonicalEffectRank(effect EffectSpec) int {
	switch effect.Kind {
	case EffectHeadroomStart:
		return 0
	case EffectConfigMutation:
		return 1 + int(effect.Config.Target)
	case EffectPTYStart, EffectExternalProcessStart:
		return 16
	case EffectBootstrapWrite:
		return 17
	default:
		return 100
	}
}

func KnownCLIType(cli contract.CLIType) bool {
	for _, known := range contract.KnownCLITypes {
		if cli == known {
			return true
		}
	}
	return false
}

type Planner interface {
	BuildPlan(context.Context, BuildRequest) (*Plan, *BuildFailure)
	Probe(context.Context, contract.CLIType) (contract.CLIAvailability, *BuildFailure)
}

// FailClosedPlanner is the production-safe bridge used until a CLI has a full
// desktop-equivalent builder. It never resolves a naked executable.
type FailClosedPlanner struct{}

func NewFailClosedPlanner() Planner { return FailClosedPlanner{} }

func (FailClosedPlanner) BuildPlan(_ context.Context, req BuildRequest) (*Plan, *BuildFailure) {
	return nil, &BuildFailure{Kind: FailureLaunchContext, CLIType: req.CLIType}
}

func (FailClosedPlanner) Probe(_ context.Context, cli contract.CLIType) (contract.CLIAvailability, *BuildFailure) {
	return contract.CLIAvailability{CLIType: cli, Available: false}, &BuildFailure{Kind: FailureLaunchContext, CLIType: cli}
}

type ExecutionBinding struct {
	SessionID        string
	RunEpoch         uint64
	RunHandle        any
	SharedAdmissions map[SharedServiceKind]any
}

type EffectEvidence struct{ Process *processcap.StartEvidence }

type PreparedEffect interface {
	Kind() EffectKind
	ArmOwnership()
	Apply(context.Context) (EffectEvidence, error)
}

type CompensationReport struct {
	Attempted uint16
	Failed    uint16
	Outcomes  []CompensationOutcome
}

type PreparedExecution interface {
	Count() int
	Effect(int) PreparedEffect
	RecordApplied(int, EffectEvidence)
	ProcessEvidence() (processcap.StartEvidence, bool)
	Abort(context.Context) CompensationReport
	MarkCommitted()
	DisposeSecrets()
}

type Executor interface {
	Prepare(context.Context, *Plan, ExecutionBinding) (PreparedExecution, error)
}

func ValidateRemoteRequest(req BuildRequest) error {
	if req.Origin != OriginRemote || req.Mode != ModeEmbedded || !KnownCLIType(req.CLIType) {
		return fmt.Errorf("%w: remote launches require known CLI and embedded mode", ErrInvalidPlan)
	}
	return nil
}

var (
	ErrInvalidPlan        = errors.New("launchplan: invalid plan")
	ErrInvalidEffectOrder = errors.New("launchplan: effects are not in canonical order")
)
