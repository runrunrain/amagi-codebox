package updater

import (
	"amagi-codebox/internal/platform"
	"archive/zip"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

type failingReadCloser struct {
	data []byte
	done bool
	err  error
}

func (r *failingReadCloser) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, r.data), nil
	}
	return 0, r.err
}

func (r *failingReadCloser) Close() error {
	return nil
}

func testHTTPResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode:    statusCode,
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(strings.NewReader(body)),
		Header:        make(http.Header),
	}
}

// --- buildUpdateInfo tests ---

func TestBuildUpdateInfoDarwinArm64UsesInstallAction(t *testing.T) {
	release := githubRelease{
		TagName:     "v1.2.3",
		HTMLURL:     "https://github.com/runrunrain/amagi-codebox/releases/tag/v1.2.3",
		Body:        "darwin release",
		PublishedAt: "2026-04-26T12:00:00Z",
		Assets: []githubReleaseAsset{
			{Name: "amagi-codebox-v1.2.3-darwin-arm64.zip", BrowserDownloadURL: "https://example.com/darwin.zip", Size: 2048},
		},
	}

	caps := platform.PlatformCapabilities{
		OS:                     "darwin",
		Arch:                   "arm64",
		PlatformID:             "darwin-arm64",
		UpdateInstallSupported: true,
	}

	info, err := buildUpdateInfo("v1.2.2", caps, release)
	if err != nil {
		t.Fatalf("buildUpdateInfo returned error: %v", err)
	}

	if !info.HasUpdate {
		t.Fatal("expected darwin arm64 update to be detected")
	}
	if info.UpdateAction != updateActionInstall {
		t.Fatalf("expected install action for darwin arm64, got %q", info.UpdateAction)
	}
	if info.DownloadURL != "https://example.com/darwin.zip" {
		t.Fatalf("expected download url to use asset url, got %q", info.DownloadURL)
	}
	if info.AssetURL != "https://example.com/darwin.zip" {
		t.Fatalf("expected darwin asset url to be preserved, got %q", info.AssetURL)
	}
	if info.AssetName != "amagi-codebox-v1.2.3-darwin-arm64.zip" {
		t.Fatalf("expected darwin asset name to be preserved, got %q", info.AssetName)
	}
}

func TestBuildUpdateInfoDarwinWithoutInstallSupportUsesDownloadPage(t *testing.T) {
	release := githubRelease{
		TagName: "v1.2.3",
		HTMLURL: "https://github.com/runrunrain/amagi-codebox/releases/tag/v1.2.3",
		Assets: []githubReleaseAsset{
			{Name: "amagi-codebox-v1.2.3-darwin-arm64.zip", BrowserDownloadURL: "https://example.com/darwin.zip", Size: 2048},
		},
	}

	caps := platform.PlatformCapabilities{OS: "darwin", Arch: "arm64", PlatformID: "darwin-arm64"}
	info, err := buildUpdateInfo("v1.2.2", caps, release)
	if err != nil {
		t.Fatalf("buildUpdateInfo returned error: %v", err)
	}
	if info.UpdateAction != updateActionOpenDownloadPage {
		t.Fatalf("expected open-download-page action, got %q", info.UpdateAction)
	}
	if info.DownloadURL != release.HTMLURL {
		t.Fatalf("expected download url to point to release page, got %q", info.DownloadURL)
	}
}

func TestBuildUpdateInfoAllowsDarwinReleasePageWithoutAsset(t *testing.T) {
	release := githubRelease{
		TagName: "v1.2.3",
		HTMLURL: "https://github.com/runrunrain/amagi-codebox/releases/tag/v1.2.3",
	}

	info, err := buildUpdateInfo("v1.2.2", platform.PlatformCapabilities{OS: "darwin", Arch: "arm64", PlatformID: "darwin-arm64"}, release)
	if err != nil {
		t.Fatalf("expected darwin release page fallback, got error: %v", err)
	}
	if info.DownloadURL != release.HTMLURL {
		t.Fatalf("expected release page fallback, got %q", info.DownloadURL)
	}
	if info.AssetName != "" {
		t.Fatalf("expected empty asset name when no darwin asset exists, got %q", info.AssetName)
	}
}

