//go:build !windows

package v2rayn

import (
	"os"
	"path/filepath"
)

const bundledCoreFile = "bin/sing_box/sing-box"

func installRoots() []string {
	var roots []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots,
			filepath.Join(home, "Library/Application Support/v2rayN"),
			filepath.Join(home, ".local/share/v2rayN"),
			filepath.Join(home, ".config/v2rayN"),
		)
	}
	for _, name := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME"} {
		if base := os.Getenv(name); filepath.IsAbs(base) {
			roots = append(roots, filepath.Join(base, "v2rayN"))
		}
	}
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Dir(exe))
	}
	return append(roots, "/Applications/v2rayN.app/Contents/Resources")
}
