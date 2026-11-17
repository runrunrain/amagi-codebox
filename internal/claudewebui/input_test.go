package claudewebui

// input_test.go — 车道分流单点与错误映射的契约测试（design §5.2 input/§5.6、
// 测试计划 input_test.go 行）：
//   - 无身份头 → 桌面车道 WriteInput/PasteChunk（含 \r/\x1b/128KiB/分块）；
//   - 有 X-Amagi-Plane-Actor-Device 头 → 设备平面车道 DeviceWrite/DevicePasteChunk；
//   - 持控失败 → 403 control.forbidden；控制服务未就绪 → 503 service.down；
//   - text UTF-8 ≤128KiB 否则 400。

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// lane 标记一次写调用落在哪条车道、用哪个变体。
type lane struct {
	name  string // "desktop" | "device"
	paste bool   // PasteChunk 变体
}

// fakeWrite 记录 InputPort 的一次调用。
type fakeWrite struct {
	lane    lane
	session string
	device  string
	data    string
}

// fakeInputPort 是 InputPort 的记录型假件（错误注入按车道区分）。
type fakeInputPort struct {
	mu         sync.Mutex
	writes     []fakeWrite
	desktopErr error
	deviceErr  error
}

func (f *fakeInputPort) record(w fakeWrite) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, w)
	if w.lane.name == "device" {
		return f.deviceErr
	}
	return f.desktopErr
}

func (f *fakeInputPort) WriteInput(_ context.Context, sessionID string, data []byte) error {
	return f.record(fakeWrite{lane: lane{name: "desktop"}, session: sessionID, data: string(data)})
}

func (f *fakeInputPort) PasteChunk(_ context.Context, sessionID string, data []byte) error {
	return f.record(fakeWrite{lane: lane{name: "desktop", paste: true}, session: sessionID, data: string(data)})
}

func (f *fakeInputPort) DeviceWrite(_ context.Context, sessionID, deviceID string, data []byte) error {
	return f.record(fakeWrite{lane: lane{name: "device"}, session: sessionID, device: deviceID, data: string(data)})
}

func (f *fakeInputPort) DevicePasteChunk(_ context.Context, sessionID, deviceID string, data []byte) error {
	return f.record(fakeWrite{lane: lane{name: "device", paste: true}, session: sessionID, device: deviceID, data: string(data)})
}

func (f *fakeInputPort) snapshot() []fakeWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append(f.writes[:0:0], f.writes...)
}

func (f *fakeInputPort) setErr(device, desktop error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deviceErr, f.desktopErr = device, desktop
}

// lastWrite 返回最后一条记录。
func lastWrite(t *testing.T, f *fakeInputPort) fakeWrite {
	t.Helper()
	ws := f.snapshot()
	if len(ws) == 0 {
		t.Fatalf("InputPort 未被调用")
	}
	return ws[len(ws)-1]
}

// postInput 发一条已鉴权 /api/input。
func postInput(t *testing.T, tp *testPlane, body string, extra map[string]string) *http.Response {
	t.Helper()
	return doReq(t, http.MethodPost, tp.baseURL+"/api/input", body, authed(extra))
}

func postInterrupt(t *testing.T, tp *testPlane, extra map[string]string) *http.Response {
	t.Helper()
	return doReq(t, http.MethodPost, tp.baseURL+"/api/interrupt", "{}", authed(extra))
}

// --- 桌面车道（无身份头）------------------------------------------------------