func TestBuildUpdateInfoDarwinArm64RequiresAssetForInstall(t *testing.T) {
	release := githubRelease{
		TagName: "v1.2.3",
		HTMLURL: "https://github.com/runrunrain/amagi-codebox/releases/tag/v1.2.3",
	}

	caps := platform.PlatformCapabilities{
		OS:                     "darwin",
		Arch:                   "arm64",
		PlatformID:             "darwin-arm64",
		UpdateInstallSupported: true,
	}

	_, err := buildUpdateInfo("v1.2.2", caps, release)
	if err == nil {
		t.Fatal("expected error when darwin arm64 install is requested but no asset exists")
	}
}

func TestBuildUpdateInfoRequiresWindowsAssetForInstall(t *testing.T) {
	release := githubRelease{TagName: "v1.2.3", HTMLURL: "https://github.com/runrunrain/amagi-codebox/releases/tag/v1.2.3"}

	_, err := buildUpdateInfo("v1.2.2", platform.PlatformCapabilities{OS: "windows", Arch: "amd64", PlatformID: "windows", UpdateInstallSupported: true}, release)
	if err == nil {
		t.Fatal("expected windows install flow to require matching asset")
	}
}

func TestBuildUpdateInfoUsesWindowsAssetForInstall(t *testing.T) {
	release := githubRelease{
		TagName: "v1.2.3",
		HTMLURL: "https://github.com/runrunrain/amagi-codebox/releases/tag/v1.2.3",
		Assets: []githubReleaseAsset{
			{Name: "amagi-codebox-v1.2.3-windows-amd64.zip", BrowserDownloadURL: "https://example.com/windows.zip", Size: 1024},
		},
	}

	info, err := buildUpdateInfo("v1.2.2", platform.PlatformCapabilities{OS: "windows", Arch: "amd64", PlatformID: "windows", UpdateInstallSupported: true}, release)
	if err != nil {
		t.Fatalf("buildUpdateInfo returned error: %v", err)
	}
	if info.UpdateAction != updateActionInstall {
		t.Fatalf("expected install action, got %q", info.UpdateAction)
	}
	if info.DownloadURL != "https://example.com/windows.zip" {
		t.Fatalf("expected install download url to use asset url, got %q", info.DownloadURL)
	}
}

func TestBuildUpdateInfoLinuxUsesDownloadPage(t *testing.T) {
	release := githubRelease{
		TagName: "v1.2.3",
		HTMLURL: "https://github.com/runrunrain/amagi-codebox/releases/tag/v1.2.3",
	}

	caps := platform.PlatformCapabilities{OS: "linux", Arch: "amd64", PlatformID: "linux"}
	info, err := buildUpdateInfo("v1.2.2", caps, release)
	if err != nil {
		t.Fatalf("buildUpdateInfo returned error: %v", err)
	}
	if info.UpdateAction != updateActionOpenDownloadPage {
		t.Fatalf("expected open-download-page for linux, got %q", info.UpdateAction)
	}
}

// --- HTTP fallback and retry tests ---

func TestCheckForUpdateToken403FallbackNoAuth(t *testing.T) {
	var authHeaders []string
	service := NewService("v1.2.2", nil)
	service.capabilities = platform.PlatformCapabilities{OS: "windows", Arch: "amd64", PlatformID: "windows", UpdateInstallSupported: true}
	service.SetToken("invalid-token")
	service.httpClient = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		authHeaders = append(authHeaders, req.Header.Get("Authorization"))
		if len(authHeaders) == 1 {
			return testHTTPResponse(http.StatusForbidden, `{"message":"bad credentials"}`), nil
		}
		return testHTTPResponse(http.StatusOK, `{"tag_name":"v1.2.3","html_url":"https://github.com/runrunrain/amagi-codebox/releases/tag/v1.2.3","assets":[{"name":"amagi-codebox-v1.2.3-windows-amd64.zip","browser_download_url":"https://example.com/windows.zip","size":7}]}`), nil
	})

	info, err := service.CheckForUpdate()
	if err != nil {
		t.Fatalf("CheckForUpdate returned error: %v", err)
	}
	if !info.HasUpdate || info.LatestVersion != "1.2.3" {
		t.Fatalf("unexpected update info: %+v", info)
	}
	if len(authHeaders) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(authHeaders))
	}
	if authHeaders[0] == "" {
		t.Fatal("expected first request to include Authorization")
	}
	if authHeaders[1] != "" {
		t.Fatal("expected fallback request to omit Authorization")
	}
}

