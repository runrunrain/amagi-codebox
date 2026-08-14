package logging

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func todayDate() string { return time.Now().Format("2006-01-02") }

// TestLog_RollsOnDateChange verifies that writing a log entry after the
// service's current log file is stale (dated for a previous day) reopens a file
// for today and writes the new line there, leaving the stale file untouched.
// This covers the cross-day rolling that NewService did not previously perform
// (it opened the startup-day file and never rolled).
func TestLog_RollsOnDateChange(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir)
	t.Cleanup(s.Close)

	today := todayDate()
	staleName := "amagi-codebox-1999-01-01.log"
	stalePath := filepath.Join(dir, "logs", staleName)
	if err := os.WriteFile(stalePath, []byte{}, 0o644); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	stale, err := os.OpenFile(stalePath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open stale: %v", err)
	}

	// Force the service onto the stale date so the next Info() must roll.
	s.mu.Lock()
	if s.logFile != nil {
		s.logFile.Close()
	}
	s.logFile = stale
	s.logDate = "1999-01-01"
	s.mu.Unlock()

	s.Info("test", "rolling-trigger", "")

	// The stale file must NOT contain the new line.
	if data, rerr := os.ReadFile(stalePath); rerr != nil {
		t.Fatalf("read stale: %v", rerr)
	} else if strings.Contains(string(data), "rolling-trigger") {
		t.Fatalf("stale file received post-roll line: %q", data)
	}

	// Today's file must contain the new line.
	todayPath := filepath.Join(dir, "logs", "amagi-codebox-"+today+".log")
	data, rerr := os.ReadFile(todayPath)
	if rerr != nil {
		t.Fatalf("read today: %v", rerr)
	}
	if !strings.Contains(string(data), "rolling-trigger") {
		t.Fatalf("today file missing post-roll line: %q", data)
	}

	// Service state should now be aligned to today with an open file.
	s.mu.Lock()
	gotDate := s.logDate
	gotNil := s.logFile == nil
	s.mu.Unlock()
	if gotDate != today {
		t.Errorf("logDate=%q want %q", gotDate, today)
	}
	if gotNil {
		t.Errorf("logFile closed after roll")
	}
}

// TestLog_WriteErrorNotSilent verifies that a file-write failure is reported
// through reportWriteError rather than being swallowed (the previous
// f.WriteString(...) dropped the error).
func TestLog_WriteErrorNotSilent(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir)
	t.Cleanup(s.Close)

	// Capture write-error reports via the seam.
	var mu sync.Mutex
	var sawErr error
	orig := reportWriteError
	t.Cleanup(func() { reportWriteError = orig })
	reportWriteError = func(err error) { mu.Lock(); sawErr = err; mu.Unlock() }

	// Close the underlying file handle but keep s.logFile pointing at it and
	// logDate current, so ensureLogFileLocked short-circuits and WriteString
	// fails on the closed fd.
	s.mu.Lock()
	if s.logFile != nil {
		s.logFile.Close()
	}
	s.mu.Unlock()

	s.Warn("test", "after-close-write", "detail")

	mu.Lock()
	errSeen := sawErr
	mu.Unlock()
	if errSeen == nil {
		t.Fatalf("write error was swallowed (reportWriteError not called)")
	}
}