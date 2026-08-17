package skins

// skins_test.go — 皮肤服务测试（plan 后端切片 A §4）。
//
// 对话框无法在测试中伪造（需要真实 Wails ctx），因此导入路径统一测
// ImportSkinImage；PickSkinImage 仅测 ctx 未注入时的明确错误。

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/settings"
)

func newTestService(t *testing.T) (*Service, string, string) {
	t.Helper()
	dir := t.TempDir()
	settingsDir := t.TempDir()
	settingsSvc := settings.NewService(settingsDir)
	if err := settingsSvc.Load(); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	logSvc := logging.NewService(t.TempDir())
	t.Cleanup(logSvc.Close)
	return NewService(settingsSvc, dir, logSvc), dir, settingsDir
}

// pngBytes 生成一张 w×h 的合法 PNG。
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: 100, G: 150, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// jpegBytes 生成一张 w×h 的合法 JPEG。
func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// writeTemp 把 bytes 写入临时文件（模拟用户选择的本地图片）。
func writeTemp(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}
	return p
}

func TestImportSkinImage_PNG(t *testing.T) {
	svc, dir, _ := newTestService(t)
	data := pngBytes(t, 7, 5)
	src := writeTemp(t, t.TempDir(), "wall.png", data)

	skin, err := svc.ImportSkinImage(src)
	if err != nil {
		t.Fatalf("ImportSkinImage: %v", err)
	}
	if skin.Width != 7 || skin.Height != 5 {
		t.Fatalf("dims = %dx%d, want 7x5", skin.Width, skin.Height)
	}
	if skin.ID == "" || skin.FileName != skin.ID+".png" {
		t.Fatalf("fileName = %q (id=%q)", skin.FileName, skin.ID)
	}
	if skin.URL != "/skins/"+skin.FileName {
		t.Fatalf("url = %q", skin.URL)
	}
	if skin.Bytes != int64(len(data)) {
		t.Fatalf("bytes = %d, want %d", skin.Bytes, len(data))
	}
	if _, err := os.Stat(filepath.Join(dir, skin.FileName)); err != nil {
		t.Fatalf("imported file missing: %v", err)
	}

	list, err := svc.ListSkins()
	if err != nil {
		t.Fatalf("ListSkins: %v", err)
	}
	if len(list) != 1 || list[0].ID != skin.ID || list[0].Width != 7 || list[0].Height != 5 {
		t.Fatalf("list = %+v", list)
	}
}

func TestImportSkinImage_JPEGNormalizedExt(t *testing.T) {
	svc, _, _ := newTestService(t)
	src := writeTemp(t, t.TempDir(), "photo.jpeg", jpegBytes(t, 4, 4))

	skin, err := svc.ImportSkinImage(src)
	if err != nil {
		t.Fatalf("ImportSkinImage: %v", err)
	}
	if !strings.HasSuffix(skin.FileName, ".jpg") {
		t.Fatalf("fileName = %q, want .jpg ext", skin.FileName)
	}
	if skin.Width != 4 || skin.Height != 4 {
		t.Fatalf("jpeg dims = %dx%d, want 4x4 (DecodeConfig)", skin.Width, skin.Height)
	}
}

func TestImportSkinImage_WEBPZeroDims(t *testing.T) {
	svc, _, _ := newTestService(t)
	// 构造合法 WEBP 魔数（RIFF....WEBP）。
	data := append([]byte("RIFF\x00\x00\x00\x00WEBP"), make([]byte, 32)...)
	src := writeTemp(t, t.TempDir(), "bg.webp", data)

	skin, err := svc.ImportSkinImage(src)
	if err != nil {
		t.Fatalf("ImportSkinImage: %v", err)
	}
	if !strings.HasSuffix(skin.FileName, ".webp") {
		t.Fatalf("fileName = %q", skin.FileName)
	}
	if skin.Width != 0 || skin.Height != 0 {
		t.Fatalf("webp dims = %dx%d, want 0x0", skin.Width, skin.Height)
	}
}

