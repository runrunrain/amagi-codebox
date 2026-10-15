package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/platform"
)

func FindCore(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if p, err := exec.LookPath("sing-box"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	paths := []string{filepath.Join(home, "Library/Application Support/v2rayN/bin/sing_box/sing-box"), "/Applications/v2rayN.app/Contents/Resources/bin/sing_box/sing-box", filepath.Join(home, ".local/share/v2rayN/bin/sing_box/sing-box")}
	for _, base := range []string{os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA")} {
		if base != "" {
			paths = append(paths, filepath.Join(base, "v2rayN/bin/sing_box/sing-box.exe"))
		}
	}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "sing-box.exe"), filepath.Join(filepath.Dir(exe), "bin/sing_box/sing-box.exe"))
	}
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

type logBuffer struct {
	mu      sync.Mutex
	data    []byte
	secrets []string
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	s := string(p)
	for _, secret := range b.secrets {
		if len(secret) > 2 {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	b.data = append(b.data, []byte(s)...)
	if len(b.data) > 32768 {
		b.data = append([]byte(nil), b.data[len(b.data)-32768:]...)
	}
	return n, nil
}
func (b *logBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.data) }

type process struct {
	cmd  *exec.Cmd
	job  uintptr
	done chan struct{}
	err  error
}
type Status struct {
	Running  bool   `json:"running"`
	PID      int    `json:"pid"`
	Core     string `json:"core"`
	Error    string `json:"error"`
	Revision uint64 `json:"revision"`
	Logs     string `json:"logs"`
}
type Manager struct {
	mu        sync.Mutex
	core, dir string
	proc      *process
	logs      logBuffer
	lastError string
	config    []byte
	ports     []int
	revision  uint64
}

func NewManager(core, dir string) *Manager { return &Manager{core: core, dir: dir} }
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Status{Core: m.core, Error: m.lastError, Revision: m.revision, Logs: m.logs.String()}
	if m.proc != nil {
		select {
		case <-m.proc.done:
			if m.proc.err != nil {
				s.Error = "内核已退出，请查看日志"
			}
		default:
			s.Running = true
			s.PID = m.proc.cmd.Process.Pid
		}
	}
	return s
}

