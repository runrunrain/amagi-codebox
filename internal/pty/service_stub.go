//go:build !windows

package pty

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
)

type outputCallback func(data []byte)
type exitCallback func(exitCode uint32)
type resizeCallback func(cols, rows int)

type Service struct {
	mu sync.Mutex
}

func NewService(log *logging.Service) *Service {
	_ = log
	return &Service{}
}

func (s *Service) RegisterOutputCallback(sessionID string, id string, cb func(data []byte)) {
	_ = s
	_ = sessionID
	_ = id
	_ = cb
}
func (s *Service) UnregisterOutputCallback(sessionID string, id string) { _ = s; _ = sessionID; _ = id }
func (s *Service) RegisterExitCallback(sessionID string, id string, cb func(exitCode uint32)) {
	_ = s
	_ = sessionID
	_ = id
	_ = cb
}
func (s *Service) UnregisterExitCallback(sessionID string, id string) { _ = s; _ = sessionID; _ = id }
func (s *Service) RegisterResizeCallback(sessionID string, id string, cb func(cols, rows int)) {
	_ = s
	_ = sessionID
	_ = id
	_ = cb
}
func (s *Service) UnregisterResizeCallback(sessionID string, id string) { _ = s; _ = sessionID; _ = id }
func (s *Service) AttachSessionObserver(sessionID string, id string, outputCB func(data []byte), resizeCB func(cols, rows int)) ([]byte, int, int, error) {
	_ = s
	_ = sessionID
	_ = id
	_ = outputCB
	_ = resizeCB
	return nil, 0, 0, fmt.Errorf("pty backend is not implemented on this platform yet")
}
func (s *Service) DetachSessionObserver(sessionID string, id string) { _ = s; _ = sessionID; _ = id }
func (s *Service) SetContext(ctx context.Context)                    { _ = s; _ = ctx }
func (s *Service) Start(sessionID, shellPath, autoCommand, workDir string, env []string, cols, rows int) (int, error) {
	_ = shellPath
	_ = autoCommand
	_ = workDir
	_ = env
	_ = cols
	_ = rows
	return 0, fmt.Errorf("pty backend is not implemented on this platform yet for session %s", sessionID)
}
func (s *Service) StartResolved(sessionID string, spec platform.ResolvedLaunchSpec) (int, error) {
	_ = spec
	return 0, fmt.Errorf("pty backend is not implemented on this platform yet for session %s", sessionID)
}
func (s *Service) Write(sessionID string, data string) error {
	_, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return err
	}
	return fmt.Errorf("pty backend is not implemented on this platform yet for session %s", sessionID)
}
func (s *Service) WriteLarge(sessionID string, data string) error { return s.Write(sessionID, data) }
func (s *Service) Resize(sessionID string, cols, rows int) error {
	_ = cols
	_ = rows
	return fmt.Errorf("pty backend is not implemented on this platform yet for session %s", sessionID)
}
func (s *Service) GetPtyDimensions(sessionID string) (cols, rows int, err error) {
	return 0, 0, fmt.Errorf("pty backend is not implemented on this platform yet for session %s", sessionID)
}
func (s *Service) Close(sessionID string) error    { return nil }
func (s *Service) CloseAll()                       {}
func (s *Service) IsRunning(sessionID string) bool { return false }
func (s *Service) GetOutputHistory(sessionID string) ([]byte, error) {
	return nil, fmt.Errorf("pty backend is not implemented on this platform yet for session %s", sessionID)
}
func (s *Service) RunningCount() int { return 0 }
