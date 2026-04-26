package updater

import (
	"amagi-codebox/internal/platform"
	"testing"
)

func TestBuildUpdateInfoUsesDarwinDownloadPagePolicy(t *testing.T) {
	release := githubRelease{
		TagName:     "v1.2.3",
		HTMLURL:     "https://github.com/runrunrain/amagi-codebox/releases/tag/v1.2.3",
		Body:        "darwin release",
		PublishedAt: "2026-04-26T12:00:00Z",
		Assets: []githubReleaseAsset{
			{Name: "amagi-codebox-v1.2.3-darwin-arm64.zip", BrowserDownloadURL: "https://example.com/darwin.zip", Size: 2048},
		},
	}

	info, err := buildUpdateInfo("v1.2.2", platform.PlatformCapabilities{OS: "darwin", Arch: "arm64", PlatformID: "darwin-arm64"}, release)
	if err != nil {
		t.Fatalf("buildUpdateInfo returned error: %v", err)
	}

	if !info.HasUpdate {
		t.Fatal("expected darwin update to be detected")
	}
	if info.UpdateAction != updateActionOpenDownloadPage {
		t.Fatalf("expected open-download-page action, got %q", info.UpdateAction)
	}
	if info.DownloadURL != release.HTMLURL {
		t.Fatalf("expected download url to point to release page, got %q", info.DownloadURL)
	}
	if info.AssetURL != "https://example.com/darwin.zip" {
		t.Fatalf("expected darwin asset url to be preserved, got %q", info.AssetURL)
	}
	if info.AssetName != "amagi-codebox-v1.2.3-darwin-arm64.zip" {
		t.Fatalf("expected darwin asset name to be preserved, got %q", info.AssetName)
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
