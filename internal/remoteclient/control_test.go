// control_test.go — 控制域 REST 测试（RC3）：acquire 200/403/409、release
// holder 语义、空 body 协议校验、请求头注入（Cookie/Origin/X-Request-ID）、
// 畸形成功体如实映射 service.down、ControlView 投影与 JSON 同形。
package remoteclient

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// newControlClient 起假宿主、装载有效凭据，返回控制域客户端 + 假宿主 +
// 本设备 wire 形态 ID。
func newControlClient(t *testing.T) (*ControlClient, *fakeHost, string) {
	t.Helper()
	f := newFakeHost(t)
	tr, err := NewTransport(f.baseURL())
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	devID := deviceWireID(7)
	if err := tr.SetCredential(devID, deviceWireSecret(11)); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	return NewControlClient(tr), f, devID
}

// newControlSession 造一个远端会话并返回其 ID。
func newControlSession(t *testing.T, c *ControlClient) contract.SessionID {
	t.Helper()
	sc := NewSessionClient(c.t)
	d, cerr := sc.CreateSession(context.Background(), contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
	if cerr != nil {
		t.Fatalf("CreateSession: %v", cerr)
	}
	return d.ID
}

// TestControlAcquireSuccess：attach（lease）后 acquire → 200 {state:you}；
// 请求空 body、携带 device Cookie/Origin/X-Request-ID。
func TestControlAcquireSuccess(t *testing.T) {
	c, f, devID := newControlClient(t)
	ctx := context.Background()
	sid := newControlSession(t, c)
	f.attachDevice(string(sid), devID)

	snap, cerr := c.AcquireControl(ctx, sid)
	if cerr != nil {
		t.Fatalf("AcquireControl: %v", cerr)
	}
	if snap.State != contract.ControlStateYou {
		t.Fatalf("acquire snapshot = %+v, want state=you", snap)
	}
	if snap.DeviceName != nil {
		t.Errorf("state=you 携带 deviceName = %v，必须省略", *snap.DeviceName)
	}

	// 投影。
	v := NewControlView(snap)
	if !v.You() || v.HeldByOther() || v.OtherDeviceName() != "" {
		t.Errorf("ControlView projection = %+v, want You", v)
	}
	// JSON 与契约快照同形（顶层 state，无 snapshot 包装）。
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal ControlView: %v", err)
	}
	if want := `{"state":"you"}`; string(raw) != want {
		t.Errorf("ControlView JSON = %s, want %s", raw, want)
	}

	// 请求形状：POST 空 body + Cookie/Origin/X-Request-ID。
	reqs := f.requests()
	var acq *capturedRequest
	for i := range reqs {
		if reqs[i].Path == contract.RESTBasePath+"/sessions/"+string(sid)+"/control/acquire" {
			acq = &reqs[i]
		}
	}
	if acq == nil {
		t.Fatalf("acquire request not captured: %+v", reqs)
	}
	if acq.Method != "POST" || acq.HasBody {
		t.Errorf("acquire request = %s emptyBody=%v, want POST empty body", acq.Method, acq.HasBody)
	}
	if !strings.Contains(acq.Cookie, devID) {
		t.Errorf("acquire Cookie missing device credential: %q", acq.Cookie)
	}
	if acq.Origin != f.baseURL() || acq.RequestID == "" {
		t.Errorf("acquire Origin=%q RequestID=%q, want same-origin + request id", acq.Origin, acq.RequestID)
	}

	// 同 device 幂等：再次 acquire 仍 200 you。
	snap2, cerr := c.AcquireControl(ctx, sid)
	if cerr != nil || snap2.State != contract.ControlStateYou {
		t.Fatalf("idempotent re-acquire = %+v err=%v", snap2, cerr)
	}
}

// TestControlAcquireBusy：他人持有 → 409 control.busy（服务端契约体透传，
// 非自产）。
func TestControlAcquireBusy(t *testing.T) {
	c, f, devID := newControlClient(t)
	sid := newControlSession(t, c)
	f.attachDevice(string(sid), devID)
	f.holdControl(string(sid), deviceWireID(9)) // 其它设备持有

	_, cerr := c.AcquireControl(context.Background(), sid)
	if cerr == nil {
		t.Fatal("acquire against held control must fail")
	}
	if cerr.StatusCode != 409 || cerr.Code() != contract.ErrorCodeControlBusy {
		t.Fatalf("acquire busy error = http %d code %s, want 409 control.busy", cerr.StatusCode, cerr.Code())
	}
	if cerr.API == nil || cerr.API.Layer != contract.ErrorLayerControl || cerr.API.ActionHint != contract.ActionHintRequestControl {
		t.Errorf("busy error body = %+v, want control layer + request_control hint", cerr.API)
	}
	if !strings.Contains(cerr.Error(), "control.busy") {
		t.Errorf("error text %q must carry stable code", cerr.Error())
	}
}

