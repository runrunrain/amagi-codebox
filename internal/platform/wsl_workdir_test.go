package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWSLValidateWorkDirRequiresPositiveMarker(t *testing.T) {
	original := wslScriptRunnerContext
	t.Cleanup(func() { wslScriptRunnerContext = original })
	for _, tc := range []struct {
		name, output string
		err          error
		wantOK       bool
	}{
		{"usable", "profile noise\n" + wslWorkDirOK + "\r\n", nil, true},
		{"relay exit zero", "", nil, false},
		{"missing", "", errors.New("exit status 43"), false},
		{"marker with failure", wslWorkDirOK, errors.New("exit status 1"), false},
		{"partial marker", wslWorkDirOK + "bad", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wslScriptRunnerContext = func(ctx context.Context, distro, script string) ([]byte, error) {
				if distro != "Ubuntu" || !strings.Contains(script, "'/Users/run.mao/workpace/amagi-codebox'") {
					t.Fatalf("wrong target/script: %q %q", distro, script)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("probe must have a deadline")
				}
				return []byte(tc.output), tc.err
			}
			err := WSLValidateWorkDirContext(context.Background(), "Ubuntu", "/Users/run.mao/workpace/amagi-codebox")
			if (err == nil) != tc.wantOK {
				t.Fatalf("err=%v wantOK=%v", err, tc.wantOK)
			}
			if err != nil && (!strings.Contains(err.Error(), "Ubuntu") || !strings.Contains(err.Error(), "/Users/run.mao")) {
				t.Fatalf("error must identify target and path: %v", err)
			}
		})
	}
}

func TestWSLValidateWorkDirCancelledBeforeRunner(t *testing.T) {
	original := wslScriptRunnerContext
	t.Cleanup(func() { wslScriptRunnerContext = original })
	wslScriptRunnerContext = func(context.Context, string, string) ([]byte, error) {
		t.Fatal("cancelled probe must not run")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WSLValidateWorkDirContext(ctx, "Ubuntu", "/tmp"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestWSLWorkDirCheckShell(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("requires a native POSIX filesystem")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell unavailable")
	}
	home := t.TempDir()
	project := filepath.Join(home, "project ' $() ; literal")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{project, "~", "~/" + filepath.Base(project), ""} {
		script, err := buildWSLWorkDirCheck(dir)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(sh, "-c", script)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), wslWorkDirOK) {
			t.Fatalf("dir=%q output=%q err=%v", dir, out, err)
		}
	}
	for _, dir := range []string{`X:\Work Space\project`, `\\wsl.localhost\Ubuntu\home\project`} {
		script, err := buildWSLWorkDirCheck(dir)
		if err != nil {
			t.Fatal(err)
		}
		// Exercise conversion/quoting without a Windows/WSL process. A UNC
		// path must not be rejected just because it has no drive letter.
		convert := "wslpath() { [ \"$1\" = '-u' ] || return 77; printf %s " + wslShSingleQuote(home) + "; };\n"
		out, err := exec.Command(sh, "-c", convert+script).CombinedOutput()
		if err != nil || !strings.Contains(string(out), wslWorkDirOK) {
			t.Fatalf("Windows dir=%q output=%q err=%v", dir, out, err)
		}
	}
	for _, dir := range []string{filepath.Join(home, "missing"), "relative/path"} {
		script, _ := buildWSLWorkDirCheck(dir)
		if out, err := exec.Command(sh, "-c", script).CombinedOutput(); err == nil || strings.Contains(string(out), wslWorkDirOK) {
			t.Fatalf("missing/relative path accepted: %q %q %v", dir, out, err)
		}
	}
	for _, dir := range []string{"/tmp\nother", "/tmp\x00bad"} {
		if _, err := buildWSLWorkDirCheck(dir); err == nil {
			t.Fatalf("control character accepted: %q", dir)
		}
	}
}
