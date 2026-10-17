package main

// launch_wsl_review_fixes_test.go — wsl-goal-closure 审核 PR01 修复的启动
// 闸门端到端回归（真实协调器 + runWSLSharedFilesAtLaunch）：
//   - v1 迁移遗留的敏感副本风险（awaiting-consent + legacy 标记）阻断启动；
//   - 受管副本在场 + 来源读失败（source-unavailable）阻断启动；
//   - 受管副本在场 + 目标身份探测失败（target-unresolved）阻断启动；
//   - 无受管副本历史的普通可选失败不阻断（PR01 反向边界）。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/wslsupply"
)

// reviewFixVisionSource 是可切换的宿主导出源（关闭 = os.ErrNotExist；瞬时
// 故障 = 非 ErrNotExist 错误；正常 = 固定字节）。
type reviewFixVisionSource struct {
	mode    string // "ok" / "closed" / "error"
	content []byte
}

func (s *reviewFixVisionSource) VisionExportBytes() ([]byte, error) {
	switch s.mode {
	case "closed":
		return nil, os.ErrNotExist
	case "error":
		return nil, errors.New("synthetic transient source failure")
	default:
		return s.content, nil
	}
}

func (s *reviewFixVisionSource) ModalityKBBytes() ([]byte, error) { return nil, nil }

// reviewFixGateEnv 持有可翻转身份/源的真实协调器环境。
type reviewFixGateEnv struct {
	c         *wslsupply.Coordinator
	src       *reviewFixVisionSource
	identity  *bool
	statePath string
	uncRoot   string
}

// uncRootOf 返回某 distro 的 UNC 根（单一 fixture 根）。
func (e *reviewFixGateEnv) uncRootOf(distro string) string { return e.uncRoot }

