package updater

import (
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
	"archive/zip"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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

// exitProcess allows tests to override os.Exit behavior.
var exitProcess = func(code int) { os.Exit(code) }

// randRead allows tests to override crypto/rand.Read for deterministic output.
var randRead = rand.Read

// launchUpdateHelper allows tests to override the helper script launch.
// Paths are passed as positional arguments to the script.
var launchUpdateHelper = func(scriptPath string, args ...string) error {
	cmd := exec.Command("/bin/sh", append([]string{scriptPath}, args...)...)
	return cmd.Start()
}

const stagingDirPrefix = ".amagi-codebox-update-staging-"

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
	token    string // GitHub Personal Access Token
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

// SetToken sets the GitHub Personal Access Token for accessing private repository Releases.
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

// DownloadAndApply downloads the update package and applies it, then exits the process.
// It dispatches to platform-specific logic based on the OS.
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

	switch s.capabilities.OS {
	case "windows":
		return s.downloadAndApplyWindowsExeZip(info, onProgress)
	case "darwin":
		return s.downloadAndApplyDarwinAppBundle(info, onProgress)
	default:
		return fmt.Errorf("auto-update is not supported on platform %s", s.capabilities.PlatformID)
	}
}

// downloadAndApplyWindowsExeZip implements the Windows exe-in-zip update flow.
// Behavior is identical to the original DownloadAndApply.
func (s *Service) downloadAndApplyWindowsExeZip(info *UpdateInfo, onProgress func(downloaded, total int64)) error {
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
	exitProcess(0)
	return nil
}

// downloadAndApplyDarwinAppBundle implements the macOS .app bundle update flow.
// It downloads the darwin-arm64 zip, extracts the new .app bundle to a staging
// directory, validates it, and launches a helper script to perform the swap.
func (s *Service) downloadAndApplyDarwinAppBundle(info *UpdateInfo, onProgress func(downloaded, total int64)) error {
	// Locate current .app bundle
	currentAppPath, err := locateCurrentAppBundle()
	if err != nil {
		return fmt.Errorf("locate current app bundle: %w", err)
	}

	parentDir := filepath.Dir(currentAppPath)

	// Verify parent directory is writable
	if err := isDirWritable(parentDir); err != nil {
		return fmt.Errorf("cannot write to application directory: %w", err)
	}

	// Use unique suffix to avoid multi-instance conflicts
	uniqueSuffix := strconv.Itoa(os.Getpid()) + "-" + randomHex(8)

	// Download zip to unique temp file
	downloadPath := filepath.Join(os.TempDir(), "amagi-codebox-update-"+uniqueSuffix+".zip")
	_ = os.Remove(downloadPath)
	if err := s.downloadZip(info.DownloadURL, downloadPath, onProgress); err != nil {
		return err
	}

	// Create unique staging directory next to current app
	stagingDir := filepath.Join(parentDir, stagingDirPrefix+uniqueSuffix)
	_ = os.RemoveAll(stagingDir)
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		os.Remove(downloadPath)
		return fmt.Errorf("create staging directory: %w", err)
	}

	// Extract .app bundle from zip into staging
	if err := extractAppBundleFromZip(downloadPath, stagingDir); err != nil {
		cleanupUpdateArtifacts(downloadPath, stagingDir)
		return fmt.Errorf("extract update: %w", err)
	}

	// Locate the staged .app bundle
	stagedAppPath := filepath.Join(stagingDir, "amagi-codebox.app")
	if _, err := os.Stat(stagedAppPath); err != nil {
		cleanupUpdateArtifacts(downloadPath, stagingDir)
		return fmt.Errorf("staged app bundle not found at %s: %w", stagedAppPath, err)
	}

	// Validate the staged bundle structure
	if err := validateAppBundle(stagedAppPath); err != nil {
		cleanupUpdateArtifacts(downloadPath, stagingDir)
		return fmt.Errorf("validate staged bundle: %w", err)
	}

	// Write and launch the helper script with paths as arguments
	pid := os.Getpid()
	helperScriptPath := filepath.Join(os.TempDir(), "amagi-codebox-update-helper-"+uniqueSuffix+".sh")
	if err := writeHelperScript(helperScriptPath); err != nil {
		cleanupUpdateArtifacts(downloadPath, stagingDir)
		return fmt.Errorf("create helper script: %w", err)
	}

	if err := launchUpdateHelper(helperScriptPath,
		strconv.Itoa(pid),
		currentAppPath,
		stagedAppPath,
		stagingDir,
	); err != nil {
		cleanupUpdateArtifacts(downloadPath, stagingDir)
		return fmt.Errorf("launch update helper: %w", err)
	}

	if s.log != nil {
		s.log.Info("updater", "macOS update helper launched, exiting main process",
			fmt.Sprintf("pid=%d app=%s staged=%s", pid, currentAppPath, stagedAppPath))
	}

	exitProcess(0)
	return nil
}

