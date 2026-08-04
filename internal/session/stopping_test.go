package session

import (
	"errors"
	"testing"
)

func TestStoppingRemainsActiveAndNonRemovableUntilTerminalReceipt(t *testing.T) {
	manager := NewManager()
	sess := manager.Create(AppTypeClaudeCode, "p", "", "m", ModeTerminal, t.TempDir(), false)
	manager.MarkStopping(sess.ID)

	if got := manager.GetStatus(sess.ID); got != StatusStopping {
		t.Fatalf("status=%q want stopping", got)
	}
	if err := manager.Remove(sess.ID); !errors.Is(err, ErrSessionStopping) {
		t.Fatalf("Remove while stopping error=%v", err)
	}
	if got := manager.ClearStopped(); got != 0 {
		t.Fatalf("ClearStopped removed stopping session: %d", got)
	}
	if got := manager.RunningCount(); got != 1 {
		t.Fatalf("RunningCount while stopping=%d want 1", got)
	}
	ids := manager.GetRunning()
	if len(ids) != 1 || ids[0] != sess.ID {
		t.Fatalf("GetRunning while stopping=%v", ids)
	}

	manager.MarkExited(sess.ID) // terminal receipt after user-requested Stop
	if got := manager.GetStatus(sess.ID); got != StatusStopped {
		t.Fatalf("terminal status=%q want stopped", got)
	}
	if err := manager.Remove(sess.ID); err != nil {
		t.Fatalf("Remove after terminal: %v", err)
	}
}
