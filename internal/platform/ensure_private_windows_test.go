//go:build windows

package platform

// ensure_private_windows_test.go — Windows 专项判据回归（修正轮 + owner
// 规范化轮）：
//   - 修正轮钉住 x/sys SECURITY_DESCRIPTOR.DACL() 的第二返回值是 defaulted
//     （SE_DACL_DEFAULTED 标志），不是 DACL 是否在场：显式（defaulted=false）
//     合法 DACL 被接受；缺失 DACL 与 NULL（「空 DACL」＝全放行）DACL 被拒
//     绝；owner 与逐 ACE 主体检查保持 fail closed。
//   - owner 规范化轮（windows-platform-real-tests-v2.log 真机缺陷：提升令
//     牌默认 TokenOwner 为 Administrators，只设 DACL 时新建对象 owner 是组
//     SID）钉住：EnsurePrivate* 把 owner 规范化回当前用户；owner 覆写与
//     DACL 收紧不改动文件字节；owner/非法主体判据不得因规范化而放宽。
//
// 判据用例以内存构造的安全描述符判定，不依赖宿主文件系统 ACL 形态；
// 端到端用例（真实 set→读回→判据）供 Windows 真机/CI 执行。
//
// 运行边界：本套件仅 Windows 构建可编译运行；本轮在 darwin 导出机仅做交叉
// 编译验证，真机复测由 Leader 执行（见 attack-platform-owner.md）。

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// buildDescriptorWithDACL 构造内存安全描述符：owner 固定为当前用户
// （defaulted=false），DACL 形态由入参决定。
func buildDescriptorWithDACL(t *testing.T, dacl *windows.ACL, present, defaulted bool) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	_, user, err := privateAllowedSIDs()
	if err != nil {
		t.Fatalf("privateAllowedSIDs: %v", err)
	}
	sd, err := windows.NewSecurityDescriptor()
	if err != nil {
		t.Fatalf("NewSecurityDescriptor: %v", err)
	}
	if err := sd.SetOwner(user, false); err != nil {
		t.Fatalf("SetOwner: %v", err)
	}
	if err := sd.SetDACL(dacl, present, defaulted); err != nil {
		t.Fatalf("SetDACL: %v", err)
	}
	return sd
}

// validateDescriptor 依生产判据验证内存描述符（allowed/当前用户与
// verifyPrivateACL 同源现取）。
func validateDescriptor(t *testing.T, sd *windows.SECURITY_DESCRIPTOR) error {
	t.Helper()
	allowed, user, err := privateAllowedSIDs()
	if err != nil {
		t.Fatalf("privateAllowedSIDs: %v", err)
	}
	return validatePrivateDescriptor(sd, allowed, user)
}

// buildSingleACEDACL 以单条显式项构造 ACL（mode/sid 由用例指定）；
// Pinner 契约与生产 buildPrivateDACL 一致。
func buildSingleACEDACL(t *testing.T, mode windows.ACCESS_MODE, sid *windows.SID) *windows.ACL {
	t.Helper()
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(sid)
	entries := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        mode,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatalf("ACLFromEntries: %v", err)
	}
	return acl
}

// TestValidatePrivateDescriptorExplicitDACLAccepted 是修正轮的直接回归：
// setPrivateOwnerAndDACL 后读回的正常形态即「在场 + defaulted=false」的显
// 式 DACL，旧实现把 defaulted 误当 present 而拒绝它（真机上 EnsurePrivate*
// 必失败）。
func TestValidatePrivateDescriptorExplicitDACLAccepted(t *testing.T) {
	dacl, err := buildPrivateDACL(false)
	if err != nil {
		t.Fatalf("buildPrivateDACL: %v", err)
	}
	sd := buildDescriptorWithDACL(t, dacl, true, false)
	if err := validateDescriptor(t, sd); err != nil {
		t.Fatalf("explicit DACL (defaulted=false) must be accepted, got: %v", err)
	}
}

func TestValidatePrivateDescriptorMissingDACLRejected(t *testing.T) {
	sd := buildDescriptorWithDACL(t, nil, false, false)
	err := validateDescriptor(t, sd)
	if err == nil {
		t.Fatal("missing DACL must be rejected")
	}
	if !strings.Contains(err.Error(), "no DACL") {
		t.Fatalf("err = %v, want 'no DACL' mention", err)
	}
	if !errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
		t.Fatalf("err = %v, want ERROR_OBJECT_NOT_FOUND in chain", err)
	}
}

func TestValidatePrivateDescriptorNullDACLRejected(t *testing.T) {
	// NULL DACL（在场、dacl==nil、err==nil）语义是「全放行」——最危险的
	// 形态之一，必须与「缺失 DACL」区分开各自拒绝。
	sd := buildDescriptorWithDACL(t, nil, true, false)
	err := validateDescriptor(t, sd)
	if err == nil {
		t.Fatal("NULL DACL must be rejected")
	}
	if !strings.Contains(err.Error(), "NULL DACL") {
		t.Fatalf("err = %v, want 'NULL DACL' mention", err)
	}
}

