package updater

import (
	"amagi-codebox/internal/platform"
	"fmt"
	pathpkg "path"
)

const (
	windowsAmd64ReleaseAssetPattern = "amagi-codebox-*-windows-amd64.zip"
	darwinArm64ReleaseAssetPattern  = "amagi-codebox-*-darwin-arm64.zip"
)

func findReleaseAsset(capabilities platform.PlatformCapabilities, assets []githubReleaseAsset) (*githubReleaseAsset, error) {
	switch capabilities.OS {
	case "windows":
		return findWindowsAsset(assets)
	case "darwin":
		return findDarwinArm64Asset(assets)
	default:
		return nil, fmt.Errorf("release asset selection is not implemented for platform %s", capabilities.PlatformID)
	}
}

func findWindowsAsset(assets []githubReleaseAsset) (*githubReleaseAsset, error) {
	return findAssetByPattern(assets, windowsAmd64ReleaseAssetPattern, "windows release asset not found")
}

func findDarwinArm64Asset(assets []githubReleaseAsset) (*githubReleaseAsset, error) {
	return findAssetByPattern(assets, darwinArm64ReleaseAssetPattern, "darwin arm64 release asset not found")
}

func findAssetByPattern(assets []githubReleaseAsset, pattern string, missingMessage string) (*githubReleaseAsset, error) {
	for i := range assets {
		matched, err := pathpkg.Match(pattern, assets[i].Name)
		if err != nil {
			return nil, fmt.Errorf("match release asset: %w", err)
		}
		if matched {
			return &assets[i], nil
		}
	}
	return nil, fmt.Errorf("%s", missingMessage)
}
