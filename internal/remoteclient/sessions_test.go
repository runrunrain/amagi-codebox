// sessions_test.go — 会话域 REST 测试：list/get/create(cliType×5)/stop/
// restart/delete 全往返 + confirm 协议校验 + 客户端侧 cliType 白名单 +
// 未配对（auth.unpaired）与撤销（auth.revoked）路径。
package remoteclient

import (
	"context"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// newPairedClient 起假宿主、装载有效凭据，返回会话客户端。
func newPairedClient(t *testing.T) (*SessionClient, *fakeHost) {
	t.Helper()
	f := newFakeHost(t)
	tr, err := NewTransport(f.baseURL())
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	if err := tr.SetCredential(deviceWireID(7), deviceWireSecret(11)); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	return NewSessionClient(tr), f
}

// TestSessionCRUDRoundTrip：create→get→list→stop→restart→delete→not_found。
func TestSessionCRUDRoundTrip(t *testing.T) {
	sc, f := newPairedClient(t)
	ctx := context.Background()

	created, cerr := sc.CreateSession(ctx, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
	if cerr != nil {
		t.Fatalf("CreateSession: %v", cerr)
	}
	if created.ID == "" || created.CLIType != contract.CLITypeClaudeCode || created.State != contract.SessionStateRunning {
		t.Fatalf("created detail = %+v", created)
	}
	if created.Workdir == "" || created.StartedAt == "" || created.Control.State == "" {
		t.Errorf("created detail missing fields: %+v", created)
	}

	// get。
	detail, cerr := sc.GetSession(ctx, created.ID)
	if cerr != nil {
		t.Fatalf("GetSession: %v", cerr)
	}
	if detail.ID != created.ID || detail.Title != created.Title {
		t.Fatalf("get mismatch: %+v", detail)
	}

	// list 含新建会话。
	list, cerr := sc.ListSessions(ctx)
	if cerr != nil {
		t.Fatalf("ListSessions: %v", cerr)
	}
	found := false
	for _, s := range list {
		if s.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created session not in list: %+v", list)
	}

	// stop → stopped。
	stopped, cerr := sc.StopSession(ctx, created.ID)
	if cerr != nil {
		t.Fatalf("StopSession: %v", cerr)
	}
	if stopped.State != contract.SessionStateStopped {
		t.Fatalf("stop state = %q", stopped.State)
	}

	// restart → running。
	restarted, cerr := sc.RestartSession(ctx, created.ID)
	if cerr != nil {
		t.Fatalf("RestartSession: %v", cerr)
	}
	if restarted.State != contract.SessionStateRunning {
		t.Fatalf("restart state = %q", restarted.State)
	}

	// delete → 204；随后 get → session.not_found。
	if cerr := sc.DeleteSession(ctx, created.ID); cerr != nil {
		t.Fatalf("DeleteSession: %v", cerr)
	}
	_, cerr = sc.GetSession(ctx, created.ID)
	if cerr == nil || cerr.Code() != contract.ErrorCodeSessionNotFound {
		t.Fatalf("get after delete: %v, want session.not_found", cerr)
	}
	if cerr := sc.DeleteSession(ctx, created.ID); cerr == nil || cerr.Code() != contract.ErrorCodeSessionNotFound {
		t.Fatalf("double delete: %v, want session.not_found", cerr)
	}

	// stop/restart/delete 请求体都带 confirm:true（假宿端已解码校验；此处
	// 断言请求方法与路径走清单形态）。
	reqs := f.requests()
	var sawStop, sawRestart, sawDelete bool
	for _, r := range reqs {
		switch {
		case r.Method == "POST" && r.Path == contract.RESTBasePath+"/sessions/"+string(created.ID)+"/stop":
			sawStop = true
		case r.Method == "POST" && r.Path == contract.RESTBasePath+"/sessions/"+string(created.ID)+"/restart":
			sawRestart = true
		case r.Method == "DELETE" && r.Path == contract.RESTBasePath+"/sessions/"+string(created.ID):
			sawDelete = true
		}
	}
	if !sawStop || !sawRestart || !sawDelete {
		t.Errorf("endpoint forms wrong: stop=%v restart=%v delete=%v", sawStop, sawRestart, sawDelete)
	}
}

// TestSessionCreateAllFiveCLITypes：五类冻结 CLI 全部可建。
func TestSessionCreateAllFiveCLITypes(t *testing.T) {
	sc, _ := newPairedClient(t)
	ctx := context.Background()
	for _, cli := range contract.KnownCLITypes {
		d, cerr := sc.CreateSession(ctx, contract.CreateSessionRequest{CLIType: cli})
		if cerr != nil {
			t.Fatalf("create %s: %v", cli, cerr)
		}
		if d.CLIType != cli {
			t.Errorf("created cliType = %q, want %q", d.CLIType, cli)
		}
	}
	list, cerr := sc.ListSessions(ctx)
	if cerr != nil {
		t.Fatalf("ListSessions: %v", cerr)
	}
	if len(list) != len(contract.KnownCLITypes) {
		t.Fatalf("list = %d sessions, want %d", len(list), len(contract.KnownCLITypes))
	}
	// 空列表：全删后 list 为非 nil 空切片。
	for _, s := range list {
		if cerr := sc.DeleteSession(ctx, s.ID); cerr != nil {
			t.Fatalf("delete %s: %v", s.ID, cerr)
		}
	}
	empty, cerr := sc.ListSessions(ctx)
	if cerr != nil {
		t.Fatalf("ListSessions empty: %v", cerr)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty list = %#v, want non-nil empty", empty)
	}
}

// TestSessionCreateRejectsUnknownCLIType：客户端侧白名单——未知 cliType 以
// bad_request 本地失败，请求不出网（假宿主零请求）。
func TestSessionCreateRejectsUnknownCLIType(t *testing.T) {
	sc, f := newPairedClient(t)
	before := len(f.requests())
	_, cerr := sc.CreateSession(context.Background(), contract.CreateSessionRequest{CLIType: "internal-cli"})
	if cerr == nil || cerr.Code() != contract.ErrorCodeBadRequest {
		t.Fatalf("code = %v, want bad_request", cerr)
	}
	if len(f.requests()) != before {
		t.Error("unknown cliType request leaked to the wire")
	}
}

// TestSessionAuthFailures：未装载凭据 → auth.unpaired（本地失败，零请求）；
// 凭据被撤销 → auth.revoked（IsAuthRevoked 触发 fail-closed 判定）。
func TestSessionAuthFailures(t *testing.T) {
	f := newFakeHost(t)
	tr, err := NewTransport(f.baseURL())
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	sc := NewSessionClient(tr)
	before := len(f.requests())

	// 未配对。
	_, cerr := sc.ListSessions(context.Background())
	if cerr == nil || cerr.Code() != contract.ErrorCodeAuthUnpaired {
		t.Fatalf("unpaired code = %v, want auth.unpaired", cerr)
	}
	if len(f.requests()) != before {
		t.Error("unpaired request leaked to the wire")
	}

	// 已撤销。
	deviceID := deviceWireID(7)
	if err := tr.SetCredential(deviceID, deviceWireSecret(11)); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	f.revokeDevice(deviceID)
	_, cerr = sc.ListSessions(context.Background())
	if cerr == nil || !cerr.IsAuthRevoked() {
		t.Fatalf("revoked = %v, want auth.revoked (IsAuthRevoked)", cerr)
	}
}
