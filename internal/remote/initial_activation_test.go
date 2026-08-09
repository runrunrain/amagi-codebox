package remote

import (
	"errors"
	"testing"
	"time"

	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
)

func TestCompositeInitialActivationStagesAndCommitsH1H3WithAuthority(t *testing.T) {
	runtime := NewControlRuntime(newCtrlFakeClock(time.Unix(100, 0)), nil)
	runtime.MarkReady()
	manager := session.NewManager()
	reservation, err := manager.ReserveCreate(session.CreateSpec{
		RequestedID: "composite-activation", AppType: session.AppTypeClaudeCode,
		Origin: launchplan.OriginDesktop, Mode: launchplan.ModeEmbedded, Workdir: "/work", RemoteEligible: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, runPermit, observation, err := runtime.BeginDesktopRun(t.Context(), "composite-activation")
	if err != nil {
		t.Fatal(err)
	}
	runtime.Projector().OfferOutput(observation, 7, []byte("startup"))
	if got := manager.ListRemoteSafeSnapshots(); len(got) != 0 {
		t.Fatalf("pending authority leaked: %#v", got)
	}
	if watermark := runtime.Hub().WatermarkFor("composite-activation"); watermark.Event != 0 {
		t.Fatalf("prepared stage advanced H3 watermark early: %#v", watermark)
	}
	binding := processcap.BindingID{Kind: processcap.BackendPTY, Owner: 1, Generation: 1}
	authorityToken, err := manager.PrepareActivation(reservation, session.PreparedAuthorityActivation{
		BindingID: binding, PID: 42, RunRevision: observation.RunEpoch(),
		StartedAt: time.Unix(100, 0), LastActivityAt: time.Unix(100, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := runtime.PrepareCompositeActivation("composite-activation", runPermit, observation)
	if err != nil {
		t.Fatal(err)
	}
	if watermark := runtime.Hub().WatermarkFor("composite-activation"); watermark.Event != 0 {
		t.Fatalf("causalPrepared became attach-visible: %#v", watermark)
	}
	if _, err := manager.CommitPreparedActivation(authorityToken, prepared.CommitNoFail); err != nil {
		t.Fatal(err)
	}
	if watermark := runtime.Hub().WatermarkFor("composite-activation"); watermark.Event != 2 || watermark.Run != (RunCausalPosition{SegmentID: 1, Source: 2}) {
		t.Fatalf("committed watermark = %#v", watermark)
	}
	runtime.FinishCompositeActivation(prepared)
	if got := manager.ListRemoteSafeSnapshots(); len(got) != 1 || got[0].Handle.SessionID() != "composite-activation" {
		t.Fatalf("authority snapshots = %#v", got)
	}
	snapshot, _, err := runtime.Feed().SnapshotAndSubscribe("composite-activation")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 2 || snapshot.Records[0].Kind != LiveRecordRunActivated || snapshot.Records[1].Kind != LiveRecordOutput || string(snapshot.Records[1].Output) != "startup" {
		t.Fatalf("initial H1 batch = %#v", snapshot.Records)
	}
	token, version := runtime.Projector().GetRunSnapshot("composite-activation")
	if token == "" || version != "1" {
		t.Fatalf("projection token=%q version=%q", token, version)
	}
	control, gateErr := runtime.Gate().SnapshotForDevice("composite-activation", "viewer")
	if gateErr != nil || control.State != contract.ControlStateNone {
		t.Fatalf("control=%#v err=%v", control, gateErr)
	}
}

func TestCompositeInitialActivationRejectsShutdownBeforeSeal(t *testing.T) {
	runtime := NewControlRuntime(newCtrlFakeClock(time.Unix(100, 0)), nil)
	runtime.MarkReady()
	_, runPermit, observation, err := runtime.BeginDesktopRun(t.Context(), "activation-preseal-shutdown")
	if err != nil {
		t.Fatal(err)
	}
	runtime.CloseForShutdown()
	if _, err := runtime.PrepareCompositeActivation("activation-preseal-shutdown", runPermit, observation); err == nil {
		t.Fatal("shutdown-fenced launch prepared an activation")
	}
}

func TestCompositeInitialActivationShutdownFenceWaitsAndConvergesUnavailable(t *testing.T) {
	runtime := NewControlRuntime(newCtrlFakeClock(time.Unix(100, 0)), nil)
	runtime.MarkReady()
	manager := session.NewManager()
	runtime.Projector().SetSessionAuthority(manager)
	reservation, err := manager.ReserveCreate(session.CreateSpec{
		RequestedID: "activation-shutdown", AppType: session.AppTypeClaudeCode,
		Origin: launchplan.OriginDesktop, Mode: launchplan.ModeEmbedded, Workdir: "/work", RemoteEligible: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, runPermit, observation, err := runtime.BeginDesktopRun(t.Context(), "activation-shutdown")
	if err != nil {
		t.Fatal(err)
	}
	authorityToken, err := manager.PrepareActivation(reservation, session.PreparedAuthorityActivation{
		BindingID: processcap.BindingID{Kind: processcap.BackendPTY, Owner: 2, Generation: 1},
		PID:       43, RunRevision: observation.RunEpoch(), StartedAt: time.Unix(100, 0), LastActivityAt: time.Unix(100, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := runtime.PrepareCompositeActivation("activation-shutdown", runPermit, observation)
	if err != nil {
		t.Fatal(err)
	}
	shutdownDone := make(chan struct{})
	go func() {
		runtime.CloseForShutdown()
		close(shutdownDone)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		prepared.entry.stateMu.Lock()
		fenced := prepared.postCommitFence
		prepared.entry.stateMu.Unlock()
		if fenced {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not claim prepared activation")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-shutdownDone:
		t.Fatal("shutdown returned before prepared activation resolved")
	default:
	}
	if _, err := manager.CommitPreparedActivation(authorityToken, prepared.CommitNoFail); err != nil {
		t.Fatal(err)
	}
	runtime.FinishCompositeActivation(prepared)
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after activation resolution")
	}
	snapshot, err := manager.RemoteSnapshotByID("activation-shutdown")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != session.AuthorityUnavailable {
		t.Fatalf("post-shutdown authority state = %d", snapshot.State)
	}
	if token, version := runtime.Projector().GetRunSnapshot("activation-shutdown"); token != "" || version != "" {
		t.Fatalf("post-shutdown projection leaked: %q/%q", token, version)
	}
}

func TestCompositeInitialActivationRejectsPreActivationExit(t *testing.T) {
	runtime := NewControlRuntime(newCtrlFakeClock(time.Unix(100, 0)), nil)
	runtime.MarkReady()
	_, runPermit, observation, err := runtime.BeginDesktopRun(t.Context(), "terminal-before-activation")
	if err != nil {
		t.Fatal(err)
	}
	runtime.Projector().OfferExit(observation, 1, true)
	if _, err := runtime.PrepareCompositeActivation("terminal-before-activation", runPermit, observation); !errors.Is(err, errInitialActivationTerminal) {
		t.Fatalf("prepare error = %v", err)
	}
	if token, version := runtime.Projector().GetRunSnapshot("terminal-before-activation"); token != "" || version != "" {
		t.Fatalf("failed activation projection leaked: %q/%q", token, version)
	}
}
