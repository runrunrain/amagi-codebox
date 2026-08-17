package skins

// asset.go — 皮肤图片的只读静态资源 Handler。
//
// 挂载到 Wails assetserver（main.go assetserver.Options.Handler）：GET
// /skins/<file> 时从皮肤目录读文件返回；只允许纯文件名（无路径分隔符），
// 防穿越双保险（分隔符拒绝 + filepath 清洗后前缀复核）；无目录列表；
// 非法路径/扩展名/不存在一律 404，非 GET 返回 405。

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// NewAssetHandler 返回从 dir 只读服务 /skins/<file> 的 http.Handler。
// 并发安全：每次请求独立打开文件句柄，无共享可变状态。
func NewAssetHandler(dir string) http.Handler {
	cleanDir := filepath.Clean(dir)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rel := strings.TrimPrefix(r.URL.Path, URLPrefix)
		// 只允许纯文件名：拒绝任何路径分隔符与目录语义（含 URL 解码后的 ../）。
		if rel == "" || strings.ContainsRune(rel, '/') || strings.ContainsRune(rel, '\\') || strings.ContainsRune(rel, 0) {
			http.NotFound(w, r)
			return
		}
		name := filepath.Clean(rel)
		if !isAllowedExt(name) {
			http.NotFound(w, r)
			return
		}
		full := filepath.Join(cleanDir, name)
		// 双保险：清洗后的路径必须仍位于皮肤目录内。
		if !strings.HasPrefix(full, cleanDir+string(filepath.Separator)) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(full)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentTypeFor(name))
		http.ServeContent(w, r, name, st.ModTime(), f)
	})
}

// isAllowedExt 判断文件名扩展是否为皮肤合法格式。
func isAllowedExt(name string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "png", "jpg", "jpeg", "webp":
		return true
	}
	return false
}

// contentTypeFor 按扩展名映射 Content-Type。
func contentTypeFor(name string) string {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	}
	return "application/octet-stream"
}
