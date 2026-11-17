package paths

// list_directories_wsl_test.go — ListWSLDirectories 的离线单测
// （list_directories_wsl.go）：$HOME 展开（空/~/~后缀）、Linux 绝对路径
// 透传与尾分隔符归一、排序/截断、parent 链、缺目录/探测失败/home 探测
// 失败的错误语义、JSON 载荷形状。wsl 探测经包级接缝注入假实现。

import (
	"encoding/json"
	"strings"
	"testing"
)

func injectWSLSeams(t *testing.T, listFn func(distro, root string) ([]string, bool, error), homeFn func(distro string) string) {
	t.Helper()
	prevList, prevHome := wslListSubdirsFn, wslUserHomeFn
	wslListSubdirsFn, wslUserHomeFn = listFn, homeFn
	t.Cleanup(func() { wslListSubdirsFn, wslUserHomeFn = prevList, prevHome })
}

func TestListWSLDirectoriesEmptyAndTildeRootExpandToDistroHome(t *testing.T) {
	var probed []string
	injectWSLSeams(t,
		func(distro, root string) ([]string, bool, error) {
			probed = append(probed, distro+"|"+root)
			return []string{}, true, nil
		},
		func(distro string) string { return "/home/wsluser" },
	)
	svc := NewPathsService(t.TempDir())
	for _, tc := range []struct{ root, wantRoot string }{
		{"", "/home/wsluser"},
		{"~", "/home/wsluser"},
		{"~/proj", "/home/wsluser/proj"},
	} {
		raw, err := svc.ListWSLDirectories("Ubuntu", tc.root)
		if err != nil {
			t.Fatalf("root %q: %v", tc.root, err)
		}
		var payload directoryListing
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("root %q payload: %v", tc.root, err)
		}
		if payload.Root != tc.wantRoot {
			t.Fatalf("root %q → payload root %q, want %q", tc.root, payload.Root, tc.wantRoot)
		}
	}
	if len(probed) != 3 || probed[0] != "Ubuntu|/home/wsluser" || probed[2] != "Ubuntu|/home/wsluser/proj" {
		t.Fatalf("probe calls = %#v", probed)
	}
}

