package remote

// remote_launch_resolver_prod.go — M2 production RemoteLaunchResolver that
// delegates the final executable/shell/env/PTY spec to the injected real
// platform.CLIResolver (design §5.4 CLIResolver seam).
//
// This resolver is the production wiring point: app.go injects the same
// platform.CLIResolver instance that M1 HostSummary uses (design §5.4:
// "composition注入 app.go 已持有的同一 platform.CLIResolver"). The noop
// resolver stays only for tests.
//
// The resolver NEVER does exec.LookPath, NEVER copies candidate literals from
// resolver.go, and NEVER logs/persists the ResolvedLaunchSpec. It only:
//   - validates the frozen CLIType and maps it to the internal AppType string;
//   - canonicalizes the workdir (omitted → user home; provided → resolve +
//     clean + symlink-resolve + exists + is-directory + enterable);
//   - assembles platform.ResolveRequest and calls CLIResolver.Resolve;
//   - classifies failures into the four frozen kinds (workdir/context/
//     capability/effect) consumed by v1ErrorMapper (design §8.3).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Shim interface (design §5.4: "不得 exec.LookPath，不得 ResolveExecutable，
// 不得复制 resolver.go 中任何 candidate literal")
// ---------------------------------------------------------------------------

// platformCLIResolveShim is the narrow surface the production resolver needs.
// The real platform.CLIResolver satisfies it; tests inject a fake.
type platformCLIResolveShim interface {
	Resolve(platform.ResolveRequest) (platform.ResolvedLaunchSpec, error)
}

// remoteLaunchDefaultsReader reads host-default recipe refs from settings
// (design §5.4 step 3: "最近一次成功且已 activation 的本地桌面 launch recipe per
// CLI"). Optional: nil means no host default; the resolver still resolves the
// binary path via Resolve, and recipe refs are empty (the launch effect owns
// provider/model wiring).
type remoteLaunchDefaultsReader interface {
	// HostDefaultRefs returns the stable recipe refs (provider/preset/model/mode/
	// shell/headroom) for a CLI type, or ok=false if no unique default exists
	// (design §5.4: "0 个或多个 fail closed，绝不取 map 第一项").
	HostDefaultRefs(cli contract.CLIType) (RemoteLaunchRecipe, bool)
}

// ---------------------------------------------------------------------------
// productionRemoteLaunchResolver
// ---------------------------------------------------------------------------

// productionRemoteLaunchResolver wraps the real platform.CLIResolver. It is the
// production implementation of RemoteLaunchResolver (design §5.4).
type productionRemoteLaunchResolver struct {
	cli      platformCLIResolveShim
	homeDir  string // canonicalized user home (omitted-workdir default)
	env      []string
	defaults remoteLaunchDefaultsReader // optional, nil-safe
}

// NewProductionRemoteLaunchResolver creates the production resolver wrapping the
// real platform.CLIResolver. homeDir is the canonicalized user home dir for the
// omitted-workdir default. env is the process env passed to Resolve (typically
// os.Environ()); nil falls back to os.Environ(). defaults is the optional
// host-default recipe reader (nil-safe).
func NewProductionRemoteLaunchResolver(
	cli platformCLIResolveShim,
	homeDir string,
	env []string,
	defaults remoteLaunchDefaultsReader,
) RemoteLaunchResolver {
	if cli == nil {
		// No real resolver: fail closed (noop). This keeps routes fail-closed
		// rather than panicking.
		return noopRemoteLaunchResolver{}
	}
	if env == nil {
		env = os.Environ()
	}
	return &productionRemoteLaunchResolver{
		cli:      cli,
		homeDir:  homeDir,
		env:      env,
		defaults: defaults,
	}
}

// ResolveCreate resolves a new-session launch (design §5.4 create path).
func (r *productionRemoteLaunchResolver) ResolveCreate(ctx context.Context, req contract.CreateSessionRequest) (RemoteLaunchResolution, *LaunchResolveFailure) {
	cliType := req.CLIType
	// 1. Validate frozen CLIType (reject any type outside the frozen wire set).
	if !isKnownCLIType(cliType) {
		return RemoteLaunchResolution{}, newLaunchResolveFailure(LaunchResolveFailureContext, cliType)
	}
	// 2. Resolve workdir (omitted → home; provided → canonicalize + validate).
	var providedWorkdir string
	if req.Workdir != nil {
		providedWorkdir = *req.Workdir
	}
	workdir, werr := r.resolveWorkdir(providedWorkdir)
	if werr != nil {
		return RemoteLaunchResolution{}, newLaunchResolveFailure(LaunchResolveFailureWorkdir, cliType)
	}
	// 3. Assemble host-default recipe refs (optional; empty is OK — Resolve only
	//    needs AppType to find the binary).
	recipe := RemoteLaunchRecipe{
		CLIType: cliType,
		Workdir: workdir,
	}
	if r.defaults != nil {
		if dr, ok := r.defaults.HostDefaultRefs(cliType); ok {
			// Merge host-default refs (provider/preset/model/mode/shell/headroom).
			recipe.ProviderRef = dr.ProviderRef
			recipe.PresetRef = dr.PresetRef
			recipe.ModelRef = dr.ModelRef
			recipe.Mode = dr.Mode
			recipe.ShellPath = dr.ShellPath
			recipe.UseHeadroom = dr.UseHeadroom
		}
		// No host default: recipe refs stay empty. Resolution still succeeds — the
		// binary is found via Resolve(AppType). Provider/model wiring is the launch
		// effect's job (raw port), not the resolver's.
	}
	if req.ProviderRef != nil {
		recipe.ProviderRef = *req.ProviderRef
	}
	if req.PresetRef != nil {
		recipe.PresetRef = *req.PresetRef
	}
	if req.ModelRef != nil {
		recipe.ModelRef = *req.ModelRef
	}
	if req.ShellRef != nil {
		recipe.ShellPath = *req.ShellRef
	}
	if req.UseHeadroom != nil {
		recipe.UseHeadroom = *req.UseHeadroom
	}
	// 4. Build ResolveRequest and call the real CLIResolver.
	spec, ferr := r.resolveSpec(recipe)
	if ferr != nil {
		return RemoteLaunchResolution{}, ferr
	}
	return RemoteLaunchResolution{Recipe: recipe, Spec: spec}, nil
}

