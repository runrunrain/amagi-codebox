package sharedext

// helpers_test.go — 测试辅助：注入引擎根目录的 Service 构造与 zip 工厂。

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// newTestService 创建 baseDir 注入 t.TempDir 的 Service，并为全部引擎
// 注入独立临时根，避免测试触碰真实 home。
type testEnv struct {
	svc    *Service
	base   string
	roots  map[Engine]string
}

func newTestService(t *testing.T) *testEnv {
	t.Helper()
	base := t.TempDir()
	svc := NewService(base, nil)
	env := &testEnv{svc: svc, base: base, roots: map[Engine]string{}}
	for _, engine := range AllEngines() {
		root := t.TempDir()
		env.roots[engine] = root
		svc.SetEngineBaseDir(engine, root)
	}
	return env
}

// skillsTarget 返回某引擎 skills 目录（注入根解析）。
func (e *testEnv) skillsTarget(engine Engine, name string) string {
	dir, err := e.svc.engineSkillsDir(engine)
	if err != nil {
		panic(err)
	}
	return filepath.Join(dir, name)
}

// writeSkillDir 在磁盘上构造一个 skill 目录（含 SKILL.md）。
func writeSkillDir(t *testing.T, parent, name, skillMD string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// makeZip 在 path 构造一个 zip：files 的 key 为 entry 名，value 为内容。
func makeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w := zip.NewWriter(f)
	for name, content := range files {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// makeZipWithSymlink 构造含符号链接 entry 的 zip（alpha skill + 链接项）。
func makeZipWithSymlink(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w := zip.NewWriter(f)
	entry, err := w.Create("alpha/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(sampleSkillMD)); err != nil {
		t.Fatal(err)
	}
	hdr := &zip.FileHeader{Name: "alpha/link.md"}
	hdr.SetMode(os.ModeSymlink | 0o644)
	linkEntry, err := w.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := linkEntry.Write([]byte("../SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

const sampleSkillMD = `---
name: alpha
description: 一个示例技能
---

# Alpha

正文第一段。
`

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
