package updater

import (
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const userAgent = "amagi-codebox-updater"

const (
	releaseRepoOwner = "runrunrain"
	releaseRepoName  = "amagi-codebox"

	updateActionInstall          = "install"
	updateActionOpenDownloadPage = "open-download-page"
)

type UpdateInfo struct {
	HasUpdate      bool   `json:"hasUpdate"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	ReleaseNotes   string `json:"releaseNotes"`
	PublishedAt    string `json:"publishedAt"`
	DownloadURL    string `json:"downloadURL"`
	ReleaseURL     string `json:"releaseURL"`
	AssetName      string `json:"assetName"`
	AssetURL       string `json:"assetURL"`
	AssetSize      int64  `json:"assetSize"`
	UpdateAction   string `json:"updateAction"`
}

type Service struct {
	repoOwner      string
	repoRepo       string
	currentVersion string
	log            *logging.Service
	capabilities   platform.PlatformCapabilities

	mu       sync.Mutex
	lastInfo *UpdateInfo
	token    string // GitHub Personal Access Token（支持私有仓库）
}

type githubRelease struct {
	TagName     string               `json:"tag_name"`
	HTMLURL     string               `json:"html_url"`
	Body        string               `json:"body"`
	PublishedAt string               `json:"published_at"`
	Assets      []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type progressWriter struct {
	downloaded int64
	total      int64
	onProgress func(downloaded, total int64)
}

func NewService(currentVersion string, log *logging.Service) *Service {
	return &Service{
		repoOwner:      releaseRepoOwner,
		repoRepo:       releaseRepoName,
		currentVersion: normalizeVersion(currentVersion),
		log:            log,
		capabilities:   platform.CurrentCapabilities(),
	}
}

// SetToken 设置 GitHub Personal Access Token，用于访问私有仓库的 Releases。
func (s *Service) SetToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
}

func (s *Service) CheckForUpdate() (*UpdateInfo, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", s.repoOwner, s.repoRepo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create update request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	s.mu.Lock()
	token := s.token
	s.mu.Unlock()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github latest release returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode latest release: %w", err)
	}

	info, err := buildUpdateInfo(s.currentVersion, s.capabilities, release)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.lastInfo = info
	s.mu.Unlock()

	if s.log != nil {
		s.log.Info("updater", "更新检查完成", fmt.Sprintf("current=%s latest=%s hasUpdate=%v", info.CurrentVersion, info.LatestVersion, info.HasUpdate))
	}

	return info, nil
}

func (s *Service) DownloadAndApply(onProgress func(downloaded, total int64)) error {
	s.mu.Lock()
	info := s.lastInfo
	s.mu.Unlock()

	if info == nil {
		return fmt.Errorf("no update info available, call CheckForUpdate first")
	}
	if !info.HasUpdate {
		return fmt.Errorf("no update available")
	}
	if info.DownloadURL == "" {
		return fmt.Errorf("missing download url")
	}
	if !s.capabilities.UpdateInstallSupported {
		return fmt.Errorf("platform %s only supports checking for updates and opening the download page", s.capabilities.PlatformID)
	}

	downloadPath := filepath.Join(os.TempDir(), "amagi-codebox-update.zip")
	extractedPath := filepath.Join(os.TempDir(), "amagi-codebox-update.exe")
	_ = os.Remove(downloadPath)
	_ = os.Remove(extractedPath)

	if err := s.downloadZip(info.DownloadURL, downloadPath, onProgress); err != nil {
		return err
	}
	defer os.Remove(downloadPath)

	if err := extractExeFromZip(downloadPath, extractedPath); err != nil {
		return err
	}
	defer os.Remove(extractedPath)

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get current executable: %w", err)
	}
	oldPath := exePath + ".old"

	if _, err := os.Stat(oldPath); err == nil {
		if err := os.Remove(oldPath); err != nil {
			return fmt.Errorf("remove old backup: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat old backup: %w", err)
	}

	if err := os.Rename(exePath, oldPath); err != nil {
		return fmt.Errorf("backup current executable: %w", err)
	}

	if err := copyFile(extractedPath, exePath); err != nil {
		_ = os.Rename(oldPath, exePath)
		return fmt.Errorf("replace executable: %w", err)
	}

	if err := startUpdatedExecutable(exePath); err != nil {
		_ = os.Remove(exePath)
		_ = os.Rename(oldPath, exePath)
		return fmt.Errorf("start updated executable: %w", err)
	}

	if s.log != nil {
		s.log.Info("updater", "更新已应用，正在重启", fmt.Sprintf("from=%s to=%s", info.CurrentVersion, info.LatestVersion))
	}

	_ = os.Remove(downloadPath)
	_ = os.Remove(extractedPath)
	os.Exit(0)
	return nil
}

func (s *Service) CleanupOldBinary() {
	exePath, err := os.Executable()
	if err != nil {
		if s.log != nil {
			s.log.Warn("updater", "清理旧版本失败", err.Error())
		}
		return
	}

	oldPath := exePath + ".old"
	if _, err := os.Stat(oldPath); err != nil {
		if !os.IsNotExist(err) && s.log != nil {
			s.log.Warn("updater", "检查旧版本备份失败", err.Error())
		}
		return
	}

	if err := os.Remove(oldPath); err != nil {
		if s.log != nil {
			s.log.Warn("updater", "删除旧版本备份失败", err.Error())
		}
		return
	}

	if s.log != nil {
		s.log.Info("updater", "旧版本备份已清理", oldPath)
	}
}

func (s *Service) downloadZip(downloadURL string, targetPath string, onProgress func(downloaded, total int64)) error {
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/octet-stream")
	s.mu.Lock()
	token := s.token
	s.mu.Unlock()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download update package: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("download returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	total := max(resp.ContentLength, 0)
	if onProgress != nil {
		onProgress(0, total)
	}

	file, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("create temp zip: %w", err)
	}
	defer file.Close()

	writer := &progressWriter{total: total, onProgress: onProgress}
	if _, err := io.Copy(file, io.TeeReader(resp.Body, writer)); err != nil {
		return fmt.Errorf("write temp zip: %w", err)
	}

	return nil
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.downloaded += int64(n)
	if w.onProgress != nil {
		w.onProgress(w.downloaded, w.total)
	}
	return n, nil
}

func extractExeFromZip(zipPath string, targetPath string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open update zip: %w", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		if !strings.EqualFold(filepath.Base(file.Name), "amagi-codebox.exe") {
			continue
		}

		rc, err := file.Open()
		if err != nil {
			return fmt.Errorf("open zip entry: %w", err)
		}

		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			rc.Close()
			return fmt.Errorf("create extracted executable: %w", err)
		}

		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		rcErr := rc.Close()
		if copyErr != nil {
			return fmt.Errorf("extract executable: %w", copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close extracted executable: %w", closeErr)
		}
		if rcErr != nil {
			return fmt.Errorf("close zip entry: %w", rcErr)
		}
		return nil
	}

	return fmt.Errorf("amagi-codebox.exe not found in update zip")
}

func copyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("open destination file: %w", err)
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("copy file content: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close destination file: %w", closeErr)
	}
	return nil
}

func normalizeVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}

func buildUpdateInfo(currentVersion string, capabilities platform.PlatformCapabilities, release githubRelease) (*UpdateInfo, error) {
	releaseURL := releasePageURL(release, releaseRepoOwner, releaseRepoName)
	action := updateActionForCapabilities(capabilities)
	asset, err := selectReleaseAssetForAction(capabilities, action, release.Assets)
	if err != nil {
		return nil, err
	}

	normalizedCurrent := normalizeVersion(currentVersion)
	latestVersion := normalizeVersion(release.TagName)

	hasUpdate := false
	cmp := compareVersions(latestVersion, normalizedCurrent)
	if cmp > 0 {
		hasUpdate = true
	} else if cmp == -2 {
		hasUpdate = latestVersion != normalizedCurrent
	}

	info := &UpdateInfo{
		HasUpdate:      hasUpdate,
		CurrentVersion: normalizedCurrent,
		LatestVersion:  latestVersion,
		ReleaseNotes:   release.Body,
		PublishedAt:    release.PublishedAt,
		ReleaseURL:     releaseURL,
		DownloadURL:    releaseURL,
		UpdateAction:   action,
	}

	if asset != nil {
		info.AssetName = asset.Name
		info.AssetURL = asset.BrowserDownloadURL
		info.AssetSize = asset.Size
		if action == updateActionInstall {
			info.DownloadURL = asset.BrowserDownloadURL
		}
	}

	return info, nil
}

func updateActionForCapabilities(capabilities platform.PlatformCapabilities) string {
	if capabilities.UpdateInstallSupported {
		return updateActionInstall
	}
	return updateActionOpenDownloadPage
}

func selectReleaseAssetForAction(capabilities platform.PlatformCapabilities, action string, assets []githubReleaseAsset) (*githubReleaseAsset, error) {
	asset, err := findReleaseAsset(capabilities, assets)
	if err == nil {
		return asset, nil
	}
	if action == updateActionOpenDownloadPage {
		return nil, nil
	}
	return nil, err
}

func releasePageURL(release githubRelease, owner string, repo string) string {
	if trimmed := strings.TrimSpace(release.HTMLURL); trimmed != "" {
		return trimmed
	}
	tag := strings.TrimSpace(release.TagName)
	if tag == "" {
		return fmt.Sprintf("https://github.com/%s/%s/releases", owner, repo)
	}
	return fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", owner, repo, tag)
}

// compareVersions 语义比较两个版本号（major.minor.patch）。
// 返回 1 表示 a > b，0 表示 a == b，-1 表示 a < b。
// 返回 -2 表示无法解析为语义版本号。
func compareVersions(a, b string) int {
	parseVer := func(v string) (major, minor, patch int, ok bool) {
		// 去掉预发布/构建元数据后缀（如 "1.0.0-beta" → "1.0.0"）
		if idx := strings.IndexAny(v, "-+"); idx >= 0 {
			v = v[:idx]
		}
		parts := strings.Split(v, ".")
		if len(parts) < 2 || len(parts) > 3 {
			return 0, 0, 0, false
		}
		var err error
		if major, err = strconv.Atoi(parts[0]); err != nil {
			return 0, 0, 0, false
		}
		if minor, err = strconv.Atoi(parts[1]); err != nil {
			return 0, 0, 0, false
		}
		if len(parts) == 3 {
			if patch, err = strconv.Atoi(parts[2]); err != nil {
				return 0, 0, 0, false
			}
		}
		return major, minor, patch, true
	}

	aMaj, aMin, aPat, aOk := parseVer(a)
	bMaj, bMin, bPat, bOk := parseVer(b)
	if !aOk || !bOk {
		return -2
	}

	if aMaj != bMaj {
		if aMaj > bMaj {
			return 1
		}
		return -1
	}
	if aMin != bMin {
		if aMin > bMin {
			return 1
		}
		return -1
	}
	if aPat != bPat {
		if aPat > bPat {
			return 1
		}
		return -1
	}
	return 0
}
