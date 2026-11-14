package claudewebui

// input.go — 双车道输入端口 + /api/input、/api/interrupt 处理器（design §5.2 input、§5.6）。
//
// 车道分流单点在本文件（design R7：分流若散落多实现，设备输入误走桌面车道
// → 首条输入夺权、第二条 403 的复发形态）：
//
//	请求携带非空 X-Amagi-Plane-Actor-Device 头（contract.WebUIPlaneActorDeviceHeader）
//	→ 设备平面车道 DeviceWrite/DevicePasteChunk（无夺权；适配器写时校验该
//	  设备持控，失败返回 ErrControlForbidden → 403 control.forbidden）；
//	否则 → 桌面车道 WriteInput/PasteChunk（take-first，与 xterm 本地打字
//	  同语义——本地用户意图，夺权是正确行为）。
//
// 该头只是「车道选择信号」，不是鉴权（鉴权永远是 Bearer token——capability
// token 视同会话权限，持 token 者本就能经桌面车道写入，伪造头不构成提权；
// 反代仍在写面覆盖式剥离/注入，见 webui_proxy.go）。
//
// 错误映射：持控失败 → 403 control.forbidden（对齐反代写面门文案）；
// 控制服务未就绪 → 503 service.down（对齐 App.PtyWrite fail-closed 口径）。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf8"

	"amagi-codebox/internal/remote/contract"
)

// InputPort 是写回 PTY 的窄端口（App 适配 control 门；design §5.2/§5.3）。
type InputPort interface {
	// 桌面车道：与 App.PtyWrite / xterm 本地打字同语义（take-first）。
	WriteInput(ctx context.Context, sessionID string, data []byte) error // ≤1KB 单发
	PasteChunk(ctx context.Context, sessionID string, data []byte) error // 长文本 1KB 分块
	// 设备平面车道（design §5.6）：无夺权；仅当 deviceID 当前持控
	// （connected/grace）才写入，否则返回 ErrControlForbidden。
	DeviceWrite(ctx context.Context, sessionID, deviceID string, data []byte) error
	DevicePasteChunk(ctx context.Context, sessionID, deviceID string, data []byte) error
}

// ErrControlForbidden 表示设备平面车道写时持控校验失败（控制门
// DenyNotController 语义）。App 适配器把 remote 控制门错误映射到本哨兵；
// server 统一映射 403 control.forbidden。
var ErrControlForbidden = errors.New("claudewebui: control.forbidden")

// ErrControlDown 表示控制服务未就绪（控制门 nil/未接线；fail-closed 口径
// 对齐 App.PtyWrite 的 remote.ErrControlNotReady）。server 映射 503
// service.down。
var ErrControlDown = errors.New("claudewebui: service.down")

const (
	// maxTextBytes 是 /api/input 的 text 与 /api/draft 的 draft 上限（128KiB）。
	maxTextBytes = 128 << 10
	// maxBodyBytes 是请求体硬上限（JSON 转义膨胀余量；超限直接 400）。
	maxBodyBytes = 1 << 20
	// maxSingleWrite 是单发阈值：payload ≤1KB 走 Write/DeviceWrite，超过走
	// PasteChunk/DevicePasteChunk 分块变体（对齐 control 门 1KB 分块惯例）。
	maxSingleWrite = 1024
	// submitSeq 是 submit=true 追加的提交键（与 TUI 回车一致）。
	submitSeq = "\r"
	// interruptSeq 是 /api/interrupt 写入的中断键（Esc，与 TUI 一致）。
	interruptSeq = "\x1b"
)

// inputRequest 是 POST /api/input 的请求体。
type inputRequest struct {
	Text   string `json:"text"`
	Submit bool   `json:"submit"`
}

// handleInput 实现 POST /api/input {text, submit}：text 须为 UTF-8 且
// ≤128KiB；submit=true 追加写 \r；>1KB 走 PasteChunk 变体。
func (p *plane) handleInput(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r, maxBodyBytes)
	if !ok {
		return
	}
	// text UTF-8 约束在原始体上校验（encoding/json 会把非法 UTF-8 静默替换
	// 成 U+FFFD，解码后再验已是死代码）。
	if !utf8.Valid(body) {
		writeAPIError(w, r, http.StatusBadRequest, errBadRequest)
		return
	}
	var req inputRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, errBadRequest)
		return
	}
	if len(req.Text) > maxTextBytes {
		writeAPIError(w, r, http.StatusBadRequest, errBadRequest)
		return
	}
	payload := []byte(req.Text)
	if req.Submit {
		payload = append(payload, submitSeq...)
	}
	p.writeLane(w, r, payload)
}

// handleInterrupt 实现 POST /api/interrupt {}：同车道规则写 \x1b（Esc）。
// 请求体允许为空或 {}，内容一律忽略（超限除外——防滥用，统一 400）。
func (p *plane) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		if _, ok := readBody(w, r, maxBodyBytes); !ok {
			return // readBody 已写出错误响应
		}
	}
	p.writeLane(w, r, []byte(interruptSeq))
}

// writeLane 是车道分流单点：按 X-Amagi-Plane-Actor-Device 头选择车道，
// 按 payload 长度选择单发/分块，并做错误映射（见文件头注释）。
func (p *plane) writeLane(w http.ResponseWriter, r *http.Request, payload []byte) {
	var err error
	if deviceID := r.Header.Get(contract.WebUIPlaneActorDeviceHeader); deviceID != "" {
		// 设备平面车道（无夺权；仅反代在写面设置该头）。
		if len(payload) <= maxSingleWrite {
			err = p.input.DeviceWrite(r.Context(), p.cfg.SessionID, deviceID, payload)
		} else {
			err = p.input.DevicePasteChunk(r.Context(), p.cfg.SessionID, deviceID, payload)
		}
	} else {
		// 桌面车道（take-first，与 xterm 本地打字同语义）。
		if len(payload) <= maxSingleWrite {
			err = p.input.WriteInput(r.Context(), p.cfg.SessionID, payload)
		} else {
			err = p.input.PasteChunk(r.Context(), p.cfg.SessionID, payload)
		}
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrControlForbidden):
			writeAPIError(w, r, http.StatusForbidden, "control.forbidden")
		case errors.Is(err, ErrControlDown):
			writeAPIError(w, r, http.StatusServiceUnavailable, "service.down")
		default:
			writeAPIError(w, r, http.StatusInternalServerError, "input.failed")
		}
		return
	}
	writeJSON(w, r, http.StatusOK, okResponse{V: 1, OK: true})
}
