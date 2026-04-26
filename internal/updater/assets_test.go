package updater

import (
	"amagi-codebox/internal/platform"
	"testing"
)

func TestFindReleaseAssetPrefersDarwinArm64Pattern(t *testing.T) {
	assets := []githubReleaseAsset{
		{Name: "amagi-codebox-v1.2.3-windows-amd64.zip", BrowserDownloadURL: "https://example.com/windows.zip"},
		{Name: "amagi-codebox-v1.2.3-darwin-arm64.zip", BrowserDownloadURL: "https://example.com/darwin.zip"},
	}

	asset, err := findReleaseAsset(platform.PlatformCapabilities{OS: "darwin", Arch: "arm64", PlatformID: "darwin-arm64"}, assets)
	if err != nil {
		t.Fatalf("findReleaseAsset returned error: %v", err)
	}
	if asset.Name != "amagi-codebox-v1.2.3-darwin-arm64.zip" {
		t.Fatalf("expected darwin arm64 asset, got %q", asset.Name)
	}
}
