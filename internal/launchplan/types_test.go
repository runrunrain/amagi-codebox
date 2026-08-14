package launchplan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/settings"
)

func TestPlanValidateCanonicalFiveCLIsAndEmbeddedProcess(t *testing.T) {
	if got := contract.KnownCLITypes; len(got) != 5 {
		t.Fatalf("known CLI count = %d", len(got))
	}
	for _, cli := range contract.KnownCLITypes {
		plan := &Plan{
			Recipe: StableRecipe{CLIType: cli, Workdir: "/work"},
			Effects: []EffectSpec{{
				Kind: EffectPTYStart,
				Process: &ProcessStartSpec{Mode: ModeEmbedded, Resolved: platform.ResolvedLaunchSpec{
					CLI: platform.ResolvedCLI{Path: "/bin/cli"},
				}},
			}},
		}
		if err := plan.Validate(); err != nil {
			t.Fatalf("%s plan: %v", cli, err)
		}
	}
}

func TestPlanRejectsProcessDuplicationAndOutOfOrderEffects(t *testing.T) {
	process := func() EffectSpec {
		return EffectSpec{Kind: EffectPTYStart, Process: &ProcessStartSpec{Mode: ModeEmbedded}}
	}
	plan := &Plan{Recipe: StableRecipe{CLIType: contract.CLITypePi, Workdir: "/work"}, Effects: []EffectSpec{process(), process()}}
	if err := plan.Validate(); err == nil {
		t.Fatal("duplicate process effect accepted")
	}
	plan.Effects = []EffectSpec{process(), {Kind: EffectHeadroomStart, Shared: &SharedStartSpec{Service: SharedClaudeHeadroom}}}
	if err := plan.Validate(); err != ErrInvalidEffectOrder {
		t.Fatalf("out-of-order error = %v", err)
	}
}

func TestFailClosedPlannerNeverProducesExecutablePlan(t *testing.T) {
	planner := NewFailClosedPlanner()
	for _, cli := range contract.KnownCLITypes {
		availability, failure := planner.Probe(context.Background(), cli)
		if availability.Available || failure == nil || failure.Kind != FailureLaunchContext {
			t.Fatalf("%s probe did not fail closed", cli)
		}
		plan, failure := planner.BuildPlan(context.Background(), BuildRequest{CLIType: cli, Origin: OriginRemote, Mode: ModeEmbedded, Workdir: "/work"})
		if plan != nil || failure == nil {
			t.Fatalf("%s build produced a plan", cli)
		}
	}
}

func TestDefaultStorePersistsOnlyStableRefsInSettingsAfterExplicitRecord(t *testing.T) {
	dir := t.TempDir()
	settingsService := settings.NewService(dir)
	if err := settingsService.Load(); err != nil {
		t.Fatalf("settings Load: %v", err)
	}
	store, err := NewDefaultStore(settingsService)
	if err != nil {
		t.Fatalf("NewDefaultStore: %v", err)
	}
	if _, ok := store.HostDefaultRefs(contract.CLITypeClaudeCode); ok {
		t.Fatal("default exists before desktop activation record")
	}
	recipe := StableRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work", ProviderRef: "provider-a", PresetRef: "preset-a", ModelRef: "model-a", UseHeadroom: true}
	if err := store.RecordDesktopActivation(recipe); err != nil {
		t.Fatalf("RecordDesktopActivation: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("settings JSON: %v", err)
	}
	defaultsJSON, err := json.Marshal(decoded["remoteLaunchDefaultsV1"])
	if err != nil {
		t.Fatalf("defaults JSON: %v", err)
	}
	var defaults map[string]map[string]any
	if err := json.Unmarshal(defaultsJSON, &defaults); err != nil {
		t.Fatalf("decode defaults: %v", err)
	}
	forbiddenKeys := map[string]struct{}{
		"apikey": {}, "secret": {}, "env": {}, "pid": {}, "output": {},
		"prompt": {}, "workdir": {}, "mode": {}, "resolved": {},
	}
	for cliType, fields := range defaults {
		for key := range fields {
			if _, forbidden := forbiddenKeys[strings.ToLower(key)]; forbidden {
				t.Fatalf("defaults[%s] contain forbidden field %q: %s", cliType, key, defaultsJSON)
			}
		}
	}
	reloadedSettings := settings.NewService(dir)
	if err := reloadedSettings.Load(); err != nil {
		t.Fatalf("settings reload: %v", err)
	}
	reloaded, err := NewDefaultStore(reloadedSettings)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := reloaded.HostDefaultRefs(contract.CLITypeClaudeCode)
	if !ok || got.ProviderRef != recipe.ProviderRef || got.Workdir != "" {
		t.Fatalf("reloaded refs = %#v, ok=%v", got, ok)
	}
}
