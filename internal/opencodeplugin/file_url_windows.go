//go:build windows

package opencodeplugin

import (
	"net/url"
	"path/filepath"
	"strings"
)

func fileURLPath(spec string) (string, error) {
	parsed, err := url.Parse(spec)
	if err != nil {
		return "", err
	}
	path, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", err
	}
	if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = strings.TrimPrefix(path, "/")
	}
	return filepath.FromSlash(path), nil
}
