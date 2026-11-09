// Command codebox-agent 是 agent-CLI 的独立薄入口（开发/CI 调试用）：
// 与主程序隐藏子命令 `codebox cli ...` 共用同一 client.Run 与命令表。
// 正式分发走主程序 `cli` 子命令 + C1 任务生成的 shim，本入口不进发布物。
package main

import (
	"context"
	"os"

	"amagi-codebox/internal/agentcli/client"
	"amagi-codebox/internal/agentcli/cmds"
	"amagi-codebox/internal/platform"
)

// Version 由构建脚本注入（-ldflags "-X main.Version=..."），默认 dev。
var Version = "dev"

func main() {
	platform.AttachParentConsole()
	os.Exit(client.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, cmds.Table(), Version))
}
