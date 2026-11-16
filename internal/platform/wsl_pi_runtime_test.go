package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWSLPiRuntimeLocalDefaultsAndExplicitOverrides(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("requires a native POSIX filesystem")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell unavailable")
	}
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaults", true: "explicit"}[explicit], func(t *testing.T) {
			home := t.TempDir()
			state := filepath.Join(home, "state space ' literal")
			env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_STATE_HOME=" + state, "PI_CODING_AGENT_DIR=/mnt/c/Users/shared/.pi/agent", "PI_CODING_AGENT_SESSION_DIR=/keep/transcripts", "NODE_COMPILE_CACHE=/keep/node-cache"}
			keys := []string{"AMAGI_DATA_DIR", "AMAGI_ARTIFACT_ROOT"}
			if explicit {
				for _, key := range keys {
					env = append(env, key+"=/explicit/"+key)
				}
			}
			cmd := exec.Command(sh, "-c", "umask 022\n"+WSLPiRuntimePrelude()+`[ "$PI_CODING_AGENT_SESSION_DIR" = '/keep/transcripts' ] && [ "$NODE_COMPILE_CACHE" = '/keep/node-cache' ] || exit 91; printf '%s\n' "$AMAGI_DATA_DIR" "$AMAGI_ARTIFACT_ROOT" "$PI_CODING_AGENT_DIR"; umask`)
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("output=%s err=%v", out, err)
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) != 4 {
				t.Fatalf("unexpected output: %s", out)
			}
			for i, suffix := range []string{"data", "artifacts"} {
				want := filepath.Join(state, "amagi-codebox", "pi", suffix)
				if explicit {
					want = "/explicit/" + keys[i]
				}
				if lines[i] != want {
					t.Errorf("%s=%q want=%q", keys[i], lines[i], want)
				}
			}
			if lines[2] != "/mnt/c/Users/shared/.pi/agent" {
				t.Fatal("canonical config/auth root changed")
			}
			if !strings.HasSuffix(lines[3], "022") {
				t.Fatalf("umask not restored: %q", lines[3])
			}
			info, err := os.Stat(filepath.Join(state, "amagi-codebox", "pi", "data"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o700 {
				t.Fatalf("runtime data must be private: %v", info.Mode())
			}
		})
	}
}

func TestWSLPiRuntimeRejectsDrvFSStateDefault(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("requires a native POSIX filesystem")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell unavailable")
	}
	home := t.TempDir()
	cmd := exec.Command(sh, "-c", WSLPiRuntimePrelude()+`printf '%s' "$AMAGI_DATA_DIR"`)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_STATE_HOME=/mnt/c/Windows-state"}
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != filepath.Join(home, ".local", "state", "amagi-codebox", "pi", "data") {
		t.Fatalf("output=%q err=%v", out, err)
	}
}

func TestWSLPiRuntimeFailureDoesNotChangeCanonicalRoot(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("requires a native POSIX filesystem")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell unavailable")
	}
	home := t.TempDir()
	blocked := filepath.Join(home, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("user-owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sh, "-c", WSLPiRuntimePrelude()+`printf 'kept:%s:%s' "$PI_CODING_AGENT_DIR" "${AMAGI_DATA_DIR-unset}"`)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_STATE_HOME=" + blocked, "PI_CODING_AGENT_DIR=/mnt/c/shared"}
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "kept:/mnt/c/shared:unset") {
		t.Fatalf("output=%q err=%v", out, err)
	}
	data, _ := os.ReadFile(blocked)
	if string(data) != "user-owned" {
		t.Fatal("existing user content overwritten")
	}
}

func TestWSLPiRuntimeInvalidHomeCannotCreateProjectDirectories(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("requires a native POSIX filesystem")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell unavailable")
	}
	project := t.TempDir()
	cmd := exec.Command(sh, "-c", WSLPiRuntimePrelude()+`printf 'data:%s' "${AMAGI_DATA_DIR-unset}"`)
	cmd.Dir = project
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), `HOME=C:\Users\foreign`}
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "data:unset") {
		t.Fatalf("output=%q err=%v", out, err)
	}
	entries, err := os.ReadDir(project)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid home created project entries: %v %v", entries, err)
	}
}
