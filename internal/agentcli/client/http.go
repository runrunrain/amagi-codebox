package client

// http.go — agent-CLI 客户端的 HTTP 层：GET PathPing 探活（5s）与 POST
// PathCommand+<name> 执行（超时：--timeout > Command.Timeout > 120s）。
// Host 头为 endpoint URL 本身（net/http 默认行为，不覆盖）。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"amagi-codebox/internal/agentcli/spec"
)

const (
	pingTimeout       = 5 * time.Second
	defaultTimeoutSec = 120
	// maxResponseBytes 是响应体读取上限（宽松：session output 等可较大）。
	maxResponseBytes = 16 << 20
)

// hashOf 与 cmds.Hash、服务端 agentcli 的 hashTable 同算法
// （Name|Risk|Confirm + 每参数 Name:Type;）。三处实现由测试锚定防漂移——
// 漂移会让客户端的 tableHash 偏差提示误报。
func hashOf(table []spec.Command) string {
	h := sha256.New()
	for _, c := range table {
		h.Write([]byte(c.Name() + "|" + string(c.Risk) + "|" + c.Confirm + "|"))
		for _, p := range c.Params {
			h.Write([]byte(p.Name + ":" + string(p.Type) + ";"))
		}
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// pingServer 探活：GET PathPing（pingTimeout 超时，携带 X-Cli-Token）。
func pingServer(ctx context.Context, client *http.Client, ep spec.Endpoint) (spec.PingData, error) {
	ctx2, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx2, http.MethodGet, ep.URL+spec.PathPing, nil)
	if err != nil {
		return spec.PingData{}, err
	}
	req.Header.Set(spec.HeaderToken, ep.CLIToken)
	resp, err := client.Do(req)
	if err != nil {
		return spec.PingData{}, fmt.Errorf("探活失败: %v", err)
	}
	defer resp.Body.Close()
	env, derr := decodeEnvelope(resp)
	if derr != nil {
		return spec.PingData{}, fmt.Errorf("探活响应无法解析（HTTP %d）", resp.StatusCode)
	}
	if !env.OK {
		if env.Error != nil {
			if env.Error.Code == spec.ErrDisabled {
				return spec.PingData{}, errors.New("命令行访问已被关闭（服务端返回 cli_disabled）")
			}
			return spec.PingData{}, fmt.Errorf("探活失败: %s", env.Error.Message)
		}
		return spec.PingData{}, fmt.Errorf("探活失败（HTTP %d）", resp.StatusCode)
	}
	var pd spec.PingData
	b, err := json.Marshal(env.Data)
	if err != nil {
		return spec.PingData{}, fmt.Errorf("探活响应数据异常: %v", err)
	}
	if err := json.Unmarshal(b, &pd); err != nil {
		return spec.PingData{}, fmt.Errorf("探活响应数据异常: %v", err)
	}
	return pd, nil
}

// timeoutError 标记命令请求因本地超时/取消而中断（评审 D-06）。服务端不
// 强行中止 Handler（见 internal/agentcli Server 文档）：不可取消的命令会在
// 客户端超时后继续完成，操作可能已生效，直接重试有重复副作用风险。
type timeoutError struct {
	cause error
	sec   int
}

func (e *timeoutError) Error() string {
	if errors.Is(e.cause, context.Canceled) && !errors.Is(e.cause, context.DeadlineExceeded) {
		return fmt.Sprintf("等待命令响应被取消: %v", e.cause)
	}
	return fmt.Sprintf("等待命令响应超时（上限 %d 秒）: %v", e.sec, e.cause)
}

func (e *timeoutError) Unwrap() error { return e.cause }

// executeCommand POST 命令并解码响应包络（HTTP 状态与包络解耦：以包络为准）。
func executeCommand(ctx context.Context, client *http.Client, ep spec.Endpoint, inv invocation, timeoutSec int) (spec.Envelope, error) {
	if timeoutSec <= 0 {
		timeoutSec = defaultTimeoutSec
	}
	body, err := json.Marshal(spec.Request{Args: inv.args, Confirm: inv.confirm})
	if err != nil {
		return spec.Envelope{}, fmt.Errorf("构造请求失败: %v", err)
	}
	ctx2, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx2, http.MethodPost, ep.URL+spec.PathCommand+inv.cmd.Name(), bytes.NewReader(body))
	if err != nil {
		return spec.Envelope{}, err
	}
	req.Header.Set(spec.HeaderToken, ep.CLIToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return spec.Envelope{}, &timeoutError{cause: err, sec: timeoutSec}
		}
		return spec.Envelope{}, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	env, derr := decodeEnvelope(resp)
	if derr != nil {
		// 响应读取中途超时/取消同样视为“结果未知”：操作可能已生效。
		if errors.Is(derr, context.DeadlineExceeded) || errors.Is(derr, context.Canceled) {
			return spec.Envelope{}, &timeoutError{cause: derr, sec: timeoutSec}
		}
		return spec.Envelope{}, fmt.Errorf("响应无法解析（HTTP %d）: %w", resp.StatusCode, derr)
	}
	return env, nil
}

func decodeEnvelope(resp *http.Response) (spec.Envelope, error) {
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return spec.Envelope{}, err
	}
	var env spec.Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return spec.Envelope{}, err
	}
	return env, nil
}
