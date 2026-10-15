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
		// T12: Linux 形态的 DrvFS 挂载点同样慢，须与盘符形态同判。
		"/mnt/d/repo":         true,
		"/mnt/c":              true, // 盘符挂载根
		"/mnt/c/":             true,
		" /mnt/e/x ":          true,  // TrimSpace before classification
		"/mnt/wsl":            false, // 非「单字符盘符」挂载
		"/mnt/docker-desktop": false,
		"/mnt/wsl/fonts":      false,
		"/mnt/usb":            false, // 多字母段：不是盘符
		"/mnt":                false, // 裸 /mnt：无盘符段
		"/mnt/":               false,
		"/home/u":             false,
		"~/projects":          false,
		"":                    false,
		`\\server\share`:      false,
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
		// T12: Linux 形态已在挂载点上，原样返回（仅用于告警日志明细）。
		"/mnt/d/repo": "/mnt/d/repo",
		"/mnt/c":      "/mnt/c",
		"/mnt/wsl":    "",
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

	// T12: Linux-form DrvFS mount (/mnt/<drive>) warns the same way.
	logMnt := logging.NewService(t.TempDir())
	t.Cleanup(func() { logMnt.Close() })
	buildResolvedStartupPlan(platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapWSL,
		WorkDir:       "/mnt/d/repo",
	}, logMnt)
	entriesMnt := logMnt.GetEntries("WARN", "pty", "DrvFS", 0)
	if len(entriesMnt) != 1 {
		t.Fatalf("/mnt-form DrvFS workdir must warn exactly once, got %#v", entriesMnt)
	}
	if !strings.Contains(entriesMnt[0].Detail, "/mnt/d/repo") {
		t.Errorf("/mnt-form warn detail should carry the mount path: %q", entriesMnt[0].Detail)
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

	// /mnt/wsl is not a drive mount: no DrvFS warning.
	logWsl := logging.NewService(t.TempDir())
	t.Cleanup(func() { logWsl.Close() })
	buildResolvedStartupPlan(platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapWSL,
		WorkDir:       "/mnt/wsl/fonts",
	}, logWsl)
	if entries := logWsl.GetEntries("WARN", "pty", "DrvFS", 0); len(entries) != 0 {
		t.Errorf("/mnt/wsl workdir must not warn: %#v", entries)
	}

	// Nil logger must not panic (log-optional contract of buildWSLCommandLine).
	buildResolvedStartupPlan(platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapWSL,
		WorkDir:       `D:\WorkPace`,
	}, nil)
}
