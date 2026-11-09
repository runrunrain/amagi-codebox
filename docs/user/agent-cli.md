# Agent CLI（codebox-agent）

Amagi CodeBox 内置一套**面向 AI agent 的本机命令行**：`codebox-agent`。它让 Claude Code、Codex、Pi 等工具里的 agent（或你的脚本）能以受控方式查询与操作桌面应用的配置——提供商、预设、密钥状态、设置、环境变量、会话、用量统计、插件，全部经由**运行中的桌面应用**执行，不存在离线直写配置文件的路径（避免与应用互相覆盖丢更新）。

## 启用与安装

- 命令行访问默认开启（设置 → 命令行访问，对应 `settings.json` 的 `agentCLIEnabled`）；关闭后所有命令返回退出码 3。
- 应用启动时会默认安装两样东西（用户清理过后不再自动装回，可在设置里手动重装）：
  - **shim**：`~/.amagi-codebox/bin/codebox-agent`（Windows 为 `codebox-agent.cmd`）。它只是转发器——`exec` 桌面应用二进制的 `cli` 子命令并固定 `AMAGI_CODEBOX_HOME`。每次应用启动都会把 shim 刷新为指向当前可执行文件。
  - **agent 技能**：`~/.agents/skills/amagi-codebox`（默认目标，教 agent 如何使用本 CLI）。也可选择安装到 `~/.claude/skills/amagi-codebox` 或 `~/.codex/skills/amagi-codebox`（设置 → 命令行访问）。
- 若 PATH 中没有该命令，用完整路径调用，例如 `~/.amagi-codebox/bin/codebox-agent status`。
- 技能目录中的 `references/commands.md` 与本文档的命令参考都由**命令表自动生成**，与应用版本一一对应；以 `codebox-agent --help` 的实际输出为准。

## 安全模型

- CLI 业务命令只连接**运行中的桌面应用**：服务监听 `127.0.0.1` 随机端口，端点信息发布在 `~/.amagi-codebox/run/endpoint.json`（目录 0700 / 文件 0600，含一次性令牌，每次应用启动重新生成）。应用未运行或命令行访问被关闭时，所有命令返回退出码 3。
- 服务端三重校验：请求必须来自回环地址、`Host` 头必须是本机、拒绝携带 `Origin` 的请求（防浏览器页面/DNS rebinding）；令牌常量时间比较。
- **密钥安全**：任何命令的输出都不含明文 API Key（`provider get` / `key status` 只返回掩码摘要）；写入密钥只能经 `provider key-set` 的 stdin。`provider save` 的 JSON 中出现密钥字段会被直接拒绝。
- **确认门**：`exec`（启动进程/写入会话/安装软件）与 `destructive`（删除/不可逆）命令必须携带 `--yes`；客户端本地预检与服务端强制校验双重把关（缺失时退出码 4）。
- **审计**：所有非只读命令在 `~/.amagi-codebox/logs/agent-cli-audit.jsonl` 留一行记录（时间、命令、风险级、结果码、耗时、参数名——**不记参数值**）。
- 不提供的面（设计上排除）：远程控制令牌与设备配对、CLI 自身开关与令牌轮换、明文密钥导出、GitAssist 提交推送、应用自更新。这些只能在桌面应用操作。

## 退出码

| 码 | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 业务错误（读 `error` 处理） |
| 2 | 参数/用法错误（含被安全策略拒绝的命令，如改禁改设置键） |
| 3 | 应用不可达——桌面应用未运行，或命令行访问被关闭 |
| 4 | 需要确认——命令有副作用且未带 `--yes` |

程序化处理请加 `--json`：stdout 只输出一个包络 `{"ok":true,"data":…}` / `{"ok":false,"error":{"code","message","hint"}}`，诊断走 stderr。注意：`provider save` / `preset save` 的 `--json <值>` 是**参数旗标**（传 JSON 配置）；需要 JSON 输出模式时把全局 `--json` 放在命令词之前（如 `codebox-agent --json provider list`）。

## 故障排查

- **退出码 3（应用未运行）**：先启动 Amagi CodeBox 桌面应用；若已运行，检查设置中「命令行访问」是否被关闭。删除 `~/.amagi-codebox/run/endpoint.json` 无需手动清理——应用退出时会撤销，启动时重新发布。
- **shim 不在 PATH**：把 `~/.amagi-codebox/bin` 加入 PATH，或直接用完整路径。
- **shim 指向旧位置/失效**：应用每次启动会自动刷新；也可在设置里「重新安装 shim」。shim 文件被手工改动后应用不会覆盖它（保护用户修改），如需接管请自行删除该文件后再安装。
- **命令表不一致警告**（stderr 提示本地/服务端 hash 不同）：shim 指向的二进制与运行中应用版本不一致，重启桌面应用或重装 shim。
- **技能未安装/过期**：设置 → 命令行访问里重新安装对应目标；技能内容被手工改动时同样不会覆盖。
- **权限报错**：确保端点文件与 `~/.amagi-codebox` 归当前用户所有（0600/0700）；其他本机用户无法读取即属预期。