// ResolveRestart re-resolves a launch from a stored recipe (design §5.4
// restart path: "restart每次从 stable recipe重建当次 request/secret，再 Resolve").
func (r *productionRemoteLaunchResolver) ResolveRestart(ctx context.Context, recipe RemoteLaunchRecipe) (RemoteLaunchResolution, *LaunchResolveFailure) {
	if !isKnownCLIType(recipe.CLIType) {
		return RemoteLaunchResolution{}, newLaunchResolveFailure(LaunchResolveFailureContext, recipe.CLIType)
	}
	spec, ferr := r.resolveSpec(recipe)
	if ferr != nil {
		return RemoteLaunchResolution{}, ferr
	}
	return RemoteLaunchResolution{Recipe: recipe, Spec: spec}, nil
}

// Probe checks CLI availability via the same Resolve path, zero side-effect
// (design §5.4: "Probe使用与真实 create相同的 default+Resolve路径且零副作用").
func (r *productionRemoteLaunchResolver) Probe(ctx context.Context, cli contract.CLIType) (contract.CLIAvailability, *LaunchResolveFailure) {
	if !isKnownCLIType(cli) {
		return contract.CLIAvailability{CLIType: cli, Available: false}, newLaunchResolveFailure(LaunchResolveFailureContext, cli)
	}
	workdir := r.homeDir
	if workdir == "" {
		workdir, _ = os.UserHomeDir()
	}
	recipe := RemoteLaunchRecipe{CLIType: cli, Workdir: workdir}
	spec, ferr := r.resolveSpec(recipe)
	if ferr != nil {
		return contract.CLIAvailability{CLIType: cli, Available: false}, ferr
	}
	available := spec.CLI.Path != ""
	return contract.CLIAvailability{CLIType: cli, Available: available}, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// resolveSpec builds the ResolveRequest from a recipe and calls the real
// CLIResolver.Resolve. Returns a capability failure on Resolve error.
func (r *productionRemoteLaunchResolver) resolveSpec(recipe RemoteLaunchRecipe) (platform.ResolvedLaunchSpec, *LaunchResolveFailure) {
	appType := string(recipe.CLIType) // canonical CLIType string == platform AppType
	resolveReq := platform.ResolveRequest{
		AppType:            appType,
		LaunchMode:         "embedded", // same as M1 HostSummary / desktop embedded PTY
		WorkDir:            recipe.Workdir,
		Env:                r.env,
		RequestedShellPath: strings.TrimSpace(recipe.ShellPath),
		PTYCols:            80,
		PTYRows:            24,
	}
	spec, err := r.cli.Resolve(resolveReq)
	if err != nil {
		return platform.ResolvedLaunchSpec{}, newLaunchResolveFailure(LaunchResolveFailureCapability, recipe.CLIType)
	}
	return spec, nil
}

// resolveWorkdir canonicalizes and validates the workdir (design §5.4 step 2).
// omitted → user home (canonicalized); provided → relative-to-home resolve,
// clean + symlink-resolve, require exists + is-directory + enterable.
// Original path never enters logs/errors.
func (r *productionRemoteLaunchResolver) resolveWorkdir(provided string) (string, error) {
	if strings.TrimSpace(provided) == "" {
		// Omitted → user home.
		if r.homeDir == "" {
			return "", errWorkdirInvalid
		}
		return canonicalDir(r.homeDir)
	}
	base := r.homeDir
	if base == "" {
		base, _ = os.UserHomeDir()
	}
	var resolved string
	if filepath.IsAbs(provided) {
		resolved = provided
	} else {
		resolved = filepath.Join(base, provided)
	}
	return canonicalDir(resolved)
}

// canonicalDir cleans, resolves symlinks, and validates that the path exists,
// is a directory, and is enterable. Returns errWorkdirInvalid on any failure.
func canonicalDir(path string) (string, error) {
	cleaned := filepath.Clean(path)
	// Symlink resolve (best-effort; if EvalSymlinks fails the path is invalid).
	real, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", errWorkdirInvalid
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", errWorkdirInvalid
	}
	if !info.IsDir() {
		return "", errWorkdirInvalid
	}
	// Enterable check: open the directory read-only.
	f, err := os.Open(real)
	if err != nil {
		return "", errWorkdirInvalid
	}
	f.Close()
	return real, nil
}

// isKnownCLIType reports whether the CLIType is one of the frozen wire types
// (design §5.4: the wire set is closed; no internal-only types are accepted).
func isKnownCLIType(cli contract.CLIType) bool {
	for _, k := range contract.KnownCLITypes {
		if k == cli {
			return true
		}
	}
	return false
}

// errWorkdirInvalid is the sentinel for workdir validation failure (design
// §5.4 workdir → 400 bad_request). The message is fixed by the mapper; this
// sentinel carries no path.
var errWorkdirInvalid = errors.New("workdir invalid")
