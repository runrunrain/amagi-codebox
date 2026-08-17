package skins

// service.go — 本地图片皮肤管理服务（皮肤/壁纸，plan 后端切片 A）。
//
// 皮肤图片库位于 ~/.amagi-codebox/skins/：导入即拷贝为 <id>.<ext>（id 为
// 16 字节随机 hex），源文件不受影响；导入（ImportSkinImage）是唯一写入
// 口，因此枚举只认扩展名即可信任文件内容。
//
// 图片格式仅接受 png / jpeg / webp（魔数校验，防改后缀），单文件 ≤ 20MB；
// 尺寸解析 png/jpeg 用 image 标准库 DecodeConfig（只读头部），webp 魔数
// 接受但尺寸记 0。
//
// 前端通过 Wails assetserver 的自定义 Handler 以 /skins/<file> 只读访问
// 皮肤图片（见 NewAssetHandler），不提供目录列表。

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/settings"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// MaxSkinBytes 单个皮肤图片大小上限（20MB）。
const MaxSkinBytes = 20 << 20

// URLPrefix 是皮肤图片的前端资源路径前缀（assetserver 自定义 Handler 挂载点）。
const URLPrefix = "/skins/"

// imageKind 是魔数识别出的图片格式，ext 是入库时的规范扩展名。
type imageKind struct {
	name string
	ext  string
}

var (
	kindPNG  = imageKind{"png", "png"}
	kindJPEG = imageKind{"jpeg", "jpg"}
	kindWEBP = imageKind{"webp", "webp"}
)

