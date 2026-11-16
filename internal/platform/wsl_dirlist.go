package platform

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	wslDirListNoDirMarker  = "__AMAGI_NODIR__"
	wslDirListOKMarker     = "__AMAGI_DIRLIST_OK__"
	wslDirListDeniedMarker = "__AMAGI_DIRLIST_DENIED__"
	wslDirListScriptCap    = 600
)

// WSLListSubdirs enumerates one level inside the captured target distro. A
// positive NUL-framed marker distinguishes an empty directory from wsl.exe's
// relay failures (which can exit zero). NUL framing prevents filename bytes
// from forging extra entries; control-byte names are filtered before counting
// the script cap so safe later entries are not silently hidden. No shell eval.
func WSLListSubdirs(distro, linuxRoot string) ([]string, bool, error) {
	return wslListSubdirsForOS(runtime.GOOS, distro, linuxRoot)
}

func wslListSubdirsForOS(osName, distro, linuxRoot string) ([]string, bool, error) {
	return wslListSubdirsContextForOS(context.Background(), osName, distro, linuxRoot)
}

func wslListSubdirsContextForOS(ctx context.Context, osName, distro, linuxRoot string) ([]string, bool, error) {
	distro = strings.TrimSpace(distro)
	if osName != "windows" {
		return nil, false, fmt.Errorf("wsl dirlist: 仅 Windows 宿主可用（当前 %s）", osName)
	}
	if distro == "" || !strings.HasPrefix(linuxRoot, "/") || strings.ContainsRune(linuxRoot, 0) {
		return nil, false, fmt.Errorf("wsl dirlist: 非法参数 distro=%q root=%q", distro, linuxRoot)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	raw, err := wslScriptRunnerContext(ctx, distro, buildWSLDirListScript(linuxRoot))
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	if err != nil {
		return nil, false, fmt.Errorf("wsl.exe 目录探测失败（发行版不可达）: %w", err)
	}
	// Linux command stdout is UTF-8, not wsl -l's UTF-16. Do not send this
	// binary protocol through decodeWSLListOutput, which interprets NULs.
	start := bytes.IndexByte(raw, 0)
	if start < 0 {
		return nil, false, fmt.Errorf("WSL 目录探测未取得成功标记，发行版不可达或命令未运行")
	}
	end := bytes.IndexByte(raw[start+1:], 0)
	if end < 0 {
		return nil, false, fmt.Errorf("WSL 目录探测输出不完整")
	}
	switch string(raw[start+1 : start+1+end]) {
	case wslDirListNoDirMarker:
		return nil, false, nil
	case wslDirListDeniedMarker:
		return nil, false, fmt.Errorf("WSL 目录不可读或不可访问: %s", linuxRoot)
	case wslDirListOKMarker:
	default:
		return nil, false, fmt.Errorf("WSL 目录探测未取得成功标记，发行版不可达或命令未运行")
	}
	payload := raw[start+end+2:]
	if len(payload) == 0 {
		return []string{}, true, nil
	}
	if payload[len(payload)-1] != 0 {
		return nil, false, fmt.Errorf("WSL 目录探测输出不完整")
	}
	names := []string{}
	for _, name := range strings.Split(string(payload[:len(payload)-1]), "\x00") {
		if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
			return nil, false, fmt.Errorf("WSL 目录探测包含非法子目录名")
		}
		names = append(names, name)
		if len(names) > wslDirListScriptCap {
			return nil, false, fmt.Errorf("WSL 目录探测条数超限")
		}
	}
	return names, true, nil
}

func buildWSLDirListScript(root string) string {
	return "LC_ALL=C\nexport LC_ALL\nr=" + wslShSingleQuote(root) + `
if [ ! -d "$r" ]; then printf '\000` + wslDirListNoDirMarker + `\000'; exit 0; fi
if [ ! -r "$r" ] || [ ! -x "$r" ]; then printf '\000` + wslDirListDeniedMarker + `\000'; exit 0; fi
case "$r" in /) g= ;; *) g=$r ;; esac
printf '\000` + wslDirListOKMarker + `\000'
n=0
for f in "$g"/*; do
  [ -d "$f" ] || continue
  name=${f##*/}
  # Count only paths safe for terminal insertion before applying the cap.
  # Otherwise filtered control-byte names can hide later valid directories
  # while the host incorrectly reports truncated=false.
  case "$name" in *[[:cntrl:]]*) continue ;; esac
  printf '%s\000' "$name"
  n=$((n+1))
  [ "$n" -lt ` + strconv.Itoa(wslDirListScriptCap) + ` ] || break
done
`
}