func TestImportSkinImage_RejectsNonMagicFormats(t *testing.T) {
	svc, _, _ := newTestService(t)
	tmp := t.TempDir()

	// GIF：标准库可解码，但魔数不在白名单内。
	gif := writeTemp(t, tmp, "a.gif", []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00"))
	if _, err := svc.ImportSkinImage(gif); err == nil {
		t.Fatal("gif accepted, want magic rejection")
	}
	// 纯文本改后缀 .png。
	txt := writeTemp(t, tmp, "fake.png", []byte("this is not an image at all"))
	if _, err := svc.ImportSkinImage(txt); err == nil {
		t.Fatal("text with .png ext accepted, want magic rejection")
	}
	// PNG 魔数 + 尾部垃圾仍应通过（前缀判定）。
	pngTail := append(append([]byte{}, pngBytes(t, 2, 2)...), []byte("trailing")...)
	src := writeTemp(t, tmp, "tail.png", pngTail)
	if _, err := svc.ImportSkinImage(src); err != nil {
		t.Fatalf("png with trailing bytes rejected: %v", err)
	}
}

func TestImportSkinImage_RejectsOversized(t *testing.T) {
	svc, _, _ := newTestService(t)
	// 合法 PNG 魔数 + 超限填充（> 20MB）。
	data := append(pngBytes(t, 1, 1), make([]byte, MaxSkinBytes)...)
	src := writeTemp(t, t.TempDir(), "big.png", data)

	if _, err := svc.ImportSkinImage(src); err == nil {
		t.Fatal("oversized accepted, want size rejection")
	}
}

func TestListSkins_EmptyDirAndOrder(t *testing.T) {
	svc, dir, _ := newTestService(t)

	// 目录不存在 → 空列表（fresh install）。
	list, err := svc.ListSkins()
	if err != nil || len(list) != 0 {
		t.Fatalf("missing dir: list=%v err=%v", list, err)
	}

	older := writeTemp(t, t.TempDir(), "a.png", pngBytes(t, 1, 1))
	newer := writeTemp(t, t.TempDir(), "b.png", pngBytes(t, 2, 2))
	s1, err := svc.ImportSkinImage(older)
	if err != nil {
		t.Fatalf("import older: %v", err)
	}
	s2, err := svc.ImportSkinImage(newer)
	if err != nil {
		t.Fatalf("import newer: %v", err)
	}
	// 显式拉开 mtime，确保降序可判定。
	oldT := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, s1.FileName), oldT, oldT); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	// 干扰文件：非法扩展名应被跳过。
	writeTemp(t, dir, "notes.txt", []byte("x"))
	writeTemp(t, dir, "readme.md", []byte("x"))

	list, err = svc.ListSkins()
	if err != nil {
		t.Fatalf("ListSkins: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2 (illegal ext must be skipped): %+v", len(list), list)
	}
	if list[0].ID != s2.ID || list[1].ID != s1.ID {
		t.Fatalf("order = [%s %s], want newer first [%s %s]", list[0].ID, list[1].ID, s2.ID, s1.ID)
	}
}