// Skin 导入后的皮肤元数据（前端缩略图网格与当前皮肤匹配依据）。
type Skin struct {
	ID         string `json:"id"`
	FileName   string `json:"fileName"`
	URL        string `json:"url"`
	Bytes      int64  `json:"bytes"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	ImportedAt string `json:"importedAt"` // RFC3339
}

// Service 管理皮肤图片库与皮肤设置。settings 委托持久化（clamp 在
// settings 层）；Enabled 时 ImageID 存在性校验在本层（plan §1）。
type Service struct {
	settings *settings.Service
	dir      string
	log      *logging.Service

	// mu 串行化文件写入/删除与 ctx 读写；ListSkins / AssetHandler 只读不持锁。
	mu  sync.Mutex
	ctx context.Context
}

// NewService 创建皮肤服务。dir 为皮肤图片库目录（~/.amagi-codebox/skins）。
func NewService(settingsSvc *settings.Service, dir string, log *logging.Service) *Service {
	return &Service{
		settings: settingsSvc,
		dir:      dir,
		log:      log,
	}
}

// SetContext 注入 Wails app context（Startup 后可用）。PickSkinImage 依赖
// 它弹出原生文件选择对话框；未注入时 PickSkinImage 返回明确错误。
func (s *Service) SetContext(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = ctx
}

// context 返回已注入的 Wails ctx（可能为 nil）。
func (s *Service) context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx
}

// PickSkinImage 弹出文件选择对话框（png/jpg/jpeg/webp），用户确认后导入
// 皮肤图片并返回元数据；用户取消返回 (nil, nil)。校验与拷贝复用
// ImportSkinImage。
func (s *Service) PickSkinImage() (*Skin, error) {
	ctx := s.context()
	if ctx == nil {
		return nil, errors.New("skins: Wails context 未注入，无法打开文件选择对话框")
	}
	filePath, err := wailsRuntime.OpenFileDialog(ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择皮肤图片",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "图片 (*.png;*.jpg;*.jpeg;*.webp)", Pattern: "*.png;*.jpg;*.jpeg;*.webp"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("open file dialog: %w", err)
	}
	if filePath == "" {
		// 用户取消
		return nil, nil
	}
	return s.ImportSkinImage(filePath)
}

// ImportSkinImage 从本地路径导入皮肤图片：魔数校验 → 大小校验（≤20MB）→
// 解析尺寸（png/jpeg）→ 以随机 id 拷贝入库为 <id>.<ext>。测试直接调用
// 本方法，绕开不可伪造的对话框。
func (s *Service) ImportSkinImage(path string) (*Skin, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	kind, err := detectImageKind(data)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxSkinBytes {
		return nil, fmt.Errorf("skins: 图片超过 %dMB 上限（当前 %dMB）", MaxSkinBytes>>20, (int64(len(data))+MaxSkinBytes-1)>>20)
	}
	width, height := probeDimensions(data, kind)

	id, err := newSkinID()
	if err != nil {
		return nil, fmt.Errorf("skins: 生成皮肤 id 失败: %w", err)
	}
	fileName := id + "." + kind.ext

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("skins: 创建皮肤目录失败: %w", err)
	}
	dst := filepath.Join(s.dir, fileName)
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return nil, fmt.Errorf("skins: 写入皮肤文件失败: %w", err)
	}

	skin := Skin{
		ID:         id,
		FileName:   fileName,
		URL:        URLPrefix + fileName,
		Bytes:      int64(len(data)),
		Width:      width,
		Height:     height,
		ImportedAt: time.Now().Format(time.RFC3339),
	}
	if s.log != nil {
		s.log.Info("skins", "导入皮肤图片", fmt.Sprintf("file=%s bytes=%d", fileName, skin.Bytes))
	}
	return &skin, nil
}

// ListSkins 枚举皮肤库中合法扩展名的文件，按导入时间（文件 mtime）降序。
// 目录不存在视为空库（fresh install），返回空切片。
func (s *Service) ListSkins() ([]Skin, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Skin{}, nil
		}
		return nil, fmt.Errorf("skins: 读取皮肤目录失败: %w", err)
	}
	type timedSkin struct {
		skin    Skin
		modTime time.Time
	}
	list := make([]timedSkin, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		switch ext {
		case "png", "jpg", "jpeg", "webp":
			// 合法皮肤扩展名（导入是唯一写入口，只认扩展名即可）
		default:
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // 枚举窗口内被删除：跳过
		}
		id := strings.TrimSuffix(name, filepath.Ext(name))
		skin := Skin{
			ID:         id,
			FileName:   name,
			URL:        URLPrefix + name,
			Bytes:      info.Size(),
			ImportedAt: info.ModTime().Format(time.RFC3339),
		}
		// 尺寸解析只读图片头部（DecodeConfig），失败时记 0 而非报错。
		if f, err := os.Open(filepath.Join(s.dir, name)); err == nil {
			if cfg, _, err := image.DecodeConfig(f); err == nil {
				skin.Width = cfg.Width
				skin.Height = cfg.Height
			}
			_ = f.Close()
		}
		list = append(list, timedSkin{skin: skin, modTime: info.ModTime()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].modTime.After(list[j].modTime) })
	out := make([]Skin, len(list))
	for i, ts := range list {
		out[i] = ts.skin
	}
	return out, nil
}

// RemoveSkin 删除指定皮肤图片。当前皮肤已启用且正是该图片时拒绝删除，
// 由前端引导用户先停用（plan §2）。
func (s *Service) RemoveSkin(id string) error {
	if id == "" {
		return errors.New("skins: 皮肤 id 不能为空")
	}
	if strings.ContainsAny(id, `/\`) {
		return errors.New("skins: 非法的皮肤 id")
	}
	if sk := s.settings.GetSkinSettings(); sk.Enabled && sk.ImageID == id {
		return errors.New("skins: 皮肤正在使用，请先停用后再删除")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.skinPathLocked(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("skins: 删除皮肤文件失败: %w", err)
	}
	if s.log != nil {
		s.log.Info("skins", "删除皮肤图片", "id="+id)
	}
	return nil
}

// GetSkinSettings 返回皮肤设置（委托 settings 服务）。
func (s *Service) GetSkinSettings() settings.SkinSettings {
	return s.settings.GetSkinSettings()
}

// SetSkinSettings 更新皮肤设置（clamp 由 settings 层完成）。Enabled=true
// 时校验 ImageID 对应文件存在于皮肤库，不存在返回错误——防止 settings
// 指向已删除/不存在的皮肤。
func (s *Service) SetSkinSettings(sk settings.SkinSettings) error {
	if sk.Enabled {
		if sk.ImageID == "" {
			return errors.New("skins: 启用皮肤时必须指定图片")
		}
		s.mu.Lock()
		_, err := s.skinPathLocked(sk.ImageID)
		s.mu.Unlock()
		if err != nil {
			return fmt.Errorf("skins: 皮肤图片 %s 不存在，请重新导入", sk.ImageID)
		}
	}
	return s.settings.SetSkinSettings(sk)
}

// skinPathLocked 解析皮肤 id 对应的库内文件路径（遍历合法扩展名）。
// 调用方必须持有 s.mu。找不到返回错误。
func (s *Service) skinPathLocked(id string) (string, error) {
	for _, ext := range []string{kindPNG.ext, kindJPEG.ext, kindWEBP.ext, "jpeg"} {
		p := filepath.Join(s.dir, id+"."+ext)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}

// AssetHandler 返回挂载本服务皮肤目录的只读 http.Handler（main.go 装配用）。
func (s *Service) AssetHandler() http.Handler {
	return NewAssetHandler(s.dir)
}

// --- 魔数与尺寸 ---

// detectImageKind 按魔数识别图片格式：PNG \x89PNG\r\n\x1a\n、JPEG
// \xFF\xD8\xFF、WEBP "RIFF"...."WEBP"。改后缀的伪造文件在此被拒。
func detectImageKind(data []byte) (imageKind, error) {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return kindPNG, nil
	}
	if len(data) >= 3 && bytes.Equal(data[:3], []byte{0xFF, 0xD8, 0xFF}) {
		return kindJPEG, nil
	}
	if len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return kindWEBP, nil
	}
	return imageKind{}, errors.New("skins: 不支持的图片格式（仅接受 png/jpg/jpeg/webp）")
}

// probeDimensions 解析图片尺寸。png/jpeg 用标准库 DecodeConfig（只读头
// 部）；webp 标准库无解码器，接受但记 0。解析失败记 0（已通过魔数校验，
// 异常头部不阻断导入）。
func probeDimensions(data []byte, kind imageKind) (int, int) {
	if kind != kindPNG && kind != kindJPEG {
		return 0, 0
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// newSkinID 生成 16 字节随机 hex id（32 字符）。
func newSkinID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
