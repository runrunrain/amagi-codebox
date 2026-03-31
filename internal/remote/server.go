package remote

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/logging"
)

// Server 远程 API HTTP 服务器，允许移动端通过 HTTP/WebSocket 操作 Amagi CodeBox 的全部功能。
type Server struct {
	host    string
	port    int
	auth    *Auth
	app     AppInterface
	log     *logging.Service
	httpSrv *http.Server
	cancel  context.CancelFunc
	running bool
	mu      sync.RWMutex
	webRoot string // 移动端 Web 前端的 dist 目录路径，为空则不提供静态文件服务
}

// NewServer 创建远程服务器实例，不启动监听。
func NewServer(port int, app AppInterface, log *logging.Service) *Server {
	return &Server{
		port: port,
		auth: newAuth(),
		app:  app,
		log:  log,
	}
}

// Start 在后台 goroutine 中启动 HTTP 服务器。
func (s *Server) Start(parentCtx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(parentCtx)
	s.cancel = cancel

	handler := s.buildHandler()

	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	s.httpSrv = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		cancel()
		return fmt.Errorf("remote server listen %s: %w", addr, err)
	}

	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	go func() {
		s.log.Info("remote", "远程服务器启动", fmt.Sprintf("port=%d token=%s", s.port, s.auth.GetToken()))
		if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.log.Error("remote", "远程服务器异常退出", err.Error())
		}
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		cancel()
	}()

	// 监控父 context 取消
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		s.httpSrv.Shutdown(shutdownCtx)
	}()

	return nil
}

// Stop 优雅关闭服务器。
func (s *Server) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

// IsRunning 返回服务器是否正在运行。
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// SetPort 设置监听端口（仅在服务器停止时有效）。
func (s *Server) SetPort(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.port = port
}

// SetHost 设置监听地址（仅在服务器停止时有效）。
func (s *Server) SetHost(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.host = host
}

// GetHost 返回监听地址。
func (s *Server) GetHost() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.host
}

// SetWebRoot 设置移动端 Web 前端的 dist 目录路径。
// 设置后远程服务器将在同一端口同时提供 API 和静态页面服务。
func (s *Server) SetWebRoot(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webRoot = path
}

// buildHandler 构建最终的 HTTP handler。
// 始终使用复合 handler：/api/ 和 /ws/ 走认证 + API，其余动态检查 webRoot 后决定走静态文件还是回退到 API。
// 这样 webRoot 可以在服务器运行期间随时设置或更新，无需重启。
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	apiHandler := corsMiddleware(s.auth.Middleware(mux))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API 和 WebSocket 请求始终走认证 handler
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
			apiHandler.ServeHTTP(w, r)
			return
		}

		// 静态文件请求：从 Settings 动态读取 webRoot（保存设置后无需重启即可生效）
		webRoot := s.app.GetSettingsService().GetMobileWebRoot()

		if webRoot == "" {
			// 未配置 webRoot，回退到 API handler（需要认证）
			apiHandler.ServeHTTP(w, r)
			return
		}

		// 检查 index.html 是否存在
		if _, err := os.Stat(webRoot + "/index.html"); err != nil {
			// webRoot 已配置但 index.html 不存在，回退到 API handler
			apiHandler.ServeHTTP(w, r)
			return
		}

		// 提供静态文件（不需要认证）
		w.Header().Set("Access-Control-Allow-Origin", "*")

		fileSystem := http.Dir(webRoot)
		fileServer := http.FileServer(fileSystem)

		path := r.URL.Path
		if path == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}

		// 检查文件是否存在
		f, err := fs.Stat(os.DirFS(webRoot), strings.TrimPrefix(path, "/"))
		if err == nil && !f.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback：非文件路径返回 index.html
		r2 := new(http.Request)
		*r2 = *r
		r2.URL = new(url.URL)
		*r2.URL = *r.URL
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}

// RegenerateToken 重新生成 Token 并返回新值。
func (s *Server) RegenerateToken() string {
	return s.auth.RegenerateToken()
}

// GetToken 返回认证 token（供前端 Wails 展示）。
func (s *Server) GetToken() string {
	return s.auth.GetToken()
}

// GetPort 返回监听端口。
func (s *Server) GetPort() int {
	return s.port
}

// corsMiddleware 添加 CORS 头，允许所有来源（移动端 WebView 需要）。
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
