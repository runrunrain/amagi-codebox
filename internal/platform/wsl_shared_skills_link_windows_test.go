package platform

import (
	"os"
	"testing"
	"time"
)

// Set AMAGI_CODEBOX_TEST_WSL to a real distro name. Both the Windows target
// and the Linux HOME are temporary; the user's shared skill link is untouched.
func TestWSLSharedSkillsLinkThroughRealDistro(t *testing.T) {
	distro := os.Getenv("AMAGI_CODEBOX_TEST_WSL")
	if distro == "" {
		t.Skip("set AMAGI_CODEBOX_TEST_WSL to an installed WSL distro")
	}
	original := wslScriptRunner
	calls := 0
	wslScriptRunner = func(distro, script string) ([]byte, error) {
		calls++
		return original(distro, "HOME=$(mktemp -d) || exit 1\ntrap 'rm -rf -- \"$HOME\"' EXIT\n"+script)
	}
	t.Cleanup(func() { wslScriptRunner = original })
	start := time.Now()
	result := WSLEnsureSharedAgentsSkillsLink(distro, t.TempDir())
	if result.State != WSLSkillsLinked || result.Target == "" || calls != 1 {
		t.Fatalf("expected one successful WSL round trip, calls=%d result=%+v", calls, result)
	}
	t.Logf("WSL translated and verified a shared link in %s", time.Since(start))
}
