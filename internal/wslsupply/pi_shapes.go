package wslsupply

// pi_shapes.go — 三个 Pi 配置投影单元的形态描述（settings 白名单 / amagi 键集 /
// mcp server 级），供 keyedProjectionAdapter 消费。

import (
	"fmt"
	"sort"
	"strings"

	"amagi-codebox/internal/piconfig"
)

// ---------------------------------------------------------------------------
// pi/settings：字段级白名单（design §四 Pi 表「不允许先整文件拷入再删
// packages」红线——从允许字段构建候选，未知字段不携带）
// ---------------------------------------------------------------------------

// piSettingsWhitelist 是已确认跨平台的偏好字段白名单。S2 收录 retry（网络/
// provider 重试偏好，纯数值，跨平台成立）。显式排除：
//   - packages：插件登记（插件实体走 S5 目标原生重放，复制登记会让 CLI 提前
//     加载未安装或 Windows 形态的资源）；
//   - lastChangelogVersion 等会话/机器状态；
//   - 一切未知字段（LocalOnly，不因「未排除」而自动携带）。
var piSettingsWhitelist = []string{"retry"}

var piSettingsShape = keyedShape{
	relPath:     PiSettingsFile,
	sensitive:   false,
	alwaysFacts: false,
	desired: func(obj map[string]any) (map[string]any, []string) {
		out := map[string]any{}
		for _, field := range piSettingsWhitelist {
			if v, ok := obj[field]; ok {
				out[field] = v
			}
		}
		return out, nil
	},
	convert: func(key string, v any, facts TransformFacts) (any, bool, string) {
		converted, ok := TransformSettingValue(v, facts)
		if !ok {
			return nil, false, "白名单字段携带不可映射的 Windows 路径值，本次剔除该字段"
		}
		return converted, true, ""
	},
	validateTarget: func(obj map[string]any) error {
		// 根为对象即可；白名单外字段形态属用户域，不做假设。
		return nil
	},
}

// ---------------------------------------------------------------------------
// pi/amagi：profile / agents / compaction / concurrency / mcp 路由
// ---------------------------------------------------------------------------

// piAmagiManagedFields 是 amagi.json 的托管顶层字段（$schema 元数据与未知
// 字段不投影，归目标用户所有）。
var piAmagiManagedFields = []string{"profile", "compaction", "concurrency"}

var piAmagiShape = keyedShape{
	relPath:     PiAmagiFile,
	sensitive:   false,
	alwaysFacts: false,
	desired:     amagiDesiredKeys,
	convert: func(key string, v any, _ TransformFacts) (any, bool, string) {
		// 角色模型/压缩模型/MCP 路由均为标识符引用（provider/model、server
		// 名），不携带路径形态；引用满足性由依赖诊断报告，不在转换层伪造。
		return v, true, ""
	},
	validateTarget: amagiValidateDoc,
	validateCandidate: func(content []byte) error {
		// 保留既有验证器：合并候选必须通过 piconfig 的 amagi.json 校验
		//（合法对象 + compaction 形态），不把非法组合写给目标。
		return piconfig.ValidateAmagiConfig(string(content))
	},
}

// amagiDesiredKeys 抽取扁平托管键集：顶层托管字段 + agents/<角色> +
// mcp/<段>（mcp.agents 按角色展开；键名含 "/" 的子键无法安全表示为路径，
// 跳过并诊断）。
func amagiDesiredKeys(obj map[string]any) (map[string]any, []string) {
	out := map[string]any{}
	var notes []string
	for _, field := range piAmagiManagedFields {
		if v, ok := obj[field]; ok {
			out[field] = v
		}
	}
	if agents, ok := obj["agents"].(map[string]any); ok {
		roles := make([]string, 0, len(agents))
		for role := range agents {
			roles = append(roles, role)
		}
		sort.Strings(roles)
		for _, role := range roles {
			if strings.Contains(role, "/") {
				notes = append(notes, fmt.Sprintf("角色名 %q 含路径分隔符，无法投影，请在 Windows 侧改名", role))
				continue
			}
			out["agents/"+role] = agents[role]
		}
	}
	if mcp, ok := obj["mcp"].(map[string]any); ok {
		keys := make([]string, 0, len(mcp))
		for k := range mcp {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "agents" {
				if perRole, ok := mcp[k].(map[string]any); ok {
					roles := make([]string, 0, len(perRole))
					for role := range perRole {
						roles = append(roles, role)
					}
					sort.Strings(roles)
					for _, role := range roles {
						if strings.Contains(role, "/") {
							notes = append(notes, fmt.Sprintf("MCP 路由角色名 %q 含路径分隔符，无法投影", role))
							continue
						}
						out["mcp/agents/"+role] = perRole[role]
					}
				}
				continue
			}
			out["mcp/"+k] = mcp[k]
		}
	}
	return out, notes
}

// amagiValidateDoc 校验目标 amagi.json 形态：根对象、agents/mcp 为对象
// （不符即 target-corrupt——拒绝不覆盖）。
func amagiValidateDoc(obj map[string]any) error {
	if agents, ok := obj["agents"]; ok {
		if _, isObj := agents.(map[string]any); !isObj {
			return fmt.Errorf("amagi.json agents must be an object")
		}
	}
	if mcp, ok := obj["mcp"]; ok {
		if _, isObj := mcp.(map[string]any); !isObj {
			return fmt.Errorf("amagi.json mcp must be an object")
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// pi/mcp：server 级（mcpServers/<name> 原子单元）
// ---------------------------------------------------------------------------

var piMCPShape = keyedShape{
	relPath:     PiMCPFile,
	sensitive:   true, // env 值可能内嵌明文 API key（chmod 失败 fail-closed 删除）
	alwaysFacts: true, // 任何写入都需 shim/路径事实（transform 确定规则）
	desired: func(obj map[string]any) (map[string]any, []string) {
		out := map[string]any{}
		var notes []string
		servers, ok := obj["mcpServers"].(map[string]any)
		if !ok {
			return out, nil // 无 mcpServers 或非对象：键集为空（非损坏——目标校验把关）
		}
		names := make([]string, 0, len(servers))
		for name := range servers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if strings.Contains(name, "/") {
				notes = append(notes, fmt.Sprintf("MCP server 名 %q 含路径分隔符，无法按键路径投影，请改名", name))
				continue
			}
			out["mcpServers/"+name] = servers[name]
		}
		return out, notes
	},
	convert: func(key string, v any, facts TransformFacts) (any, bool, string) {
		entry, ok := v.(map[string]any)
		if !ok {
			return nil, false, "server 定义不是对象，需手动适配"
		}
		result := TransformMCPServer(entry, facts)
		if result.Outcome == TransformRejected {
			return nil, false, result.Reason
		}
		return result.Entry, true, ""
	},
	validateTarget: func(obj map[string]any) error {
		if servers, ok := obj["mcpServers"]; ok {
			if _, isObj := servers.(map[string]any); !isObj {
				return fmt.Errorf("mcp.json mcpServers must be an object")
			}
		}
		return nil
	},
}
