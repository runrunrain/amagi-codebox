package platform

// move_no_replace_test.go — 原子不替换移动原语的语义回归（G0）：
// 目标已存在（文件/目录/空目录）绝不替换且双方保留；目标不存在原子移动
// 成功；并发竞争创建目标失败不丢任何一方；source 缺失等文件系统错误如实
// 上抛、不触发任何回退路径。全部使用临时目录合成数据。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func writeMoveFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMoveNoReplaceTargetExistsNeverReplaced(t *testing.T) {
	dir := t.TempDir()

	t.Run("existing file target", func(t *testing.T) {
		src := filepath.Join(dir, "src-file.txt")
		dst := filepath.Join(dir, "dst-file.txt")
		writeMoveFixture(t, src, "source-bytes")
		writeMoveFixture(t, dst, "destination-bytes")
		err := MoveNoReplace(src, dst)
		if !errors.Is(err, os.ErrExist) {
			t.Fatalf("err = %v, want os.ErrExist", err)
		}
		gotSrc, rerr := os.ReadFile(src)
		if rerr != nil || string(gotSrc) != "source-bytes" {
			t.Fatalf("source lost/altered: %q err=%v", gotSrc, rerr)
		}
		gotDst, rerr := os.ReadFile(dst)
		if rerr != nil || string(gotDst) != "destination-bytes" {
			t.Fatalf("destination replaced: %q err=%v", gotDst, rerr)
		}
	})

	t.Run("existing non-empty dir target", func(t *testing.T) {
		src := filepath.Join(dir, "srcdir")
		if err := os.MkdirAll(filepath.Join(src, "inner"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "inner", "f.txt"), []byte("src"), 0o644); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, "dstdir")
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, "occupant.txt"), []byte("occupant"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := MoveNoReplace(src, dst); !errors.Is(err, os.ErrExist) {
			t.Fatalf("err = %v, want os.ErrExist", err)
		}
		if _, err := os.Stat(filepath.Join(src, "inner", "f.txt")); err != nil {
			t.Fatalf("source tree lost: %v", err)
		}
		got, rerr := os.ReadFile(filepath.Join(dst, "occupant.txt"))
		if rerr != nil || string(got) != "occupant" {
			t.Fatalf("existing dir target replaced: %q err=%v", got, rerr)
		}
	})

	t.Run("existing empty dir target", func(t *testing.T) {
		src := filepath.Join(dir, "srcdir2")
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, "dstdir2")
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := MoveNoReplace(src, dst); !errors.Is(err, os.ErrExist) {
			t.Fatalf("err = %v, want os.ErrExist (empty dir must not be replaced either)", err)
		}
		if info, err := os.Stat(src); err != nil || !info.IsDir() {
			t.Fatalf("source dir lost: info=%v err=%v", info, err)
		}
		if info, err := os.Stat(dst); err != nil || !info.IsDir() {
			t.Fatalf("empty target dir lost: info=%v err=%v", info, err)
		}
	})
}

func TestMoveNoReplaceTargetAbsentMoves(t *testing.T) {
	dir := t.TempDir()

	t.Run("file", func(t *testing.T) {
		src := filepath.Join(dir, "m-src.txt")
		dst := filepath.Join(dir, "m-dst.txt")
		writeMoveFixture(t, src, "atomic-bytes")
		if err := MoveNoReplace(src, dst); err != nil {
			t.Fatalf("move: %v", err)
		}
		if _, err := os.Lstat(src); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("source must be gone, err=%v", err)
		}
		got, rerr := os.ReadFile(dst)
		if rerr != nil || string(got) != "atomic-bytes" {
			t.Fatalf("destination bytes = %q err=%v", got, rerr)
		}
	})

	t.Run("non-empty dir", func(t *testing.T) {
		src := filepath.Join(dir, "m-srcdir")
		if err := os.MkdirAll(filepath.Join(src, "a", "b"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "a", "b", "deep.txt"), []byte("deep"), 0o644); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, "m-dstdir")
		if err := MoveNoReplace(src, dst); err != nil {
			t.Fatalf("move dir: %v", err)
		}
		got, rerr := os.ReadFile(filepath.Join(dst, "a", "b", "deep.txt"))
		if rerr != nil || string(got) != "deep" {
			t.Fatalf("dir tree bytes lost: %q err=%v", got, rerr)
		}
		if _, err := os.Lstat(src); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("source dir must be gone, err=%v", err)
		}
	})
}

