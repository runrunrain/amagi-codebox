// Package cmds 是 agent-CLI 命令表的内容（纯数据，无 App 依赖），按领域分文件。
// 每个领域文件导出一个 xxxCommands() []spec.Command；root 包以同名 Name() 为键注册 Handler，
// 并由 root 测试断言"命令表 ↔ Handler 1:1"。
package cmds

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
)

// Table 返回完整命令表（稳定顺序：按领域聚合后按 Name 排序）。
func Table() []spec.Command {
	var all []spec.Command
	for _, group := range [][]spec.Command{
		systemCommands(),  // status / cli info / env / remote status
		configCommands(),  // provider / preset / key / settings / envvars / workdir
		sessionCommands(), // session list/get/output/launch/send/stop/remove/clear-stopped
		usageCommands(),   // usage ... / logs
		pluginCommands(),  // plugin ...
		gitCommands(),     // git repo-info / branches
	} {
		all = append(all, group...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Name() < all[j].Name() })
	return all
}

// Find 按命令名（点号形态）查找。
func Find(table []spec.Command, name string) (spec.Command, bool) {
	for _, c := range table {
		if c.Name() == name {
			return c, true
		}
	}
	return spec.Command{}, false
}

// Hash 是命令表结构哈希（用于 ping 的偏差提示）。
func Hash(table []spec.Command) string {
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

var _ = strings.Join