func TestDownloadZipInitialEOFRetry(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "update.zip")
	var requests int
	service := NewService("v1.2.2", nil)
	service.httpClient = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return nil, io.EOF
		}
		return testHTTPResponse(http.StatusOK, "zip-content"), nil
	})

	if err := service.downloadZip("https://github.com/runrunrain/amagi-codebox/releases/download/v1.2.3/amagi-codebox-v1.2.3-windows-amd64.zip", targetPath, nil); err != nil {
		t.Fatalf("downloadZip returned error: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected 2 requests, got %d", requests)
	}
	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "zip-content" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

func TestDownloadZipBodyEOFRetryCleansPartialFile(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "update.zip")
	var requests int
	service := NewService("v1.2.2", nil)
	service.httpClient = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: int64(len("partial-final")),
				Body:          &failingReadCloser{data: []byte("partial"), err: io.ErrUnexpectedEOF},
				Header:        make(http.Header),
			}, nil
		}
		return testHTTPResponse(http.StatusOK, "final"), nil
	})

	if err := service.downloadZip("https://github.com/runrunrain/amagi-codebox/releases/download/v1.2.3/amagi-codebox-v1.2.3-windows-amd64.zip", targetPath, nil); err != nil {
		t.Fatalf("downloadZip returned error: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected 2 requests, got %d", requests)
	}
	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "final" {
		t.Fatalf("expected partial file to be overwritten, got %q", string(content))
	}
}

func TestDownloadZip403FallbackNoAuth(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "update.zip")
	var authHeaders []string
	service := NewService("v1.2.2", nil)
	service.SetToken("invalid-token")
	service.httpClient = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		authHeaders = append(authHeaders, req.Header.Get("Authorization"))
		if len(authHeaders) == 1 {
			return testHTTPResponse(http.StatusForbidden, "forbidden"), nil
		}
		return testHTTPResponse(http.StatusOK, "zip-content"), nil
	})

	if err := service.downloadZip("https://api.github.com/repos/runrunrain/amagi-codebox/releases/assets/123", targetPath, nil); err != nil {
		t.Fatalf("downloadZip returned error: %v", err)
	}
	if len(authHeaders) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(authHeaders))
	}
	if authHeaders[0] == "" {
		t.Fatal("expected first asset API request to include Authorization")
	}
	if authHeaders[1] != "" {
		t.Fatal("expected fallback request to omit Authorization")
	}
}

func TestDownloadZipPublicBrowserURLDoesNotSendAuthorization(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "update.zip")
	var authHeader string
	service := NewService("v1.2.2", nil)
	service.SetToken("configured-token")
	service.httpClient = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		authHeader = req.Header.Get("Authorization")
		return testHTTPResponse(http.StatusOK, "zip-content"), nil
	})

	if err := service.downloadZip("https://github.com/runrunrain/amagi-codebox/releases/download/v1.2.3/amagi-codebox-v1.2.3-windows-amd64.zip", targetPath, nil); err != nil {
		t.Fatalf("downloadZip returned error: %v", err)
	}
	if authHeader != "" {
		t.Fatal("expected public browser download URL to omit Authorization")
	}
}

func TestDownloadZipRemovesPartialFileAfterExhaustedBodyEOF(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "update.zip")
	service := NewService("v1.2.2", nil)
	service.httpClient = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: int64(len("partial")),
			Body:          &failingReadCloser{data: []byte("partial"), err: io.ErrUnexpectedEOF},
			Header:        make(http.Header),
		}, nil
	})

	err := service.downloadZip("https://github.com/runrunrain/amagi-codebox/releases/download/v1.2.3/amagi-codebox-v1.2.3-windows-amd64.zip", targetPath, nil)
	if err == nil {
		t.Fatal("expected downloadZip to fail after retry exhaustion")
	}
	if !strings.Contains(err.Error(), "failed after retries") {
		t.Fatalf("expected retry exhaustion context, got: %v", err)
	}
	if _, statErr := os.Stat(targetPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected partial file to be removed, stat error: %v", statErr)
	}
}

// --- App bundle location tests ---

