//go:build !windows

package tray

import "context"

type Service struct{}

func NewService() *Service { return &Service{} }

func (s *Service) Start(ctx context.Context, icon []byte, onQuit func()) {
	_ = s
	_ = ctx
	_ = icon
	_ = onQuit
}

func (s *Service) SetStatus(text string) { _ = s; _ = text }

func (s *Service) Stop() {}
