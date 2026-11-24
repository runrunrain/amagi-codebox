package v2rayn

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const bundledCoreFile = "bin/sing_box/sing-box.exe"

func installRoots() []string {
	// v2rayN portable builds keep guiConfigs and bin alongside the exe,
	// often on a different drive from CodeBox and outside AppData.
	roots := runningInstallRoots()
	for _, name := range []string{"APPDATA", "LOCALAPPDATA", "ProgramFiles", "ProgramFiles(x86)"} {
		if base := os.Getenv(name); filepath.IsAbs(base) {
			roots = append(roots, filepath.Join(base, "v2rayN"))
		}
	}
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Dir(exe))
	}
	return roots
}

func runningInstallRoots() []string {
	var sessionID uint32
	if windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &sessionID) != nil {
		return nil
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()

	// Only query instances in the current Windows session owned by the
	// current user. A protected or already exited process is skipped.
	roots := []string{}
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err := windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "v2rayN.exe") {
			continue
		}
		var candidateSession uint32
		if windows.ProcessIdToSessionId(entry.ProcessID, &candidateSession) != nil || candidateSession != sessionID {
			continue
		}
		if dir := processInstallRoot(entry.ProcessID, user.User.Sid); dir != "" {
			roots = append(roots, dir)
		}
	}
	slices.Sort(roots)
	return slices.Compact(roots)
}

func processInstallRoot(pid uint32, owner *windows.SID) string {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(process) }()
	var token windows.Token
	if windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token) != nil {
		return ""
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil || !owner.Equals(user.User.Sid) {
		return ""
	}

	buf := make([]uint16, 32768) // maximum Windows extended path length
	size := uint32(len(buf))
	if windows.QueryFullProcessImageName(process, 0, &buf[0], &size) != nil {
		return ""
	}
	exe := windows.UTF16ToString(buf[:size])
	if !filepath.IsAbs(exe) || !strings.EqualFold(filepath.Base(exe), "v2rayN.exe") {
		return ""
	}
	return filepath.Dir(exe)
}
