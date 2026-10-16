package platform

import (
	"encoding/json"
	"testing"
)

// resetDrvFSAdvisoriesForTest 清空进程内 advisory 记录（仅测试使用）。
func resetDrvFSAdvisoriesForTest() {
	drvfsAdvisoryMu.Lock()
	drvfsAdvisories = nil
	drvfsAdvisoryMu.Unlock()
}

// TestDrvFSAdvisoryRecordDedup verifies the dedup contract: the same
// (Source, WorkDir) keeps only the freshest entry in place, while different
// sources or workdirs accumulate independently.
func TestDrvFSAdvisoryRecordDedup(t *testing.T) {
	resetDrvFSAdvisoriesForTest()

	RecordDrvFSAdvisory(DrvFSAdvisory{Source: "pty", WorkDir: `D:\repo`, MountPath: "/mnt/d/repo", Detail: "v1", At: 100})
	RecordDrvFSAdvisory(DrvFSAdvisory{Source: "gitassist", WorkDir: "/mnt/d/repo", MountPath: "/mnt/d/repo", Detail: "git", At: 101})
	RecordDrvFSAdvisory(DrvFSAdvisory{Source: "pty", WorkDir: `D:\repo`, MountPath: "/mnt/d/repo", Detail: "v2", At: 200})

	entries := DrvFSAdvisories()
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (same source+workDir deduped): %#v", len(entries), entries)
	}
	if entries[0].Source != "pty" || entries[0].Detail != "v2" || entries[0].At != 200 {
		t.Errorf("deduped pty entry should keep the freshest, got %#v", entries[0])
	}
	if entries[1].Source != "gitassist" || entries[1].Detail != "git" {
		t.Errorf("different source must accumulate independently, got %#v", entries[1])
	}
}

// TestDrvFSAdvisoryAtDefaultAndCap verifies the zero-At default (now) and the
// ring-buffer cap: oldest entries are dropped once the max is exceeded.
func TestDrvFSAdvisoryAtDefaultAndCap(t *testing.T) {
	resetDrvFSAdvisoriesForTest()

	RecordDrvFSAdvisory(DrvFSAdvisory{Source: "paths", WorkDir: "/mnt/c"})
	if got := DrvFSAdvisories(); len(got) != 1 || got[0].At <= 0 {
		t.Fatalf("zero At must default to now, got %#v", got)
	}

	resetDrvFSAdvisoriesForTest()
	for i := 0; i < drvfsAdvisoryMaxEntries+5; i++ {
		RecordDrvFSAdvisory(DrvFSAdvisory{Source: "paths", WorkDir: "/mnt/c", Detail: "same", At: int64(i)})
	}
	// 同 key 去重：cap 分支本身不会因重复 key 触发，构造不同 workDir 再验。
	resetDrvFSAdvisoriesForTest()
	for i := 0; i < drvfsAdvisoryMaxEntries+5; i++ {
		RecordDrvFSAdvisory(DrvFSAdvisory{Source: "paths", WorkDir: "/mnt/c/" + string(rune('a'+i%26)) + "-" + string(rune('0'+i%10)), At: int64(i)})
	}
	entries := DrvFSAdvisories()
	if len(entries) != drvfsAdvisoryMaxEntries {
		t.Fatalf("entries = %d, want capped at %d", len(entries), drvfsAdvisoryMaxEntries)
	}
	if entries[0].At != 5 {
		t.Errorf("oldest overflow entries must be dropped; first At = %d, want 5", entries[0].At)
	}
}

// TestDrvFSAdvisorySnapshotIsCopy verifies the read-only query returns a copy:
// mutating the returned slice must not affect subsequent reads.
func TestDrvFSAdvisorySnapshotIsCopy(t *testing.T) {
	resetDrvFSAdvisoriesForTest()
	RecordDrvFSAdvisory(DrvFSAdvisory{Source: "pty", WorkDir: `C:\x`, MountPath: "/mnt/c/x", At: 1})

	snap := DrvFSAdvisories()
	snap[0].Detail = "mutated"
	snap = snap[:0]

	again := DrvFSAdvisories()
	if len(again) != 1 || again[0].Detail != "" {
		t.Fatalf("snapshot must be a copy; internal state changed to %#v", again)
	}

	// JSON 形态钉定（前端 App.GetDrvFSAdvisories 消费同一载荷）。
	b, err := json.Marshal(again)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `[{"source":"pty","workDir":"C:\\x","mountPath":"/mnt/c/x","detail":"","at":1}]`
	if string(b) != want {
		t.Fatalf("json = %s, want %s", b, want)
	}
}
