package launcher

import (
	"errors"
	"testing"
)

func TestProcFSIdentityIncludesBootGenerationAndMismatchNeverSignals(t *testing.T) {
	const stat = "731 (worker with spaces) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 424242 20 21\n"
	identityA, err := procFSProcessIdentity("11111111-1111-4111-8111-111111111111", []byte(stat))
	if err != nil {
		t.Fatalf("identity A: %v", err)
	}
	identityB, err := procFSProcessIdentity("22222222-2222-4222-8222-222222222222", []byte(stat))
	if err != nil {
		t.Fatalf("identity B: %v", err)
	}
	if identityA == identityB {
		t.Fatalf("different boot generations produced same identity %q", identityA)
	}
	terminal, signal, err := classifyRecoveredExternalProcess(identityA, identityB, true, nil)
	if err != nil || !terminal || signal {
		t.Fatalf("cross-boot PID/tick reuse classified terminal=%v signal=%v err=%v", terminal, signal, err)
	}
}

func TestLegacyProcFSIdentityRemainsUnknownAndNeverSignals(t *testing.T) {
	const stat = "731 (legacy worker) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 424242 20 21\n"
	observed, err := procFSProcessIdentity("11111111-1111-4111-8111-111111111111", []byte(stat))
	if err != nil {
		t.Fatalf("current identity: %v", err)
	}
	terminal, signal, classifyErr := classifyRecoveredExternalProcess("procfs:424242", observed, true, nil)
	if terminal || signal || classifyErr == nil {
		t.Fatalf("legacy live identity terminal=%v signal=%v err=%v want retained/no-signal/migration-error", terminal, signal, classifyErr)
	}

	terminal, signal, classifyErr = classifyRecoveredExternalProcess("procfs:424242", "", false, nil)
	if !terminal || signal || classifyErr != nil {
		t.Fatalf("legacy absent identity terminal=%v signal=%v err=%v want proven terminal", terminal, signal, classifyErr)
	}
}

func TestProcFSBootIdentityUncertaintyFailsClosedWithoutSignal(t *testing.T) {
	const stat = "8 (worker) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 99 20\n"
	if _, err := procFSProcessIdentity("", []byte(stat)); err == nil {
		t.Fatal("missing boot identity unexpectedly accepted")
	}
	inspectErr := errors.New("boot identity unavailable")
	terminal, signal, err := classifyRecoveredExternalProcess("expected", "", true, inspectErr)
	if !errors.Is(err, inspectErr) || terminal || signal {
		t.Fatalf("uncertain boot identity terminal=%v signal=%v err=%v", terminal, signal, err)
	}
}