## 命令参考

<!-- BEGIN generated-command-reference：由 install.GenerateCommandReference(cmds.Table()) 生成，勿手改；重生成：go test ./internal/agentcli/install -run TestAgentCLIDocsUpToDate -update -->
# codebox-agent 命令参考

由命令表自动生成；以 `codebox-agent --help` 与各命令的 `--help` 为准。

风险级：`read` 只读 · `mutate` 修改配置/状态（可逆） · `exec` 启动进程/写入会话/安装软件 · `destructive` 删除/清空（不可逆）。`exec` 与 `destructive` 命令需 `--yes` 注入确认值。

## 通用

### logs

查询应用运行日志

```
codebox-agent logs [--level <level>] [--source <source>] [--keyword <keyword>] [--limit <limit>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--level` | 旗标 | `string` | 否 | 可选；最低日志级别（含该级及以上），缺省全部；可选值：DEBUG / INFO / WARN / ERROR |
| `--source` | 旗标 | `string` | 否 | 可选；按来源过滤（如 app/session/usage） |
| `--keyword` | 旗标 | `string` | 否 | 可选；消息与详情的子串匹配 |
| `--limit` | 旗标 | `int` | 否 | 最多返回条数（新→旧），缺省 100；默认 100 |

风险：`read`（只读）。

输出已做凭据清洗：已知密钥的精确值、Authorization/Bearer 值与常见 token 前缀（sk-/ghp_/xox 等）替换为 ***。

示例：

```
codebox-agent logs --limit 20
```
```
codebox-agent logs --level ERROR --keyword timeout
```

---

### status

应用总览：版本/平台/环境检测摘要/会话计数/远程开关/agent-cli 状态

```
codebox-agent status
```

风险：`read`（只读）。

只读聚合视图，不含任何令牌或密钥；会话计数按状态分组。

示例：

```
codebox-agent status
```
```
codebox-agent status --json
```

---

## 系统与状态

### cli.info

agent-CLI 自身信息：shim 路径、endpoint 文件路径、skill 安装状态

```
codebox-agent cli info
```

风险：`read`（只读）。

只报告文件存在性，绝不读取/回显 endpoint.json 内容（其中含令牌）。

示例：

```
codebox-agent cli info
```

---

### env.check

实时环境检测：全部工具或指定工具

```
codebox-agent env check [--tool <tool>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--tool` | 旗标 | `string` | 否 | 可选；只检测该工具（claude-code/opencode/codex/pi/omp/headroom），缺省检测全部；可选值：claude-code / opencode / codex / pi / omp / headroom |

风险：`read`（只读）。

超时 300 秒。

示例：

```
codebox-agent env check
```
```
codebox-agent env check --tool codex
```

---

### env.install

安装指定 CLI 工具（npm 等外部安装器，可能改动 PATH）

```
codebox-agent env install --tool <tool>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--tool` | 旗标 | `string` | 是 | 要安装的工具名（claude-code/opencode/codex/pi/omp/headroom）；可选值：claude-code / opencode / codex / pi / omp / headroom |

风险：`exec`（启动进程/写入会话/安装软件）；确认值 `INSTALL`——执行需 `--yes`。

超时 900 秒。

exec 级操作，需 --yes 确认；安装耗时可达数分钟。外部安装器不可中途取消：超时不等于未安装，先用 env status / env check 查询结果再决定是否重试。

示例：

```
codebox-agent env install --tool opencode --yes
```

---

### env.status

最近一次环境检测结果（缓存快照，不实时探测）

```
codebox-agent env status
```

风险：`read`（只读）。

尚未检测过时 checked=false；需要实时结论请用 env check。

示例：

```
codebox-agent env status
```

---

### remote.status

远程控制服务状态（开关/端口/运行态，不含令牌）

```
codebox-agent remote status
```

风险：`read`（只读）。

脱敏视图：永不返回 token 字段，仅返回 tokenConfigured 布尔值。

示例：

```
codebox-agent remote status
```

---

## 配置

### envvars.delete

删除一个环境变量（不可逆）

