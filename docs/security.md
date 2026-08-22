# 安全策略（Security Policy）

本文档描述 Amagi CodeBox 的安全边界、密钥存储机制、远程控制认证模型与本地敏感数据的存放方式。内容基于 `internal/secrets`、`internal/remote`、`internal/launcher`、`internal/config`（vision 导出）与 `internal/usage` 源码核实；无法静态确认的行为标注「待核实」。

相关文档：
- 远程控制 API 的端点与协议见 `user/remote-mobile.md` 与 `developer/remote-api-v1-contract.md`。
- 视觉模型导出契约见 `vision-export-contract.md`。

## 漏洞报告

如果你发现 Amagi CodeBox 的安全漏洞，请负责任地披露：

1. **不要公开开 issue。**
2. 发送邮件至 **maorun@live.cn**，包含：漏洞描述、复现步骤、潜在影响、修复建议（如有）。
3. 你会在 48 小时内收到确认回复。
4. 修复会以 patch 版本优先发布。

### 支持版本

| Version | Supported |
|---------|-----------|
| 1.3.x   | Yes       |
| < 1.3   | No        |

## 密钥存储（API Key）

API key 不写入 `models.json`（该文件只保存 provider/preset 配置）；key 由 `internal/secrets` 统一管理，落点为 `~/.amagi-codebox/secrets.enc`（`internal/secrets/service.go`）。底层存储按平台经 build tag 分流，共三态：

| 平台 | 实现文件 | 机制 | 失败语义 |
|------|----------|------|----------|
| Windows | `internal/secrets/store_windows.go` | DPAPI（`billgraziano/dpapi`）加密 `secrets.enc` 内容，解密结果即明文键值对 | 解密失败返回错误 |
| macOS（cgo） | `internal/secrets/store_darwin_cgo.go` | Security framework Keychain generic password（`SecKeychainFindGenericPassword` / `SecKeychainAddGenericPassword`） | Keychain 授权弹窗期间不持锁（避免 UI 卡死） |
| macOS（nocgo） | `internal/secrets/store_darwin_nocgo.go` | 无 Keychain 绑定，`Load`/`Save` 均返回 `ErrSecretStoreNotReady` | **密钥不可用，不静默落明文** |
| Linux / 其他 | `internal/secrets/store_other.go` | 不支持：`Load` 返回空 map，`Save` 静默丢弃 | **无 keychain 即不存密钥，故意无明文兜底** |

要点：
- **无明文兜底是有意设计**：在缺少平台保护的系统上，密钥宁可不持久化，也不退化为明文文件。
- `secrets.enc` 的 "enc" 指 Windows 下 DPAPI 加密；macOS cgo 模式下条目实际存于系统 Keychain。不要把该文件当作可移植的密文跨机器复制（DPAPI 密文绑定机器/用户）。
- 服务层（`SecretsService`）持有内存缓存，读写经 `sync.RWMutex`；Keychain I/O 在锁外执行（注释明确说明原因：OS 凭据弹窗可能阻塞）。

## 本地敏感数据清单

`~/.amagi-codebox/` 下与安全相关的文件：

| 文件/目录 | 内容 | 敏感性 |
|-----------|------|--------|
| `secrets.enc` | API key（DPAPI 加密或 Keychain 投影） | **高**：泄露等价于泄露全部 key |
| `amagi-media-models.json`（实际在 `~/.agents/`） | 视觉模型导出，**含明文 API key** | **高**：文件 0600，目录 0755，tmp+rename 原子写（`internal/config/vision.go`）。仅当用户给 preset 打 vision/video 标记时生成 |
| `models.json` | provider/preset 配置（base URL、模型名、参数） | 中：不含 key，但暴露服务端点 |
| `devices.json` + `devices.revocations.wal` | 远程控制已配对设备快照与撤销台账（`internal/remote/device_store.go`） | 中高：含设备标识与能力令牌元数据 |
| `usage.db` | SQLite 用量统计（会话 token/成本，`internal/usage/service.go`） | 中：含使用行为轨迹 |
| `envvars.json` | 用户自定义环境变量 | **可能高**：若用户把 secret 写进环境变量则明文落盘 |
| `logs/` | 应用日志 | 中：可能包含 provider 名、路径等元数据 |
| `settings.json` / `paths.json` / `agent-profiles.json` / `workspaces.json` / `usage-pricing.json` / `injection-rules.json` / `skins/` | 常规配置 | 低 |