// TestControlAcquireForbiddenWithoutLease：未 attach（无 live lease）→ 403
// control.forbidden。
func TestControlAcquireForbiddenWithoutLease(t *testing.T) {
	c, f, _ := newControlClient(t)
	sid := newControlSession(t, c)
	_ = f // 不 attach：无 lease

	_, cerr := c.AcquireControl(context.Background(), sid)
	if cerr == nil || cerr.StatusCode != 403 || cerr.Code() != contract.ErrorCodeControlForbidden {
		t.Fatalf("acquire without lease = %v (http %d), want 403 control.forbidden", cerr, cerr.StatusCode)
	}
}

// TestControlReleaseSemantics：holder 释放 → 200 {state:none}；非 holder
// 释放 → 403 control.forbidden；释放后他人仍 busy。
func TestControlReleaseSemantics(t *testing.T) {
	c, f, devID := newControlClient(t)
	ctx := context.Background()
	sid := newControlSession(t, c)
	f.attachDevice(string(sid), devID)

	// 未持有即释放 → 403。
	_, cerr := c.ReleaseControl(ctx, sid)
	if cerr == nil || cerr.Code() != contract.ErrorCodeControlForbidden {
		t.Fatalf("release without control = %v, want 403 control.forbidden", cerr)
	}

	// acquire → release → 200 none。
	if _, cerr := c.AcquireControl(ctx, sid); cerr != nil {
		t.Fatalf("acquire: %v", cerr)
	}
	snap, cerr := c.ReleaseControl(ctx, sid)
	if cerr != nil {
		t.Fatalf("release: %v", cerr)
	}
	if snap.State != contract.ControlStateNone {
		t.Fatalf("release snapshot = %+v, want state=none", snap)
	}

	// 释放后他人持有 → 恢复 busy。
	f.holdControl(string(sid), deviceWireID(9))
	if _, cerr := c.AcquireControl(ctx, sid); cerr == nil || cerr.Code() != contract.ErrorCodeControlBusy {
		t.Fatalf("acquire after release = %v, want control.busy", cerr)
	}
}

// TestControlReleaseEmptyBodyProtocol：release 携带非空 body → 服务端
// bad_request（空 body 协议校验镜像）。
func TestControlEmptyBodyProtocol(t *testing.T) {
	c, f, devID := newControlClient(t)
	sid := newControlSession(t, c)
	f.attachDevice(string(sid), devID)

	// 绕过域层直接以非空 body 打 acquire 路由。
	cerr := c.t.do(context.Background(), epControlAcquire, requestOption{sessionID: string(sid), body: map[string]any{"x": 1}, auth: true}, nil)
	if cerr == nil || cerr.Code() != contract.ErrorCodeBadRequest {
		t.Fatalf("acquire with body = %v, want bad_request", cerr)
	}
}

// TestControlAcquireMalformedSnapshot：200 但快照违反条件联合（state=other
// 缺 deviceName）→ 如实映射 service.down，不伪装成功。
func TestControlAcquireMalformedSnapshot(t *testing.T) {
	c, f, devID := newControlClient(t)
	sid := newControlSession(t, c)
	f.attachDevice(string(sid), devID)
	f.mu.Lock()
	f.ctlAcquireBody = `{"state":"other"}`
	f.mu.Unlock()

	_, cerr := c.AcquireControl(context.Background(), sid)
	if cerr == nil || cerr.Code() != contract.ErrorCodeServiceDown {
		t.Fatalf("malformed acquire snapshot = %v, want service.down", cerr)
	}
}

// TestControlViewProjection：四态投影辅助（you/other/none/desktop）。
func TestControlViewProjection(t *testing.T) {
	other := "iPad"
	cases := []struct {
		snap  contract.ControlSnapshot
		you   bool
		other bool
		name  string
	}{
		{contract.ControlSnapshot{State: contract.ControlStateYou}, true, false, ""},
		{contract.ControlSnapshot{State: contract.ControlStateNone}, false, false, ""},
		{contract.ControlSnapshot{State: contract.ControlStateDesktop}, false, false, ""},
		{contract.ControlSnapshot{State: contract.ControlStateOther, DeviceName: &other}, false, true, "iPad"},
	}
	for _, tc := range cases {
		v := NewControlView(tc.snap)
		if v.You() != tc.you || v.HeldByOther() != tc.other || v.OtherDeviceName() != tc.name {
			t.Errorf("ControlView(%+v) = you:%v other:%v name:%q", tc.snap, v.You(), v.HeldByOther(), v.OtherDeviceName())
		}
	}
}
