package client

// endpoint.go — 端点发现（DESIGN §1/§3.1）：--home > 环境变量 AMAGI_CODEBOX_HOME
// > ~/.amagi-codebox；读 <home>/run/endpoint.json（spec.Endpoint）。缺失/损坏/
// pid 不存活/URL 非回环一律视为“应用未运行或未开启”（退出码 3）。
// 端点文件含令牌：错误信息绝不包含文件内容。

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/platform"
)

// unreachableHint 是所有“应用不可达/未开启”场景的标准提示。
const unreachableHint = "应用未运行或命令行访问未开启（设置→命令行访问）"

// codeUnreachable 是客户端本地的错误码（spec 未定义“不可达”业务码；
// 仅用于 --json 包络，不参与服务端协议）。
const codeUnreachable = "unreachable"

// resolveHome 解析配置目录：--home > AMAGI_CODEBOX_HOME > ~/.amagi-codebox。
func resolveHome(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if v := os.Getenv("AMAGI_CODEBOX_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法定位用户主目录: %w", err)
	}
	return filepath.Join(home, ".amagi-codebox"), nil
}

// loadEndpoint 读取并校验端点文件。校验失败时返回的错误 message 只描述原因，
// 不包含文件内容（含令牌）。
func loadEndpoint(home string) (spec.Endpoint, error) {
	path := filepath.Join(home, "run", "endpoint.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return spec.Endpoint{}, fmt.Errorf("端点文件不存在（%s）", path)
		}
		return spec.Endpoint{}, fmt.Errorf("读取端点文件失败: %v", err)
	}
	var ep spec.Endpoint
	if err := json.Unmarshal(b, &ep); err != nil {
		return spec.Endpoint{}, fmt.Errorf("端点文件损坏（%s）", path)
	}
	if ep.SchemaVersion != spec.EndpointSchemaVersion {
		return spec.Endpoint{}, fmt.Errorf("端点文件版本不兼容（schemaVersion=%d）", ep.SchemaVersion)
	}
	if ep.CLIToken == "" {
		return spec.Endpoint{}, errors.New("端点文件缺少令牌")
	}
	u, err := url.Parse(ep.URL)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return spec.Endpoint{}, errors.New("端点文件 URL 非法")
	}
	// 令牌只发给回环地址：端点文件即使被篡改也不会泄漏到外网。
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return spec.Endpoint{}, errors.New("端点文件 URL 非回环地址，拒绝连接")
	}
	if ep.PID != 0 && !platform.ProcessAlive(ep.PID) {
		return spec.Endpoint{}, errors.New("端点文件已过期（发布进程已退出）")
	}
	return ep, nil
}
