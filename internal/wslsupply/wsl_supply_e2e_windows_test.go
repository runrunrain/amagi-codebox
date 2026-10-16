//go:build windows

package wslsupply

// wsl_supply_e2e_windows_test.go — 真实 WSL 入口冒烟（P3-S0，只读、零凭据
// 接触）。默认跳过；设置 AMAGI_WSL_E2E=1：
//
//	AMAGI_WSL_E2E=1 go test -run TestManualRealWSLSupplyTarget ./internal/wslsupply/
//
// 覆盖（生产探测面，零注入）：DefaultWSLDistro → WSLTargetIdentity 批量身份
// 探测（uid/home/machine-id，单次 wsl.exe 调用）→ ResolveTarget（含 generation
// 与 UNC 根映射）→ Inspect（UNC 只读 stat，不读 auth.json 内容、不写目标）。
// 与 launcher 的 TestManualRealWSLPiLoginSync 不同，本冒烟永不触碰凭据字节：
// A2 判定/写入的实证由该测试承担（双侧快照恢复）。

import (
	"context"
	"os"
	"testing"

	"amagi-codebox/internal/platform"
)

func TestManualRealWSLSupplyTarget(t *testing.T) {
	if os.Getenv("AMAGI_WSL_E2E") == "" {
		t.Skip("set AMAGI_WSL_E2E=1 to run the real-WSL supply target smoke")
	}
	distro := platform.DefaultWSLDistro(nil)
	if distro == "" {
		t.Skip("no usable WSL distro on this machine")
	}
	c := NewCoordinator(t.TempDir(), nil, DefaultProbes())
	target, err := c.ResolveTarget(context.Background(), distro, CLIPi)
	if err != nil {
		t.Fatalf("ResolveTarget(%q): %v", distro, err)
	}
	t.Logf("target: distro=%s uid=%s home=%s linuxRoot=%s uncRoot=%s generation=%s",
		target.Distro, target.UID, target.Home, target.LinuxRoot, target.UNCRoot, target.Generation)
	if target.Home == "" || target.LinuxRoot == "" || target.UNCRoot == "" {
		t.Fatalf("incomplete target resolution: %+v", target)
	}
	snap, err := c.Inspect(context.Background(), target)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	for _, meta := range snap.Files {
		t.Logf("probe %s: exists=%v size=%d (hash omitted for credential files: %v)",
			meta.RelPath, meta.Exists, meta.Size, meta.Hash == "")
	}
	st := c.GetStatus(distro, CLIPi)
	if !st.Checked {
		t.Fatal("status must be checked after real inspect")
	}
	t.Logf("status: generation=%s home=%s checkedAt=%s units=%d", st.Generation, st.Home, st.CheckedAt, len(st.Units))
}
