package paths

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// mustList 调用 ListDirectories 并解码返回的 JSON 载荷。
func mustList(t *testing.T, root string) (directoryListing, string) {
	t.Helper()
	svc := NewPathsService(t.TempDir())
	raw, err := svc.ListDirectories(root)
	if err != nil {
		t.Fatalf("ListDirectories(%q) unexpected error: %v", root, err)
	}
	var got directoryListing
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode listing JSON %q: %v", raw, err)
	}
	return got, raw
}

func TestListDirectories_EmptyRootFallsBackToHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve user home dir: %v", err)
	}
	got, _ := mustList(t, "")
	if got.Root != home {
		t.Fatalf("root = %q, want user home %q", got.Root, home)
	}
	if len(got.Dirs) > listDirectoriesLimit {
		t.Fatalf("dirs count %d exceeds limit %d", len(got.Dirs), listDirectoriesLimit)
	}
}

func TestListDirectories_ListsOnlySortedVisibleDirs(t *testing.T) {
	tmp := t.TempDir()
	for _, name := range []string{"Beta", "alpha", "Gamma", ".hidden"} {
		if err := os.Mkdir(filepath.Join(tmp, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "notadir.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	got, raw := mustList(t, tmp)

	// 大小写不敏感排序、跳过 . 开头目录与文件。
	wantNames := []string{"alpha", "Beta", "Gamma"}
	if len(got.Dirs) != len(wantNames) {
		t.Fatalf("dirs = %+v, want %v", got.Dirs, wantNames)
	}
	for i, want := range wantNames {
		if got.Dirs[i].Name != want {
			t.Fatalf("dirs[%d].Name = %q, want %q (dirs=%+v)", i, got.Dirs[i].Name, want, got.Dirs)
		}
		if wantPath := filepath.Join(got.Root, want); got.Dirs[i].Path != wantPath {
			t.Fatalf("dirs[%d].Path = %q, want %q", i, got.Dirs[i].Path, wantPath)
		}
	}

	// root 规范化为绝对路径；parent 指向上级；未超限不截断。
	if got.Root != tmp {
		t.Fatalf("root = %q, want %q", got.Root, tmp)
	}
	if got.Parent == nil || *got.Parent != filepath.Dir(tmp) {
		t.Fatalf("parent = %v, want %q", got.Parent, filepath.Dir(tmp))
	}
	if got.Truncated {
		t.Fatalf("truncated = true, want false")
	}

	// JSON 字段顺序：root / parent / dirs / truncated。
	for _, pair := range [][2]string{
		{`"root":`, `"parent":`},
		{`"parent":`, `"dirs":`},
		{`"dirs":`, `"truncated":`},
	} {
		if strings.Index(raw, pair[0]) > strings.Index(raw, pair[1]) {
			t.Fatalf("JSON field order wrong: %s should precede %s in %s", pair[0], pair[1], raw)
		}
	}

	// 传入未规范化路径（尾部 /./）应清洗为同一目录。
	if cleaned, _ := mustList(t, tmp+string(filepath.Separator)+"."); cleaned.Root != tmp {
		t.Fatalf("root from uncleaned path = %q, want %q", cleaned.Root, tmp)
	}
}

func TestListDirectories_FileSystemRootParentIsNull(t *testing.T) {
	fsRoot := "/"
	if runtime.GOOS == "windows" {
		fsRoot = filepath.VolumeName(t.TempDir()) + `\`
	}
	got, raw := mustList(t, fsRoot)
	if got.Root != fsRoot {
		t.Fatalf("root = %q, want %q", got.Root, fsRoot)
	}
	if got.Parent != nil {
		t.Fatalf("parent = %v (want nil) at file system root", *got.Parent)
	}
	if !strings.Contains(raw, `"parent":null`) {
		t.Fatalf("raw JSON should contain \"parent\":null, got %s", raw)
	}
}

func TestListDirectories_InvalidRootErrors(t *testing.T) {
	svc := NewPathsService(t.TempDir())

	missing := filepath.Join(t.TempDir(), "no-such-dir")
	if _, err := svc.ListDirectories(missing); err == nil {
		t.Fatalf("ListDirectories(missing %q) should error", missing)
	}

	file := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}
	if _, err := svc.ListDirectories(file); err == nil {
		t.Fatalf("ListDirectories(file %q) should error", file)
	}
}

func TestListDirectories_TruncatesAtLimit(t *testing.T) {
	restore := listDirectoriesLimit
	listDirectoriesLimit = 2
	t.Cleanup(func() { listDirectoriesLimit = restore })

	tmp := t.TempDir()
	for _, name := range []string{"c", "a", "b"} {
		if err := os.Mkdir(filepath.Join(tmp, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}

	t.Run("exceeds limit truncates", func(t *testing.T) {
		got, _ := mustList(t, tmp)
		if got.Truncated != true {
			t.Fatalf("truncated = false, want true")
		}
		if len(got.Dirs) != 2 {
			t.Fatalf("dirs = %+v, want 2 entries", got.Dirs)
		}
	})

	t.Run("exactly at limit not truncated", func(t *testing.T) {
		exact := t.TempDir()
		for _, name := range []string{"only", "second"} {
			if err := os.Mkdir(filepath.Join(exact, name), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", name, err)
			}
		}
		got, _ := mustList(t, exact)
		if got.Truncated {
			t.Fatalf("truncated = true, want false when entries == limit")
		}
		if len(got.Dirs) != 2 {
			t.Fatalf("dirs = %+v, want 2 entries", got.Dirs)
		}
	})
}
