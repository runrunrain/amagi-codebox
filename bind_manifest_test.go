package main

// bind_manifest_test.go — T-24 production Wails Bind surface test (design §4.1,
// §6.3, §10.3 C-01). Asserts that raw PTY/Headroom services are NOT bound,
// and that the App exposes neither the cleanup bypass (StopAllSessions) nor the
// raw callback/history exports (R3-Minor-01).

import (
	"reflect"
	"strings"
	"testing"
)

// forbiddenTypePrefixes are package-qualified type suffixes that must NEVER be
// in the production Bind list — the raw service objects themselves (design §6.3).
var forbiddenTypePrefixes = []string{
	"pty.Service",
	"headroom.HeadroomService",
}

// appForbiddenMethods are App-level methods that must NOT be exported (raw
// cleanup bypass + raw callback/history surface — design §6.3, §10.3, §8.6.3).
// These are App-specific names that do not collide with legitimate services.
var appForbiddenMethods = []string{
	"StopAllSessions",  // §10.3 cleanup bypass
	"GetOutputHistory", // R3-Minor-01 raw history (no run token)
	"RegisterOutputCallback",
	"UnregisterOutputCallback",
	"RegisterExitCallback",
	"UnregisterExitCallback",
	"RegisterResizeCallback",
	"UnregisterResizeCallback",
}

// appGatedMutationMethods are the App exported methods that perform session/PTY
// mutation and MUST be the gated facade (routed through the ControlGate), not a
// raw passthrough (M-005: every mutation goes through the Gate). The test asserts
// these exist (the gated facade is reachable) AND that no raw bypass method leaks.
var appGatedMutationMethods = []string{
	"PtyWrite",
	"PtyWriteLarge",
	"PtyResize",
	"StopSession",
	"RemoveSession",
	"ClearStoppedSessions",
}

// appRawMutationNameFragments are name fragments that must NOT appear in any
// exported App method (they would indicate a raw service mutation leaked onto
// the bound App surface). Legitimate facades use the Pty*/Proxy*/Headroom*
// prefixes instead.
var appRawMutationNameFragments = []string{
	"WriteRaw",
	"ResizeRaw",
	"CloseSession",
	"CloseAll",
}

func TestBindManifest_NoRawServicesBound(t *testing.T) {
	app := newTestApp(t)
	bindList := buildWailsBindList(app)
	for _, obj := range bindList {
		typeName := reflect.TypeOf(obj).String()
		for _, prefix := range forbiddenTypePrefixes {
			if strings.HasSuffix(typeName, prefix) {
				t.Errorf("raw service %s is in the production Bind list (C-01 violation)", typeName)
			}
		}
	}
}

func TestBindManifest_AppForbiddenMethodsAbsent(t *testing.T) {
	app := newTestApp(t)
	appType := reflect.TypeOf(app)
	for _, name := range appForbiddenMethods {
		if _, ok := appType.MethodByName(name); ok {
			t.Errorf("App exposes forbidden method %s (must be removed/unexported)", name)
		}
	}
}

func TestBindManifest_GatedFacadesReachable(t *testing.T) {
	app := newTestApp(t)
	appType := reflect.TypeOf(app)
	for _, name := range []string{"PtyWrite", "PtyResize", "PtyWriteLarge", "GetOutputHistorySnapshot"} {
		if _, ok := appType.MethodByName(name); !ok {
			t.Errorf("App gated facade %s is missing", name)
		}
	}
}

