# Amagi CodeBox 文档

Amagi CodeBox 是管理 Claude Code / OpenCode / Codex 多服务提供商配置的跨平台桌面应用（Wails v2：Go 后端 + Vue 3 前端，编译为单二进制）。

本目录是项目文档总入口，按受众分三层组织。项目概览与快速上手见仓库根目录的 [README](../README.md)；面向 AI 助手的项目导览见根目录的 [CLAUDE.md](../CLAUDE.md)。

## 目录结构

```
docs/
├── README.md          # 本文件，文档总索引
├── api.md             # Wails 绑定的完整后端 API 参考
├── security.md        # 数据加密与传输安全策略
├── user/              # 终端用户文档
├── developer/         # 贡献者与开发文档
└── ops/               # 打包发布与运维文档
```

## 参考

- [API 参考](api.md) — Wails 绑定的全部公开方法，按服务分组（迁移自根目录原 `API.md`）
- [安全策略](security.md) — API 密钥加密存储（Windows DPAPI / macOS Keychain）与远程控制传输安全

## 终端用户

面向使用 Amagi CodeBox 管理配置、启动会话、使用终端与远程控制的用户。

- [下载安装](user/installation.md) — 环境要求、下载渠道、单实例保护、系统托盘、首次运行
- [界面功能总览](user/usage.md) — 各功能页（会话设置、终端、提供商中心、扩展、规则、环境检测、日志）与启动会话流程
- [提供商与预设配置](user/providers.md) — Provider / Preset / Parameters 概念、三种应用类型、`config.json` 结构
- [内嵌终端](user/terminal.md) — xterm.js + ConPTY/PTY、多 Tab、终端预设与回调机制
- [插件系统](user/plugins.md) — Claude Code 与 Codex 插件、工作空间部署与冲突检测
- [远程控制与移动端](user/remote-mobile.md) — HTTP/WebSocket 远程 API、Token 认证、移动端连接
- [常见问题](user/faq.md) — 环境检测、安装、单实例、托盘、配置目录

## 开发者 / 贡献者

面向阅读与修改本项目代码的贡献者。

- [整体架构](developer/architecture.md) — Wails 绑定主干、`app.go` 枢纽、服务包组织、会话生命周期
- [前后端桥接](developer/frontend-backend.md) — Wails 自动生成的 TS 绑定、`api/*.ts` 包装、Pinia store 与完整调用链
- [跨平台 build tags](developer/platform-build-tags.md) — `//go:build` 文件分流机制、各平台差异（secrets / pty / platform）
- [构建与本地开发](developer/build-dev.md) — 依赖、`wails dev`/`build`、前端与移动端构建、版本注入
- [测试约定](developer/testing.md) — Go 测试、前端类型门、移动端 vitest、CI 覆盖说明
- [后端 API 参考（开发者视角）](developer/api-reference.md) — 绑定生成机制、如何新增 API、前端调用范式

## 打包发布 / 运维

面向打包发布与持续集成的维护者。

- [打包发布](ops/release.md) — 本地构建产物、GitHub Releases、发布流程
- [版本号管理](ops/versioning.md) — 版本变量、ldflags 注入、版本来源链、版本同步点
- [CI/CD 流程](ops/ci-cd.md) — CI 与 Release workflow 触发条件与执行步骤

## 约定

- 文档与代码、注释同样以中文为主，技术术语、命令、路径保留英文。
- 交叉引用统一使用相对路径。
- 文档中标注"待核实"的项为无法仅凭源码静态确认的内容（多为 GUI 运行时行为或发行资产），以避免编造。
