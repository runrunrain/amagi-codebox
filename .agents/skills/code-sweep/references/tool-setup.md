# 工具安装与降级（Windows + Git Bash）

原则：两分钟装不上就走降级路线，清扫不因装工具卡死。常驻安装只做一次，之后复用。

## golangci-lint（版本必须与 .golangci.yml 的 v2.12.2 对齐）

免安装一次性运行（首次约 1–2 分钟编译，零污染，推荐先试这个）：

```bash
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...
```

常驻安装（三选一）：

```bash
scoop install golangci-lint                          # 有 scoop 时
winget install GolangCI.golangci-lint                # 有 winget 时
# 或从 https://github.com/golangci/golangci-lint/releases 下载 windows-amd64.zip，
# 解压后把 exe 放进 PATH（注意与 Go 自带的 golangci-lint shim 区分）
```

装完校验 `golangci-lint version` 输出 2.12.2。版本不一致时结果与 CI 不可比——仍可跑，但汇报里必须注明实际版本。

## govulncheck

```bash
# 免安装一次性
go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# 常驻（GOBIN 在 PATH 时）
go install golang.org/x/vuln/cmd/govulncheck@latest
```

仓库有 `vendor/`，govulncheck 会照常分析依赖图；它只报告**实际被调用路径**可达的漏洞（call-stack 可达），未命中调用路径的漏洞只出现在尾部 informational 部分，不属于 P0。

## knip（npx 即用，无需安装）

在**各子项目目录内**（frontend / webui-claude / mobile）执行 `npx knip`，首次会提示下载包，确认即可。

Wails 项目误报重灾区，一律忽略：

- `wailsjs/` —— Wails 自动生成的绑定代码，生成器不遵守 knip 的入口假设
- `dist/`、`node_modules/`
- 通过字符串/反射引用的模块（knip 看不见动态引用）

## 降级组合（工具实在装不上时）

| 原 scan | 降级 |
|---|---|
| golangci-lint | `go vet ./...`（govet 一层；汇报注明只覆盖了 govet） |
| govulncheck | 跳过，汇报注明该线缺失 |
| knip | 各子项目内 `npx tsc --noEmit` + `npx eslint .`；死代码粗查可用 grep 导出符号引用，仅建议级 |

降级跑出来的结果同样走第 3 步归并分级，只是覆盖面变窄，汇报时说清楚。
