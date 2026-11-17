package platform

// wsl_unc.go — Linux 路径 → Windows UNC（\\wsl.localhost\<distro>\…）翻译。
//
// 用途：WSL 会话的 Claude Code Web 平面（claude-webui design §5.1 WSL 扩展）。
// distro 内进程（claude）把转录写到发行版文件系统（ext4）的 Linux 路径下；
// CodeBox（Windows 进程）的数据面 server 需要从宿主侧读到同一份文件——
// 经 WSL 的 9P 文件共享（\\wsl.localhost\<distro>\...）以只读语义访问。
// tailer 是 Stat 轮询（500ms）而非 fsnotify——inotify 事件不经 9P 传播，
// 轮询语义天然兼容 UNC 根（design §5.2 既有机制，无改动）。
//
// 纯字符串翻译：不做存在性探测（调用方按 fail-closed 自行 Stat/降级）；
// 非 Windows 宿主不会走到本函数的消费链（BootstrapWSL 仅 Windows 解析器
// 产出），字符串拼接本身跨平台无害（macOS CI 可编译可单测）。

import (
	"fmt"
	"strings"
)

// wslUNCSharePrefix 是 WSL 发行版文件共享的 UNC 前缀（Win10 2004+ 的
// wsl.localhost 形态；旧 \\wsl$ 形态仍可用但 wsl.localhost 是当前规范名，
// 且与 distro 名含 $ 等字符时的歧义隔离更好）。
const wslUNCSharePrefix = `\\wsl.localhost\`

// WSLToUNCPath 把 distro 内绝对 Linux 路径翻译为宿主侧 UNC 路径：
// ("Ubuntu-24.04", "/home/u/.claude") → `\\wsl.localhost\Ubuntu-24.04\home\u\.claude`。
//
// 规则（fail closed）：
//   - distro 为空、linuxPath 非绝对路径（无前导 /）、或含内嵌 NUL → 错误；
//   - 其余逐段翻译：'/' → '\'；不归一大小写、不去重分隔符（Linux 路径
//     段名原样保留， distro 名大小写敏感按 WSL 注册名原样拼接）。
//
// 返回值可直接交给 os.Stat / os.ReadFile 等 Windows API（9P 重定向由
// Windows 文件系统层接管）。
func WSLToUNCPath(distro, linuxPath string) (string, error) {
	distro = strings.TrimSpace(distro)
	if distro == "" {
		return "", fmt.Errorf("WSL UNC 翻译失败：发行版名为空（路径 %q）", linuxPath)
	}
	if strings.ContainsRune(distro, '\\') || strings.ContainsRune(distro, '/') {
		return "", fmt.Errorf("WSL UNC 翻译失败：发行版名 %q 含路径分隔符", distro)
	}
	if !strings.HasPrefix(linuxPath, "/") {
		return "", fmt.Errorf("WSL UNC 翻译失败：路径 %q 不是 distro 内绝对 Linux 路径", linuxPath)
	}
	if strings.ContainsRune(linuxPath, 0) {
		return "", fmt.Errorf("WSL UNC 翻译失败：路径含内嵌 NUL")
	}
	return wslUNCSharePrefix + distro + `\` + strings.ReplaceAll(strings.TrimPrefix(linuxPath, "/"), "/", `\`), nil
}
