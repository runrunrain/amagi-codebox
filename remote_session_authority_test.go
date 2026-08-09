package main

import (
	"testing"

	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/remote/contract"
)

func TestRemoteHostSummaryUsesSameFailClosedPlannerAsCreate(t *testing.T) {
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
}
