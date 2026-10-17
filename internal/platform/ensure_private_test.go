package platform

// ensure_private_test.go — 宿主本地私有化原语的语义回归（G0）：建立/收紧/
// 验证 0700/0600、拒绝符号链接、类型不符拒绝。POSIX 权限位断言仅在非
// Windows 宿主验证；Windows ACL 由 ensure_private_windows_test.go 专项验证。

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestEnsurePrivateDirEstablishesAndTightens(t *testing.T) {
	dir := t.TempDir()

	t.Run("creates with private mode", func(t *testing.T) {
		p := filepath.Join(dir, "fresh", "nested", "private")
		if err := EnsurePrivateDir(p); err != nil {
			t.Fatalf("EnsurePrivateDir: %v", err)
		}
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Fatal("expected directory")
		}
		if runtime.GOOS != "windows" {
			if got := info.Mode().Perm(); got != 0o700 {
				t.Fatalf("mode = %o, want 700", got)
			}
		}
	})

	t.Run("tightens existing wide dir", func(t *testing.T) {
		p := filepath.Join(dir, "wide-dir")
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := EnsurePrivateDir(p); err != nil {
			t.Fatalf("EnsurePrivateDir: %v", err)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Lstat(p)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o700 {
				t.Fatalf("mode = %o, want tightened 700", got)
			}
		}
	})

	t.Run("rejects existing file as dir target", func(t *testing.T) {
		p := filepath.Join(dir, "not-a-dir")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := EnsurePrivateDir(p); !errors.Is(err, ErrPrivatePathNotDir) {
			t.Fatalf("err = %v, want ErrPrivatePathNotDir", err)
		}
	})

	t.Run("rejects relative path", func(t *testing.T) {
		if err := EnsurePrivateDir("relative/dir"); err == nil {
			t.Fatal("relative path must be rejected")
		}
	})
}

func TestEnsurePrivateFileEstablishesAndTightens(t *testing.T) {
	dir := t.TempDir()

	t.Run("creates new private file", func(t *testing.T) {
		p := filepath.Join(dir, "fresh-file", "secret.json")
		if err := EnsurePrivateFile(p); err != nil {
			t.Fatalf("EnsurePrivateFile: %v", err)
		}
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() {
			t.Fatal("expected regular file")
		}
		if runtime.GOOS != "windows" {
			if got := info.Mode().Perm(); got != 0o600 {
				t.Fatalf("mode = %o, want 600", got)
			}
		}
	})

	t.Run("tightens existing wide file without touching bytes", func(t *testing.T) {
		p := filepath.Join(dir, "wide-file.json")
		if err := os.WriteFile(p, []byte(`{"k":"v"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := EnsurePrivateFile(p); err != nil {
			t.Fatalf("EnsurePrivateFile: %v", err)
		}
		got, rerr := os.ReadFile(p)
		if rerr != nil || string(got) != `{"k":"v"}` {
			t.Fatalf("bytes altered: %q err=%v", got, rerr)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Lstat(p)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Fatalf("mode = %o, want tightened 600", perm)
			}
		}
	})

	t.Run("rejects directory as file target", func(t *testing.T) {
		p := filepath.Join(dir, "a-dir")
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := EnsurePrivateFile(p); !errors.Is(err, ErrPrivatePathNotRegular) {
			t.Fatalf("err = %v, want ErrPrivatePathNotRegular", err)
		}
	})
}

func privateSymlinkFixture(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		// Windows ERROR_PRIVILEGE_NOT_HELD. Do not skip merely because this
		// is Windows, or conceal unrelated fixture/ACL errors as a privilege gap.
		if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
			t.Skip("symlink fixture creation denied: Windows ERROR_PRIVILEGE_NOT_HELD")
		}
		t.Fatal(err)
	}
}

func TestEnsurePrivateRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "secret"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("dir symlink", func(t *testing.T) {
		link := filepath.Join(dir, "dir-link")
		privateSymlinkFixture(t, real, link)
		if err := EnsurePrivateDir(link); !errors.Is(err, ErrPrivatePathSymlink) {
			t.Fatalf("err = %v, want ErrPrivatePathSymlink", err)
		}
	})

	t.Run("file symlink", func(t *testing.T) {
		link := filepath.Join(dir, "file-link")
		privateSymlinkFixture(t, filepath.Join(real, "secret"), link)
		if err := EnsurePrivateFile(link); !errors.Is(err, ErrPrivatePathSymlink) {
			t.Fatalf("err = %v, want ErrPrivatePathSymlink", err)
		}
		// 真实目标不得被跟随收紧（仍是 600，且未被替换/删除）。
		got, rerr := os.ReadFile(filepath.Join(real, "secret"))
		if rerr != nil || string(got) != "s" {
			t.Fatalf("link target unexpectedly altered: %q err=%v", got, rerr)
		}
	})

	t.Run("dangling symlink also rejected", func(t *testing.T) {
		link := filepath.Join(dir, "dangling-link")
		privateSymlinkFixture(t, filepath.Join(dir, "no-such-target"), link)
		if err := EnsurePrivateFile(link); !errors.Is(err, ErrPrivatePathSymlink) {
			t.Fatalf("err = %v, want ErrPrivatePathSymlink", err)
		}
	})
}
