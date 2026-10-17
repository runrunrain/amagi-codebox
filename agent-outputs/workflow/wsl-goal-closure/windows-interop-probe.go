//go:build ignore

// Authorized integration probe: metadata and exclusively-created synthetic
// fixtures only. No actual CLI invocation, credential reads, or global changes.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"amagi-codebox/internal/platform"
)

func event(name string, ok bool, detail string) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"check": name, "ok": ok, "detail": detail})
}
func wsl(distro, cwd string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	argv := []string{"-d", distro}
	if cwd != "" {
		argv = append(argv, "--cd", cwd)
	}
	argv = append(argv, "--exec")
	argv = append(argv, args...)
	out, err := exec.CommandContext(ctx, "wsl.exe", argv...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("WSL command %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}
func probe(root, distro, testBinary, count string) (err error) {
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(filepath.Base(root), ".amagi-codebox-validation-") {
		return errors.New("unexpected fixture root")
	}
	st, err := os.Lstat(root)
	if err != nil || !st.IsDir() {
		return errors.New("fixture root missing or not directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	id := platform.WSLTargetIdentityContext(ctx, distro)
	cancel()
	if !id.OK || id.UID == "" || !strings.HasPrefix(id.Home, "/") || id.MachineID == "" {
		return errors.New("complete WSL identity unavailable")
	}
	event("production-context-identity", true, "UID, home and machine-id available; raw identity not logged")

	// Exclusive Windows fixture with Unicode and spaces; no existing project is used.
	project := filepath.Join(root, "Windows 工程 空格")
	if err = os.Mkdir(project, 0700); err != nil {
		return err
	}
	defer func() {
		e := os.RemoveAll(project)
		event("cleanup-windows-fixture", e == nil, "")
		err = errors.Join(err, e)
	}()
	if err = os.WriteFile(filepath.Join(project, "from-windows.txt"), []byte("WINDOWS_FIXTURE"), 0600); err != nil {
		return err
	}
	paths := platform.WSLTranslatePaths(distro, project)
	key := strings.ToLower(strings.TrimRight(strings.ReplaceAll(project, "/", `\`), `\`))
	linuxProject := paths[key]
	if linuxProject == "" {
		return errors.New("production WSL path translation did not resolve existing Unicode project")
	}
	pwd, e := wsl(distro, linuxProject, "/bin/pwd")
	if e != nil {
		return e
	}
	if pwd != linuxProject {
		return errors.New("WSL cwd does not equal mapped Windows project")
	}
	body, e := wsl(distro, linuxProject, "/bin/cat", "from-windows.txt")
	if e != nil {
		return e
	}
	if body != "WINDOWS_FIXTURE" {
		return errors.New("Linux could not read Windows fixture bytes")
	}
	_, e = wsl(distro, linuxProject, "/bin/sh", "-c", `printf '%s' "$2" > "$1"`, "probe", "from-linux.txt", "LINUX_FIXTURE")
	if e != nil {
		return e
	}
	b, e := os.ReadFile(filepath.Join(project, "from-linux.txt"))
	if e != nil {
		return e
	}
	if string(b) != "LINUX_FIXTURE" {
		return errors.New("Windows could not read Linux fixture bytes")
	}
	event("windows-project-unicode-spaces", true, "production translation + WSL cwd + bidirectional file IO")

	// Resolve cmd.exe using the real mount mapping rather than assuming /mnt/c.
	cmdPath := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	cmdMap := platform.WSLTranslatePaths(distro, cmdPath)
	cmdKey := strings.ToLower(strings.TrimRight(strings.ReplaceAll(cmdPath, "/", `\`), `\`))
	if cmdMap[cmdKey] == "" {
		return errors.New("Windows cmd.exe mount mapping unavailable")
	}
	out, e := wsl(distro, linuxProject, cmdMap[cmdKey], "/d", "/c", "echo", "WSL_WINDOWS_INTEROP_OK")
	if e != nil {
		return e
	}
	if !strings.Contains(out, "WSL_WINDOWS_INTEROP_OK") {
		return errors.New("Windows executable interop marker absent")
	}
	event("windows-executable-from-wsl", true, "cmd.exe invoked from mapped Windows fixture cwd")

	// The Linux temporary root is exclusively created and validated before cleanup.
	linuxRoot, e := wsl(distro, "", "/usr/bin/mktemp", "-d", "/tmp/amagi-codebox-wsl-XXXXXXXXXX")
	if e != nil {
		return e
	}
	if !strings.HasPrefix(linuxRoot, "/tmp/amagi-codebox-wsl-") || strings.Count(linuxRoot, "/") != 2 || strings.ContainsAny(linuxRoot, "\r\n\x00") {
		return errors.New("unexpected mktemp result; no deletion attempted")
	}
	event("linux-fixture-created", true, linuxRoot)
	defer func() {
		_, e := wsl(distro, "", "/bin/rm", "-rf", "--", linuxRoot)
		event("cleanup-linux-fixture", e == nil, linuxRoot)
		err = errors.Join(err, e)
	}()
	unc := platform.WSLToUNC(distro, linuxRoot)
	if unc == "" {
		return errors.New("WSL UNC share unavailable")
	}
	linuxFile := linuxRoot + "/权限 空格.txt"
	if e = os.WriteFile(filepath.Join(unc, "权限 空格.txt"), []byte("UNC_FIXTURE"), 0600); e != nil {
		return e
	}
	body, e = wsl(distro, "", "/bin/cat", linuxFile)
	if e != nil {
		return e
	}
	if body != "UNC_FIXTURE" {
		return errors.New("UNC write not visible from distro")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	e = platform.WSLChmodContext(ctx, distro, "600", linuxFile)
	cancel()
	if e != nil {
		return e
	}
	ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	mode, ok := platform.WSLFileModeContext(ctx, distro, linuxFile)
	cancel()
	if !ok || mode != "600" {
		return fmt.Errorf("mode roundtrip failed: ok=%v mode=%q", ok, mode)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	stamp, ok := platform.WSLFileMTimeContext(ctx, distro, linuxFile)
	cancel()
	if !ok || stamp <= 0 {
		return errors.New("mtime context probe failed")
	}
	rawStamp, e := wsl(distro, "", "/usr/bin/stat", "-c", "%Y", linuxFile)
	if e != nil {
		return e
	}
	if rawStamp != fmt.Sprint(stamp) {
		return errors.New("mtime disagrees with direct distro stat")
	}
	event("unc-and-context-stat-chmod", true, "UNC bytes, mode 600 and mtime verified through actual distro")

	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if platform.WSLTargetIdentityContext(cancelled, distro).OK {
		return errors.New("cancelled identity reported available")
	}
	if _, ok := platform.WSLFileModeContext(cancelled, distro, linuxFile); ok {
		return errors.New("cancelled mode reported available")
	}
	if _, ok := platform.WSLFileMTimeContext(cancelled, distro, linuxFile); ok {
		return errors.New("cancelled mtime reported available")
	}
	if e = platform.WSLChmodContext(cancelled, distro, "777", linuxFile); !errors.Is(e, context.Canceled) {
		return errors.New("cancelled chmod not rejected")
	}
	rawMode, e := wsl(distro, "", "/usr/bin/stat", "-c", "%a", linuxFile)
	if e != nil {
		return e
	}
	if rawMode != "600" {
		return errors.New("cancelled chmod modified permissions")
	}
	event("already-cancelled-context", true, "no chmod effect; does NOT establish termination of an in-flight Linux child")

	// Run the existing no-replace suite on the 9P UNC share, not Windows TEMP.
	testctx, testcancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer testcancel()
	test := exec.CommandContext(testctx, filepath.Join(root, testBinary), "-test.v", "-test.timeout=30s", "-test.count="+count, "-test.run=^TestMoveNoReplace")
	for _, v := range os.Environ() {
		k, _, _ := strings.Cut(v, "=")
		if !strings.EqualFold(k, "TEMP") && !strings.EqualFold(k, "TMP") {
			test.Env = append(test.Env, v)
		}
	}
	test.Env = append(test.Env, "TEMP="+unc, "TMP="+unc)
	output, e := test.CombinedOutput()
	fmt.Printf("UNC_NO_REPLACE_TEST_OUTPUT_BEGIN\n%sUNC_NO_REPLACE_TEST_OUTPUT_END\n", output)
	event("unc-no-replace-suite", e == nil, "existing file/directory/conflict/concurrency cases with TEMP/TMP on WSL UNC")
	if e != nil {
		return e
	}
	return nil
}
func main() {
	watchdog := time.AfterFunc(110*time.Second, func() {
		event("watchdog", false, "process stopped; inspect previously recorded fixture cleanup obligations")
		os.Exit(124)
	})
	defer watchdog.Stop()
	if len(os.Args) != 3 && len(os.Args) != 5 {
		event("usage", false, "expected fixture-root distro [test-binary count]")
		os.Exit(2)
	}
	testBinary, count := "platform-owner.test.exe", "1"
	if len(os.Args) == 5 {
		testBinary, count = os.Args[3], os.Args[4]
		n, err := strconv.Atoi(count)
		if err != nil || n < 1 || n > 20 || filepath.Base(testBinary) != testBinary || !strings.HasSuffix(testBinary, ".test.exe") {
			event("usage", false, "invalid local test filename or repetition count")
			os.Exit(2)
		}
	}
	err := probe(os.Args[1], os.Args[2], testBinary, count)
	if err != nil {
		event("result", false, err.Error())
		os.Exit(1)
	}
	event("result", true, "scope: platform primitives and synthetic Windows-project IO; not full CLI product acceptance")
}