func TestLocateAppBundleFromPathValidBundle(t *testing.T) {
	tmpDir := t.TempDir()
	appDir := filepath.Join(tmpDir, "amagi-codebox.app", "Contents", "MacOS")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exePath := filepath.Join(appDir, "amagi-codebox")
	if err := os.WriteFile(exePath, []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := locateAppBundleFromPath(exePath)
	if err != nil {
		t.Fatalf("locateAppBundleFromPath returned error: %v", err)
	}
	// EvalSymlinks may resolve /var to /private/var on macOS
	expected, err := filepath.EvalSymlinks(filepath.Join(tmpDir, "amagi-codebox.app"))
	if err != nil {
		t.Fatal(err)
	}
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

func TestLocateAppBundleFromPathRejectsNonBundle(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "bin", "amagi-codebox")
	if err := os.MkdirAll(filepath.Dir(exePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exePath, []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := locateAppBundleFromPath(exePath)
	if err == nil {
		t.Fatal("expected error for non-bundle path")
	}
	if !strings.Contains(err.Error(), "not running from a .app bundle") {
		t.Fatalf("expected .app bundle error, got: %v", err)
	}
}

func TestLocateAppBundleFromPathRejectsMissingAppSuffix(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "myapp", "Contents", "MacOS", "amagi-codebox")
	if err := os.MkdirAll(filepath.Dir(exePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exePath, []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := locateAppBundleFromPath(exePath)
	if err == nil {
		t.Fatal("expected error for directory without .app suffix")
	}
	if !strings.Contains(err.Error(), "not a .app bundle") {
		t.Fatalf("expected .app suffix error, got: %v", err)
	}
}

// --- Zip slip and entry filtering tests ---

func TestShouldSkipZipEntry(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"__MACOSX", true},
		{"__MACOSX/._Info.plist", true},
		{"amagi-codebox.app/Contents/Info.plist", false},
		{"amagi-codebox.app/Contents/Resources/.DS_Store", true},
		{".DS_Store", true},
		{"amagi-codebox.app/Contents/MacOS/amagi-codebox", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := shouldSkipZipEntry(tc.name)
			if result != tc.expected {
				t.Fatalf("shouldSkipZipEntry(%q) = %v, want %v", tc.name, result, tc.expected)
			}
		})
	}
}

func TestIsWithinDirRejectsZipSlip(t *testing.T) {
	tests := []struct {
		path string
		dir  string
		ok   bool
	}{
		{"/tmp/staging/amagi-codebox.app/Contents/Info.plist", "/tmp/staging", true},
		{"/tmp/staging/amagi-codebox.app", "/tmp/staging", true},
		{"/tmp/staging", "/tmp/staging", true},
		{"/tmp/evil", "/tmp/staging", false},
		{"/tmp/staging/../../etc/passwd", "/tmp/staging", false},
		{"/tmp/staging/../evil", "/tmp/staging", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := isWithinDir(tc.path, tc.dir)
			if result != tc.ok {
				t.Fatalf("isWithinDir(%q, %q) = %v, want %v", tc.path, tc.dir, result, tc.ok)
			}
		})
	}
}

func TestExtractAppBundleFromZipRejectsZipSlip(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")

	// Create a zip with a zip slip entry
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	header := &zip.FileHeader{Name: "../../etc/evil"}
	header.SetMode(0o644)
	writer, err := w.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	writer.Write([]byte("evil"))
	w.Close()
	f.Close()

	stagingDir := filepath.Join(tmpDir, "staging")
	os.MkdirAll(stagingDir, 0o755)

	err = extractAppBundleFromZip(zipPath, stagingDir)
	if err == nil {
		t.Fatal("expected zip slip error")
	}
	if !strings.Contains(err.Error(), "zip slip") {
		t.Fatalf("expected zip slip error message, got: %v", err)
	}
}

// --- Bundle validation tests ---

func TestValidateAppBundleValid(t *testing.T) {
	tmpDir := t.TempDir()
	appPath := filepath.Join(tmpDir, "amagi-codebox.app")
	createValidAppBundle(t, appPath)

	if err := validateAppBundle(appPath); err != nil {
		t.Fatalf("validateAppBundle returned error for valid bundle: %v", err)
	}
}