```
codebox-agent envvars delete <key>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `key` | 位置 | `string` | 是 | 环境变量名 |

风险：`destructive`（删除/清空，不可逆）；确认值 `DELETE`——执行需 `--yes`。

示例：

```
codebox-agent envvars delete ANTHROPIC_LOG --yes
```

---

### envvars.list

列出应用注入各 CLI 的环境变量（敏感名脱敏）

```
codebox-agent envvars list
```

风险：`read`（只读）。

名称含 KEY/TOKEN/SECRET/PASSWORD/PASSWD/CREDENTIAL/AUTH 的变量值一律返回掩码并附 valueSet 布尔；其余原样回显。

示例：

```
codebox-agent envvars list
```

---

### envvars.set

设置一个环境变量（key=value）

```
codebox-agent envvars set <key> <value>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `key` | 位置 | `string` | 是 | 环境变量名（保留键 WSLENV/PATH/PATHEXT 等会被服务端拒绝） |
| `value` | 位置 | `string` | 是 | 环境变量值 |

风险：`mutate`（修改配置/状态，可逆）。

敏感名（含 KEY/TOKEN/SECRET/PASSWORD/PASSWD/CREDENTIAL/AUTH）的回显与后续 list 输出均为掩码，底层存储保留真实值。

示例：

```
codebox-agent envvars set ANTHROPIC_LOG debug
```

---

### key.status

密钥状态诊断（掩码输出，支持按提供商过滤）

```
codebox-agent key status [--provider <provider>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--provider` | 旗标 | `string` | 否 | 可选；只看该提供商，缺省返回全部 |

风险：`read`（只读）。

输出为 GetKeyDiagnostics 的脱敏投影：maskedKey/stored_masked 均 已掩码。

示例：

```
codebox-agent key status
```
```
codebox-agent key status --provider acme
```

---

### preset.delete

删除终端预设（不可逆）

```
codebox-agent preset delete <type> <name>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `type` | 位置 | `string` | 是 | 预设类型桶；可选值：claude_code / opencode / codex / pi / omp / anthropic / openai |
| `name` | 位置 | `string` | 是 | 预设名 |

风险：`destructive`（删除/清空，不可逆）；确认值 `DELETE`——执行需 `--yes`。

示例：

```
codebox-agent preset delete claude_code daily --yes
```

---

### preset.list

列出终端预设（合并视图，按类型桶）

```
codebox-agent preset list --type <type>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--type` | 旗标 | `string` | 是 | 预设类型桶；claude_code 归入 anthropic 桶，codex/pi/omp 归入 openai 桶，opencode 独立；可选值：claude_code / opencode / codex / pi / omp / anthropic / openai |

风险：`read`（只读）。

示例：

```
codebox-agent preset list --type claude_code
```
```
codebox-agent preset list --type opencode
```

---

### preset.save

新建或更新终端预设

```
codebox-agent preset save <type> <name> [--json <json>] [--provider <provider>] [--model <model>] [--vision] [--video]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `type` | 位置 | `string` | 是 | 预设类型桶（同 preset list --type）；可选值：claude_code / opencode / codex / pi / omp / anthropic / openai |
| `name` | 位置 | `string` | 是 | 预设名（桶内唯一键） |
| `--json` | 旗标 | `json` | 否 | 可选；完整预设 JSON（结构与 preset list 条目一致），未给字段保留原值 |
| `--provider` | 旗标 | `string` | 否 | 可选；关联提供商名，覆盖 json 中的值 |
| `--model` | 旗标 | `string` | 否 | 可选；模型名，覆盖 json 中的值 |
| `--vision` | 旗标 | `bool` | 否 | 可选；标记该模型可识图 |
| `--video` | 旗标 | `bool` | 否 | 可选；标记该模型可识视频 |

风险：`mutate`（修改配置/状态，可逆）。

json 与旗标可组合（旗标优先）；两者都未提供 provider 时报参数错误。

示例：

```
codebox-agent preset save claude_code daily --provider acme --model m-daily
```
```
codebox-agent preset save opencode oc1 --json '{"provider":"acme","model":"m-1"}'
```

---

### provider.delete

删除提供商配置及其密钥（不可逆）

```
codebox-agent provider delete <name>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `name` | 位置 | `string` | 是 | 提供商名称 |

风险：`destructive`（删除/清空，不可逆）；确认值 `DELETE`——执行需 `--yes`。

连带删除密钥存储条目并同步各 harness 配置。

示例：

```
codebox-agent provider delete acme --yes
```

---

### provider.get

查看单个提供商配置（脱敏视图）

```
codebox-agent provider get <name>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `name` | 位置 | `string` | 是 | 提供商名称 |

