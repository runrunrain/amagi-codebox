// Package client 是 agent-CLI 的客户端运行时（DESIGN §1）：
// 解析 argv → 本地预检（必填/枚举/类型校验、confirm 预检，全部先于任何网络）
// → 端点发现 → 探活 → 执行 → 输出。供主程序 `cli` 子命令与 cmd/codebox-agent
// 薄入口共用；命令表由调用方注入（cmds.Table()）。
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
)

const usageHint = "查看帮助：" + progName + " help，或 " + progName + " help <命令>"

// failLocal 输出本地失败（--json 时 stdout 仅一个 Envelope，否则 stderr）并返回退出码。
func failLocal(jsonMode bool, stdout, stderr io.Writer, e *spec.Error, exit int) int {
	if jsonMode {
		_ = writeEnvelopeJSON(stdout, spec.Envelope{OK: false, Error: e})
	} else {
		printHumanError(stderr, e)
	}
	return exit
}

// Run 执行一次 CLI 调用并返回退出码（spec.Exit*）。
func Run(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer, table []spec.Command, appVersion string) int {
	g, rest, err := extractGlobals(argv, table)
	if err != nil {
		return failLocal(g.json, stdout, stderr, &spec.Error{Code: spec.ErrBadArgs, Message: err.Error(), Hint: usageHint}, spec.ExitUsage)
	}
	if g.version {
		fmt.Fprintf(stdout, "%s %s\n", progName, appVersion)
		return spec.ExitOK
	}
	// 本地命令（不连服务）：--version / help / completion / 空参数总览。
	if len(rest) == 0 {
		renderOverview(stdout, table, appVersion)
		return spec.ExitOK
	}
	switch rest[0] {
	case "help":
		return runHelp(stdout, stderr, rest[1:], table, appVersion)
	case "completion":
		return runCompletion(stdout, stderr, rest[1:], table)
	}

	stdinSrc := &stdinSource{r: stdin}
	inv, perr := parseCommandArgs(table, rest, stdinSrc, g.help)
	if perr != nil {
		var unk *unknownCommandError
		if errors.As(perr, &unk) {
			// 裸组名 → 组帮助（exit 0）；其余 → 用法错误 + 拼写建议（exit 2）。
			if len(unk.words) == 1 && renderGroupHelp(stdout, table, unk.words[0], appVersion) {
				return spec.ExitOK
			}
			joined := strings.Join(unk.words, " ")
			msg := fmt.Sprintf("未知命令 %q", joined)
			if s := suggestCommands(table, joined, unk.words[len(unk.words)-1]); len(s) > 0 {
				msg += "\n你是不是想输入：\n  " + strings.Join(s, "\n  ")
			}
			return failLocal(g.json, stdout, stderr, &spec.Error{Code: spec.ErrUnknownCommand, Message: msg, Hint: usageHint}, spec.ExitUsage)
		}
		return failLocal(g.json, stdout, stderr, &spec.Error{Code: spec.ErrBadArgs, Message: perr.Error(), Hint: usageHint}, spec.ExitUsage)
	}
	if g.help {
		renderCommandHelp(stdout, inv.cmd, appVersion)
		return spec.ExitOK
	}

	// confirm 预检：先于端点发现与任何网络——应用未运行时缺 --yes 仍返回 4。
	if inv.cmd.Confirm != "" && !g.yes {
		e := &spec.Error{
			Code:    spec.ErrConfirmationRequired,
			Message: fmt.Sprintf("命令 %s 需要确认（风险级 %s）", inv.cmd.Name(), inv.cmd.Risk),
			Hint:    "加 --yes 以确认执行",
		}
		return failLocal(g.json, stdout, stderr, e, spec.ExitConfirm)
	}
	if g.yes && inv.cmd.Confirm != "" {
		inv.confirm = inv.cmd.Confirm
	}

	unreachable := func(msg string) int {
		return failLocal(g.json, stdout, stderr, &spec.Error{Code: codeUnreachable, Message: msg, Hint: unreachableHint}, spec.ExitUnreachable)
	}
	home, err := resolveHome(g.home)
	if err != nil {
		return unreachable(err.Error())
	}
	ep, err := loadEndpoint(home)
	if err != nil {
		return unreachable(err.Error())
	}
	hc := newHTTPClient()
	pd, err := pingServer(ctx, hc, ep)
	if err != nil {
		return unreachable(err.Error())
	}
	if pd.TableHash != "" && pd.TableHash != hashOf(table) {
		fmt.Fprintf(stderr, "警告: 客户端命令表与服务端不一致（本地 %s / 服务端 %s），帮助与参数校验可能有偏差\n", hashOf(table), pd.TableHash)
	}
	timeout := defaultTimeoutSec
	if inv.cmd.Timeout > 0 {
		timeout = inv.cmd.Timeout
	}
	if g.timeoutSet {
		timeout = g.timeout
	}
	env, err := executeCommand(ctx, hc, ep, inv, timeout)
	if err != nil {
		// 命令等待超时/取消：服务端不强行中止 Handler，不可取消的命令会在
		// 超时后继续完成（见 Server 文档，评审 D-06）——提示操作可能已生效，
		// 先查状态再决定是否重试，避免重复副作用。
		var te *timeoutError
		if errors.As(err, &te) {
			return failLocal(g.json, stdout, stderr, &spec.Error{
				Code:    codeUnreachable,
				Message: te.Error(),
				Hint:    "命令等待超时已中断；操作可能已生效，请先查询状态（如 status）再决定是否重试",
			}, spec.ExitUnreachable)
		}
		return unreachable(err.Error())
	}
	if !env.OK && env.Error == nil {
		env.Error = spec.Errorf(spec.ErrInternal, "服务端返回了无错误详情的失败包络")
	}
	if g.json {
		_ = writeEnvelopeJSON(stdout, env)
	} else if env.OK {
		renderData(stdout, env.Data)
	} else {
		printHumanError(stderr, env.Error)
	}
	if env.OK {
		return spec.ExitOK
	}
	return exitCodeFor(env.Error.Code)
}