func TestRemoveSkin_ProtectsEnabledSkin(t *testing.T) {
	svc, _, _ := newTestService(t)
	skin, err := svc.ImportSkinImage(writeTemp(t, t.TempDir(), "a.png", pngBytes(t, 2, 2)))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	// 启用（SetSkinSettings 校验存在性，应通过）。
	if err := svc.SetSkinSettings(settings.SkinSettings{Enabled: true, ImageID: skin.ID, Dim: 40, Blur: 5}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	// 正在使用的皮肤拒绝删除。
	if err := svc.RemoveSkin(skin.ID); err == nil {
		t.Fatal("enabled skin removed, want protection error")
	}
	// 停用后可删。
	if err := svc.SetSkinSettings(settings.SkinSettings{Enabled: false, ImageID: "", Dim: 35}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := svc.RemoveSkin(skin.ID); err != nil {
		t.Fatalf("remove after disable: %v", err)
	}
	list, _ := svc.ListSkins()
	if len(list) != 0 {
		t.Fatalf("skin still listed after remove: %+v", list)
	}
	// 删除不存在的 id → 错误。
	if err := svc.RemoveSkin("nonexistent"); err == nil {
		t.Fatal("removing nonexistent id succeeded")
	}
}

func TestSetSkinSettings_ExistenceValidation(t *testing.T) {
	svc, _, _ := newTestService(t)

	// enabled + 空 ImageID → 错误。
	if err := svc.SetSkinSettings(settings.SkinSettings{Enabled: true, ImageID: "", Dim: 35}); err == nil {
		t.Fatal("enabled with empty imageId accepted")
	}
	// enabled + 不存在的 ImageID → 错误。
	if err := svc.SetSkinSettings(settings.SkinSettings{Enabled: true, ImageID: "ghost", Dim: 35}); err == nil {
		t.Fatal("enabled with missing image accepted")
	}
	// disabled 不做存在性校验（恢复默认）。
	if err := svc.SetSkinSettings(settings.SkinSettings{Enabled: false, ImageID: "", Dim: 35}); err != nil {
		t.Fatalf("disabled set: %v", err)
	}

	skin, err := svc.ImportSkinImage(writeTemp(t, t.TempDir(), "a.png", pngBytes(t, 1, 1)))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	want := settings.SkinSettings{Enabled: true, ImageID: skin.ID, Dim: 40, Blur: 5}
	if err := svc.SetSkinSettings(want); err != nil {
		t.Fatalf("enable with imported skin: %v", err)
	}
	if got := svc.GetSkinSettings(); got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestPickSkinImage_RequiresContext(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.PickSkinImage(); err == nil {
		t.Fatal("PickSkinImage without ctx succeeded, want explicit error")
	}
}

// --- AssetHandler ---

func serveAsset(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAssetHandler_ServesWithMIME(t *testing.T) {
	_, dir, _ := newTestService(t)
	h := NewAssetHandler(dir)

	pngData := pngBytes(t, 3, 3)
	if err := os.WriteFile(filepath.Join(dir, "a.png"), pngData, 0o600); err != nil {
		t.Fatalf("write png: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.jpg"), []byte("\xFF\xD8\xFFrest"), 0o600); err != nil {
		t.Fatalf("write jpg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.webp"), append([]byte("RIFF\x00\x00\x00\x00WEBP"), 0x1), 0o600); err != nil {
		t.Fatalf("write webp: %v", err)
	}

	cases := []struct {
		path string
		mime string
		body []byte
	}{
		{"/skins/a.png", "image/png", pngData},
		{"/skins/b.jpg", "image/jpeg", []byte("\xFF\xD8\xFFrest")},
		{"/skins/c.webp", "image/webp", append([]byte("RIFF\x00\x00\x00\x00WEBP"), 0x1)},
	}
	for _, tc := range cases {
		rec := serveAsset(t, h, http.MethodGet, tc.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: code=%d, want 200", tc.path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != tc.mime {
			t.Fatalf("%s: content-type=%q, want %q", tc.path, got, tc.mime)
		}
		if !bytes.Equal(rec.Body.Bytes(), tc.body) {
			t.Fatalf("%s: body mismatch (%d bytes)", tc.path, rec.Body.Len())
		}
	}
}

func TestAssetHandler_RejectsTraversalAndMissing(t *testing.T) {
	_, dir, _ := newTestService(t)
	h := NewAssetHandler(dir)

	secret := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	cases := []struct {
		name string
		path string
		want int
	}{
		{"traversal literal", "/skins/../secret.txt", http.StatusNotFound},
		{"traversal encoded", "/skins/%2e%2e/secret.txt", http.StatusNotFound},
		{"prefix root", "/skins/", http.StatusNotFound},
		{"plain slash root", "/skins", http.StatusNotFound},
		{"missing file", "/skins/missing.png", http.StatusNotFound},
		{"illegal ext", "/skins/notes.txt", http.StatusNotFound},
		{"no ext", "/skins/README", http.StatusNotFound},
		{"outside prefix", "/other/a.png", http.StatusNotFound},
	}
	for _, tc := range cases {
		rec := serveAsset(t, h, http.MethodGet, tc.path)
		if rec.Code != tc.want {
			t.Fatalf("%s (%s): code=%d, want %d", tc.name, tc.path, rec.Code, tc.want)
		}
		if tc.want == http.StatusNotFound && bytes.Contains(rec.Body.Bytes(), []byte("top secret")) {
			t.Fatalf("%s: secret leaked", tc.path)
		}
	}

	// 目录伪装成皮肤名 → 404。
	if err := os.Mkdir(filepath.Join(dir, "d.png"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if rec := serveAsset(t, h, http.MethodGet, "/skins/d.png"); rec.Code != http.StatusNotFound {
		t.Fatalf("directory: code=%d, want 404", rec.Code)
	}

	// 非 GET → 405。
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := serveAsset(t, h, method, "/skins/a.png")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s: code=%d, want 405", method, rec.Code)
		}
	}
}