func TestValidatePrivateDescriptorDefaultedDACLRejected(t *testing.T) {
	dacl, err := buildPrivateDACL(false)
	if err != nil {
		t.Fatalf("buildPrivateDACL: %v", err)
	}
	// defaulted=true：DACL 来自默认机制而非我们的显式设置，fail closed。
	sd := buildDescriptorWithDACL(t, dacl, true, true)
	err = validateDescriptor(t, sd)
	if err == nil {
		t.Fatal("defaulted DACL must be rejected")
	}
	if !strings.Contains(err.Error(), "defaulted") {
		t.Fatalf("err = %v, want 'defaulted' mention", err)
	}
}

func TestValidatePrivateDescriptorOwnerMismatchRejected(t *testing.T) {
	dacl, err := buildPrivateDACL(false)
	if err != nil {
		t.Fatalf("buildPrivateDACL: %v", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid: %v", err)
	}
	sd, err := windows.NewSecurityDescriptor()
	if err != nil {
		t.Fatalf("NewSecurityDescriptor: %v", err)
	}
	if err := sd.SetOwner(system, false); err != nil {
		t.Fatalf("SetOwner: %v", err)
	}
	if err := sd.SetDACL(dacl, true, false); err != nil {
		t.Fatalf("SetDACL: %v", err)
	}
	err = validateDescriptor(t, sd)
	if err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("err = %v, want owner mismatch rejection", err)
	}
}

func TestValidatePrivateDescriptorUnexpectedPrincipalRejected(t *testing.T) {
	// allow-ACE 但主体是 Everyone（World）：ACE 类型检查放行、主体集合拒绝。
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid: %v", err)
	}
	sd := buildDescriptorWithDACL(t, buildSingleACEDACL(t, windows.SET_ACCESS, world), true, false)
	err = validateDescriptor(t, sd)
	if err == nil || !strings.Contains(err.Error(), "unexpected principal") {
		t.Fatalf("err = %v, want unexpected-principal rejection", err)
	}
}

func TestValidatePrivateDescriptorDenyACERejected(t *testing.T) {
	// deny-ACE：非 ACCESS_ALLOWED 类型即拒绝（即使主体在允许集合内）。
	_, user, err := privateAllowedSIDs()
	if err != nil {
		t.Fatalf("privateAllowedSIDs: %v", err)
	}
	sd := buildDescriptorWithDACL(t, buildSingleACEDACL(t, windows.DENY_ACCESS, user), true, false)
	err = validateDescriptor(t, sd)
	if err == nil || !strings.Contains(err.Error(), "not an allow-ACE") {
		t.Fatalf("err = %v, want non-allow-ACE rejection", err)
	}
}

// TestEnsurePrivateWindowsRealACLRoundTrip 为 Windows 真实 ACL 子系统保留的
// 端到端回归：set → 读回 → 判据。旧缺陷在真机上必失败（显式 DACL 被拒）。
func TestEnsurePrivateWindowsRealACLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	private := filepath.Join(dir, "private-dir")
	if err := EnsurePrivateDir(private); err != nil {
		t.Fatalf("EnsurePrivateDir: %v", err)
	}
	secret := filepath.Join(dir, "secret.json")
	if err := EnsurePrivateFile(secret); err != nil {
		t.Fatalf("EnsurePrivateFile: %v", err)
	}
	if err := verifyPrivateACL(private); err != nil {
		t.Fatalf("verifyPrivateACL(dir): %v", err)
	}
	if err := verifyPrivateACL(secret); err != nil {
		t.Fatalf("verifyPrivateACL(file): %v", err)
	}
}

// currentTokenElevated 报告当前进程令牌是否提升（UAC 视角）——决定
// 「把 owner 置为 Administrators（令牌默认 owner 的合法取值）」前置是否
// 可用：非提升令牌无权置为组 SID，属环境能力差异而非缺陷。
func currentTokenElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// forceOwnerTo 尽力把 path 的 owner 置为指定 SID，返回是否成功。
// ERROR_ACCESS_DENIED/ERROR_INVALID_OWNER 表示「无权置为该 owner」：非
// 提升环境下是预期前置不可用，交调用方降级；其它错误如实失败。
func forceOwnerTo(t *testing.T, path string, sid *windows.SID) bool {
	t.Helper()
	err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, sid, nil, nil, nil)
	if err == nil {
		return true
	}
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_INVALID_OWNER) {
		return false
	}
	t.Fatalf("force owner on %s: %v", path, err)
	return false
}

// ownerSIDOf 读回 path 的 owner SID（仅供测试前置/断言使用）。
func ownerSIDOf(t *testing.T, path string) *windows.SID {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(owner) %s: %v", path, err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatalf("sd.Owner() %s: %v", path, err)
	}
	return owner
}

// adminsSID 是内建 Administrators 组 SID（提升令牌的默认 owner）。
func adminsSID(t *testing.T) *windows.SID {
	t.Helper()
	sid, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid(Administrators): %v", err)
	}
	return sid
}

