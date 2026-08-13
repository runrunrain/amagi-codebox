package updater

import (
	"runtime"
	"strings"
	"testing"
)

func TestMaybeRunWindowsUpdateHelperIgnoresNormalLaunch(t *testing.T) {
	handled, err := MaybeRunWindowsUpdateHelper([]string{"amagi-codebox"})
	if err != nil || handled {
		t.Fatalf("normal launch should not be handled: handled=%v err=%v", handled, err)
	}
}

func TestMaybeRunWindowsUpdateHelperRejectsMalformedInvocation(t *testing.T) {
	handled, err := MaybeRunWindowsUpdateHelper([]string{"amagi-codebox", windowsUpdateHelperFlag})
	if !handled || err == nil {
		t.Fatalf("malformed helper invocation should fail closed: handled=%v err=%v", handled, err)
	}
}

func TestMaybeRunWindowsUpdateHelperRejectsNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows guard test")
	}
	handled, err := MaybeRunWindowsUpdateHelper([]string{"amagi-codebox", windowsUpdateHelperFlag, "1", "current.exe", "staged.exe"})
	if !handled || err == nil || !strings.Contains(err.Error(), "only supported on Windows") {
		t.Fatalf("expected platform guard: handled=%v err=%v", handled, err)
	}
}