// CleanupOldBinary removes backup files/directories left by previous updates.
// On macOS it cleans up the .app.old directory; on Windows it cleans the .exe.old file.
func (s *Service) CleanupOldBinary() {
	exePath, err := os.Executable()
	if err != nil {
		if s.log != nil {
			s.log.Warn("updater", "清理旧版本失败", err.Error())
		}
		return
	}

	switch s.capabilities.OS {
	case "darwin":
		s.cleanupOldDarwinApp(exePath)
	default:
		s.cleanupOldExe(exePath)
	}
}

func (s *Service) cleanupOldExe(exePath string) {
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

func (s *Service) cleanupOldDarwinApp(exePath string) {
	appPath, err := locateAppBundleFromPath(exePath)
	if err != nil {
		// Not running from a .app bundle; fallback to exe-based cleanup
		s.cleanupOldExe(exePath)
		return
	}

	oldAppPath := appPath + ".old"
	if _, err := os.Stat(oldAppPath); err != nil {
		if !os.IsNotExist(err) && s.log != nil {
			s.log.Warn("updater", "检查旧版本 .app 备份失败", err.Error())
		}
		return
	}

	if err := os.RemoveAll(oldAppPath); err != nil {
		if s.log != nil {
			s.log.Warn("updater", "删除旧版本 .app 备份失败", err.Error())
		}
		return
	}

	if s.log != nil {
		s.log.Info("updater", "旧版本 .app 备份已清理", oldAppPath)
	}
}

func cleanupUpdateArtifacts(downloadPath string, stagingDir string) {
	os.Remove(downloadPath)
	os.RemoveAll(stagingDir)
}

// locateCurrentAppBundle locates the .app bundle containing the currently running executable.
func locateCurrentAppBundle() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get executable path: %w", err)
	}
	return locateAppBundleFromPath(exePath)
}

// locateAppBundleFromPath resolves the .app bundle root directory from an executable path.
// The executable must be at <App>.app/Contents/MacOS/<name>.
func locateAppBundleFromPath(exePath string) (string, error) {
	resolved, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}

	macosDir := filepath.Dir(resolved)
	contentsDir := filepath.Dir(macosDir)

	if filepath.Base(macosDir) != "MacOS" {
		return "", fmt.Errorf("not running from a .app bundle (expected Contents/MacOS parent, got %s)", macosDir)
	}
	if filepath.Base(contentsDir) != "Contents" {
		return "", fmt.Errorf("not running from a .app bundle (expected Contents parent, got %s)", contentsDir)
	}

	appDir := filepath.Dir(contentsDir)
	if !strings.HasSuffix(filepath.Base(appDir), ".app") {
		return "", fmt.Errorf("parent directory is not a .app bundle: %s", appDir)
	}

	return appDir, nil
}

