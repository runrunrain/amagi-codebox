package paths

// list_directories_wsl.go — WSL 会话的目录树（快捷功能「输入工作路径」）。
//
// WSL 内嵌会话的工作目录在 distro 文件系统内；Windows 侧 ListDirectories
// 对 Linux 形态 root 显式拒绝（LinuxStylePathError，P4-A1/E8），此前 WSL
// 会话的路径选择器只能看到「形态不可用」。本文件提供 distro 内的同类
// 列表，载荷与 ListDirectories 完全同形（directoryListing JSON，前端
// pathPickerModel 无需分叉类型）、同一排序/截断语义（大小写不敏感排序、
// listDirectoriesLimit 上限 + truncated）、同一「空 root = 主目录」约定
// ——区别仅在主目录是 distro 内 $HOME（有限时实测，不猜
// /home/<用户名> 一类字符串映射）。
//
// root 形态契约（由 App facade（app_wsldirpicker.go）归一后进入）：
//   - 空 / ~ / ~/…：distro $HOME（+ 相对后缀），探测失败明确报错；
//   - /…：Linux 绝对路径原样（存在性由目标 distro 判定，缺失报错不降级
//     ——不静默换目录）；
//   - Windows 盘符等其它形态：明确报错（盘符→Linux 转换在 facade 层经
//     distro 内 wslpath 实测完成，见 WSLTranslatePaths）。
//
// 接缝（包级 var，测试注入假实现，不触达真实 wsl.exe）。

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"amagi-codebox/internal/platform"
)

var (
	// wslListSubdirsFn：distro 一层子目录探测（生产 platform.WSLListSubdirs）。
	wslListSubdirsFn = platform.WSLListSubdirs
	// Bound the HOME expansion too: an unavailable distro must not hang the
	// picker before its bounded directory-list probe is even reached.
	wslUserHomeFn = func(distro string) string {
		home, err := platform.WSLResolveDirectoryContext(context.Background(), distro, "~")
		if err != nil {
			return ""
		}
		return home
	}
)

// ListWSLDirectories 列出 distro 内 root 下一层子目录（仅目录、点前缀
// 跳过、大小写不敏感排序、listDirectoriesLimit 上限 + truncated 标志），
// 返回与 ListDirectories 同形的 JSON 字符串。distro 为目标发行版名（启动
// 链同一 DefaultWSLDistro 事实源）。错误语义：
//   - root 在 distro 内不存在/非目录：明确报错（消息含「从主目录开始」
//     指引），不降级 home（不静默换目录）；
//   - distro / wsl.exe 不可达：报「发行版不可达」；
//   - $HOME 探测失败：报无法解析主目录。
func (s *PathsService) ListWSLDirectories(distro, root string) (string, error) {
	if hasWSLPathControl(root) {
		return "", fmt.Errorf("WSL 目录含不支持的控制字符，不能插入终端")
	}
	distro = strings.TrimSpace(distro)
	if distro == "" {
		return "", fmt.Errorf("未指定 WSL 发行版，无法列出目录")
	}
	root, err := resolveWSLListRoot(distro, root)
	if err != nil {
		return "", err
	}
	if hasWSLPathControl(root) {
		return "", fmt.Errorf("WSL 目录含不支持的控制字符，不能插入终端")
	}
	names, exists, err := wslListSubdirsFn(distro, root)
	if err != nil {
		return "", fmt.Errorf("读取 WSL 目录失败（发行版不可达）：%v", err)
	}
	if !exists {
		return "", fmt.Errorf("目录在 WSL 发行版内不存在或不可访问：%s；可点「从主目录开始」回到发行版主目录", root)
	}

	dirs := make([]directoryListingEntry, 0, len(names))
	truncated := false
	for _, name := range names {
		// Linux permits newlines and terminal escapes in filenames. NUL framing
		// preserves them in transport; never offer control bytes for insertion.
		if hasWSLPathControl(name) {
			continue
		}
		if len(dirs) >= listDirectoriesLimit {
			truncated = true
			break
		}
		dirs = append(dirs, directoryListingEntry{Name: name, Path: joinWSLRoot(root, name)})
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})

	out, err := json.Marshal(directoryListing{
		Root:      root,
		Parent:    wslParentOf(root),
		Dirs:      dirs,
		Truncated: truncated,
	})
	if err != nil {
		return "", fmt.Errorf("marshal directory listing: %w", err)
	}
	return string(out), nil
}

// resolveWSLListRoot 归一 root 为 distro 内绝对 Linux 路径：空/~ → $HOME；
// ~/… → $HOME/…；/… 原样（仅去尾部重复分隔符）。其余形态报错（不在此
// 做任何 Windows→Linux 字符串猜测——转换由 facade 经 wslpath 实测完成）。
func resolveWSLListRoot(distro, root string) (string, error) {
	if strings.ContainsRune(root, 0) {
		return "", fmt.Errorf("WSL 目录含无效 NUL 字符")
	}
	switch {
	case root == "" || root == "~":
		return wslHomeOrError(distro)
	case strings.HasPrefix(root, "~/"):
		home, err := wslHomeOrError(distro)
		if err != nil {
			return "", err
		}
		// root[1:] 形如 "/rest"，拼接后即 $HOME/rest。
		return strings.TrimSuffix(home, "/") + root[1:], nil
	}
	if !strings.HasPrefix(root, "/") {
		return "", fmt.Errorf("WSL 目录 root 必须是 Linux 绝对路径（/…、~ 或留空表示主目录），收到 %q", root)
	}
	for len(root) > 1 && strings.HasSuffix(root, "/") {
		root = strings.TrimSuffix(root, "/")
	}
	return root, nil
}

func wslHomeOrError(distro string) (string, error) {
	home := wslUserHomeFn(distro)
	if !strings.HasPrefix(home, "/") {
		return "", fmt.Errorf("无法解析 WSL 发行版（%s）主目录：发行版不可达或探测失败；请检查 WSL 后重试", distro)
	}
	return home, nil
}

// joinWSLRoot 拼接 root 与子目录名（根路径不产生 //）。
func joinWSLRoot(root, name string) string {
	if root == "/" {
		return "/" + name
	}
	return root + "/" + name
}

func hasWSLPathControl(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
}

// wslParentOf 返回 Linux 路径父目录指针；文件系统根（/）无父级返回 nil。
func wslParentOf(root string) *string {
	if root == "/" {
		return nil
	}
	if idx := strings.LastIndexByte(root, '/'); idx > 0 {
		parent := root[:idx]
		return &parent
	}
	parent := "/"
	return &parent
}
