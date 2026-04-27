package remote

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/logging"
)

// Server 远程 API HTTP 服务器，允许移动端通过 HTTP/WebSocket 操作 Amagi CodeBox 的全部功能。
type Server struct {
	host              string
	port              int
	auth              *Auth
	app               AppInterface
	log               *logging.Service
	httpSrv           *http.Server
	cancel            context.CancelFunc
	running           bool
	mu                sync.RWMutex
	webRoot           string   // 移动端 Web 前端的 dist 目录路径，为空则不提供静态文件服务
	mobileAssets      embed.FS // 构建时嵌入的移动端 Web 资源（mobile/dist）
	mobileAssetsPrefix string  // mobileAssets 中的路径前缀，默认 "mobile/dist"
}

// NewServer 创建远程服务器实例，不启动监听。
// mobileAssets 为构建时嵌入的移动端 Web 资源（mobile/dist），可为空 embed.FS。
func NewServer(port int, app AppInterface, log *logging.Service, mobileAssets embed.FS) *Server {
	return &Server{
		port:               port,
		auth:               newAuth(),
		app:                app,
		log:                log,
		mobileAssets:       mobileAssets,
		mobileAssetsPrefix: "mobile/dist",
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
		launchHost := s.desktopLaunchHost()
		s.log.Info("remote", "远程服务器启动", fmt.Sprintf("listen_host=%s port=%d desktop_host=%s", s.GetHost(), s.GetPort(), launchHost))
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

// SetMobileAssetsPrefix sets the path prefix within the embedded FS where mobile
// assets are located. Defaults to "mobile/dist". Exported for test use.
func (s *Server) SetMobileAssetsPrefix(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mobileAssetsPrefix = prefix
}

// GetMobileWebRootStatus 返回当前生效的移动端静态资源目录状态。
func (s *Server) GetMobileWebRootStatus() (root string, configured bool, exists bool) {
	root = s.getEffectiveWebRoot()
	if root == "" {
		return "", false, false
	}

	indexPath := filepath.Join(root, "index.html")
	if info, err := os.Stat(indexPath); err == nil && !info.IsDir() {
		return root, true, true
	}

	return root, true, false
}

// HasEmbeddedMobileWeb 报告是否包含内置的移动端 Web 资源。
func (s *Server) HasEmbeddedMobileWeb() bool {
	indexPath := s.mobileAssetsPrefix + "/index.html"
	f, err := s.mobileAssets.Open(indexPath)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// BuildDesktopLaunchURL 返回桌面入口 Web UI 地址。
// 本地通配/回环地址统一映射到 127.0.0.1，其余具体 host 保留原值。
func (s *Server) BuildDesktopLaunchURL() string {
	host := s.desktopLaunchHost()
	query := url.Values{}
	query.Set("autoconnect", "1")
	query.Set("launch", s.auth.IssueLaunchGrant(host))
	return fmt.Sprintf("http://%s/?%s", net.JoinHostPort(host, strconv.Itoa(s.GetPort())), query.Encode())
}

func (s *Server) desktopLaunchHost() string {
	return desktopLaunchHostForListenHost(s.GetHost())
}

func desktopLaunchHostForListenHost(host string) string {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return "127.0.0.1"
	}

	canonical := strings.TrimPrefix(strings.TrimSuffix(trimmed, "]"), "[")
	if strings.EqualFold(canonical, "localhost") {
		return "127.0.0.1"
	}

	if ip := net.ParseIP(canonical); ip != nil {
		if ip.IsLoopback() || ip.IsUnspecified() {
			return "127.0.0.1"
		}
		return canonical
	}

	return trimmed
}

func (s *Server) getEffectiveWebRoot() string {
	if s.app != nil && s.app.GetSettingsService() != nil {
		if root := strings.TrimSpace(s.app.GetSettingsService().GetMobileWebRoot()); root != "" {
			return root
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.webRoot)
}

// buildHandler 构建最终的 HTTP handler。
// 始终使用复合 handler：/api/ 和 /ws/ 走认证 + API，其余动态检查 webRoot 后决定走静态文件还是回退到 API。
// 静态资源优先级：用户配置的 MobileWebRoot（index.html 存在）> 内置 embedded mobile dist > 回退 API handler。
// webRoot 可以在服务器运行期间随时设置或更新，无需重启。
func (s *Server) buildHandler() http.Handler {
	protectedMux := http.NewServeMux()
	s.registerRoutes(protectedMux)

	bootstrapMux := http.NewServeMux()
	bootstrapMux.HandleFunc("POST /api/bootstrap/consume", s.handleConsumeLaunchGrant)

	apiHandler := corsMiddleware(s.auth.Middleware(protectedMux))
	bootstrapHandler := corsMiddleware(bootstrapMux)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/bootstrap/consume" {
			bootstrapHandler.ServeHTTP(w, r)
			return
		}

		// API 和 WebSocket 请求始终走认证 handler
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
			apiHandler.ServeHTTP(w, r)
			return
		}

		// 静态文件请求：从 Settings 动态读取 webRoot（保存设置后无需重启即可生效）
		webRoot, configured, exists := s.GetMobileWebRootStatus()

		// 优先级 1：用户配置的 MobileWebRoot 且 index.html 存在
		if configured && exists {
			fileSystem := http.Dir(webRoot)
			fileServer := http.FileServer(fileSystem)
			s.serveStaticOrSPA(w, r, fileServer, func(p string) (fs.FileInfo, error) {
				return fs.Stat(os.DirFS(webRoot), p)
			})
			return
		}

		// 优先级 2：内置 embedded mobile dist
		if s.HasEmbeddedMobileWeb() {
			subFS, err := fs.Sub(s.mobileAssets, s.mobileAssetsPrefix)
			if err == nil {
				fileServer := http.FileServer(http.FS(subFS))
				s.serveStaticOrSPA(w, r, fileServer, func(p string) (fs.FileInfo, error) {
					return fs.Stat(subFS, p)
				})
				return
			}
		}

		// 优先级 3：都不可用 -> 回退 API handler（需要认证）
		apiHandler.ServeHTTP(w, r)
	})
}

// serveStaticOrSPA 提供静态文件服务，对未知路径执行 SPA fallback（返回 index.html）。
func (s *Server) serveStaticOrSPA(w http.ResponseWriter, r *http.Request, fileServer http.Handler, statFunc func(string) (fs.FileInfo, error)) {
	path := r.URL.Path
	if path == "/" {
		fileServer.ServeHTTP(w, r)
		return
	}

	// 检查文件是否存在
	cleanPath := strings.TrimPrefix(path, "/")
	f, err := statFunc(cleanPath)
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

// corsMiddleware 仅为同源浏览器请求回显 CORS 头，拒绝跨源页面借宿主浏览器访问本地 API。
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if !isAllowedCORSOrigin(r, origin) {
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusForbidden)
					return
				}
			} else {
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			}
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
