package main

import (
	"testing"
	"time"

	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
)

// The hidden reservation may use a host fallback before the WSL resolver runs.
// Publishing that fallback would make GitPanel/restart disagree with the PTY.
func TestWSLResolvedWorkdirPublishedToBothSessionViews(t *testing.T) {
	manager := session.NewManager()
	reservation, err := manager.ReserveCreate(session.CreateSpec{
		AppType: session.AppTypePi, Origin: launchplan.OriginDesktop,
		Mode: launchplan.ModeEmbedded, Workdir: `C:\Users\test`, RemoteEligible: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "/home/test/中文 工程"
	spec := platform.ResolvedLaunchSpec{BootstrapMode: platform.BootstrapWSL, WorkDir: want}
	published := resolvedDesktopSession(reservation.Session(), spec)
	if reservation.Session().WorkDir == want {
		t.Fatal("normalizing activation must not mutate the hidden reservation")
	}
	token, err := manager.PrepareActivation(reservation, session.PreparedAuthorityActivation{
		Session:   published,
		Recipe:    launchplan.StableRecipe{CLIType: contract.CLITypePi, Workdir: want},
		BindingID: processcap.BindingID{Kind: processcap.BackendPTY, Owner: 10, Generation: 1},
		PID:       101, RunRevision: 1, StartedAt: time.Unix(100, 0), LastActivityAt: time.Unix(100, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CommitPreparedActivation(token, func() {}); err != nil {
		t.Fatal(err)
	}
	legacy := manager.List()
	remote := manager.ListRemoteSafeSnapshots()
	if len(legacy) != 1 || legacy[0].WorkDir != want {
		t.Fatalf("desktop projection does not match PTY: %+v", legacy)
	}
	if len(remote) != 1 || remote[0].Workdir != want {
		t.Fatalf("remote projection does not match PTY: %+v", remote)
	}
}