// hexHashOf 是测试侧 sha256 hex 助手。
func hexHashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newReviewFixGateEnv(t *testing.T, identityOK bool) *reviewFixGateEnv {
	t.Helper()
	uncRoot := t.TempDir()
	cfgDir := t.TempDir()
	modes := map[string]string{}
	identityOKFlag := identityOK
	src := &reviewFixVisionSource{mode: "ok", content: []byte(`{"version":1,"updated_at":"t","models":[{"id":"p/m","api_key":"sk"}]}`)}
	c := wslsupply.NewCoordinator(cfgDir, nil, wslsupply.Probes{
		Identity: func(_ context.Context, distro string) platform.WSLUserIdentity {
			if !identityOKFlag {
				return platform.WSLUserIdentity{}
			}
			// 逐目标身份：非 Ubuntu 目标使用不同 Home（旧公式指纹随身份区分——
			// 攻坚契约 PR01 的逐目标核验语义，克隆目标按指纹独立裁决）。
			home := "/home/u"
			if distro != "Ubuntu" {
				home = "/home/" + strings.ToLower(distro)
			}
			return platform.WSLUserIdentity{UID: "1000", Home: home, MachineID: "m-review", OK: true}
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(_ context.Context, _, mode string, paths ...string) error {
			for _, p := range paths {
				modes[p] = mode
			}
			return nil
		},
		StatMode: func(_ context.Context, _, linuxPath string) (string, bool) {
			m, ok := modes[linuxPath]
			return m, ok
		},
	})
	c.RegisterProjection(wslsupply.NewVisionExportAdapter(src))
	c.RegisterProjection(wslsupply.NewModalityKBAdapter(src))
	return &reviewFixGateEnv{c: c, src: src, identity: &identityOKFlag, statePath: filepath.Join(cfgDir, wslsupply.StateFileName), uncRoot: uncRoot}
}

func (e *reviewFixGateEnv) publish(t *testing.T) {
	t.Helper()
	if err := e.c.GrantUnitConsent(context.Background(), "Ubuntu", wslsupply.CLICommon, wslsupply.UnitSharedVisionExport); err != nil {
		t.Fatal(err)
	}
	res, err := e.c.RunUnits(context.Background(), "Ubuntu", wslsupply.CLICommon, wslsupply.ReasonLaunch,
		[]wslsupply.UnitID{wslsupply.UnitSharedVisionExport})
	if err != nil {
		t.Fatal(err)
	}
	if res.Results[0].Outcome != wslsupply.UnitApplied {
		t.Fatalf("setup publish: %+v", res.Results[0])
	}
}

// TestWSLSharedGateReviewFixPR01LegacyRiskBlocks：v1 曾发布 → 升级 → 宿主
// 关闭导出：新代 awaiting-consent + 旧副本可能在 → 阻断并给出中文可处理
// 指引（反例：phase-review-repro.go LEGACY_WITHDRAWAL）。
func TestWSLSharedGateReviewFixPR01LegacyRiskBlocks(t *testing.T) {
	env := newReviewFixGateEnv(t, true)
	// 攻坚契约 PR01：v1 线索用旧公式真实指纹 + 与实际旧副本一致的字节 hash
	//（逐目标核验语义：指纹匹配本目标 → 风险归属命中，文件在即阻断）。
	oldSum := sha256.Sum256([]byte("m-review|1000|/home/u"))
	oldGen := hex.EncodeToString(oldSum[:])
	oldCopy := []byte(`{"version":1,"api_key":"OLD-LEGACY"}`)
	legacy := wslsupply.StateFile{
		SchemaVersion: 1, Generation: oldGen, Distro: "Ubuntu", Home: "/home/u",
		Units: []wslsupply.StateEntry{{
			Unit: wslsupply.UnitSharedVisionExport, Policy: wslsupply.PolicyManagedNamespace,
			Generation: oldGen, Consented: true, LastWrittenHash: hexHashOf(oldCopy), LastOutcome: "written",
		}},
	}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.statePath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	// 旧副本仍在 WSL 目标上（升级前发布过）。
	visionPath := filepath.Join(env.uncRootOf("Ubuntu"), "home/u/.agents", "amagi-media-models.json")
	if err := os.MkdirAll(filepath.Dir(visionPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(visionPath, oldCopy, 0o600); err != nil {
		t.Fatal(err)
	}
	// 宿主关闭导出（升级后用户停用视觉清单导出）。
	env.src.mode = "closed"
	gateErr := runWSLSharedFilesAtLaunch(context.Background(), env.c, "Ubuntu")
	if gateErr == nil {
		t.Fatal("legacy sensitive copy risk must block the launch gate")
	}
	if !strings.Contains(gateErr.Error(), "视觉清单") || !strings.Contains(gateErr.Error(), "处理") {
		t.Fatalf("block must carry an actionable Chinese error, got %v", gateErr)
	}
	// 其它 distro 不被误算（无该 distro 的遗留风险）。
	if err := runWSLSharedFilesAtLaunch(context.Background(), env.c, "Other"); err != nil {
		t.Fatalf("other distro must not inherit the legacy risk: %v", err)
	}
}

// TestWSLSharedGateReviewFixPR01ManagedSourceErrorBlocks：受管副本在场 +
// 来源读失败（blocked source-unavailable）→ 阻断（反例：MANAGED_SOURCE_ERROR）。
func TestWSLSharedGateReviewFixPR01ManagedSourceErrorBlocks(t *testing.T) {
	env := newReviewFixGateEnv(t, true)
	env.publish(t)
	env.src.mode = "error"
	err := runWSLSharedFilesAtLaunch(context.Background(), env.c, "Ubuntu")
	if err == nil {
		t.Fatal("managed copy + source failure must block the launch gate")
	}
	if !strings.Contains(err.Error(), "视觉清单") {
		t.Fatalf("block must identify the vision export copy, got %v", err)
	}
}

// TestWSLSharedGateReviewFixPR01IdentityFailureBlocks：受管副本在场 + 目标
// 身份探测失败（blocked target-unresolved）→ 阻断；无副本历史时同型失败
// 不阻断普通启动。
func TestWSLSharedGateReviewFixPR01IdentityFailureBlocks(t *testing.T) {
	t.Run("with managed copy blocks", func(t *testing.T) {
		env := newReviewFixGateEnv(t, true)
		env.publish(t)
		*env.identity = false // 身份探测失败（machine-id 不可得等）
		err := runWSLSharedFilesAtLaunch(context.Background(), env.c, "Ubuntu")
		if err == nil {
			t.Fatal("identity failure with a possible managed copy must block the launch gate")
		}
		if !strings.Contains(err.Error(), "视觉清单") {
			t.Fatalf("block must identify the vision export copy, got %v", err)
		}
	})
	t.Run("without managed copy passes", func(t *testing.T) {
		env := newReviewFixGateEnv(t, false)
		err := runWSLSharedFilesAtLaunch(context.Background(), env.c, "Ubuntu")
		if err != nil {
			t.Fatalf("identity failure without any copy history must not block normal launch: %v", err)
		}
	})
}

// TestWSLSharedGateReviewFixPR01NoHistoryPasses：从未授权/从未发布的目标上
// awaiting-consent / unmanaged-existing 等预期态不阻断普通启动。
func TestWSLSharedGateReviewFixPR01NoHistoryPasses(t *testing.T) {
	env := newReviewFixGateEnv(t, true)
	env.src.mode = "closed" // 宿主无导出 + 本目标从未发布 → ActionNone/Fresh
	if err := runWSLSharedFilesAtLaunch(context.Background(), env.c, "Ubuntu"); err != nil {
		t.Fatalf("no-history optional state must not block launch: %v", err)
	}
}
