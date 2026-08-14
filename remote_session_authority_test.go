package main

import (
	"encoding/json"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/remote/contract"
)

func TestRemoteHostSummaryUsesConfiguredPlannerProbe(t *testing.T) {
	app := &App{remotePlanner: launchplan.NewFailClosedPlanner()}
	summary, err := app.buildRemoteHostSummary()
	if err != nil {
		t.Fatalf("buildRemoteHostSummary: %v", err)
	}
	if len(summary.CLIAvailability) != len(contract.KnownCLITypes) || len(summary.CLIAvailability) != 5 {
		t.Fatalf("availability count = %d", len(summary.CLIAvailability))
	}
	for i, cli := range contract.KnownCLITypes {
		item := summary.CLIAvailability[i]
		if item.CLIType != cli || item.Available {
			t.Fatalf("availability[%d] = %#v", i, item)
		}
	}
	if summary.LaunchSettings == nil || len(summary.LaunchSettings.CLIs) != len(contract.KnownCLITypes) {
		t.Fatalf("launch settings missing: %#v", summary.LaunchSettings)
	}
}

func TestRemoteLaunchSettingsExposeReferencesWithoutSecrets(t *testing.T) {
	app := newTestApp(t)
	if err := app.Config.SaveProvider("safe-provider", config.Provider{
		OpenAI: &config.OpenAIFormat{
			Enabled: true, APIKey: "embedded-secret", BaseURL: "https://private.example/v1",
		},
		DefaultModel: "safe-model",
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.Secrets.SetAPIKey("safe-provider", "stored-secret"); err != nil {
		t.Fatal(err)
	}
	settings := app.buildRemoteLaunchSettings()
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	for _, forbidden := range []string{"embedded-secret", "stored-secret", "private.example", "apiKey", "baseURL"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("remote launch settings leaked %q: %s", forbidden, serialized)
		}
	}
	if !strings.Contains(serialized, "safe-provider") || !strings.Contains(serialized, "safe-model") {
		t.Fatalf("safe references missing: %s", serialized)
	}
}
