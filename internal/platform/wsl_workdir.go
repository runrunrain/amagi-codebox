package platform

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const wslWorkDirOK = "__AMAGI_WORKDIR_OK__"

// WSLValidateWorkDirContext checks the actual distro before ConPTY is created.
// wsl.exe can exit zero after a relay chdir failure, so a successful exit alone
// is not evidence that the directory was usable. Require a marker emitted only
// after cd succeeds. Do not cache existence or guess mappings for stale paths.
func WSLValidateWorkDirContext(ctx context.Context, distro, workDir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	distro = strings.TrimSpace(distro)
	if distro == "" {
		return fmt.Errorf("WSL 工作目录校验失败：没有可用的发行版")
	}
	script, err := buildWSLWorkDirCheck(workDir)
	if err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, probeErr := wslScriptRunnerContext(probeCtx, distro, script)
	if probeCtx.Err() != nil {
		return fmt.Errorf("WSL %s 工作目录 %q 校验未完成: %w", distro, workDir, probeCtx.Err())
	}
	if probeErr == nil {
		for _, line := range strings.Split(decodeWSLListOutput(out), "\n") {
			if strings.TrimSpace(line) == wslWorkDirOK {
				return nil
			}
		}
	}
	// Keep the original requested directory visible, including macOS /Users
	// paths imported in favourites. Never silently run in a different project.
	return fmt.Errorf("WSL %s 中工作目录 %q 不存在、不可访问或无法校验；已拒绝启动。请重新选择该发行版中存在的目录，或使用本机已存在的 Windows 盘符路径；迁移自 macOS 的 /Users/... 不会自动映射", distro, workDir)
}

func buildWSLWorkDirCheck(workDir string) (string, error) {
	if strings.ContainsAny(workDir, "\x00\r\n") {
		return "", fmt.Errorf("WSL 工作目录含不支持的控制字符")
	}
	// No eval: only bare ~ and ~/ are expanded; metacharacters in a project
	// name stay literal. wslpath honours the distro's real automount settings.
	return "p=" + wslShSingleQuote(workDir) + `
case "$p" in
  '') p="$HOME" ;;
  '~') p="$HOME" ;;
  '~/'*) p="$HOME/${p#\~/}" ;;
  [A-Za-z]:*|\\\\*) p=$(wslpath -u "$p") || exit 41 ;;
esac
case "$p" in /*) ;; *) exit 42 ;; esac
[ -d "$p" ] && [ -x "$p" ] || exit 43
CDPATH= cd -P "$p" || exit 44
printf '\n` + wslWorkDirOK + `\n'
`, nil
}
