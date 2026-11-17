package pty

import (
	"context"
	"fmt"

	"amagi-codebox/internal/platform"
)

var resolveWSLStartupDirectory = platform.WSLResolveDirectoryContext

// Resolve and validate once; use that exact absolute target for --cd. A
// successful HOME check alone must not launch in an inherited Windows cwd.
func prepareWSLStartupSpec(spec platform.ResolvedLaunchSpec, distro string) (platform.ResolvedLaunchSpec, error) {
	// 预检已定谳（WSLWorkDirPreflightResolved）：WorkDir 是启动预检融合探测
	// 返回的绝对物理路径（cd -P 后的 $PWD），跳过重复目录探测；false 路径
	// （remote/restart/旧调用）保持原 resolveWSLStartupDirectory 行为不变。
	if spec.WSLWorkDirPreflightResolved {
		return spec, nil
	}
	directory, err := resolveWSLStartupDirectory(context.Background(), distro, spec.WorkDir)
	if err != nil {
		return spec, fmt.Errorf("WSL %s 工作目录 %q 不可用，已拒绝启动（不回退换目录）；请重新选择发行版内目录或本机 Windows 盘符路径，macOS /Users/... 不会自动映射: %w", distro, spec.WorkDir, err)
	}
	spec.WorkDir = directory
	return spec, nil
}

// SessionWSLTarget reports the immutable target of the actual PTY process,
// never a re-resolution of a requested shell or the current default distro.
func (s *Service) SessionWSLTarget(sessionID string) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, ok := s.sessions[sessionID]
	if !ok {
		return false, "", fmt.Errorf("PTY session %s is unavailable; cannot determine its filesystem", sessionID)
	}
	if ps.bootstrapMode != platform.BootstrapWSL {
		return false, "", nil
	}
	if ps.wslDistro == "" {
		return true, "", fmt.Errorf("WSL session %s has no recorded target distro", sessionID)
	}
	return true, ps.wslDistro, nil
}