func TestValidateAppBundleMissingInfoPlist(t *testing.T) {
	tmpDir := t.TempDir()
	appPath := filepath.Join(tmpDir, "amagi-codebox.app")
	macosDir := filepath.Join(appPath, "Contents", "MacOS")
	if err := os.MkdirAll(macosDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exePath := filepath.Join(macosDir, "amagi-codebox")
	if err := os.WriteFile(exePath, []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := validateAppBundle(appPath)
	if err == nil {
		t.Fatal("expected error for missing Info.plist")
	}
	if !strings.Contains(err.Error(), "Info.plist") {
		t.Fatalf("expected Info.plist error, got: %v", err)
	}
}

func TestValidateAppBundleMissingExecutable(t *testing.T) {
	tmpDir := t.TempDir()
	appPath := filepath.Join(tmpDir, "amagi-codebox.app")
	contentsDir := filepath.Join(appPath, "Contents")
	if err := os.MkdirAll(contentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentsDir, "Info.plist"), []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := validateAppBundle(appPath)
	if err == nil {
		t.Fatal("expected error for missing executable")
	}
	if !strings.Contains(err.Error(), "MacOS/amagi-codebox") {
		t.Fatalf("expected executable error, got: %v", err)
	}
}

func TestValidateAppBundleNotExecutable(t *testing.T) {
	tmpDir := t.TempDir()
	appPath := filepath.Join(tmpDir, "amagi-codebox.app")
	createValidAppBundleWithPerm(t, appPath, 0o644)

	err := validateAppBundle(appPath)
	if err == nil {
		t.Fatal("expected error for non-executable binary")
	}
	if !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("expected not-executable error, got: %v", err)
	}
}

func TestValidateAppBundleNotDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	appPath := filepath.Join(tmpDir, "amagi-codebox.app")
	if err := os.WriteFile(appPath, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := validateAppBundle(appPath)
	if err == nil {
		t.Fatal("expected error for non-directory path")
	}
	if !strings.Contains(err.Error(), "expected directory") {
		t.Fatalf("expected directory error, got: %v", err)
	}
}

func TestValidateAppBundleMissingAppSuffix(t *testing.T) {
	tmpDir := t.TempDir()
	appPath := filepath.Join(tmpDir, "amagi-codebox")
	createValidAppBundle(t, appPath)

	err := validateAppBundle(appPath)
	if err == nil {
		t.Fatal("expected error for missing .app suffix")
	}
	if !strings.Contains(err.Error(), ".app suffix") {
		t.Fatalf("expected .app suffix error, got: %v", err)
	}
}

// --- Helper script tests ---

func TestHelperScriptIsStaticAndParameterized(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "helper.sh")

	if err := writeHelperScript(scriptPath); err != nil {
		t.Fatalf("writeHelperScript returned error: %v", err)
	}

	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read helper script: %v", err)
	}

	script := string(content)

	// The script must NOT contain any embedded real filesystem paths.
	// Only $1/$2/$3/$4 should carry paths at runtime.
	forbidden := []string{
		"/Applications/",
		"/Users/",
		"/tmp/staged",
		"/tmp/staging",
		"/usr/",
		"/var/",
	}
	for _, f := range forbidden {
		if strings.Contains(script, f) {
			t.Errorf("helper script must not contain embedded path %q; paths should be passed as $1-$4 arguments", f)
		}
	}

	// The script must use positional parameters for all paths
	required := []string{
		`"$1"`, // PID
		`"$2"`, // CURRENT_APP
		`"$3"`, // STAGED_APP
		`"$4"`, // STAGING_DIR
	}
	for _, r := range required {
		if !strings.Contains(script, r) {
			t.Errorf("helper script missing positional parameter reference %s", r)
		}
	}
}

func TestHelperScriptContainsSafetyAssertions(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "helper.sh")

	if err := writeHelperScript(scriptPath); err != nil {
		t.Fatalf("writeHelperScript returned error: %v", err)
	}

	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read helper script: %v", err)
	}

	script := string(content)

	// Safety assertions that must be present
	assertions := []struct {
		desc, substr string
	}{
		{"non-empty CURRENT_APP check", `CURRENT_APP is empty`},
		{"non-empty STAGED_APP check", `STAGED_APP is empty`},
		{"non-empty STAGING_DIR check", `STAGING_DIR is empty`},
		{"CURRENT_APP .app suffix check", `CURRENT_APP does not end with .app`},
		{"OLD_APP .app.old suffix check", `OLD_APP does not end with .app.old`},
		{"STAGED_APP .app suffix check", `STAGED_APP does not end with .app`},
		{"STAGING_DIR prefix check", `does not have expected prefix`},
		{".amagi-codebox-update-staging- prefix", `.amagi-codebox-update-staging-`},
		{"parent dir check OLD vs CURRENT", `OLD_APP parent differs from CURRENT_APP parent`},
		{"parent dir check STAGING vs CURRENT", `STAGING_DIR parent differs from CURRENT_APP parent`},
	}

	for _, a := range assertions {
		if !strings.Contains(script, a.substr) {
			t.Errorf("helper script missing safety assertion: %s (expected %q)", a.desc, a.substr)
		}
	}
}