// runHelp 处理 `help [命令|组]`（本地命令）。
func runHelp(stdout, stderr io.Writer, args []string, table []spec.Command, appVersion string) int {
	var words []string
	for _, a := range args {
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			continue // 忽略旗标（如 help --json）
		}
		words = append(words, a)
	}
	if len(words) == 0 {
		renderOverview(stdout, table, appVersion)
		return spec.ExitOK
	}
	if cmd, k := matchCommand(table, words); k > 0 && k == len(words) {
		renderCommandHelp(stdout, cmd, appVersion)
		return spec.ExitOK
	}
	if len(words) == 1 && renderGroupHelp(stdout, table, words[0], appVersion) {
		return spec.ExitOK
	}
	joined := strings.Join(words, " ")
	msg := fmt.Sprintf("未知的帮助目标 %q", joined)
	if s := suggestCommands(table, joined, words[len(words)-1]); len(s) > 0 {
		msg += "\n你是不是想输入：\n  " + strings.Join(s, "\n  ")
	}
	printHumanError(stderr, &spec.Error{Code: spec.ErrUnknownCommand, Message: msg, Hint: usageHint})
	return spec.ExitUsage
}

// runCompletion 处理 `completion bash|zsh|fish`（本地命令，不连服务）。
func runCompletion(stdout, stderr io.Writer, args []string, table []spec.Command) int {
	shell := ""
	for _, a := range args {
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			continue
		}
		if shell == "" {
			shell = a
		}
	}
	switch shell {
	case "bash", "zsh", "fish":
		renderCompletion(stdout, shell, table)
		return spec.ExitOK
	case "":
		printHumanError(stderr, &spec.Error{Code: spec.ErrBadArgs, Message: "用法：completion bash|zsh|fish", Hint: usageHint})
		return spec.ExitUsage
	default:
		printHumanError(stderr, &spec.Error{Code: spec.ErrBadArgs, Message: "不支持的 shell " + fmt.Sprintf("%q", shell) + "（可用 bash|zsh|fish）"})
		return spec.ExitUsage
	}
}
