package launcher

import (
	"errors"
	"fmt"
	"strings"
)

// ErrLegacyProcFSIdentity marks a pre-boot-ID procfs identity. A live PID with
// this schema is ownership-ambiguous: it must remain adopted, must not be
// signalled, and must not be reported terminal until absence is observed.
var ErrLegacyProcFSIdentity = errors.New("legacy procfs process identity requires migration")

// procFSProcessIdentity combines the Linux boot generation with /proc stat
// starttime. starttime alone resets on reboot and can collide for a reused PID.
func procFSProcessIdentity(bootID string, stat []byte) (string, error) {
	bootID = strings.ToLower(strings.TrimSpace(bootID))
	if !validProcFSBootID(bootID) {
		return "", fmt.Errorf("invalid procfs boot identity")
	}
	text := string(stat)
	// /proc/<pid>/stat field 2 is parenthesized comm and may contain spaces or
	// parentheses. Fields after the final ')' begin at field 3; starttime is 22.
	closeParen := strings.LastIndexByte(text, ')')
	if closeParen < 0 {
		return "", fmt.Errorf("malformed procfs stat")
	}
	fields := strings.Fields(text[closeParen+1:])
	if len(fields) <= 19 || fields[19] == "" {
		return "", fmt.Errorf("short procfs stat")
	}
	for _, ch := range fields[19] {
		if ch < '0' || ch > '9' {
			return "", fmt.Errorf("invalid procfs starttime")
		}
	}
	return "procfs:" + bootID + ":" + fields[19], nil
}

func validProcFSBootID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, ch := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if ch != '-' {
				return false
			}
			continue
		}
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return false
		}
	}
	return true
}

func legacyProcFSStarttime(identity string) (string, bool) {
	const prefix = "procfs:"
	if !strings.HasPrefix(identity, prefix) {
		return "", false
	}
	starttime := strings.TrimPrefix(identity, prefix)
	if starttime == "" || strings.Contains(starttime, ":") {
		return "", false
	}
	for _, ch := range starttime {
		if ch < '0' || ch > '9' {
			return "", false
		}
	}
	return starttime, true
}

// classifyRecoveredExternalProcess is the no-signal authority decision shared
// by all recovered-process paths. Only an exact current-schema identity may
// authorize a signal. Absence proves terminal. A current-schema mismatch proves
// the old identity terminal without signalling a reused PID. A live legacy
// procfs identity has no boot generation, so it remains an explicit migration
// uncertainty rather than being silently completed or allowed to signal.
func classifyRecoveredExternalProcess(expected, observed string, running bool, inspectErr error) (terminal, signal bool, err error) {
	if inspectErr != nil {
		return false, false, inspectErr
	}
	if !running {
		return true, false, nil
	}
	if _, legacy := legacyProcFSStarttime(expected); legacy {
		return false, false, ErrLegacyProcFSIdentity
	}
	if observed != expected {
		return true, false, nil
	}
	return false, true, nil
}
