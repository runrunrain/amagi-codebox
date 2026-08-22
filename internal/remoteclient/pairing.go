// pairing.go 配对流（蓝图 §6 流程 1）：
//
//	transport 探活 GET /api/remote/v1/host/summary（无鉴权）→ 用户在宿主端
//	打开配对窗获取一次性配对码 → POST pairing/complete → 服务端经 Set-Cookie
//	下发 device 凭据 → 拆出 DeviceID + secret：secret 存本机 Keychain 条目
//	`codebox-remoteclient/<DeviceID>`（D-T04，secret 不落盘、不入登记簿），
//	DeviceID 写入登记簿（hosts.go）。
//
//	凭据落库前先做一次已验证的 GET host/summary（携带新 Cookie）：既证明凭据
//	真实可用，也让「配对完成即被撤销」的竞态以 auth.revoked 家族如实浮出。
//	配对码一次性、hostPort 走白名单校验（§9）；失败族（码错/过期/已撤销/
//	网络）全部经 ClientError 稳定码透传（transport.go 单点映射）。
package remoteclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// PairingService 编排配对流：凭据生命周期（Keychain）+ 登记簿回填。
type PairingService struct {
	Registry *HostRegistry
	Creds    CredentialStore
}

// NewPairingService 构建配对服务。
func NewPairingService(registry *HostRegistry, creds CredentialStore) (*PairingService, error) {
	if registry == nil || creds == nil {
		return nil, errors.New("pairing service requires registry and credential store")
	}
	return &PairingService{Registry: registry, Creds: creds}, nil
}

// PairingResult 是配对成功的本机落点：凭据已入 Keychain、登记簿已回填。
type PairingResult struct {
	EntryID    string
	DeviceID   string
	DeviceName string // 本设备在宿主侧的登记名
	HostPort   string
	Summary    contract.HostSummary // 验证请求返回的宿主摘要
}

// localInfraError 把本机基础设施失败（Keychain/登记簿文件）综合为 service.down
// 形态：这不是远端故障，但按恢复语义同归 check-desktop 提示（人工介入本机）。
func localInfraError(cause error) *ClientError {
	return serviceDownError(0, cause)
}

// CompletePairing 执行完整配对流（蓝图 §6 流程 1）。失败时保证零残留：
// 不写 Keychain、不写登记簿。
func (p *PairingService) CompletePairing(ctx context.Context, hostPort, code, deviceName string) (*PairingResult, *ClientError) {
	// 输入校验（§9 白名单；配对码/设备名非空）。
	hp, err := ValidateHostPort(hostPort)
	if err != nil {
		return nil, localAPIError(0, contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
			contract.ActionHintRetry, "invalid hostPort", err)
	}
	code = strings.TrimSpace(code)
	deviceName = strings.TrimSpace(deviceName)
	if code == "" || deviceName == "" {
		return nil, localAPIError(0, contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
			contract.ActionHintRetry, "pairing code and device name are required", nil)
	}
	t, err := NewTransport("http://" + hp)
	if err != nil {
		return nil, localAPIError(0, contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
			contract.ActionHintRetry, "invalid hostPort", err)
	}

	// 1) 探活（无鉴权）：宿主必须存活且讲 v1 协议。契约形态错误（如未配对
	// 401 auth.unpaired）视为存活；网络失败如实透出（网络失败族）。
	if _, cerr := t.HostSummaryUnauthenticated(ctx); cerr != nil {
		if cerr.API == nil || cerr.StatusCode <= 0 {
			return nil, cerr
		}
	}

	// 2) POST pairing/complete（唯一无凭据端点；code 仅随请求体发送一次）。
	var body contract.PairingCompleteResponse
	resp, cerr := t.doResp(ctx, epPairingComplete, requestOption{
		body: contract.PairingCompleteRequest{Code: code, DeviceName: deviceName},
	}, &body)
	if cerr != nil {
		// 码错（401 auth.unpaired）/ 过期（410 auth.window_expired）/
		// 限流（429 rate.limited）等服务端失败族在此透传。
		return nil, cerr
	}

	// 3) Set-Cookie 解析：v1.<DeviceID>.<secret>（严格格式见 transport.go）。
	deviceID, secret, perr := parsePairingCookie(resp)
	if perr != nil {
		return nil, serviceDownError(resp.StatusCode, perr)
	}
	if string(body.Device.ID) != deviceID {
		// 响应体与 Cookie 指向不同设备：宿主行为异常，不落库。
		return nil, serviceDownError(resp.StatusCode,
			fmt.Errorf("pairing response device mismatch (cookie=%s, body=%s)", deviceID, body.Device.ID))
	}

	// 4) 凭据验证：携带新 Cookie 的 GET host/summary。auth.revoked（配对即被
	// 撤销的竞态）→ 已撤销失败族；其它失败也不落库（凭据未经证明）。
	t.SetCredential(deviceID, secret) //nolint:errcheck // 已严格解析，必为非空
	summary, cerr := t.HostSummary(ctx)
	if cerr != nil {
		return nil, cerr
	}

	// 5) 落库：Keychain 先行（凭据权威），登记簿随后；登记簿失败则回滚凭据。
	if err := p.Creds.Put(credentialEntryName(deviceID), secret); err != nil {
		return nil, localInfraError(fmt.Errorf("store device credential: %w", err))
	}
	entryID, err := p.Registry.UpsertPaired(hp, deviceID, "", HealthReachable, time.Now())
	if err != nil {
		_ = p.Creds.Delete(credentialEntryName(deviceID))
		return nil, localInfraError(fmt.Errorf("update host registry: %w", err))
	}
	return &PairingResult{
		EntryID:    entryID,
		DeviceID:   deviceID,
		DeviceName: body.Device.Name,
		HostPort:   hp,
		Summary:    summary,
	}, nil
}

// parsePairingCookie 从 pairing/complete 响应中定位唯一 device Cookie 并解析
// 出 DeviceID + secret（镜像服务端 parseDeviceCookie 的唯一性/严格性要求）。
func parsePairingCookie(resp *http.Response) (deviceID, secret string, err error) {
	if resp == nil {
		return "", "", errors.New("pairing response missing device cookie")
	}
	var found *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == deviceCookieName {
			if found != nil {
				return "", "", errors.New("pairing response has duplicate device cookies")
			}
			found = c
		}
	}
	if found == nil {
		return "", "", errors.New("pairing response missing device cookie")
	}
	return parseDeviceCookieValue(found.Value)
}

// ForgetHost 移除登记簿条目并清理其 Keychain 凭据（凭据删除失败则整体失败、
// 不留孤儿条目——凭据孤儿比条目孤儿危害更大）。legacy token 条目
// （codebox-remoteclient/<DeviceID>/legacy，WD-5）随设备凭据一并清理。
func (p *PairingService) ForgetHost(id string) error {
	entry, ok := p.Registry.Get(id)
	if !ok {
		return fmt.Errorf("host %q not found", id)
	}
	if entry.DeviceID != "" {
		if err := p.Creds.Delete(legacyTokenEntryName(entry.DeviceID)); err != nil {
			return fmt.Errorf("delete legacy token for %s: %w", entry.DeviceID, err)
		}
		if err := p.Creds.Delete(credentialEntryName(entry.DeviceID)); err != nil {
			return fmt.Errorf("delete credential for %s: %w", entry.DeviceID, err)
		}
	}
	return p.Registry.Remove(id)
}
