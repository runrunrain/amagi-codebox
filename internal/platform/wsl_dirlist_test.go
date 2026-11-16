package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func fakeDirRunner(t *testing.T, fn func(context.Context, string, string) ([]byte, error)) {
	t.Helper()
	old := wslScriptRunnerContext
	wslScriptRunnerContext = fn
	t.Cleanup(func() { wslScriptRunnerContext = old })
}

func dirFrame(kind string, names ...string) string {
	s := "\x00" + kind + "\x00"
	for _, name := range names {
		s += name + "\x00"
	}
	return s
}

func TestWSLDirListProtocolRequiresProofAndPreservesNames(t *testing.T) {
	for _, tc := range []struct {
		name, output    string
		exists, wantErr bool
		names           []string
	}{
		{"empty directory", dirFrame(wslDirListOKMarker), true, false, []string{}},
		{"profile noise", "profile noise\n" + dirFrame(wslDirListOKMarker, "space dir", "line\n__AMAGI_E__fake", wslDirListNoDirMarker), true, false, []string{"space dir", "line\n__AMAGI_E__fake", wslDirListNoDirMarker}},
		{"relay exit zero", "", false, true, nil},
		{"noise only", "profile noise", false, true, nil},
		{"missing", dirFrame(wslDirListNoDirMarker), false, false, nil},
		{"denied", dirFrame(wslDirListDeniedMarker), false, true, nil},
		{"truncated", dirFrame(wslDirListOKMarker) + "unterminated", false, true, nil},
		{"invalid basename", dirFrame(wslDirListOKMarker, "../wrong"), false, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeDirRunner(t, func(ctx context.Context, distro, script string) ([]byte, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 11*time.Second {
					t.Fatal("missing bounded deadline")
				}
				return []byte(tc.output), nil
			})
			names, exists, err := wslListSubdirsForOS("windows", "Ubuntu", "/home/u")
			if exists != tc.exists || (err != nil) != tc.wantErr || !reflect.DeepEqual(names, tc.names) {
				t.Fatalf("names=%#v exists=%v err=%v", names, exists, err)
			}
		})
	}
}

func TestWSLDirListQuotesAndCancellation(t *testing.T) {
	called := false
	fakeDirRunner(t, func(ctx context.Context, distro, script string) ([]byte, error) {
		called = true
		if distro != "Captured" || !strings.Contains(script, `r='/home/my user/re'\''po'`) {
			t.Fatalf("wrong target/quoting: %s %s", distro, script)
		}
		return []byte(dirFrame(wslDirListOKMarker)), nil
	})
	if _, _, err := wslListSubdirsForOS("windows", "Captured", "/home/my user/re'po"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("probe not executed")
	}
	called = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := wslListSubdirsContextForOS(ctx, "windows", "Captured", "/home/u"); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancel err=%v called=%v", err, called)
	}
	for _, args := range [][3]string{{"darwin", "Ubuntu", "/tmp"}, {"windows", "", "/tmp"}, {"windows", "Ubuntu", "relative"}, {"windows", "Ubuntu", "/tmp\x00bad"}} {
		if _, _, err := wslListSubdirsForOS(args[0], args[1], args[2]); err == nil {
			t.Fatalf("invalid request accepted: %#v", args)
		}
	}
	if called {
		t.Fatal("invalid request ran a probe")
	}
}

