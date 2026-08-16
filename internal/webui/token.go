package webui

// token.go — 契约 v1.0.2 每会话 capability token（R1 Critical 对齐消费）。
//
// token 语义（契约 v1.0.2）：
//   - codebox 在启动 embedded pi 前生成 ≥128bit 随机 token，经 env
//     `AMAGI_WEBUI_TOKEN` 注入扩展；扩展对 /api/* 与 WS 握手强制校验。
//   - codebox 探测 /api/info 时携带 `Authorization: Bearer <token>`；
//     纯注册表发现（token 未注入/注入失败）时从注册表条目的 token 字段补读。
//   - iframe src 经 fragment `#/t=<token>` 把 token 传给 sandbox 页面
//    （fragment 不入 HTTP 日志/Referer）。

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// tokenBytes 是 capability token 的熵字节数（16 字节 = 128bit，hex 编码后
// 32 字符）。契约 v1.0.2 要求 ≥128bit。
const tokenBytes = 16

// GenerateToken 生成一个 128bit 随机 capability token（hex 编码，32 字符）。
func GenerateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate webui token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
