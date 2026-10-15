package envcheck

import (
	"strings"
	"testing"
)

// withWSLNetworkingMode installs the networking-mode probe result for one test
// (mirrors withWSLHybridGates for the OS/distro gates).
func withWSLNetworkingMode(t *testing.T, mode string) {
	t.Helper()
	prev := wslNetworkingMode
	wslNetworkingMode = func() string { return mode }
	t.Cleanup(func() { wslNetworkingMode = prev })
}

func TestAppendWSLNetworkingModeInfo_ThreeModes(t *testing.T) {
	cases := []struct {
		mode           string
		wantMessage    string
		wantDetailSubs []string
	}{
		{
			mode:           "mirrored",
			wantMessage:    "WSL 网络模式：mirrored",
			wantDetailSubs: []string{"AI 代理分流与 Headroom 对 WSL 会话可用"},
		},
		{
			mode:           "nat",
			wantMessage:    "WSL 网络模式：NAT",
			wantDetailSubs: []string{"不可用", "networkingMode=mirrored", "wsl --shutdown"},
		},
		{
			mode:           "",
			wantMessage:    "WSL 网络模式：未知（按 NAT 处理）",
			wantDetailSubs: []string{"未检测到", "networkingMode=mirrored", "wsl --shutdown"},
		},
	}
	for _, tc := range cases {
		withWSLHybridGates(t, "windows", "Ubuntu-Test")
		withWSLNetworkingMode(t, tc.mode)
		svc := NewServiceWithRunner(&wslHybridProbeRunner{})
		status := &CheckStatus{Tool: ToolPi}
		svc.appendWSLNetworkingModeInfo(status)

		issue := findIssue(status, "wsl_networking_mode")
		if issue == nil {
			t.Fatalf("mode %q: expected wsl_networking_mode issue, got %#v", tc.mode, status.Issues)
		}
		if issue.Severity != SeverityInfo {
			t.Errorf("mode %q: severity = %q, want info (must not flip AllOK)", tc.mode, issue.Severity)
		}
		if issue.Message != tc.wantMessage {
			t.Errorf("mode %q: message = %q, want %q", tc.mode, issue.Message, tc.wantMessage)
		}
		for _, sub := range tc.wantDetailSubs {
			if !strings.Contains(issue.Detail, sub) {
				t.Errorf("mode %q: detail missing %q: %s", tc.mode, sub, issue.Detail)
			}
		}
	}
}

func TestAppendWSLNetworkingModeInfo_Gates(t *testing.T) {
	// Tools that never launch inside WSL (Headroom) get no card.
	withWSLHybridGates(t, "windows", "Ubuntu-Test")
	withWSLNetworkingMode(t, "mirrored")
	svc := NewServiceWithRunner(&wslHybridProbeRunner{})
	status := &CheckStatus{Tool: ToolHeadroom}
	svc.appendWSLNetworkingModeInfo(status)
	if findIssue(status, "wsl_networking_mode") != nil {
		t.Errorf("headroom must be skipped: %#v", status.Issues)
	}

	// Non-Windows host: WSL is not a launch path.
	withWSLHybridGates(t, "darwin", "Ubuntu-Test")
	status2 := &CheckStatus{Tool: ToolPi}
	svc.appendWSLNetworkingModeInfo(status2)
	if findIssue(status2, "wsl_networking_mode") != nil {
		t.Errorf("non-Windows host must be skipped: %#v", status2.Issues)
	}

	// No usable distro: no WSL session can land anywhere.
	withWSLHybridGates(t, "windows", "")
	status3 := &CheckStatus{Tool: ToolPi}
	svc.appendWSLNetworkingModeInfo(status3)
	if findIssue(status3, "wsl_networking_mode") != nil {
		t.Errorf("no-distro host must be skipped: %#v", status3.Issues)
	}
}

// TestFinishToolCheckAppendsWSLNetworkingModeInfo pins the wiring: the info
// card rides the single finishToolCheck choke point every CheckOne path passes
// through, and being severity-only it must not touch the AllOK inputs
// (Installed / PATHOk / Error).
func TestFinishToolCheckAppendsWSLNetworkingModeInfo(t *testing.T) {
	withWSLHybridGates(t, "windows", "Ubuntu-Test")
	withWSLNetworkingMode(t, "nat")
	svc := NewServiceWithRunner(&wslHybridProbeRunner{stdout: "pi\t/home/u/.npm-global/bin/pi\n"})

	status, err := svc.finishToolCheck(&CheckStatus{Tool: ToolPi, Installed: true, PATHOk: true}, nil)
	if err != nil {
		t.Fatalf("finishToolCheck: %v", err)
	}
	if findIssue(status, "wsl_networking_mode") == nil {
		t.Errorf("finishToolCheck must append the networking-mode card: %#v", status.Issues)
	}
	if !status.Installed || !status.PATHOk || status.Error != "" {
		t.Errorf("info card must not touch AllOK inputs: installed=%v pathOk=%v error=%q", status.Installed, status.PATHOk, status.Error)
	}
}