// TestBindManifest_AppMutationSurfaceGated (M-005): the exported App methods
// that mutate session/PTY state are exactly the known gated facades, and no raw
// service mutation name fragment leaks onto the bound App surface. This turns
// the bind check from "raw service objects absent" into an exported-mutation
// manifest assertion.
// appClassifiedNonSessionMutations (R12 Minor): non-session/PTY mutations exported on
// the bound App surface that were introduced by M2 recovery/headroom work. Each entry
// is explicitly classified with its gating semantics so future manifest drift is
// reviewable: these are NOT raw session mutations (they do not touch PTY/session
// state), they require explicit user confirmation or backend authority fencing.
var appClassifiedNonSessionMutations = map[string]string{
	// Requires explicit PG-06-style user confirmation in the desktop UI; backend
	// refuses while the process is live and writes a typed audit on completion.
	"ConfirmExternalCleanupRecovery": "user-confirm + backend live-refuse + typed audit",
	// Mutates global codex headroom; gated by coordinator admission (R4-002) and the
	// corrupt-store recovery fence (R9-002).
	"SetCodexGlobalHeadroom": "coordinator admission + recovery fence",
}

func TestBindManifest_ClassifiedNonSessionMutations(t *testing.T) {
	app := newTestApp(t)
	appType := reflect.TypeOf(app)
	for name, classification := range appClassifiedNonSessionMutations {
		if _, ok := appType.MethodByName(name); !ok {
			t.Errorf("classified non-session mutation %s (%s) missing from App", name, classification)
		}
	}
}

func TestBindManifest_AppMutationSurfaceGated(t *testing.T) {
	app := newTestApp(t)
	appType := reflect.TypeOf(app)

	// Every gated mutation method must be present (the facade is reachable).
	for _, name := range appGatedMutationMethods {
		if _, ok := appType.MethodByName(name); !ok {
			t.Errorf("gated mutation facade %s is missing from App", name)
		}
	}

	// No exported App method may carry a raw-mutation name fragment.
	for i := 0; i < appType.NumMethod(); i++ {
		m := appType.Method(i)
		for _, frag := range appRawMutationNameFragments {
			if strings.Contains(m.Name, frag) {
				t.Errorf("App exports raw-mutation method %s (must stay behind the gate)", m.Name)
			}
		}
	}
}

func TestBindManifest_AppIsBound(t *testing.T) {
	app := newTestApp(t)
	bindList := buildWailsBindList(app)
	var found bool
	for _, obj := range bindList {
		if _, ok := obj.(*App); ok {
			found = true
		}
	}
	if !found {
		t.Error("App object is not in the production Bind list")
	}
}

func TestBindManifest_HeadroomFacadesReachable(t *testing.T) {
	app := newTestApp(t)
	appType := reflect.TypeOf(app)
	// Lease-guarded facade methods that replace the raw Headroom binding.
	for _, name := range []string{"HeadroomStart", "HeadroomStop", "HeadroomGetStatus"} {
		if _, ok := appType.MethodByName(name); !ok {
			t.Errorf("App facade %s is missing (replaces raw service binding)", name)
		}
	}
}

// TestBindManifest_SecretsDeadBindMethodsRemoved freezes the removal of the
// provider-specific key accessor shim methods that had no Go or frontend/src
// callers (verified by grep across .go and frontend/src — only stale
// frontend/wailsjs generated stubs remained, which wails build regenerates).
// They reached the frontend directly via app.Secrets (bind_list.go) and so were
// dead surface on the bound SecretsService. GetAllProviders is intentionally
// retained: it has a real Go caller in cmd/codebox (secretProviderNames).
func TestBindManifest_SecretsDeadBindMethodsRemoved(t *testing.T) {
	app := newTestApp(t)
	if app.Secrets == nil {
		t.Fatal("app.Secrets is nil; secrets service not wired")
	}
	rt := reflect.TypeOf(app.Secrets)
	for _, name := range []string{"GetZhipuAPIKey", "SetZhipuAPIKey", "GetMinimaxAPIKey", "SetMinimaxAPIKey"} {
		if _, ok := rt.MethodByName(name); ok {
			t.Errorf("removed dead bind method %s reappeared on SecretsService", name)
		}
	}
	// Sanity: GetAllProviders must remain (cmd/codebox depends on it).
	if _, ok := rt.MethodByName("GetAllProviders"); !ok {
		t.Errorf("GetAllProviders must remain on SecretsService (cmd/codebox caller)")
	}
}
