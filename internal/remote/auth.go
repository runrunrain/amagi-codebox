package remote

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
)

// Auth 管理远程 API 的 Bearer Token 认证
type Auth struct {
	token string
	mu    sync.RWMutex
}

func newAuth() *Auth {
	a := &Auth{}
	a.regenerate()
	return a
}

// regenerate 生成新的随机 token（32字节 hex = 64字符）
func (a *Auth) regenerate() {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// 极端情况：rand 失败，使用固定占位符（不影响启动，但安全性降低）
		a.token = "insecure-fallback-token-rand-failed"
		return
	}
	a.mu.Lock()
	a.token = hex.EncodeToString(buf)
	a.mu.Unlock()
}

// GetToken 返回当前 token
func (a *Auth) GetToken() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.token
}

// RegenerateToken 生成新的随机 token 并返回新值
func (a *Auth) RegenerateToken() string {
	a.regenerate()
	return a.GetToken()
}

// validate 校验 Authorization header 或 URL 参数中的 token
func (a *Auth) validate(r *http.Request) bool {
	a.mu.RLock()
	expected := a.token
	a.mu.RUnlock()

	// 检查 Authorization: Bearer <token>
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") && parts[1] == expected {
			return true
		}
	}

	// 检查 URL 参数 ?token=xxx（WebSocket 使用）
	if t := r.URL.Query().Get("token"); t == expected {
		return true
	}

	return false
}

// Middleware 返回验证 Bearer Token 的 HTTP 中间件
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.validate(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
