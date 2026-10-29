package piconfig

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var codexAdvisorCommand = exec.CommandContext

func validateAdvisor(obj map[string]any) error {
	raw, exists := obj["advisor"]
	if !exists {
		return nil
	}
	c, ok := raw.(map[string]any)
	if !ok {
		return errors.New("advisor 必须是对象")
	}
	known := map[string]bool{"backend": true, "model": true, "codex": true, "leader": true, "roles": true, "maxCalls": true, "maxTokens": true, "timeoutMs": true}
	for key := range c {
		if !known[key] {
			return errors.New("advisor 含未知字段，请在 JSON 模式修复")
		}
	}
	backend, typed := c["backend"].(string)
	if _, present := c["backend"]; present && !typed {
		return errors.New("advisor.backend 必须是字符串")
	}
	if backend != "" && backend != "pi" && backend != "codex-chat" {
		return errors.New("advisor.backend 只允许 pi/codex-chat")
	}
	if backend == "codex-chat" {
		target := map[string]any{}
		if raw, exists := c["codex"]; exists {
			var ok bool
			target, ok = raw.(map[string]any)
			if !ok {
				return errors.New("advisor.codex 必须是对象")
			}
		}
		for key := range target {
			if key != "mode" && key != "socketPath" && key != "threadId" && key != "model" && key != "reasoningEffort" {
				return errors.New("advisor.codex 含未知字段")
			}
		}
		mode, modeTyped := target["mode"].(string)
		if _, exists := target["mode"]; exists && !modeTyped {
			return errors.New("advisor.codex.mode 必须为字符串")
		}
		if mode == "" {
			if target["threadId"] != nil {
				mode = "existing"
			} else {
				mode = "managed"
			}
		}
		if mode == "managed" {
			if target["threadId"] != nil || target["socketPath"] != nil {
				return errors.New("自有咨询会话不接受其他聊天 ID 或共享连接")
			}
			if model, exists := target["model"]; exists {
				value, ok := model.(string)
				if !ok || !regexp.MustCompile(`^\S+$`).MatchString(value) {
					return errors.New("advisor.codex.model 无效")
				}
			}
			if raw, exists := target["reasoningEffort"]; exists {
				effort, ok := raw.(string)
				if !ok || (effort != "low" && effort != "medium" && effort != "high" && effort != "xhigh" && effort != "max") {
					return errors.New("advisor.codex.reasoningEffort 无效")
				}
			}
		} else if mode == "existing" {
			socket, ok := target["socketPath"].(string)
			if !ok || !(socket == "auto" || strings.HasPrefix(socket, "/") || strings.HasPrefix(socket, `\\`) || regexp.MustCompile(`^[A-Za-z]:[\\/]`).MatchString(socket)) {
				return errors.New("advisor.codex.socketPath 必须为 auto 或绝对路径")
			}
			id, ok := target["threadId"].(string)
			if !ok || !regexp.MustCompile(`^[a-zA-Z0-9-]{8,128}$`).MatchString(id) {
				return errors.New("advisor.codex.threadId 无效")
			}
			if _, exists := target["reasoningEffort"]; exists {
				return errors.New("既有聊天不接受思考档位覆盖")
			}
			if _, exists := target["model"]; exists {
				return errors.New("既有聊天不接受模型覆盖")
			}
		} else {
			return errors.New("advisor.codex.mode 只允许 managed/existing")
		}

	} else {
		model, exists := c["model"]
		if !exists {
			return errors.New("advisor.model 必须配置模型或 null")
		}
		if model != nil {
			s, ok := model.(string)
			if !ok || !regexp.MustCompile(`^[^\s/]+/\S+$`).MatchString(s) {
				return errors.New("advisor.model 必须是 provider/model[:thinking]")
			}
		}
		if _, exists := c["codex"]; exists {
			return errors.New("pi 后端不接受 codex 字段")
		}
	}
	if leader, exists := c["leader"]; exists {
		if _, ok := leader.(bool); !ok {
			return errors.New("advisor.leader 必须是 boolean")
		}
	}
	if roles, exists := c["roles"]; exists {
		values, ok := roles.([]any)
		if !ok {
			return errors.New("advisor.roles 必须是角色名数组")
		}
		for _, v := range values {
			s, ok := v.(string)
			if !ok || !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(s) {
				return errors.New("advisor.roles 含无效角色名")
			}
		}
	}
	for key, max := range map[string]float64{"maxCalls": 50, "maxTokens": 16384, "timeoutMs": 300000} {
		if value, exists := c[key]; exists {
			n, ok := value.(float64)
			if !ok || n < 1 || n > max || n != float64(int64(n)) {
				return fmt.Errorf("advisor.%s 必须是 1–%.0f 的整数", key, max)
			}
		}
	}
	return nil
}

// ListCodexAdvisorChats 只连接已有共享服务。不会启动 daemon、resume 聊天或发送咨询。
func (s *Service) ListCodexAdvisorChats(socketPath string) (string, error) {
	if strings.TrimSpace(socketPath) == "" {
		return "", errors.New("请先填写 Codex 桌面端共享 socket 路径")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := []string{"app-server", "proxy"}
	if socketPath != "auto" {
		args = append(args, "--sock", socketPath)
	}
	cmd := codexAdvisorCommand(ctx, "codex", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return "", errors.New("无法启动 codex 连接代理，请检查安装及 PATH")
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	send := func(v any) error { return json.NewEncoder(stdin).Encode(v) }
	if err = send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "amagi_codebox_advisor", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}}}); err != nil {
		return "", errors.New("Codex 共享连接不可写")
	}
	for scanner.Scan() {
		var m struct {
			ID     int             `json:"id"`
			Error  json.RawMessage `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			return "", errors.New("Codex 返回非法连接协议")
		}
		if len(m.Error) > 0 {
			return "", errors.New("Codex 共享服务拒绝了只读查询")
		}
		switch m.ID {
		case 1:
			if err = send(map[string]any{"method": "initialized"}); err != nil {
				return "", err
			}
			if err = send(map[string]any{"id": 2, "method": "thread/list", "params": map[string]any{"limit": 50}}); err != nil {
				return "", err
			}
		case 2:
			return string(m.Result), nil
		}
	}
	return "", errors.New("Codex 桌面共享服务不可用或连接超时；未启动其他服务接管会话")
}
