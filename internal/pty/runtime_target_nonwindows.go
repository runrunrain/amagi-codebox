//go:build !windows

package pty

// SessionWSLTarget is always native on non-Windows hosts. The App facade
// separately validates session membership before asking for this target.
func (s *Service) SessionWSLTarget(sessionID string) (bool, string, error) {
	return false, "", nil
}