// TestMoveNoReplaceConcurrentSingleWinner：N 个并发写者竞争同一目标——
// 原子 no-replace 语义下恰好一个成功，其余得到 os.ErrExist；任何一方的
// 字节都不丢（失败者 source 在场，目标内容恰为某一个完整写者字节）。
func TestMoveNoReplaceConcurrentSingleWinner(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "race-target")
	const writers = 16
	sources := make([]string, writers)
	for i := range sources {
		sources[i] = filepath.Join(dir, "race-src", fmt.Sprintf("%02d", i))
	}
	for i, src := range sources {
		writeMoveFixture(t, src, fmt.Sprintf("winner-%02d", i))
	}
	var (
		mu       sync.Mutex
		wins     int
		conflics int
		firstErr error
	)
	var wg sync.WaitGroup
	for _, src := range sources {
		wg.Add(1)
		go func(src string) {
			defer wg.Done()
			err := MoveNoReplace(src, dst)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, os.ErrExist):
				conflics++
			default:
				if firstErr == nil {
					firstErr = err
				}
			}
		}(src)
	}
	wg.Wait()
	t.Logf("writers=%d wins=%d conflicts=%d", writers, wins, conflics)
	if firstErr != nil {
		t.Errorf("unexpected non-conflict error: %v", firstErr)
	}
	if wins != 1 {
		t.Errorf("exactly one winner expected, got %d (conflicts=%d)", wins, conflics)
	}
	if wins+conflics != writers {
		t.Errorf("some writers' outcome lost: wins=%d conflicts=%d writers=%d", wins, conflics, writers)
	}
	got, rerr := os.ReadFile(dst)
	if rerr != nil {
		t.Fatalf("destination missing after race: %v", rerr)
	}
	found := false
	for i := range sources {
		if string(got) == fmt.Sprintf("winner-%02d", i) {
			found = true
		}
	}
	if !found {
		t.Fatalf("destination bytes %q do not match any complete writer", got)
	}
	// 失败者的 source 必须仍在（不丢任何一方）。
	remaining := 0
	for i, src := range sources {
		body, err := os.ReadFile(src)
		expected := fmt.Sprintf("winner-%02d", i)
		if err == nil {
			remaining++
			if string(body) != expected {
				t.Fatalf("losing source bytes changed: index=%d", i)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("source unreadable: index=%d err=%v", i, err)
		} else if string(got) != expected {
			t.Fatalf("non-winning source disappeared: index=%d", i)
		}
	}
	if remaining != writers-1 {
		t.Fatalf("failed movers' sources must remain, got %d of %d", remaining, writers-1)
	}
}

func TestMoveNoReplaceErrorPaths(t *testing.T) {
	dir := t.TempDir()
	t.Run("missing source does not create destination", func(t *testing.T) {
		src := filepath.Join(dir, "no-such-source")
		dst := filepath.Join(dir, "dst-should-not-exist")
		err := MoveNoReplace(src, dst)
		if err == nil || !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v, want os.ErrNotExist", err)
		}
		if _, derr := os.Lstat(dst); !errors.Is(derr, os.ErrNotExist) {
			t.Fatalf("destination must not be created by a failed move, err=%v", derr)
		}
	})
	t.Run("empty paths rejected", func(t *testing.T) {
		if err := MoveNoReplace("", filepath.Join(dir, "d")); err == nil {
			t.Fatal("empty source must error")
		}
		if err := MoveNoReplace(filepath.Join(dir, "s"), ""); err == nil {
			t.Fatal("empty destination must error")
		}
	})
	t.Run("same path rejected", func(t *testing.T) {
		p := filepath.Join(dir, "same.txt")
		writeMoveFixture(t, p, "x")
		if err := MoveNoReplace(p, p); err == nil {
			t.Fatal("identical source/destination must error")
		}
	})
}