// extractAppBundleFromZip extracts a macOS .app bundle from a zip file into stagingDir.
// It skips __MACOSX/ metadata and .DS_Store files, enforces zip slip protection,
// and preserves file permissions from the zip header.
func extractAppBundleFromZip(zipPath string, stagingDir string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open update zip: %w", err)
	}
	defer reader.Close()

	for _, f := range reader.File {
		if shouldSkipZipEntry(f.Name) {
			continue
		}

		targetPath := filepath.Join(stagingDir, f.Name)
		if !isWithinDir(targetPath, stagingDir) {
			return fmt.Errorf("zip slip detected: entry %q escapes staging directory", f.Name)
		}

		if f.Mode().IsDir() {
			if err := os.MkdirAll(targetPath, f.Mode()); err != nil {
				return fmt.Errorf("create directory %q: %w", f.Name, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("create parent directory for %q: %w", f.Name, err)
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %q: %w", f.Name, err)
		}

		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode())
		if err != nil {
			rc.Close()
			return fmt.Errorf("create file %q: %w", targetPath, err)
		}

		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		rc.Close()

		if copyErr != nil {
			return fmt.Errorf("extract %q: %w", f.Name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close extracted file %q: %w", targetPath, closeErr)
		}
	}

	return nil
}

// shouldSkipZipEntry returns true for macOS metadata entries that should be skipped.
func shouldSkipZipEntry(name string) bool {
	if filepath.Base(name) == ".DS_Store" {
		return true
	}
	if name == "__MACOSX" || strings.HasPrefix(name, "__MACOSX/") {
		return true
	}
	return false
}

// isWithinDir checks that path does not escape dir. Used for zip slip protection.
func isWithinDir(path string, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// validateAppBundle checks that a macOS .app bundle has the required structure and
// executable bits for amagi-codebox.
func validateAppBundle(appPath string) error {
	info, err := os.Stat(appPath)
	if err != nil {
		return fmt.Errorf("app directory does not exist: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("expected directory, got file: %s", appPath)
	}
	if !strings.HasSuffix(filepath.Base(appPath), ".app") {
		return fmt.Errorf("directory does not have .app suffix: %s", appPath)
	}

	infoPlist := filepath.Join(appPath, "Contents", "Info.plist")
	if _, err := os.Stat(infoPlist); err != nil {
		return fmt.Errorf("Contents/Info.plist missing or inaccessible: %w", err)
	}

	mainExePath := filepath.Join(appPath, "Contents", "MacOS", "amagi-codebox")
	exeInfo, err := os.Stat(mainExePath)
	if err != nil {
		return fmt.Errorf("Contents/MacOS/amagi-codebox missing or inaccessible: %w", err)
	}
	if exeInfo.Mode()&0o111 == 0 {
		return fmt.Errorf("Contents/MacOS/amagi-codebox is not executable")
	}

	return nil
}

// isDirWritable checks if a directory is writable by creating and removing a temp file.
func isDirWritable(dir string) error {
	testFile := filepath.Join(dir, ".amagi-codebox-write-test-"+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(testFile, []byte("test"), 0o644); err != nil {
		return fmt.Errorf("directory %q is not writable: %w", dir, err)
	}
	os.Remove(testFile)
	return nil
}

// writeHelperScript writes a static /bin/sh script that performs the macOS .app bundle swap.
// All paths are passed as positional arguments at invocation time; no paths are embedded
// in the script body. The script includes safety assertions before any destructive operation.
//
// Usage: /bin/sh <script> <pid> <currentApp> <stagedApp> <stagingDir>
func writeHelperScript(scriptPath string) error {
	const script = `#!/bin/sh
# amagi-codebox macOS update helper
# Arguments: $1=pid $2=currentApp $3=stagedApp $4=stagingDir
# No paths are embedded in this script; all come from positional parameters.

set -e

PID="$1"
CURRENT_APP="$2"
STAGED_APP="$3"
STAGING_DIR="$4"

LOGFILE="/tmp/amagi-codebox-update-$PID.log"

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') $1" >> "$LOGFILE"; }
die() { log "FATAL: $1"; exit 1; }

log "Update helper started (helper PID=$$, main PID=$PID)"

# --- Safety assertions: reject obviously wrong arguments ---

[ -n "$CURRENT_APP" ]  || die "CURRENT_APP is empty"
[ -n "$STAGED_APP" ]   || die "STAGED_APP is empty"
[ -n "$STAGING_DIR" ]  || die "STAGING_DIR is empty"
[ -n "$PID" ]          || die "PID is empty"

case "$CURRENT_APP" in
    *.app) ;;
    *)     die "CURRENT_APP does not end with .app: $CURRENT_APP" ;;
esac

OLD_APP="${CURRENT_APP}.old"

case "$OLD_APP" in
    *.app.old) ;;
    *)         die "OLD_APP does not end with .app.old: $OLD_APP" ;;
esac

case "$STAGED_APP" in
    *.app) ;;
    *)     die "STAGED_APP does not end with .app: $STAGED_APP" ;;
esac

case "$(basename "$STAGING_DIR")" in
    .amagi-codebox-update-staging-*) ;;
    *) die "STAGING_DIR basename does not have expected prefix: $STAGING_DIR" ;;
esac

# Parent directory relationships: staging and old must share parent with current app
CURRENT_PARENT="$(dirname "$CURRENT_APP")"
OLD_PARENT="$(dirname "$OLD_APP")"
STAGING_PARENT="$(dirname "$STAGING_DIR")"

[ "$OLD_PARENT" = "$CURRENT_PARENT" ]     || die "OLD_APP parent differs from CURRENT_APP parent"
[ "$STAGING_PARENT" = "$CURRENT_PARENT" ] || die "STAGING_DIR parent differs from CURRENT_APP parent"

log "Safety assertions passed"

# Wait for main process to exit
while kill -0 "$PID" 2>/dev/null; do
    sleep 0.5
done

log "Main process $PID has exited"

# Remove previous backup if it exists
if [ -d "$OLD_APP" ]; then
    log "Removing previous backup: $OLD_APP"
    rm -rf "$OLD_APP"
fi

# Move current app to .old backup
log "Backing up current app to $OLD_APP"
if ! mv "$CURRENT_APP" "$OLD_APP"; then
    log "FATAL: failed to move current app to backup"
    rm -rf "$STAGING_DIR"
    exit 1
fi

# Move staged app to current position
log "Moving staged app to $CURRENT_APP"
if ! mv "$STAGED_APP" "$CURRENT_APP"; then
    log "FATAL: failed to move staged app, attempting rollback"
    if [ -d "$OLD_APP" ]; then
        mv "$OLD_APP" "$CURRENT_APP"
    fi
    rm -rf "$STAGING_DIR"
    exit 1
fi

# Clean up staging directory
rm -rf "$STAGING_DIR"

# Clear quarantine attributes
log "Clearing quarantine attributes on $CURRENT_APP"
xattr -cr "$CURRENT_APP" 2>/dev/null || true

# Launch new app
log "Launching updated app"
if ! open "$CURRENT_APP"; then
    log "ERROR: failed to launch new app, attempting rollback"
    rm -rf "$CURRENT_APP"
    if [ -d "$OLD_APP" ]; then
        mv "$OLD_APP" "$CURRENT_APP"
        open "$CURRENT_APP"
    fi
    exit 1
fi

log "Update completed successfully"
`
	return os.WriteFile(scriptPath, []byte(script), 0o755)
}

// randomHex returns n hex characters of randomness for unique file naming.
func randomHex(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = randRead(b)
	return fmt.Sprintf("%x", b)[:n]
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

// compareVersions semantically compares two version numbers (major.minor.patch).
// Returns 1 if a > b, 0 if a == b, -1 if a < b.
// Returns -2 if either version cannot be parsed as semantic version.
func compareVersions(a, b string) int {
	parseVer := func(v string) (major, minor, patch int, ok bool) {
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