func TestWSLDirListProbeErrorPropagates(t *testing.T) {
	fakeDirRunner(t, func(context.Context, string, string) ([]byte, error) { return nil, context.DeadlineExceeded })
	if _, _, err := wslListSubdirsForOS("windows", "Ubuntu", "/tmp"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}

func TestWSLDirectoryScriptsOnPOSIXFilesystem(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("requires native POSIX filesystem")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh unavailable")
	}
	home := t.TempDir()
	root := filepath.Join(home, "space ' literal")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "trailing space ", wslDirListNoDirMarker}
	for _, name := range append(append([]string{}, want...), ".hidden", "line\n__AMAGI_E__fake") {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	fakeDirRunner(t, func(ctx context.Context, distro, script string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, sh, "-c", script)
		cmd.Env = append(os.Environ(), "HOME="+home)
		return cmd.Output()
	})
	names, exists, err := wslListSubdirsForOS("windows", "test", root)
	sort.Strings(names)
	sort.Strings(want)
	if err != nil || !exists || !reflect.DeepEqual(names, want) {
		t.Fatalf("names=%#v exists=%v err=%v", names, exists, err)
	}
	if _, exists, err := wslListSubdirsForOS("windows", "test", filepath.Join(home, "missing")); err != nil || exists {
		t.Fatalf("missing exists=%v err=%v", exists, err)
	}
	for _, input := range []string{"", "~", root} {
		resolved, err := WSLResolveDirectoryContext(context.Background(), "test", input)
		expected := home
		if input == root {
			expected = root
		}
		if err != nil || resolved != expected {
			t.Fatalf("input=%q resolved=%q err=%v", input, resolved, err)
		}
	}
	if os.Getuid() != 0 {
		if err := os.Chmod(root, 0000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(root, 0700)
		if _, _, err := wslListSubdirsForOS("windows", "test", root); err == nil || !strings.Contains(err.Error(), "不可读") {
			t.Fatalf("permission error=%v", err)
		}
	}
}

func TestWSLResolveDirectoryKeepsPhysicalSymlinkDotDotTarget(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("requires native POSIX filesystem")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh unavailable")
	}
	root := t.TempDir()
	for _, dir := range []string{"real/target", "real/peer", "peer"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "real/target"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	fakeDirRunner(t, func(ctx context.Context, _ string, script string) ([]byte, error) {
		return exec.CommandContext(ctx, sh, "-c", script).Output()
	})
	// Do not filepath.Join this input: its lexical clean would erase the case.
	input := root + "/link/../peer"
	resolved, err := WSLResolveDirectoryContext(context.Background(), "Captured", input)
	if err != nil || resolved != filepath.Join(root, "real/peer") {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
}

func TestWSLResolveDirectoryRequiresPositiveMarker(t *testing.T) {
	fakeDirRunner(t, func(context.Context, string, string) ([]byte, error) { return nil, nil })
	if _, err := WSLResolveDirectoryContext(context.Background(), "test", "~"); err == nil {
		t.Fatal("exit zero without execution proof accepted")
	}
}

func TestWSLDirListCountsSafeEntriesBeforeCap(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("requires native POSIX filesystem")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh unavailable")
	}
	root := t.TempDir()
	for i := 0; i < 101; i++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("a%03d\nunsafe", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 501; i++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("z%03d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	fakeDirRunner(t, func(ctx context.Context, _ string, script string) ([]byte, error) {
		return exec.CommandContext(ctx, sh, "-c", script).Output()
	})
	names, exists, err := wslListSubdirsForOS("windows", "captured", root)
	if err != nil || !exists || len(names) != 501 {
		t.Fatalf("safe count=%d exists=%v err=%v", len(names), exists, err)
	}
	for _, name := range names {
		if strings.ContainsRune(name, '\n') {
			t.Fatalf("unsafe name leaked: %q", name)
		}
	}
}

func TestWSLListSubdirsRealProbe(t *testing.T) {
	distro := strings.TrimSpace(os.Getenv("AMAGI_WSL_DIRLIST_E2E"))
	if distro == "" {
		t.Skip("set AMAGI_WSL_DIRLIST_E2E to enable actual WSL probe")
	}
	names, exists, err := wslListSubdirsForOS("windows", distro, "/")
	if err != nil || !exists || len(names) == 0 {
		t.Fatalf("actual WSL probe names=%v exists=%v err=%v", names, exists, err)
	}
}
