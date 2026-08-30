//go:build windows

package pty

import (
	"strings"
	"testing"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
)

// TestWorkDirMapsToDrvFS pins the C8 classification: only Windows drive-letter
// workdirs map onto the /mnt/<drive> DrvFS/9P mount; Linux-style paths stay on
// ext4 and empty/UNC paths are out of scope.
func TestWorkDirMapsToDrvFS(t *testing.T) {
	cases := map[string]bool{
		`D:\WorkPace`:    true,
		`C:\`:            true,
		`c:/Users/x`:     true,
		` x:\odd\space `: true, // TrimSpace before classification
		"/home/u":        false,
		"~/projects":     false,
		"":               false,
		`\\server\share`: false,
	}
	for in, want := range cases {
		if got := workDirMapsToDrvFS(in); got != want {
			t.Errorf("workDirMapsToDrvFS(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDrvfsMountPath(t *testing.T) {
	cases := map[string]string{
		`D:\WorkPace`: "/mnt/d/WorkPace",
		`C:\`:         "/mnt/c/",
		`c:/Users/x`:  "/mnt/c/Users/x",
		"/home/u":     "",
	}
	for in, want := range cases {
		if got := drvfsMountPath(in); got != want {
			t.Errorf("drvfsMountPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBuildWSLCommandLineWarnsOnDrvFSWorkDir verifies the C8 advisory: a
// drive-letter --cd workdir emits one warn log ("DrvFS 工作区 I/O 显著慢于
// ext4") carrying the mapped /mnt path, while an ext4-style workdir stays
// silent and a nil logger never panics. Log-only by design: no session-start
// advisory UI mechanism exists to hook into.
func TestBuildWSLCommandLineWarnsOnDrvFSWorkDir(t *testing.T) {
	if platform.DefaultWSLDistro(nil) == "" {
		t.Skip("no usable WSL distro on this machine; skipping WSL command line build")
	}

	log := logging.NewService(t.TempDir())
	t.Cleanup(func() { log.Close() }) // Close releases the day-log file handle so TempDir cleanup can remove it on Windows
	cmdLine, autoCmd := buildResolvedStartupPlan(platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapWSL,
		WorkDir:       `D:\WorkPace`,
	}, log)
	if !strings.Contains(cmdLine, `--cd "D:\WorkPace"`) {
		t.Fatalf("commandLine missing --cd workdir: %s", cmdLine)
	}
	if autoCmd != "" {
		t.Fatalf("WSL mode must not use a typed-in autoCommand, got %q", autoCmd)
	}
	entries := log.GetEntries("WARN", "pty", "DrvFS", 0)
	if len(entries) == 0 {
		t.Fatal("expected a DrvFS warn log for a drive-letter workdir")
	}
	e := entries[0]
	if !strings.Contains(e.Message, "DrvFS 工作区 I/O 显著慢于 ext4") {
		t.Errorf("warn message missing the expected text: %q", e.Message)
	}
	if !strings.Contains(e.Detail, "/mnt/d/WorkPace") {
		t.Errorf("warn detail should carry the mapped /mnt path: %q", e.Detail)
	}

	// ext4-style workdir (Linux path passed to --cd): no DrvFS warning.
	logExt4 := logging.NewService(t.TempDir())
	t.Cleanup(func() { logExt4.Close() })
	buildResolvedStartupPlan(platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapWSL,
		WorkDir:       "/home/u/projects",
	}, logExt4)
	if entries := logExt4.GetEntries("WARN", "pty", "DrvFS", 0); len(entries) != 0 {
		t.Errorf("ext4 workdir must not warn: %#v", entries)
	}

	// Nil logger must not panic (log-optional contract of buildWSLCommandLine).
	buildResolvedStartupPlan(platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapWSL,
		WorkDir:       `D:\WorkPace`,
	}, nil)
}
