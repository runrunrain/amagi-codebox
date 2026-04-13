package plugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) findClaudeMd(installPath string) (string, bool, error) {
	for _, candidate := range []string{"CLAUDE.md", "CLAUDE.template.md"} {
		absPath := filepath.Join(installPath, candidate)
		info, err := os.Stat(absPath)
		if err == nil {
			if info.IsDir() {
				continue
			}
			return filepath.ToSlash(candidate), true, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		return "", false, fmt.Errorf("stat %s: %w", absPath, err)
	}
	return "", false, nil
}

func uniqueHookName(info HookInfo, seen map[string]int) string {
	base := strings.Join(filterNonEmpty([]string{info.Event, info.Type, info.Command}), ":")
	if base == "" {
		base = "hook"
	}
	seen[base]++
	if seen[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s#%d", base, seen[base])
}

func filterNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}
