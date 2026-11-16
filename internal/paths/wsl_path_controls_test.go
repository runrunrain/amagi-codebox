package paths

import (
	"encoding/json"
	"testing"
)

func TestWSLDirectoryTrailingSpacesStayByteExact(t *testing.T) {
	root := "/home/u/project "
	injectWSLSeams(t, func(_ string, got string) ([]string, bool, error) {
		if got != root {
			t.Fatalf("trimmed root: %q", got)
		}
		return []string{"child "}, true, nil
	}, nil)
	raw, err := NewPathsService(t.TempDir()).ListWSLDirectories("Captured", root)
	if err != nil {
		t.Fatal(err)
	}
	var listing directoryListing
	if err := json.Unmarshal([]byte(raw), &listing); err != nil {
		t.Fatal(err)
	}
	if listing.Root != root || len(listing.Dirs) != 1 || listing.Dirs[0].Path != root+"/child " {
		t.Fatalf("space path altered: %s", raw)
	}
}

func TestWSLDirectoryControlBytesCannotReachTerminalInsertion(t *testing.T) {
	calls := 0
	injectWSLSeams(t, func(string, string) ([]string, bool, error) {
		calls++
		return []string{"safe", "line\n__AMAGI_E__ghost", "escape\x1b[201~", "tab\tname"}, true, nil
	}, nil)
	service := NewPathsService(t.TempDir())
	raw, err := service.ListWSLDirectories("Captured", "/home/u")
	if err != nil {
		t.Fatal(err)
	}
	var listing directoryListing
	if err := json.Unmarshal([]byte(raw), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Dirs) != 1 || listing.Dirs[0].Path != "/home/u/safe" {
		t.Fatalf("unsafe terminal path leaked: %s", raw)
	}
	for _, root := range []string{"/home/u\n", "/home/u\x00fake", "/home/u/escape\x1b[201~"} {
		if _, err := service.ListWSLDirectories("Captured", root); err == nil {
			t.Fatalf("control root accepted: %q", root)
		}
	}
	if calls != 1 {
		t.Fatalf("invalid roots reached probe: %d calls", calls)
	}
}