### Agent 根目录权限范式

写入 AI CLI 的 agent 根（其中含注入的 API key）遵循统一范式：
- 目录 `0700`、文件 `0600`，临时文件 + rename 原子写入。
- 实现落点：`internal/launcher/pi_config.go`（`~/.pi/agent/models.json`，注释明确「agentDir 以 0700 创建，models.json 以 0600 写入」）、`internal/launcher/omp_config.go` + `internal/ompconfig/service.go`（`writePrivateFile` 0600，`~/.omp/agent/models.yml`）。
- 视觉导出文件同样 tmp+rename 原子写、0600（见上表）。

## 远程控制认证与设备管理

远程控制服务（`internal/remote`）开启后暴露 HTTP + WebSocket API，安全模型如下（`internal/remote/auth.go`、`device.go`、`device_store.go`、`revocation_ledger.go`）：

- **Bearer Token 认证**：HTTP 请求必须携带 `Authorization: Bearer <token>`（严格 scheme 解析，`parseBearer`）。主 token 为 32 字节随机值（`crypto/rand`），轮转时旧值清零。
- **WebSocket token 载体受限**：WS 握手经 query 携带 token 仅在 `isAllowedLegacyTokenCarrier` 为真时接受（legacy carrier 冻结），新接入一律走 header。
- **短期授权票据**：
  - `launchGrant`：发射授权，TTL 2 分钟，上限 64 个，满则签发失败（绝不 drop-oldest）。
  - `localSession`（cookie `amagi_codebox_local_session`）：本机会话，TTL 12 小时，上限 64。
- **设备配对**：配对面（`PairingPolicy`，`device.go`）使用大写 RFC4648 Base32 无 padding 配对码；配对窗口信息只在桌面端创建时返回一次。
- **设备凭证存储**：`devices.json` 是设备/凭证的**快照投影**（snapshot projection）；撤销的唯一事实源是 append-only 台账 `devices.revocations.wal`（`revocation_ledger.go`，domain 分隔 `amagi-codebox/revocation-ledger/v1`，prepare+commit 两行式追加，启动时校验序列与尾部分类）。撤销设备即向 WAL 追加 tombstone，重启后由台账重建快照，另有 `device-store-backups/` 备份目录。
- **环回地址门控**：敏感端点（如 `POST /api/sessions/launch-omp`）经 `requireLoopbackPeer` 限定对端为 loopback。

运营建议：
- 远程控制默认应仅在可信网络开启；token 定期轮转（轮转接口见 `user/remote-mobile.md`）。
- 丢失移动设备时立即在桌面端撤销配对设备（写 WAL tombstone），不要只改 token——设备能力令牌独立存在。
- 不要把 `~/.amagi-codebox/` 整个目录纳入云同步或备份到外网位置。

## 供应链与构建安全

- Go 依赖全部 vendored 并提交（`vendor/`，构建用 `-mod=vendor`）；新增依赖须 `go get` 后 `go mod vendor` 一并提交审查。
- CI/release 工具链精确 pin：Go `1.25.0`、Node `20.19.0`、golangci-lint `v2.12.2`（`.github/workflows/ci.yml`、`release.yml`）。
- 发布产物目前**未代码签名、未公证**（`release.yml` 中占位步骤禁用），macOS 用户需手动放行 Gatekeeper——这是当前最大的分发信任缺口，启用时间表待核实。详见 `ops/release.md`。

## 待核实项

- macOS nocgo 构建下用户感知到的「密钥不可用」提示路径（GUI 行为，静态不可确认）。
- DPAPI 加密范围是否绑定当前用户（`billgraziano/dpapi` 默认行为，未在本仓库显式配置）。
- 日志中是否可能记录完整 base URL 之外的敏感头信息（需运行时审计，未逐条核实）。
