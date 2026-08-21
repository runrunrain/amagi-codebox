//go:build !windows

package wslsetup

import (
	"fmt"

	"amagi-codebox/internal/logging"
)

// Service is a no-op on non-Windows platforms: installing CLIs into WSL only
// makes sense on Windows. The stub keeps the App wiring and cross-platform
// builds uniform (per the repo's build-tag convention).
type Service struct {
	log *logging.Service
}

func NewService(log *logging.Service) *Service {
	return &Service{log: log}
}

// GetStatus always reports WSL unavailable off Windows.
func (s *Service) GetStatus() Status {
	return Status{Available: false, Reason: "WSL CLI install is only supported on Windows"}
}

// InstallTool is unsupported off Windows. It still rejects unknown tools first,
// mirroring the Windows contract (unsupported tool -> error), then reports the
// platform limitation for supported tools.
func (s *Service) InstallTool(tool string) (*InstallResult, error) {
	key := normalizeToolKey(tool)
	pkg, ok := packageForTool(key)
	if !ok {
		return nil, fmt.Errorf("unsupported CLI tool for WSL install: %q", tool)
	}
	return &InstallResult{
		Tool:    key,
		Package: pkg,
		Success: false,
		Error:   "WSL CLI install is only supported on Windows",
	}, nil
}