风险：`read`（只读）。

输出结构可直接修改后用于 provider save --json（在现有配置上合并，未提及字段保留）；认证类 header 值已掩码，传回掩码占位即保留现有值；密钥请用 provider key-set 单独写入。

示例：

```
codebox-agent provider get acme
```

---

### provider.key-set

写入提供商 API 密钥（key 建议经 stdin 传入）

```
codebox-agent provider key-set <name> --key <key>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `name` | 位置 | `string` | 是 | 提供商名称 |
| `--key` | 旗标 | `string` | 是 | API 密钥；传 - 表示从 stdin 读取，避免出现在 argv/审计日志中；支持 `-` 从 stdin 读取；敏感值，日志与回显脱敏 |

风险：`mutate`（修改配置/状态，可逆）。

Secret 参数：审计日志与错误回显均脱敏；成功后返回掩码确认，绝不回显明文。

示例：

```
codebox-agent provider key-set acme --key - <<< "$ACME_KEY"
```
```
echo "$ACME_KEY" | codebox-agent provider key-set acme --key -
```

---

### provider.list

列出提供商配置（脱敏视图，含密钥状态摘要）

```
codebox-agent provider list [--type <type>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--type` | 旗标 | `string` | 否 | 可选；按兼容格式过滤：anthropic 或 openai，缺省返回全部；可选值：anthropic / openai |

风险：`read`（只读）。

输出不含任何明文/密钥字段；key.maskedKey 为掩码摘要；认证类 header（authorization/x-api-key/token/secret/cookie 等）的值一律返回掩码并附 headersSet 布尔。

示例：

```
codebox-agent provider list
```
```
codebox-agent provider list --type openai
```

---

### provider.save

新建或覆盖保存提供商配置（JSON 与 provider get 输出同构）

```
codebox-agent provider save <name> --json <json>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `name` | 位置 | `string` | 是 | 提供商名称（不可含 /，首尾不可有空白） |
| `--json` | 旗标 | `json` | 是 | 提供商配置 JSON（结构与 provider get 输出一致）；任何位置出现 apiKey/authKey 等密钥字段会被拒绝 |

风险：`mutate`（修改配置/状态，可逆）。

在现有配置上合并：DTO 出现的字段覆盖，未提及的字段（含 url_history、preset 的 opencodeConfig、未列出的 header/preset）保留。认证类 header（authorization/x-api-key/token/secret/cookie 等）输出已掩码，save 传回掩码占位即保留现有值；明文认证 header 会被拒绝（改用桌面应用或 $ENV: 引用）；密钥只能经 provider key-set（stdin）写入。

示例：

```
codebox-agent provider save acme --json '{"defaultModel":"m-1","openai":{"enabled":true,"baseUrl":"https://api.test/v1"}}'
```

---

### settings.get

读取应用设置（脱敏：凭据字段只返回是否已设置）

```
codebox-agent settings get
```

风险：`read`（只读）。

githubToken 等凭据不回显明文，仅返回 githubTokenSet 布尔值。

示例：

```
codebox-agent settings get
```

---

### settings.set

修改白名单内的应用设置项

```
codebox-agent settings set <key> <value>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `key` | 位置 | `string` | 是 | 设置键（白名单见 Notes；remote*/cli*/含 token 的键一律拒绝） |
| `value` | 位置 | `string` | 是 | 设置值（terminal.scrollback 为整数） |

风险：`mutate`（修改配置/状态，可逆）。

白名单：dashboard.provider/preset/opencodeprovider/mode/shell/claudemode/claudeshell/opencodemode/opencodeshell/codexmode/codexshell/pimode/pishell/ompmode/ompshell、terminal.scrollback、mobilewebroot。命中禁改面（remote*/cli*/token）返回 forbidden_command。

示例：

```
codebox-agent settings set dashboard.provider acme
```
```
codebox-agent settings set terminal.scrollback 50000
```

---

### workdir.add

保存一个工作目录（按路径去重）

```
codebox-agent workdir add <path> [--label <label>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `path` | 位置 | `string` | 是 | 目录绝对路径 |
| `--label` | 旗标 | `string` | 否 | 可选；显示标签，缺省用路径末段 |

风险：`mutate`（修改配置/状态，可逆）。

示例：

```
codebox-agent workdir add /Users/me/work/proj
```
```
codebox-agent workdir add /Users/me/work/proj --label 项目A
```

---

### workdir.list

列出已保存的工作目录

```
codebox-agent workdir list
```

风险：`read`（只读）。

示例：

```
codebox-agent workdir list
```

---

### workdir.remove

移除一个已保存的工作目录

```
codebox-agent workdir remove <path>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `path` | 位置 | `string` | 是 | 目录路径（须与保存时一致） |

风险：`mutate`（修改配置/状态，可逆）。

示例：

```
codebox-agent workdir remove /Users/me/work/proj
```

---

## 会话

### session.clear-stopped

清理全部已停止会话（含控制面墓碑）

```
codebox-agent session clear-stopped
```

风险：`destructive`（删除/清空，不可逆）；确认值 `CLEAR`——执行需 `--yes`。

返回逐项结果：cleared/clearedIds/retainedIds/failed（部分清理失败不影响其余条目）。

示例：

```
codebox-agent session clear-stopped --yes --json
```

---

### session.get

查看单个会话摘要

```
codebox-agent session get <sessionId>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `sessionId` | 位置 | `string` | 是 | 会话 ID |

风险：`read`（只读）。

示例：

```
codebox-agent session get abc123
```

---

### session.launch

启动一个 CLI 会话（claudecode/opencode/codex/pi/omp）

```
codebox-agent session launch <appType> [--provider <provider>] [--model <model>] [--preset <preset>] [--work-dir <workDir>] [--mode <mode>] [--headroom] [--shell <shell>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `appType` | 位置 | `string` | 是 | CLI 类型；可选值：claudecode / opencode / codex / pi / omp |
| `--provider` | 旗标 | `string` | 否 | 提供商名（claudecode 必填；codex/pi/omp 可选，与 preset 二选一时优先显式 model） |
| `--model` | 旗标 | `string` | 否 | 模型名（codex/pi/omp 的第一参数；也可传 terminal preset 键） |
| `--preset` | 旗标 | `string` | 否 | 预设名（claudecode/opencode 用 preset 名；codex/pi/omp 未给 model 时作为 preset 键传入） |
| `--work-dir` | 旗标 | `string` | 否 | 工作目录：必须是已存在的目录（校验时归一化为绝对路径）；省略则走应用默认目录 |
| `--mode` | 旗标 | `string` | 否 | 启动模式，默认 embedded；可选值：embedded / terminal；默认 embedded |
| `--headroom` | 旗标 | `bool` | 否 | 启用上下文压缩代理（仅 claudecode 支持）；默认 false |
| `--shell` | 旗标 | `string` | 否 | 覆盖启动 shell 路径（可选） |

风险：`exec`（启动进程/写入会话/安装软件）；确认值 `LAUNCH`——执行需 `--yes`。

默认 embedded（内嵌终端，输出可用 session output 读取）。terminal 模式也允许，但输出进独立终端窗口，agent 无法读取其输出。headroom 仅 claudecode 支持（其他类型传 true 返回 bad_args）。启动入口不可中途取消：超时不等于未生效，先用 session list/get 查询是否已启动，再决定是否重试。启动前应向用户确认工作目录与提供商（传 --shell 或 --work-dir 时必须一并确认）。返回 sessionId 与会话摘要。

示例：

```
codebox-agent session launch claudecode --provider myprov --preset default --work-dir ~/proj --yes
```
```
codebox-agent session launch codex --provider myprov --model gpt-5 --work-dir ~/proj --yes --json
```

---

### session.list

列出全部会话（可按状态过滤）

```
codebox-agent session list [--status <status>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--status` | 旗标 | `string` | 否 | 按状态过滤（省略则返回全部）；可选值：running / stopping / stopped / exited / failed |

风险：`read`（只读）。

示例：

```
codebox-agent session list
```
```
codebox-agent session list --status running --json
```

---

### session.output

读取内嵌会话的输出历史（默认剥离 ANSI/控制序列）

```
codebox-agent session output <sessionId> [--tail-bytes <tailBytes>] [--since-seq <sinceSeq>] [--raw]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `sessionId` | 位置 | `string` | 是 | 会话 ID |
| `--tail-bytes` | 旗标 | `int` | 否 | 仅返回最后 N 字节（按 UTF-8 字符边界安全截断；作用于处理后的文本；--raw 模式作用于原始字节） |
| `--since-seq` | 旗标 | `int` | 否 | 上一轮返回的 seq：seq 未前进时返回空文本，前进时返回完整快照供整体重算（seq 是输出分块计数，非字节偏移，不支持精确增量） |
| `--raw` | 旗标 | `bool` | 否 | 返回未剥离控制序列的原始文本 |

风险：`read`（只读）。

超时 30 秒。

默认剥离 ANSI 与控制序列：CSI/OSC 转义序列整段丢弃，回车(\r)按行首覆盖、退格(\b)按光标左移+覆盖写处理；CSI 光标移动/清屏等序列按丢弃处理（不模拟光标移动）。terminal 模式会话的输出进独立终端窗口，本命令对其无内容（仅 embedded 会话有内嵌输出历史）。输出可能含终端回显的敏感信息（如粘贴过的密钥），注意不要写入日志。

示例：

```
codebox-agent session output abc123
```
```
codebox-agent session output abc123 --tail-bytes 4096 --since-seq 42 --json
```

---

### session.remove

移除已停止会话的记录

```
codebox-agent session remove <sessionId>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `sessionId` | 位置 | `string` | 是 | 会话 ID |

风险：`destructive`（删除/清空，不可逆）；确认值 `REMOVE`——执行需 `--yes`。

会话仍在运行/停止中返回 conflict，先 stop 再 remove。

示例：

```
codebox-agent session remove abc123 --yes
```

---

### session.send

向运行中的内嵌会话发送输入

```
codebox-agent session send <sessionId> <text> [--enter]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `sessionId` | 位置 | `string` | 是 | 会话 ID |
| `text` | 位置 | `string` | 是 | 要发送的文本（可用 - 从 stdin 读取）；支持 `-` 从 stdin 读取 |
| `--enter` | 旗标 | `bool` | 否 | 在文本末尾追加回车（\r） |

风险：`exec`（启动进程/写入会话/安装软件）；确认值 `SEND`——执行需 `--yes`。

会话必须处于 running（不存在→not_found，非 running→conflict，控制运行时未就绪→not_ready）。≤1KB 文本走键盘输入通道，更长文本走分块粘贴通道（等价于桌面终端粘贴）。写入不可中途取消：超时不等于未送达，重试前先用 session output 查看回显，避免重复输入。

示例：

```
codebox-agent session send abc123 "claude --help" --enter --yes
```

---

### session.stop

停止一个会话

```
codebox-agent session stop <sessionId>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `sessionId` | 位置 | `string` | 是 | 会话 ID |

风险：`destructive`（删除/清空，不可逆）；确认值 `STOP`——执行需 `--yes`。

示例：

```
codebox-agent session stop abc123 --yes
```

---

## 用量与日志

### usage.daily

按日聚合的用量趋势

```
codebox-agent usage daily [--days <days>] [--granularity <granularity>] [--start-date <startDate>] [--end-date <endDate>] [--app-type <appType>] [--provider <provider>] [--source <source>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--days` | 旗标 | `int` | 否 | 最近 N 天；与 startDate/endDate 互斥，缺省 30；默认 30 |
| `--granularity` | 旗标 | `string` | 否 | 聚合粒度，缺省 day；可选值：day / week；默认 day |
| `--start-date` | 旗标 | `string` | 否 | 可选；UTC 日期（YYYY-MM-DD），闭区间，与 endDate 配对使用 |
| `--end-date` | 旗标 | `string` | 否 | 可选；UTC 日期（YYYY-MM-DD），闭区间 |
| `--app-type` | 旗标 | `string` | 否 | 可选；按 CLI 应用过滤：claudecode/opencode/codex/pi/omp；可选值：claudecode / opencode / codex / pi / omp |
| `--provider` | 旗标 | `string` | 否 | 可选；按提供商名过滤 |
| `--source` | 旗标 | `string` | 否 | 可选；按数据源过滤（如 session_log），缺省全部 |

风险：`read`（只读）。

给了 startDate/endDate 时忽略 days。

示例：

```
codebox-agent usage daily
```
```
codebox-agent usage daily --days 7 --app-type codex
```
```
codebox-agent usage daily --start-date 2026-07-01 --end-date 2026-07-07
```

---

### usage.models

按模型聚合的用量统计（含缓存命中率与价格覆盖情况）

```
codebox-agent usage models [--start-date <startDate>] [--end-date <endDate>] [--app-type <appType>] [--provider <provider>] [--source <source>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--start-date` | 旗标 | `string` | 否 | 可选；UTC 日期（YYYY-MM-DD），闭区间，与 endDate 配对使用 |
| `--end-date` | 旗标 | `string` | 否 | 可选；UTC 日期（YYYY-MM-DD），闭区间 |
| `--app-type` | 旗标 | `string` | 否 | 可选；按 CLI 应用过滤：claudecode/opencode/codex/pi/omp；可选值：claudecode / opencode / codex / pi / omp |
| `--provider` | 旗标 | `string` | 否 | 可选；按提供商名过滤 |
| `--source` | 旗标 | `string` | 否 | 可选；按数据源过滤（如 session_log），缺省全部 |

风险：`read`（只读）。

示例：

```
codebox-agent usage models
```
```
codebox-agent usage models --provider acme
```

---

### usage.providers

按提供商聚合的用量统计

```
codebox-agent usage providers [--start-date <startDate>] [--end-date <endDate>] [--app-type <appType>] [--provider <provider>] [--source <source>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--start-date` | 旗标 | `string` | 否 | 可选；UTC 日期（YYYY-MM-DD），闭区间，与 endDate 配对使用 |
| `--end-date` | 旗标 | `string` | 否 | 可选；UTC 日期（YYYY-MM-DD），闭区间 |
| `--app-type` | 旗标 | `string` | 否 | 可选；按 CLI 应用过滤：claudecode/opencode/codex/pi/omp；可选值：claudecode / opencode / codex / pi / omp |
| `--provider` | 旗标 | `string` | 否 | 可选；按提供商名过滤 |
| `--source` | 旗标 | `string` | 否 | 可选；按数据源过滤（如 session_log），缺省全部 |

风险：`read`（只读）。

示例：

```
codebox-agent usage providers
```

---

### usage.requests

请求明细日志（分页）

```
codebox-agent usage requests [--model <model>] [--page <page>] [--page-size <pageSize>] [--start-date <startDate>] [--end-date <endDate>] [--app-type <appType>] [--provider <provider>] [--source <source>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--model` | 旗标 | `string` | 否 | 可选；按模型名（归一化）过滤 |
| `--page` | 旗标 | `int` | 否 | 页码，1 起，缺省 1；默认 1 |
| `--page-size` | 旗标 | `int` | 否 | 每页条数，缺省 50，上限 500；默认 50 |
| `--start-date` | 旗标 | `string` | 否 | 可选；UTC 日期（YYYY-MM-DD），闭区间，与 endDate 配对使用 |
| `--end-date` | 旗标 | `string` | 否 | 可选；UTC 日期（YYYY-MM-DD），闭区间 |
| `--app-type` | 旗标 | `string` | 否 | 可选；按 CLI 应用过滤：claudecode/opencode/codex/pi/omp；可选值：claudecode / opencode / codex / pi / omp |
| `--provider` | 旗标 | `string` | 否 | 可选；按提供商名过滤 |
| `--source` | 旗标 | `string` | 否 | 可选；按数据源过滤（如 session_log），缺省全部 |

风险：`read`（只读）。

示例：

```
codebox-agent usage requests
```
```
codebox-agent usage requests --model gpt-5 --page 2 --page-size 20
```

---

### usage.summary

用量汇总：请求/token/成本（按币种）与实际日期范围

```
codebox-agent usage summary [--start-date <startDate>] [--end-date <endDate>] [--app-type <appType>] [--provider <provider>] [--source <source>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--start-date` | 旗标 | `string` | 否 | 可选；UTC 日期（YYYY-MM-DD），闭区间，与 endDate 配对使用 |
| `--end-date` | 旗标 | `string` | 否 | 可选；UTC 日期（YYYY-MM-DD），闭区间 |
| `--app-type` | 旗标 | `string` | 否 | 可选；按 CLI 应用过滤：claudecode/opencode/codex/pi/omp；可选值：claudecode / opencode / codex / pi / omp |
| `--provider` | 旗标 | `string` | 否 | 可选；按提供商名过滤 |
| `--source` | 旗标 | `string` | 否 | 可选；按数据源过滤（如 session_log），缺省全部 |

风险：`read`（只读）。

成本单位为 micro-currency（1e-6）；总成本按币种分组并折算 USD 汇总。

示例：

```
codebox-agent usage summary
```
```
codebox-agent usage summary --app-type claudecode --start-date 2026-07-01 --end-date 2026-07-31
```

---

### usage.sync

立即同步各 CLI 会话日志到本地用量库

```
codebox-agent usage sync
```

风险：`mutate`（修改配置/状态，可逆）。

超时 600 秒。

扫描会话日志文件入库；首次或日志量大时耗时较长。同步不可中途取消：超时不等于未完成，重试前先用 usage summary 查询数据是否已入库。

示例：

```
codebox-agent usage sync
```

---

## 插件

### plugin.disable

禁用插件

```
codebox-agent plugin disable <app> <id>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `app` | 位置 | `string` | 是 | 插件引擎：claudecode\|codex\|opencode\|pi\|omp；可选值：claudecode / codex / opencode / pi / omp |
| `id` | 位置 | `string` | 是 | 插件标识（同 plugin enable） |

风险：`mutate`（修改配置/状态，可逆）。

opencode 与 pi 引擎不支持启停（见 plugin enable Notes）。

示例：

```
codebox-agent plugin disable claudecode myplugin@mymarket
```

---

### plugin.enable

启用插件

```
codebox-agent plugin enable <app> <id>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `app` | 位置 | `string` | 是 | 插件引擎：claudecode\|codex\|opencode\|pi\|omp；可选值：claudecode / codex / opencode / pi / omp |
| `id` | 位置 | `string` | 是 | 插件标识（claudecode/codex 为 name@marketplace；omp 为包名或 pluginId） |

风险：`mutate`（修改配置/状态，可逆）。

opencode 与 pi 引擎不支持启停：opencode 无启停面（安装/卸载即开关），pi 的 packages[] 存在即启用——对这两者调用返回 bad_args。

示例：

```
codebox-agent plugin enable claudecode myplugin@mymarket
```

---

### plugin.install

安装插件（网络操作，写各 CLI 插件目录）

```
codebox-agent plugin install <app> <name>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `app` | 位置 | `string` | 是 | 插件引擎：claudecode\|codex\|opencode\|pi\|omp；可选值：claudecode / codex / opencode / pi / omp |
| `name` | 位置 | `string` | 是 | 安装标识：claudecode=插件名（用户级安装）；codex=plugin@marketplace；opencode=模块地址 spec；pi=包源（npm:…/git:…/本地路径）；omp=包名或 spec |

风险：`exec`（启动进程/写入会话/安装软件）；确认值 `INSTALL`——执行需 `--yes`。

超时 600 秒。

安装经网络/包管理器，不可中途取消：超时不等于未安装，先用 plugin list 查询结果再决定是否重试。

示例：

```
codebox-agent plugin install claudecode myplugin@mymarket --yes
```
```
codebox-agent plugin install pi npm:@foo/bar@1.0.0 --yes
```

---

### plugin.list

列出已安装插件（五引擎统一 DTO）

```
codebox-agent plugin list [--app <app>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--app` | 旗标 | `string` | 否 | 插件引擎：claudecode\|codex\|opencode\|pi\|omp（省略则聚合全部引擎，失败引擎记入 errors 字段）；可选值：claudecode / codex / opencode / pi / omp |

风险：`read`（只读）。

统一字段：app/id/name/version/enabled/description/scope/source/installPath。source 按引擎取值：claudecode/codex=市场名、opencode=模块 spec、pi=包源字符串（npm:/git:/本地路径）、omp=类别（npm/marketplace）。

示例：

```
codebox-agent plugin list --app claudecode
```
```
codebox-agent plugin list --json
```

---

### plugin.uninstall

卸载插件

```
codebox-agent plugin uninstall <app> <id>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `app` | 位置 | `string` | 是 | 插件引擎：claudecode\|codex\|opencode\|pi\|omp；可选值：claudecode / codex / opencode / pi / omp |
| `id` | 位置 | `string` | 是 | 卸载标识：claudecode/codex=plugin@marketplace；opencode=模块 spec；pi=包源字符串；omp=插件名 |

风险：`destructive`（删除/清空，不可逆）；确认值 `UNINSTALL`——执行需 `--yes`。

超时 600 秒。

卸载不可中途取消：超时不等于未卸载，先用 plugin list 查询结果再决定是否重试。

示例：

```
codebox-agent plugin uninstall claudecode myplugin@mymarket --yes
```

---

## Git

### git.branches

列出目录的本地分支（标注当前分支）

```
codebox-agent git branches <dir>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `dir` | 位置 | `string` | 是 | 工作目录绝对路径 |

风险：`read`（只读）。

非 git 仓库返回 not_found。只读查询可安全重试；Handler 入口已检查取消（底层 gitassist 暂无 ctx 传递接口）。

示例：

```
codebox-agent git branches /Users/me/work/proj
```

---

### git.repo-info

查看目录的 git 仓库状态快照（分支/上游/变更计数）

```
codebox-agent git repo-info <dir>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `dir` | 位置 | `string` | 是 | 工作目录绝对路径 |

风险：`read`（只读）。

非 git 仓库不报错，返回 isGitRepo=false；提交/推送不在 CLI 面内。只读查询可安全重试。

示例：

```
codebox-agent git repo-info /Users/me/work/proj
```

---

<!-- END generated-command-reference -->
