package main

import (
	"errors"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

func TestPlannerRoutesEveryNativeCLIThroughNetworkPolicy(t *testing.T) {
	for _, cli := range []string{"claudecode", "codex", "pi", "omp", "opencode"} {
		calls := 0
		p := &appLaunchPlanner{resolver: fakeCLIResolver{}, networkEnv: func(env []string) ([]string, error) {
			calls++
			return append(env, "HTTP_PROXY=http://127.0.0.1:18890"), nil
		}}
		spec, err := p.resolveLaunch(platform.ResolveRequest{AppType: cli, Env: []string{"AUTH=keep"}})
		if err != nil || calls != 1 || !strings.Contains(strings.Join(spec.Env.Variables, "\n"), "HTTP_PROXY=http://127.0.0.1:18890") {
			t.Fatalf("%s missing proxy policy: %v", cli, err)
		}
		p.networkEnv = func([]string) ([]string, error) { return nil, errors.New("core unavailable") }
		if _, err = p.resolveLaunch(platform.ResolveRequest{AppType: cli}); err == nil {
			t.Fatal("planner silently bypassed failed proxy core")
		}
	}
}
func TestAIProxyFacadeHandlesMissingService(t *testing.T) {
	a := &App{}
	if _, err := a.GetAIProxyState(); err == nil {
		t.Fatal("uninitialized service accepted")
	}
	if _, err := a.StartAIProxy(); err == nil {
		t.Fatal("uninitialized service started")
	}
}