func TestInputDesktopLane(t *testing.T) {
	tp := newTestPlane(t, nil)

	resp := postInput(t, tp, `{"text":"hello","submit":true}`, nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s, want 200", resp.StatusCode, body)
	}
	w := lastWrite(t, tp.input)
	if w.lane.name != "desktop" || w.lane.paste {
		t.Fatalf("应走桌面车道单发: %+v", w.lane)
	}
	if w.session != tp.cfg.SessionID {
		t.Fatalf("sessionID = %q, want %q", w.session, tp.cfg.SessionID)
	}
	if w.data != "hello\r" {
		t.Fatalf("submit 应追加 \\r: %q", w.data)
	}

	resp = postInput(t, tp, `{"text":"no submit"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if w = lastWrite(t, tp.input); w.data != "no submit" {
		t.Fatalf("无 submit 不应追加: %q", w.data)
	}

	// 中断：桌面车道写 \x1b（Esc）。
	resp = postInterrupt(t, tp, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("interrupt status = %d", resp.StatusCode)
	}
	if w = lastWrite(t, tp.input); w.lane.name != "desktop" || w.data != "\x1b" {
		t.Fatalf("interrupt 桌面车道应为 \\x1b: %+v data=%q", w.lane, w.data)
	}
}

func TestInputPasteChunkThreshold(t *testing.T) {
	tp := newTestPlane(t, nil)

	// 1KB 边界：恰好单发，超过一分块（对齐控制门 1KB 分块惯例）。
	resp := postInput(t, tp, `{"text":"`+strings.Repeat("a", 1024)+`"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("1024B status = %d", resp.StatusCode)
	}
	if w := lastWrite(t, tp.input); w.lane.paste {
		t.Fatalf("≤1KB 应单发 WriteInput")
	}
	resp = postInput(t, tp, `{"text":"`+strings.Repeat("b", 1025)+`"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("1025B status = %d", resp.StatusCode)
	}
	w := lastWrite(t, tp.input)
	if w.lane.name != "desktop" || !w.lane.paste || len(w.data) != 1025 {
		t.Fatalf(">1KB 应走 PasteChunk: %+v len=%d", w.lane, len(w.data))
	}
}

// --- 设备平面车道（有身份头）---------------------------------------------------

func TestInputDeviceLane(t *testing.T) {
	tp := newTestPlane(t, nil)
	devHdr := map[string]string{"X-Amagi-Plane-Actor-Device": "dev-7"}

	resp := postInput(t, tp, `{"text":"from device","submit":true}`, devHdr)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	w := lastWrite(t, tp.input)
	if w.lane.name != "device" || w.lane.paste {
		t.Fatalf("应走设备车道单发: %+v", w.lane)
	}
	if w.device != "dev-7" || w.session != tp.cfg.SessionID {
		t.Fatalf("设备车道参数错位: device=%q session=%q", w.device, w.session)
	}
	if w.data != "from device\r" {
		t.Fatalf("设备车道 submit 应追加 \\r: %q", w.data)
	}

	resp = postInterrupt(t, tp, devHdr)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("interrupt status = %d", resp.StatusCode)
	}
	if w = lastWrite(t, tp.input); w.lane.name != "device" || w.data != "\x1b" {
		t.Fatalf("interrupt 设备车道应为 \\x1b: data=%q", w.data)
	}

	resp = postInput(t, tp, `{"text":"`+strings.Repeat("c", 2048)+`"}`, devHdr)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("long status = %d", resp.StatusCode)
	}
	if w = lastWrite(t, tp.input); w.lane.name != "device" || !w.lane.paste || w.device != "dev-7" {
		t.Fatalf("设备车道长文本应走 DevicePasteChunk: %+v", w.lane)
	}
}

// --- 车道错误映射 -------------------------------------------------------------

func TestInputLaneErrorMapping(t *testing.T) {
	tp := newTestPlane(t, nil)
	devHdr := map[string]string{"X-Amagi-Plane-Actor-Device": "dev-9"}

	// 持控失败 → 403 control.forbidden（对齐反代写面门文案）。
	tp.input.setErr(ErrControlForbidden, nil)
	resp := postInput(t, tp, `{"text":"x"}`, devHdr)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("持控失败 status = %d, want 403", resp.StatusCode)
	}
	var eb errorBody
	decodeJSON(t, resp, &eb)
	if eb.Error != "control.forbidden" {
		t.Fatalf("错误码 = %q, want control.forbidden", eb.Error)
	}

	// 控制服务未就绪 → 503 service.down（App.PtyWrite fail-closed 口径）。
	tp.input.setErr(ErrControlDown, ErrControlDown)
	resp = postInput(t, tp, `{"text":"x"}`, devHdr)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("未就绪 status = %d, want 503", resp.StatusCode)
	}
	decodeJSON(t, resp, &eb)
	if eb.Error != "service.down" {
		t.Fatalf("错误码 = %q, want service.down", eb.Error)
	}
	// 桌面车道同款映射。
	resp = postInterrupt(t, tp, nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("桌面车道未就绪 status = %d, want 503", resp.StatusCode)
	}
}

// --- text 校验（UTF-8 ≤128KiB）--------------------------------------------------

func TestInputValidation(t *testing.T) {
	tp := newTestPlane(t, nil)

	// 128KiB 恰好允许（走分块）；+1 字节拒绝。
	resp := postInput(t, tp, `{"text":"`+strings.Repeat("x", 128*1024)+`"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("128KiB status = %d, want 200", resp.StatusCode)
	}
	resp = postInput(t, tp, `{"text":"`+strings.Repeat("x", 128*1024+1)+`"}`, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("128KiB+1 status = %d, want 400", resp.StatusCode)
	}

	// 非 UTF-8 体 → 400（须在原始体上校验：encoding/json 会把非法字节静默
	// 替换成 U+FFFD，解码后再验已是死代码）。
	resp = postInput(t, tp, "{\"text\":\"a\xffb\"}", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非 UTF-8 status = %d, want 400", resp.StatusCode)
	}

	// JSON 损坏 → 400。
	resp = postInput(t, tp, `{"text":`, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("损坏 JSON status = %d, want 400", resp.StatusCode)
	}
}