func TestHelperScriptContainsRollbackAndOpen(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "helper.sh")

	if err := writeHelperScript(scriptPath); err != nil {
		t.Fatalf("writeHelperScript returned error: %v", err)
	}

	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read helper script: %v", err)
	}

	script := string(content)

	checks := []struct {
		desc, substr string
	}{
		{"wait for pid", "kill -0"},
		{"remove old backup", "rm -rf"},
		{"move current to old", `.old`},
		{"move staged to current", "mv"},
		{"xattr clear quarantine", "xattr -cr"},
		{"open new app", "open"},
		{"rollback on failure", "rollback"},
		{"logging", "log"},
		{"shebang", "#!/bin/sh"},
	}

	for _, c := range checks {
		if !strings.Contains(script, c.substr) {
			t.Errorf("helper script missing %s (expected %q)", c.desc, c.substr)
		}
	}

	// Verify script is executable
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("helper script is not executable")
	}
}

func TestHelperScriptIsDeterministicAcrossWrites(t *testing.T) {
	tmpDir := t.TempDir()
	path1 := filepath.Join(tmpDir, "helper1.sh")
	path2 := filepath.Join(tmpDir, "helper2.sh")

	if err := writeHelperScript(path1); err != nil {
		t.Fatal(err)
	}
	if err := writeHelperScript(path2); err != nil {
		t.Fatal(err)
	}

	c1, err := os.ReadFile(path1)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatal(err)
	}

	if string(c1) != string(c2) {
		t.Fatal("writeHelperScript must produce identical output on every call (static content)")
	}
}

func TestHelperScriptNoSubshellOrBacktickPathEmbedding(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "helper.sh")

	if err := writeHelperScript(scriptPath); err != nil {
		t.Fatalf("writeHelperScript returned error: %v", err)
	}

	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read helper script: %v", err)
	}

	script := string(content)

	// Check no backtick command substitution that could embed paths
	if idx := strings.Index(script, "`"); idx >= 0 {
		t.Errorf("helper script contains backtick at position %d, which may indicate embedded command substitution", idx)
	}

	// Check no $() command substitution that embeds filesystem paths
	// The only $() usage should be $(date ...) and $(basename ...) and $(dirname ...) which are safe
	lines := strings.Split(script, "\n")
	for _, line := range lines {
		if strings.Contains(line, "$(") {
			trimmed := strings.TrimSpace(line)
			// Allowed: $(date ...), $(basename ...), $(dirname ...)
			if !strings.Contains(trimmed, "$(date") &&
				!strings.Contains(trimmed, "$(basename") &&
				!strings.Contains(trimmed, "$(dirname") {
				t.Errorf("unexpected $() usage in helper script: %s", trimmed)
			}
		}
	}
}

func TestRandomHexProducesUniqueValues(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		v := randomHex(8)
		if len(v) != 8 {
			t.Fatalf("randomHex(8) returned %d chars, want 8", len(v))
		}
		if seen[v] {
			t.Fatalf("randomHex produced duplicate value: %s", v)
		}
		seen[v] = true
	}
}

func TestStagingDirPrefixConstant(t *testing.T) {
	if !strings.HasPrefix(stagingDirPrefix, ".amagi-codebox-update-staging-") {
		t.Fatalf("stagingDirPrefix must start with .amagi-codebox-update-staging-, got %q", stagingDirPrefix)
	}
}

// --- Helpers ---

func createValidAppBundle(t *testing.T, appPath string) {
	t.Helper()
	createValidAppBundleWithPerm(t, appPath, 0o755)
}

func createValidAppBundleWithPerm(t *testing.T, appPath string, exePerm os.FileMode) {
	t.Helper()
	contentsDir := filepath.Join(appPath, "Contents")
	macosDir := filepath.Join(contentsDir, "MacOS")
	if err := os.MkdirAll(macosDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentsDir, "Info.plist"), []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macosDir, "amagi-codebox"), []byte("#!/bin/sh"), exePerm); err != nil {
		t.Fatal(err)
	}
}