func (m *Manager) Apply(ctx context.Context, config []byte, ports []int, revision uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.core == "" {
		return errors.New("未发现 sing-box；请安装或在高级设置中指定内核路径")
	}
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(m.dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(m.dir, "candidate-*.json")
	if err != nil {
		return err
	}
	candidate := f.Name()
	defer os.Remove(candidate)
	if _, err = f.Write(config); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	check := exec.CommandContext(checkCtx, m.core, "check", "-c", candidate)
	platform.SuppressConsoleWindow(check)
	output, err := check.CombinedOutput()
	if err != nil {
		if checkCtx.Err() != nil {
			return errors.New("内核配置检查超时或已取消")
		}
		return fmt.Errorf("内核配置检查失败：%s", redact(string(output), config))
	}
	// Invalid configurations leave the currently running process intact. A
	// failed replacement restores the previous configuration when possible.
	oldConfig, oldPorts, oldRevision := m.config, m.ports, m.revision
	wasRunning := m.isRunning()
	if err := m.stop(); err != nil {
		return err
	}
	m.setSecrets(config)
	if err := m.start(config, ports); err != nil {
		m.lastError = err.Error()
		if wasRunning {
			m.setSecrets(oldConfig)
			if restoreErr := m.start(oldConfig, oldPorts); restoreErr != nil {
				m.lastError += "；旧配置恢复失败：" + restoreErr.Error()
			} else {
				m.revision = oldRevision
				m.lastError += "；已恢复旧配置"
			}
		}
		return errors.New(m.lastError)
	}
	m.config = append([]byte(nil), config...)
	m.ports = append([]int(nil), ports...)
	m.revision = revision
	m.lastError = ""
	return nil
}

// reapDecision 判定是否回收遗留内核：owned = 进程所有权成立（命令行/可执行文件
// 指向本 Manager 的 runtime），parentAlive = 父进程仍存活。仅在确属本应用遗留且
// 父进程已死（孤儿）时回收，避免误杀 v2rayN 等他人的 sing-box 或其他存活实例的内核。
func reapDecision(owned, parentAlive bool) bool { return owned && !parentAlive }

func (m *Manager) isRunning() bool {
	if m.proc == nil {
		return false
	}
	select {
	case <-m.proc.done:
		return false
	default:
		return true
	}
}
func (m *Manager) Stop() error { m.mu.Lock(); defer m.mu.Unlock(); return m.stop() }

// pidPath 记录当前内核 pid，供下次启动回收上次异常退出遗留的孤儿（见 reapStaleCore）。
func (m *Manager) pidPath() string { return filepath.Join(m.dir, "core.pid") }
func (m *Manager) stop() error {
	if !m.isRunning() {
		m.proc = nil
		_ = os.Remove(m.pidPath())
		return nil
	}
	p := m.proc
	interruptCore(p.cmd.Process)
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		_ = p.cmd.Process.Kill()
		select {
		case <-p.done:
		case <-time.After(time.Second):
			// 进程可能未死：保留 pidfile 作为下次启动回收的线索。
			return errors.New("内核停止超时")
		}
	}
	m.proc = nil
	_ = os.Remove(m.pidPath())
	return nil
}
func (m *Manager) start(config []byte, ports []int) error {
	// 先回收上次异常退出遗留的孤儿内核（父进程已死且确属本 runtime），
	// 否则其监听端口会让下方探测直接报“已被占用”，且每次重启复现。
	if pid := reapStaleCore(m.core, m.pidPath()); pid != 0 {
		_, _ = fmt.Fprintf(&m.logs, "已回收上次异常退出的代理内核（pid %d），端口恢复可用\n", pid)
	}
	var listeners []net.Listener
	for _, port := range ports {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			for _, l := range listeners {
				_ = l.Close()
			}
			return fmt.Errorf("代理端口 %d 已被占用", port)
		}
		listeners = append(listeners, ln)
	}
	for _, ln := range listeners {
		_ = ln.Close()
	}
	path := filepath.Join(m.dir, "core.json")
	if err := AtomicWrite(path, config); err != nil {
		return err
	}
	p := &process{cmd: exec.Command(m.core, "run", "-c", path, "--disable-color"), done: make(chan struct{})}
	platform.SuppressConsoleWindow(p.cmd)
	p.cmd.Stdout = &m.logs
	p.cmd.Stderr = &m.logs
	if err := p.cmd.Start(); err != nil {
		return errors.New("无法启动 sing-box，请检查内核路径与执行权限")
	}
	p.tie()
	m.proc = p
	go func() { p.err = p.cmd.Wait(); p.release(); close(p.done) }()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			return errors.New("内核启动失败，请查看日志")
		default:
		}
		ready := true
		for _, port := range ports {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
			if err != nil {
				ready = false
				break
			}
			_ = conn.Close()
		}
		if ready {
			// pidfile 仅是回收线索，写失败不影响运行。
			if err := os.WriteFile(m.pidPath(), []byte(strconv.Itoa(p.cmd.Process.Pid)), 0o600); err != nil {
				_, _ = fmt.Fprintf(&m.logs, "写入内核 pidfile 失败：%v\n", err)
			}
			return nil
		}
		time.Sleep(40 * time.Millisecond)
	}
	_ = m.stop()
	return errors.New("内核启动超时")
}

func AtomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".save-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func secretValues(config []byte) []string {
	// Avoid including node credentials in UI diagnostics.
	var values []string
	var document any
	if json.Unmarshal(config, &document) != nil {
		return nil
	}
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				if k == "password" || k == "uuid" || k == "username" {
					if s, ok := child.(string); ok && len(s) > 2 {
						values = append(values, s)
					}
				}
				visit(child)
			}
		case []any:
			for _, child := range x {
				visit(child)
			}
		}
	}
	visit(document)
	return values
}
func redact(s string, config []byte) string {
	for _, v := range secretValues(config) {
		s = strings.ReplaceAll(s, v, "[redacted]")
	}
	if len(s) > 2000 {
		s = s[:2000]
	}
	return s
}
func (m *Manager) setSecrets(config []byte) {
	m.logs.mu.Lock()
	defer m.logs.mu.Unlock()
	m.logs.secrets = secretValues(config)
}
