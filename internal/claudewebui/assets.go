package claudewebui

// assets.go — SPA 静态面（design §5.2 server、§6.1、§7.3/R6）。
//
// `//go:embed all:dist` 嵌入 webui-claude 构建产物（vite outDir 指向本目录，
// base:'./' 相对化以同时服务桌面回环根路径与远程子路径两种形态，§7.2）。
// dist 仅 .gitkeep 占位（fresh checkout / 未跑前端构建）时返回提示页并
// log WARN 一次——探测与数据面不受影响（可观测的降级而非失败，R11）。
//
// 静态面无鉴权、ACAO:*（跨源模块脚本 CORS-mode 加载需要；静态面不含会话
// 数据，design §6.1/反代静态豁免同口径）。

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"amagi-codebox/internal/logging"
)

//go:embed all:dist
var distFS embed.FS

// distRoot 返回 dist 子树（sync.OnceValue 缓存；embed 子树在进程内不变）。
var distRoot = sync.OnceValue(func() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("claudewebui: embed dist 子树缺失: " + err.Error())
	}
	return sub
})

// distHasIndex 报告 dist 是否含真实 SPA 产物（index.html）。仅 .gitkeep 时
// 为 false → 提示页降级。
var distHasIndex = sync.OnceValue(func() bool {
	_, err := distRoot().Open("index.html")
	return err == nil
})

// assetsHost 是 per-plane 的静态面服务句柄（WARN 一次的载体）。
type assetsHost struct {
	log      *logging.Service
	warnOnce sync.Once
}

func newAssetsHost(log *logging.Service) *assetsHost {
	return &assetsHost{log: log}
}

func (a *assetsHost) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if !distHasIndex() {
		a.warnOnce.Do(func() {
			if a.log != nil {
				a.log.Warn("claudewebui", "SPA 产物缺失（internal/claudewebui/dist 仅占位），静态面降级提示页",
					"build=npm run build:claude-webui")
			}
		})
		servePlaceholder(w, r)
		return
	}

	// path.Clean 归一（embed FS 自身拒绝 ..，双保险）；hash 路由主用 /，
	// 未知路径回退 index.html（SPA 兜底，无害）。
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	data, err := fs.ReadFile(distRoot(), name)
	if err != nil {
		if data, err = fs.ReadFile(distRoot(), "index.html"); err != nil {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
	}
	ctype := mime.TypeByExtension(filepath.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(data)
}

// placeholderHTML 是产物缺失时的降级提示页（body 透明，对齐 SPA 内嵌约定
// §7.2；不暴露任何会话信息）。
const placeholderHTML = `<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Amagi CodeBox — Web 平面未构建</title>
<style>
body { margin: 2rem; font-family: system-ui, sans-serif; background: transparent; color: inherit; line-height: 1.6; }
code { padding: .15rem .4rem; border-radius: .25rem; background: rgba(127,127,127,.18); }
</style>
</head>
<body>
<h2>Web 平面资源未构建</h2>
<p>会话数据面运行正常，但 <code>internal/claudewebui/dist</code> 仅含占位文件——Web 平面 SPA 产物缺失。</p>
<p>请先运行 <code>npm run build:claude-webui</code>（或经 <code>wails build</code> 的聚合构建钩子）生成产物后重试。</p>
</body>
</html>
`

func servePlaceholder(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write([]byte(placeholderHTML))
}