// TestEnsurePrivateWindowsNormalizesOwnerToCurrentUser 是 owner 规范化轮的
// 直接回归（真机 v2 日志：EnsurePrivateDir/File 与 RealACLRoundTrip 全因
// owner 为 Administrators 组而非当前用户失败）：提升令牌下新建对象默认
// owner 是 Administrators，EnsurePrivate* 必须把它规范化回当前用户。
// 非提升环境无法主动构造非当前用户 owner 前置，退化为断言 owner 仍为当
// 前用户（同一验证路径）；提升令牌下前置构造失败则环境前提断裂、如实
// 失败，不得跳过。
func TestEnsurePrivateWindowsNormalizesOwnerToCurrentUser(t *testing.T) {
	dir := t.TempDir()
	admins := adminsSID(t)
	_, user, err := privateAllowedSIDs()
	if err != nil {
		t.Fatalf("privateAllowedSIDs: %v", err)
	}
	if owner := ownerSIDOf(t, dir); windows.EqualSid(owner, user) && currentTokenElevated() {
		// 提升令牌新建对象默认 owner 应是 Administrators；若是当前用户，
		// 说明环境前提与真机证据不一致，仅提示不失败（不影响断言有效性：
		// 下面会主动强制非当前用户 owner 前置）。
		t.Logf("elevated token but temp dir already owned by current user")
	}

	t.Run("dir owner normalized", func(t *testing.T) {
		p := filepath.Join(dir, "norm-dir")
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		forced := forceOwnerTo(t, p, admins)
		if !forced && currentTokenElevated() {
			t.Fatal("elevated token must be able to force owner to Administrators (default token owner)")
		}
		if forced && windows.EqualSid(ownerSIDOf(t, p), user) {
			t.Fatal("precondition failed: forced owner unexpectedly equals current user")
		}
		if err := EnsurePrivateDir(p); err != nil {
			t.Fatalf("EnsurePrivateDir: %v", err)
		}
		if !windows.EqualSid(ownerSIDOf(t, p), user) {
			t.Fatal("owner must be normalized to the current user after EnsurePrivateDir")
		}
		if err := verifyPrivateACL(p); err != nil {
			t.Fatalf("verifyPrivateACL: %v", err)
		}
	})

	t.Run("file owner normalized", func(t *testing.T) {
		p := filepath.Join(dir, "norm-file.json")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		forced := forceOwnerTo(t, p, admins)
		if !forced && currentTokenElevated() {
			t.Fatal("elevated token must be able to force owner to Administrators (default token owner)")
		}
		if err := EnsurePrivateFile(p); err != nil {
			t.Fatalf("EnsurePrivateFile: %v", err)
		}
		if !windows.EqualSid(ownerSIDOf(t, p), user) {
			t.Fatal("owner must be normalized to the current user after EnsurePrivateFile")
		}
		if err := verifyPrivateACL(p); err != nil {
			t.Fatalf("verifyPrivateACL: %v", err)
		}
	})
}

// TestEnsurePrivateWindowsTightenPreservesFileBytes 针对性钉住：owner 覆写
// 与 DACL 收紧路径不得触碰文件字节（受管文件常已存有既有内容）。
func TestEnsurePrivateWindowsTightenPreservesFileBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bytes.json")
	content := []byte(`{"secret":"unchanged-bytes"}`)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	forceOwnerTo(t, p, adminsSID(t)) // 尽力构造最严前置（owner 非当前用户）
	if err := EnsurePrivateFile(p); err != nil {
		t.Fatalf("EnsurePrivateFile: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("bytes altered: %q", got)
	}
}

// TestEnsurePrivateWindowsForeignSecurityStillRejected 钉住 owner 规范化
// 修复不得放宽 fail-closed 判据：合法 owner + 非法主体（Everyone）DACL 仍
// 拒绝；私有 DACL + owner 被拨回 Administrators 仍拒绝。
func TestEnsurePrivateWindowsForeignSecurityStillRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tamper.json")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateFile(p); err != nil {
		t.Fatalf("EnsurePrivateFile: %v", err)
	}

	// (a) owner 合法（当前用户）但 DACL 含 Everyone allow-ACE：仍拒绝。
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid(World): %v", err)
	}
	if err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, buildSingleACEDACL(t, windows.SET_ACCESS, world), nil); err != nil {
		t.Fatalf("tamper DACL with Everyone ACE: %v", err)
	}
	if err := verifyPrivateACL(p); err == nil || !strings.Contains(err.Error(), "unexpected principal") {
		t.Fatalf("err = %v, want unexpected-principal rejection", err)
	}

	// (b) 私有 DACL + owner 被拨回 Administrators 组：owner 判据不放宽。
	if err := EnsurePrivateFile(p); err != nil {
		t.Fatalf("re-tighten: %v", err)
	}
	if forceOwnerTo(t, p, adminsSID(t)) {
		if err := verifyPrivateACL(p); err == nil || !strings.Contains(err.Error(), "owner") {
			t.Fatalf("err = %v, want owner mismatch rejection", err)
		}
	}
}
