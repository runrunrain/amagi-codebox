package remote

// M-004: restart is a real re-resolve+start (not stop-only), lifecycle raw
// effects checkpoint before irreversible syscalls, and stop/remove propagate the
// PTY Close error.

import (
	"context"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// TestM004_CreateStoresRecipe: CreateSession stores the launch recipe so a later
// restart can re-resolve faithfully.
func TestM004_CreateStoresRecipe(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	principal := DevicePrincipal{DeviceID: "dev1"}
	req := contract.CreateSessionRequest{CLIType: contract.CLITypeCodex}
	res, aerr := adapter.CreateSession(context.Background(), "req1", principal, req)
	if aerr != nil {
		t.Fatalf("create: %v", aerr)
	}
	recipe, ok := adapter.Catalog().Recipe(res.Detail.ID)
	if !ok {
		t.Fatal("recipe should be stored after create")
	}
	if recipe.CLIType != contract.CLITypeCodex {
		t.Fatalf("stored recipe cliType=%s want codex", recipe.CLIType)
	}
}

// TestM004_RestartReResolvesAndStarts: restart stops the old process, re-resolves
// the stored recipe, and starts a new process (not a stop-only lie).
func TestM004_RestartReResolvesAndStarts(t *testing.T) {
	adapter, _, launchRaw, sessRaw := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")
	adapter.Catalog().StoreRecipe("s1", RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	entry, _ := adapter.Catalog().Entry("s1")

	baseStops := len(sessRaw.stopped)
	baseStarts := len(launchRaw.started)

	ctx := context.Background()
	result, err := adapter.Gate().DoDesktopLifecycle(ctx, adapter.Runtime().DesktopAuthority(), "s1", LifecycleRestart,
		func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			return adapter.restartRawEffect(ctx, p, "s1", entry)
		})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if !result.RestartBoundary || result.State != contract.SessionStateRunning {
		t.Fatalf("expected running+boundary, got state=%s boundary=%v", result.State, result.RestartBoundary)
	}
	if len(sessRaw.stopped) != baseStops+1 {
		t.Fatalf("old process not stopped: stops=%d want %d", len(sessRaw.stopped), baseStops+1)
	}
	if len(launchRaw.started) != baseStarts+1 {
		t.Fatalf("new process not started: starts=%d want %d", len(launchRaw.started), baseStarts+1)
	}
}

// TestM004_RestartResolveFailurePropagates: a resolve failure aborts restart and
// does not start a new process.
func TestM004_RestartResolveFailurePropagates(t *testing.T) {
	adapter, _, launchRaw, _ := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")
	adapter.Catalog().StoreRecipe("s1", RemoteLaunchRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work"})
	entry, _ := adapter.Catalog().Entry("s1")

	// Swap in a resolver that fails restart.
	adapter.resolver = &failingRestartResolver{}
	baseStarts := len(launchRaw.started)
	ctx := context.Background()
	_, err := adapter.Gate().DoDesktopLifecycle(ctx, adapter.Runtime().DesktopAuthority(), "s1", LifecycleRestart,
		func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			return adapter.restartRawEffect(ctx, p, "s1", entry)
		})
	if err == nil {
		t.Fatal("restart with failing resolver should error")
	}
	if len(launchRaw.started) != baseStarts {
		t.Fatal("no new process should start on resolve failure")
	}
}

// failingRestartResolver fails ResolveRestart, succeeds otherwise.
type failingRestartResolver struct{ fakeResolver }

func (failingRestartResolver) ResolveRestart(ctx context.Context, recipe RemoteLaunchRecipe) (RemoteLaunchResolution, *LaunchResolveFailure) {
	return RemoteLaunchResolution{}, newLaunchResolveFailure(LaunchResolveFailureCapability, recipe.CLIType)
}
