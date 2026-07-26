//go:build !windows

package opencodeplugin

import "net/url"

func fileURLPath(spec string) (string, error) {
	parsed, err := url.Parse(spec)
	if err != nil {
		return "", err
	}
	return url.PathUnescape(parsed.Path)
}