func TestListWSLDirectoriesLinuxAbsolutePassthrough(t *testing.T) {
	var gotRoot string
	injectWSLSeams(t,
		func(distro, root string) ([]string, bool, error) {
			gotRoot = root
			return []string{}, true, nil
		},
		nil,
	)
	svc := NewPathsService(t.TempDir())
	if _, err := svc.ListWSLDirectories("Ubuntu", "/home/u/repo/"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if gotRoot != "/home/u/repo" {
		t.Fatalf("probed root = %q, want trailing separators stripped", gotRoot)
	}
	if _, err := svc.ListWSLDirectories("Ubuntu", "/"); err != nil {
		t.Fatalf("root slash: %v", err)
	}
}

func TestListWSLDirectoriesRejectsNonLinuxForms(t *testing.T) {
	called := false
	injectWSLSeams(t,
		func(distro, root string) ([]string, bool, error) { called = true; return nil, true, nil },
		nil,
	)
	svc := NewPathsService(t.TempDir())
	for _, root := range []string{`C:\Users\u`, `C:/Work`, "relative/path", ".."} {
		_, err := svc.ListWSLDirectories("Ubuntu", root)
		if err == nil {
			t.Fatalf("root %q must be rejected", root)
		}
		if !strings.Contains(err.Error(), "Linux 绝对路径") {
			t.Fatalf("root %q error must explain the accepted forms, got: %v", root, err)
		}
	}
	if called {
		t.Fatal("rejected roots must not reach the probe")
	}
}

func TestListWSLDirectoriesSortsCapsAndJoins(t *testing.T) {
	names := []string{"zeta", "Alpha", "mid dir", "beta", "gamma", "delta"}
	injectWSLSeams(t,
		func(distro, root string) ([]string, bool, error) { return names, true, nil },
		nil,
	)
	prevLimit := listDirectoriesLimit
	listDirectoriesLimit = 3
	t.Cleanup(func() { listDirectoriesLimit = prevLimit })

	svc := NewPathsService(t.TempDir())
	raw, err := svc.ListWSLDirectories("Ubuntu", "/home/u")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var payload directoryListing
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if len(payload.Dirs) != 3 || !payload.Truncated {
		t.Fatalf("cap semantics broken: dirs=%d truncated=%v", len(payload.Dirs), payload.Truncated)
	}
	want := []string{"Alpha", "mid dir", "zeta"} // 探测序前 3 条，宿主侧小写序
	for i, w := range want {
		if payload.Dirs[i].Name != w {
			t.Fatalf("dirs[%d].Name = %q, want %q (sorted case-insensitively)", i, payload.Dirs[i].Name, w)
		}
		if payload.Dirs[i].Path != "/home/u/"+w {
			t.Fatalf("dirs[%d].Path = %q", i, payload.Dirs[i].Path)
		}
	}
	if payload.Parent == nil || *payload.Parent != "/home" {
		t.Fatalf("parent = %#v, want /home", payload.Parent)
	}
}

func TestListWSLDirectoriesParentChainAndRootJoin(t *testing.T) {
	injectWSLSeams(t,
		func(distro, root string) ([]string, bool, error) { return []string{"bin", "etc"}, true, nil },
		nil,
	)
	svc := NewPathsService(t.TempDir())

	raw, err := svc.ListWSLDirectories("Ubuntu", "/")
	if err != nil {
		t.Fatalf("root slash: %v", err)
	}
	var payload directoryListing
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.Parent != nil {
		t.Fatalf("filesystem root must have no parent, got %q", *payload.Parent)
	}
	if payload.Dirs[0].Path != "/bin" || payload.Dirs[1].Path != "/etc" {
		t.Fatalf("root join produced %q/%q, want /bin,/etc", payload.Dirs[0].Path, payload.Dirs[1].Path)
	}

	raw, err = svc.ListWSLDirectories("Ubuntu", "/home")
	if err != nil {
		t.Fatalf("/home: %v", err)
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.Parent == nil || *payload.Parent != "/" {
		t.Fatalf("/home parent = %#v, want /", payload.Parent)
	}
}

func TestListWSLDirectoriesMissingRootError(t *testing.T) {
	injectWSLSeams(t,
		func(distro, root string) ([]string, bool, error) { return nil, false, nil },
		nil,
	)
	svc := NewPathsService(t.TempDir())
	_, err := svc.ListWSLDirectories("Ubuntu", "/gone/dir")
	if err == nil || !strings.Contains(err.Error(), "不存在或不可访问") || !strings.Contains(err.Error(), "从主目录开始") {
		t.Fatalf("missing root must carry clear guidance, got: %v", err)
	}
}

func TestListWSLDirectoriesProbeFailureError(t *testing.T) {
	injectWSLSeams(t,
		func(distro, root string) ([]string, bool, error) { return nil, false, errProbeBoom },
		nil,
	)
	svc := NewPathsService(t.TempDir())
	_, err := svc.ListWSLDirectories("Ubuntu", "/home/u")
	if err == nil || !strings.Contains(err.Error(), "发行版不可达") {
		t.Fatalf("probe failure must surface unreachable distro, got: %v", err)
	}
}

func TestListWSLDirectoriesHomeProbeFailureError(t *testing.T) {
	injectWSLSeams(t,
		func(distro, root string) ([]string, bool, error) { return []string{}, true, nil },
		func(distro string) string { return "" },
	)
	svc := NewPathsService(t.TempDir())
	for _, root := range []string{"", "~", "~/proj"} {
		_, err := svc.ListWSLDirectories("Ubuntu", root)
		if err == nil || !strings.Contains(err.Error(), "无法解析") || !strings.Contains(err.Error(), "主目录") {
			t.Fatalf("root %q: home probe failure must be explicit, got: %v", root, err)
		}
	}
}

func TestListWSLDirectoriesRequiresDistro(t *testing.T) {
	svc := NewPathsService(t.TempDir())
	if _, err := svc.ListWSLDirectories("  ", "/home/u"); err == nil {
		t.Fatal("empty distro must fail")
	}
}

type probeError struct{}

func (probeError) Error() string { return "wsl.exe boom" }

var errProbeBoom = probeError{}
