package webui

// probe.go — /api/info 探测与注册表回退扫描（契约 v1.0.2 §4.1 / §7.2 / §7.3）。
//
// 探测语义（§4.1 冻结 + R1 审核 Major3 修订）：
//   - 200 且 ready=true → 就绪；采纳校验由调用方（tracker 强校验）执行
//   - 503 → 解析响应体：v==1 且 pid 与目标会话匹配才视为"未就绪"（结构
//     正常的 info 响应，ready:false，不是错误）；v 非法/pid 不匹配/体损坏
//     的 503 视为不可达（端口被他服务占用场景），转注册表回退而非积压等待
//   - 连接失败 / 其他状态码 / v != 1 → 不可达
//   - v1.0.2：token 非空时携带 `Authorization: Bearer <token>`；服务端据此
//     校验 capability，无 token 的跨源请求被拒绝（401/403 → 不可达）
//
// 注册表（§7.3 冻结格式 + v1.0.2 token 字段）：~/.pi/agent/amagi/webui-registry/<pid>.json
// 目录式 per-pid；注册表只是发现加速器，权威判定永远是探测。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// probeOutcome 是单次 /api/info 探测的三态分类。
type probeOutcome int

const (
	probeUnreachable probeOutcome = iota // 连接失败 / 非预期状态码 / 协议版本不符
	probeNotReady                        // 503：server 已起但 session_start 未就绪
	probeReady                           // 200：就绪
)

// Info 是 /api/info 响应的探测侧视图（契约 §4.1 字段表；只取探测所需字段，
// 其余字段按契约 §2 前向兼容规则忽略）。
type Info struct {
	V         int    `json:"v"`
	Ready     bool   `json:"ready"`
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
}

// probeInfo 对 127.0.0.1:<port>/api/info 执行一次探测（v1.0.2：token 非空
// 时携带 Authorization: Bearer 头）。expectPID>0 时用于 503 体的 pid 校验
// （Major3：仅 v/pid 校验通过的 503 才视为"未就绪"）。
// 返回的 *Info 仅在 probeReady 时保证非 nil。
func probeInfo(ctx context.Context, client *http.Client, port int, token string, expectPID int) (*Info, probeOutcome) {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/info", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, probeUnreachable
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, probeUnreachable
	}
	defer resp.Body.Close()

	is503 := resp.StatusCode == http.StatusServiceUnavailable
	if resp.StatusCode != http.StatusOK && !is503 {
		return nil, probeUnreachable
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, probeUnreachable
	}
	var info Info
	if err := json.Unmarshal(body, &info); err != nil {
		// 体损坏：503 也无法证实归属目标服务，按不可达处理。
		return nil, probeUnreachable
	}
	// 契约 §2 客户端义务：v 缺失/非法视为协议错误；v>1 本探测端不支持，按不可达处理。
	if info.V != 1 {
		return nil, probeUnreachable
	}
	if is503 {
		// Major3：仅 v/pid 校验通过的 503 才是"目标服务未就绪"；否则是端口
		// 被他服务占用/陈旧注册表指向，按不可达让调用方转注册表回退。
		if expectPID > 0 && info.PID != expectPID {
			return nil, probeUnreachable
		}
		return nil, probeNotReady
	}
	if !info.Ready {
		return nil, probeUnreachable
	}
	return &info, probeReady
}

// RegistryEntry 是注册表条目（契约 §7.3 冻结格式，per-pid 一文件一条目）。
// Token 为契约 v1.0.2 新增字段：AMAGI_WEBUI_TOKEN 未注入时（独立终端场景）
// 扩展自生成 token 并写入条目，发现方探测时携带。
type RegistryEntry struct {
	V           int    `json:"v"`
	PID         int    `json:"pid"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	SessionID   string `json:"sessionId"`
	SessionFile string `json:"sessionFile"`
	Ready       bool   `json:"ready"`
	StartedAt   string `json:"startedAt"`
	Token       string `json:"token"`

	// file 是本条目来源文件路径（scanRegistry 填充，不参与 JSON）；
	// 供发现方在候选淘汰时 best-effort 删除陈旧注册文件（§7.3）。
	file string `json:"-"`
}

// scanRegistry 枚举注册表目录下的全部条目。目录不存在 / 单文件损坏 /
// 字段非法（port<=0）的条目一律跳过——注册表是加速器，损坏条目不构成错误。
func scanRegistry(dir string) []RegistryEntry {
	if dir == "" {
		return nil
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var entries []RegistryEntry
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		var e RegistryEntry
		if err := json.Unmarshal(data, &e); err != nil {
			continue
		}
		if e.V != 1 || e.Port <= 0 {
			continue
		}
		e.file = filepath.Join(dir, f.Name())
		entries = append(entries, e)
	}
	return entries
}
