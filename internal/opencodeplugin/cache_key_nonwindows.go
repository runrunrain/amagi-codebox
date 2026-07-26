//go:build !windows

package opencodeplugin

func openCodeCacheKey(spec string) string {
	return spec
}
