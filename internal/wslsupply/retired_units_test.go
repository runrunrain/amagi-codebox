package wslsupply

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPruneRetiredUnits 正负成对：已退役的 Pi 单元（pi/settings 等）与共享技能
// 单元（shared/skill/<name>）登记在读取时剔除；在役单元（shared/vision-export、
// claude/mcp、claude 资源目录单元）原样保留——「清理遗留但不清在役」。
func TestPruneRetiredUnits(t *testing.T) {
	f := &StateFile{SchemaVersion: stateSchemaVersion, Units: []StateEntry{
		{Unit: "pi/settings", LastOutcome: "awaiting-consent"},
		{Unit: "pi/mcp", LastOutcome: "awaiting-consent"},
		{Unit: "pi/managed-models", LastOutcome: "written"},
		{Unit: "shared/skill/alpha", LastOutcome: "written"},
		{Unit: "shared/skill/beta", LastOutcome: "awaiting-consent"},
		{Unit: UnitSharedVisionExport, LastOutcome: "written"},
		{Unit: "claude/mcp", LastOutcome: "written"},
		{Unit: "claude/res/commands/demo", LastOutcome: "written"},
	}}
	dropped := pruneRetiredUnits(f)
	wantDropped := map[UnitID]bool{
		"pi/settings": true, "pi/mcp": true, "pi/managed-models": true,
		"shared/skill/alpha": true, "shared/skill/beta": true,
	}
	if len(dropped) != len(wantDropped) {
		t.Fatalf("dropped %v, want %d entries", dropped, len(wantDropped))
	}
	for _, u := range dropped {
		if !wantDropped[u] {
			t.Fatalf("unexpected drop: %s", u)
		}
	}
	kept := map[UnitID]bool{}
	for _, e := range f.Units {
		kept[e.Unit] = true
	}
	for _, stay := range []UnitID{UnitSharedVisionExport, "claude/mcp", "claude/res/commands/demo"} {
		if !kept[stay] {
			t.Fatalf("active unit %s must survive prune", stay)
		}
	}
	if len(f.Units) != 3 {
		t.Fatalf("3 units must remain, got %d", len(f.Units))
	}
	// 幂等：再次 prune 无可剔除。
	if again := pruneRetiredUnits(f); len(again) != 0 {
		t.Fatalf("prune must be idempotent, dropped again: %v", again)
	}
}

// TestStateLoadPrunesRetiredUnits 端到端：带退役登记的旧状态文件经 stateStore
// 读取后不再包含 pi/* 与 shared/skill/*（「避免错误设置」的读取面保证）。
func TestStateLoadPrunesRetiredUnits(t *testing.T) {
	dir := t.TempDir()
	legacy := StateFile{
		SchemaVersion: stateSchemaVersion,
		Units: []StateEntry{
			{Unit: "pi/mcp", LastOutcome: "awaiting-consent", LastSuccess: time.Now()},
			{Unit: "shared/skill/alpha", LastOutcome: "written", LastSuccess: time.Now()},
			{Unit: UnitSharedVisionExport, LastOutcome: "written", LastSuccess: time.Now()},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	s := newStateStore(dir)
	st := s.load()
	for _, u := range st.Units {
		if retiredPiUnit(u.Unit) || retiredSkillUnit(u.Unit) {
			t.Fatalf("retired unit %s must not surface after load", u.Unit)
		}
	}
	if len(st.Units) != 1 || st.Units[0].Unit != UnitSharedVisionExport {
		t.Fatalf("only vision-export must remain, got %+v", st.Units)
	}
}
