# codebox-agent 命令参考

由命令表自动生成；以 `codebox-agent --help` 与各命令的 `--help` 为准。

风险级：`read` 只读 · `mutate` 修改配置/状态（可逆） · `exec` 启动进程/写入会话/安装软件 · `destructive` 删除/清空（不可逆）。`exec` 与 `destructive` 命令需 `--yes` 注入确认值。

## 通用

### logs

查询应用日志。

```
codebox-agent logs [--level <level>] [--limit <limit>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--level` | 旗标 | `string` | 否 | 级别过滤；可选值：debug / info / warn / error |
| `--limit` | 旗标 | `int` | 否 | 条数上限；默认 100 |

风险：`read`（只读）。

---

### status

应用总览：版本、平台能力、会话计数与环境摘要。

```
codebox-agent status
```

风险：`read`（只读）。

示例：

```
codebox-agent status --json
```

---

## 系统与状态

### env.check

实时探测环境（全部或指定工具）。

```
codebox-agent env check [tool]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `tool` | 位置 | `string` | 否 | 只探测该工具；缺省全部；可选值：claudecode / opencode / codex / pi / omp |

风险：`read`（只读）。

示例：

```
codebox-agent env check claudecode
```

---

## 配置

### provider.get

查看单个提供商（输出脱敏）。

```
codebox-agent provider get <name> [--type <type>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `name` | 位置 | `string` | 是 | 提供商名称 |
| `--type` | 旗标 | `string` | 否 | 按应用类型消歧；可选值：claudecode / opencode / codex / pi / omp |

风险：`read`（只读）。

---

### provider.key-set

写入提供商 API Key（经 stdin，不回显）。

```
codebox-agent provider key-set <name> --key <key>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `name` | 位置 | `string` | 是 | 提供商名称 |
| `--key` | 旗标 | `string` | 是 | 密钥值；传 - 从 stdin 读取；支持 `-` 从 stdin 读取；敏感值，日志与回显脱敏 |

风险：`mutate`（修改配置/状态，可逆）。

密钥不会出现在 argv、日志与回显中。

示例：

```
printf '%s' "$KEY" | codebox-agent provider key-set myprov -
```

---

### provider.save

保存提供商（完整 JSON）。

```
codebox-agent provider save --json <json>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--json` | 旗标 | `json` | 是 | 提供商 JSON（同桌面端导入格式） |

风险：`mutate`（修改配置/状态，可逆）。

JSON 含 key 字段时会被拒绝，改用 provider key-set。

---

## 会话

### session.launch

启动 AI-CLI 会话，返回 sessionId。

```
codebox-agent session launch --app-type <appType> --provider <provider> [--model <model>] [--work-dir <workDir>] [--mode <mode>] [--headroom]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--app-type` | 旗标 | `string` | 是 | 目标 CLI；可选值：claudecode / opencode / codex / pi / omp |
| `--provider` | 旗标 | `string` | 是 | 提供商名称 |
| `--model` | 旗标 | `string` | 否 | 模型覆盖 |
| `--work-dir` | 旗标 | `string` | 否 | 工作目录 |
| `--mode` | 旗标 | `string` | 否 | 会话模式；默认 embedded |
| `--headroom` | 旗标 | `bool` | 否 | 启用 Headroom；默认 false |

风险：`exec`（启动进程/写入会话/安装软件）；确认值 `LAUNCH`——执行需 `--yes`。

超时 300 秒。

示例：

```
codebox-agent session launch --app-type claudecode --provider myprov --yes
```

---

### session.output

读取会话输出（默认剥离 ANSI）。

```
codebox-agent session output <id> [--tail-bytes <tailBytes>] [--since-seq <sinceSeq>] [--raw]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `id` | 位置 | `string` | 是 | 会话 ID |
| `--tail-bytes` | 旗标 | `int` | 否 | 只读末尾 N 字节；默认 4096 |
| `--since-seq` | 旗标 | `int` | 否 | 从该序号起增量读取 |
| `--raw` | 旗标 | `bool` | 否 | 保留 ANSI 转义 |

风险：`read`（只读）。

输出可能含敏感信息，注意脱敏。

---

### session.send

向运行中的会话发送输入。

```
codebox-agent session send <id> <text> [--enter]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `id` | 位置 | `string` | 是 | 会话 ID |
| `text` | 位置 | `string[]` | 是 | 输入文本（多段合并） |
| `--enter` | 旗标 | `bool` | 否 | 追加回车 |

风险：`exec`（启动进程/写入会话/安装软件）；确认值 `SEND`——执行需 `--yes`。

---

### session.stop

停止会话。

```
codebox-agent session stop <id>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `id` | 位置 | `string` | 是 | 会话 ID |

风险：`destructive`（删除/清空，不可逆）；确认值 `STOP`——执行需 `--yes`。

---

## 用量与日志

### usage.daily

按日汇总用量。

```
codebox-agent usage daily [--days <days>]
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `--days` | 旗标 | `int` | 否 | 统计天数；默认 7 |

风险：`read`（只读）。

---

## 插件

### plugin.install

安装插件。

```
codebox-agent plugin install <app> <name>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `app` | 位置 | `string` | 是 | 插件引擎；可选值：claudecode / codex / opencode / pi / omp |
| `name` | 位置 | `string` | 是 | 插件名 |

风险：`exec`（启动进程/写入会话/安装软件）；确认值 `INSTALL`——执行需 `--yes`。

---

## Git

### git.repo-info

查看仓库状态摘要。

```
codebox-agent git repo-info <dir>
```

| 参数 | 形态 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| `dir` | 位置 | `string` | 是 | 仓库路径 |

风险：`read`（只读）。

---

