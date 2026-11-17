package agentcli

// endpoint.go — 端点文件 <home>/run/endpoint.json 的发布/撤销/陈旧清理
//（DESIGN §3.1，生命周期对齐 feishu-sync cli-endpoint）。
//
// 权限：run 目录 0700、endpoint.json 0600、tmp+rename 原子写（写法沿用
// internal/settings saveLocked：单一可写句柄上 write+chmod+Sync，rename 是
// 最后一个可失败步——Windows FlushFileBuffers 需要写句柄）。
//
// 所有权：撤销时仅当文件里的 pid == 当前进程才删，防止多实例误删他人文件；
// 陈旧清理时 pid 所属进程已死（platform.ProcessAlive）或文件损坏才删。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/platform"
)

const (
	runDirPerm   = 0o700
	endpointPerm = 0o600
	// endpointTmpPattern 是端点原子写的临时文件模式（os.CreateTemp 独占
	// 随机名，评审 D-03：并发生布方共用固定名会互相改名失败/残底对方内容）。
	endpointTmpPattern = ".endpoint-*.tmp"
)

// endpointPath 返回端点文件路径（导出供 root 门面展示）。
func endpointPath(configDir string) string {
	return filepath.Join(configDir, "run", "endpoint.json")
}

// writeFull 在单一句柄上写满整个缓冲（对齐 internal/settings writeFull）。
func writeFull(f *os.File, b []byte) error {
	for len(b) > 0 {
		n, err := f.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

// publishEndpoint 原子写入端点文件。目录与文件权限显式设置（chmod 兜底
// 已存在路径的 umask 放宽）。临时文件用同目录独占随机名（os.CreateTemp，
// 评审 D-03）：并发发布互不干扰，失败只清理自己的 tmp。失败时不留半成品。
func publishEndpoint(configDir string, ep spec.Endpoint) error {
	path := endpointPath(configDir)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, runDirPerm); err != nil {
		return err
	}
	if err := os.Chmod(dir, runDirPerm); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ep, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	tmp, err := os.CreateTemp(dir, endpointTmpPattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	werr := func() error {
		if err := writeFull(tmp, b); err != nil {
			return err
		}
		if err := tmp.Chmod(endpointPerm); err != nil {
			return err
		}
		// Windows FlushFileBuffers 需要写句柄（O_RDWR），同 settings R5-N01。
		return tmp.Sync()
	}()
	cerr := tmp.Close()
	if werr != nil {
		_ = os.Remove(tmpName)
		return werr
	}
	if cerr != nil {
		_ = os.Remove(tmpName)
		return cerr
	}
	// rename 是最后一个可失败步（原子提交）。
	return os.Rename(tmpName, path)
}

// readEndpointFile 读取并解析端点文件。
func readEndpointFile(path string) (spec.Endpoint, error) {
	var ep spec.Endpoint
	b, err := os.ReadFile(path)
	if err != nil {
		return ep, err
	}
	if err := json.Unmarshal(b, &ep); err != nil {
		return ep, err
	}
	return ep, nil
}

// checkEndpointOwner 检测现有端点文件的跨进程所有权（评审 D-04）：文件
// 属于另一个存活进程时返回错误（调用方拒绝发布，不清理、不覆盖）。文件
// 不存在、损坏或属于本进程/死进程 → nil（后者由 CleanStaleEndpoint 清理）。
// alive 由调用方注入（生产为 platform.ProcessAlive，测试可模拟两实例）。
func checkEndpointOwner(configDir string, alive func(pid int) bool) error {
	ep, err := readEndpointFile(endpointPath(configDir))
	if err != nil {
		return nil
	}
	if ep.PID != 0 && ep.PID != os.Getpid() && alive(ep.PID) {
		return fmt.Errorf("agent-cli: 端点文件被另一运行中的实例占用（pid=%d），拒绝发布以免覆盖；如确认无其它实例，可删除 %s 后重启", ep.PID, endpointPath(configDir))
	}
	return nil
}

// removeOwnEndpoint 撤销本进程发布的端点文件：pid 匹配才删（多实例保护）；
// 文件损坏读不出 pid 视为垃圾一并清理；不存在则静默。
func removeOwnEndpoint(configDir string) {
	path := endpointPath(configDir)
	if _, err := os.Stat(path); err != nil {
		return
	}
	ep, err := readEndpointFile(path)
	if err == nil && ep.PID != 0 && ep.PID != os.Getpid() {
		// 属于其它实例的文件：不动。
		return
	}
	_ = os.Remove(path)
}

// CleanStaleEndpoint 清理陈旧端点文件（Start 前调用）：文件不存在静默返回；
// pid 所属进程已死或文件损坏则删除；其它存活实例的文件保留。
func CleanStaleEndpoint(configDir string) {
	path := endpointPath(configDir)
	if _, err := os.Stat(path); err != nil {
		return
	}
	ep, err := readEndpointFile(path)
	if err == nil && platform.ProcessAlive(ep.PID) && ep.PID != os.Getpid() {
		return
	}
	_ = os.Remove(path)
}
