//go:build windows

package tray

import (
	"context"
	"runtime"
	"sync"

	"github.com/energye/systray"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:generate echo "Tray icons are embedded from build/windows/icon.ico via go:embed in app.go"

// Service 管理系统托盘图标和菜单。
// 通过 Wails Startup 生命周期钩子初始化。
type Service struct {
	ctx     context.Context
	mu      sync.Mutex
	running bool

	// 状态回调
	onQuit func()

	// 菜单项引用（用于动态更新）
	mStatus *systray.MenuItem
}

// NewService 创建托盘服务实例。
func NewService() *Service {
	return &Service{}
}

// Start 在专用且锁定的 OS 线程中启动系统托盘。
// 必须在 Wails OnStartup 中调用，传入 Wails context。
// icon 是 ICO 文件的字节内容。
func (s *Service) Start(ctx context.Context, icon []byte, onQuit func()) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.ctx = ctx
	s.onQuit = onQuit
	s.running = true
	s.mu.Unlock()

	go func() {
		// Wails 自身维护 GUI / 消息循环，systray 的 Win32 窗口与消息泵也要求稳定绑定到同一线程。
		// 如果直接在未锁线程的 goroutine 中运行，Go 调度器可能在阻塞/唤醒后迁移到底层其他线程，
		// 从而把托盘窗口、回调和消息循环拆散，继续放大右键菜单显示不稳定问题。
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		systray.Run(func() {
			s.onReady(icon)
		}, func() {
			// systray 退出时的清理
		})
	}()
}

// onReady systray 初始化完成后的回调。
func (s *Service) onReady(icon []byte) {
	systray.SetIcon(icon)
	systray.SetTitle("Amagi CodeBox")
	systray.SetTooltip("Amagi CodeBox - Claude Code 服务管理器")

	// 注册托盘图标点击事件（必须在 CreateMenu 之前）
	systray.SetOnClick(func(menu systray.IMenu) {
		// 左键点击：显示窗口
		wailsRuntime.WindowShow(s.ctx)
	})
	systray.SetOnDClick(func(menu systray.IMenu) {
		// 双击：显示窗口
		wailsRuntime.WindowShow(s.ctx)
	})

	// 创建菜单（必须在 SetOn*Click 之后、AddMenuItem 之前调用）
	systray.CreateMenu()

	// 状态指示（禁用的文本项）
	s.mStatus = systray.AddMenuItem("状态: 就绪", "当前状态")
	s.mStatus.Disable()

	systray.AddSeparator()

	mShow := systray.AddMenuItem("显示窗口", "显示主窗口")
	mHide := systray.AddMenuItem("隐藏窗口", "隐藏主窗口")

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("退出", "完全退出应用")

	// 注册菜单项点击回调
	mShow.Click(func() {
		wailsRuntime.WindowShow(s.ctx)
	})
	mHide.Click(func() {
		wailsRuntime.WindowHide(s.ctx)
	})
	mQuit.Click(func() {
		if s.onQuit != nil {
			s.onQuit()
		}
	})
}

// SetStatus 更新托盘菜单中的状态文本。
func (s *Service) SetStatus(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mStatus != nil {
		s.mStatus.SetTitle("状态: " + text)
	}
}

// Stop 停止系统托盘。
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		systray.Quit()
		s.running = false
	}
}
