//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ensure_private_windows.go — Windows 受管路径私有化：真实 DACL，而非
// os.Chmod（后者只拨只读属性，不构成访问隔离）。
//
// 实现：SetNamedSecurityInfo 一次性把 owner 规范化为当前用户并以显式
// owner-only DACL 覆盖目标（PROTECTED_DACL 阻断继承混入），随后
// GetNamedSecurityInfo 读回并逐 ACE 验证——每个 allow-ACE 的 SID 必须属于
// 允许集合 {当前用户, LOCAL SYSTEM, 内建 Administrators}，且不含 deny/其它
// 类型 ACE；owner 必须是当前用户；DACL 必须显式在场（缺失、NULL（全放行）
// 与 defaulted 均判隔离未验证）。验证不可证即返回错误（fail closed）。
//
// owner 规范化的依据（真机缺陷，windows-platform-real-tests-v2.log）：提升
// 令牌（admin token）的默认 TokenOwner 是内建 Administrators 组，
// os.MkdirAll/os.OpenFile 新建的对象以组为 owner 落盘，验证轮随即被
// 「owner 必须是当前用户」判据 fail closed 拒绝。因此设置 DACL 的同一调用
// 以当前用户 SID 覆写 owner——这是进程身份本就可合法持有的取值，只作用于
// 受管路径自身，不启用 SeTakeOwnership/Restore/Backup 等额外系统特权，不
// 触及全局 ACL/账户；无权设置 owner（如目标 owner 他人且无 WRITE_OWNER）
// 时如实返回错误，不静默降级。
//
// 威胁模型（明示）：允许当前用户、LOCAL SYSTEM 与内建 Administrators 完全
// 访问（SYSTEM/管理员是 Windows 文件所有权/备份语义的标准伴生主体）；其它
// 本机用户零访问。错误信息不携带 SID 字符串或 DACL 原始内容。
//
// 未真机复测声明：本轮 owner 规范化修复在 darwin 导出机以目标包测试与
// Windows 交叉编译覆盖；真实 ACL 行为（owner 覆写、继承阻断效果、SID/ACE
// 形态、UNC 目标）由 Leader 在 Windows 真机复测。

// privateAllowedSIDs 返回允许完全访问的 SID 集合（当前用户 + LOCAL SYSTEM
// + 内建 Administrators）。每次现取当前用户 SID（进程身份变化时保持正确）。
func privateAllowedSIDs() ([]*windows.SID, *windows.SID, error) {
	token := windows.GetCurrentProcessToken()
	tu, err := token.GetTokenUser()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve current user: %w", err)
	}
	user := tu.User.Sid
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve LOCAL SYSTEM SID: %w", err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve Administrators SID: %w", err)
	}
	return []*windows.SID{user, system, admins}, user, nil
}

// buildPrivateDACL 构造显式 owner-only DACL：三个允许主体各一条 SET_ACCESS
// GENERIC_ALL 显式项（目录附子对象继承位，作用于目录内后续新建对象）。
// runtime.Pinner 固定 SID 以满足 x/sys 的 TrusteeValueFromSID 安全契约
// （BuildSecurityDescriptor 调用期间保持固定，函数返回即解除）。
func buildPrivateDACL(isDir bool) (*windows.ACL, error) {
	sids, _, err := privateAllowedSIDs()
	if err != nil {
		return nil, err
	}
	inheritance := uint32(0)
	if isDir {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	entries := make([]windows.EXPLICIT_ACCESS, 0, len(sids))
	var pinner runtime.Pinner
	defer pinner.Unpin()
	for _, sid := range sids {
		pinner.Pin(sid)
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	sd, err := windows.BuildSecurityDescriptor(nil, nil, entries, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("build private DACL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, fmt.Errorf("extract private DACL: %w", err)
	}
	return dacl, nil
}

// setPrivateOwnerAndDACL 一次性把目标的 owner 规范化为当前用户，并用
// 显式 owner-only DACL 覆盖目标的 DACL（PROTECTED 阻断继承混入）：提升
// 令牌下新建对象的默认 owner 是 Administrators 组，只设 DACL 会让验证轮
// 因 owner 判据 fail closed。仅以当前用户 SID（进程身份可合法持有的
// owner 取值）覆写受管路径自身，不启用额外系统特权；无权设置时如实
// 失败。按名字操作会跟随符号链接，符号链接拒绝由共享入口的前后双查承担。
func setPrivateOwnerAndDACL(path string, isDir bool) error {
	dacl, err := buildPrivateDACL(isDir)
	if err != nil {
		return err
	}
	_, user, err := privateAllowedSIDs()
	if err != nil {
		return err
	}
	var info windows.SECURITY_INFORMATION = windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info, user, nil, dacl, nil); err != nil {
		return fmt.Errorf("apply private owner and DACL: %w", err)
	}
	return nil
}

// verifyPrivateACL 读回目标的 owner 与 DACL，交 validatePrivateDescriptor
// 按统一判据验证（见该函数）。错误不携带 SID/DACL 内容。
func verifyPrivateACL(path string) error {
	allowed, currentUser, err := privateAllowedSIDs()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read back security descriptor: %w", err)
	}
	return validatePrivateDescriptor(sd, allowed, currentUser)
}

// validatePrivateDescriptor 验证（内存或读回的）安全描述符：
//   - owner 必须是当前用户；
//   - DACL 必须显式在场：缺失、NULL（「空 DACL」＝全放行）与 defaulted
//     （SE_DACL_DEFAULTED，默认机制产物而非我们设置的显式 DACL）均判为
//     「隔离未验证」；
//   - 每个 ACE 必须是 ACCESS_ALLOWED 类型且 SID ∈ 允许集合。
//
// x/sys 契约（vendor security_windows.go）：SECURITY_DESCRIPTOR.DACL 的
// 第二返回值是 defaulted（DACL 是否来自默认机制），不是 present；DACL 不
// 在场时以 err=ERROR_OBJECT_NOT_FOUND 报告；在场但为 NULL DACL 时
// dacl==nil 且 err==nil。修正前的实现把 defaulted 误当 present，导致
// 显式合法 DACL（defaulted=false）被拒、默认 DACL 反而放行。
// 任何 deny-ACE、允许集合之外的 allow-ACE、越界/无法解析的 ACE 都判为
// 「隔离未验证」。错误不携带 SID/DACL 内容。
func validatePrivateDescriptor(sd *windows.SECURITY_DESCRIPTOR, allowed []*windows.SID, currentUser *windows.SID) error {
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("read back owner: %w", err)
	}
	if !windows.EqualSid(owner, currentUser) {
		return fmt.Errorf("private path owner is not the current user (isolation unverified)")
	}
	dacl, defaulted, err := sd.DACL()
	if err != nil {
		if errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
			return fmt.Errorf("private path has no DACL (isolation unverified): %w", err)
		}
		return fmt.Errorf("read back DACL: %w", err)
	}
	if dacl == nil {
		return fmt.Errorf("private path has a NULL DACL granting everyone access (isolation unverified)")
	}
	if defaulted {
		return fmt.Errorf("private path DACL is defaulted, not explicit (isolation unverified)")
	}
	isAllowed := func(sid *windows.SID) bool {
		for _, a := range allowed {
			if windows.EqualSid(a, sid) {
				return true
			}
		}
		return false
	}
	// 逐 ACE 手工遍历（x/sys 的 ACL 不暴露迭代器/尺寸字段）：ACL 头 8 字节
	//（revision/sbz1/aclSize/aceCount/sbz2），其后连续 ACE 结构。aclSize 未
	// 导出，以同构镜像头读出。
	header := (*windowsACLHeader)(unsafe.Pointer(dacl))
	aclSize := uintptr(header.AclSize)
	aclBase := unsafe.Pointer(dacl)
	offset := uintptr(8)
	for i := uint16(0); i < dacl.AceCount; i++ {
		if offset+uintptr(unsafe.Sizeof(windows.ACCESS_ALLOWED_ACE{})) > aclSize {
			return fmt.Errorf("private DACL ACE #%d out of bounds (isolation unverified)", i)
		}
		ace := (*windows.ACCESS_ALLOWED_ACE)(unsafe.Pointer(uintptr(aclBase) + offset))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("private DACL ACE #%d is not an allow-ACE (isolation unverified)", i)
		}
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !isAllowed(aceSID) {
			return fmt.Errorf("private DACL ACE #%d grants an unexpected principal (isolation unverified)", i)
		}
		offset += uintptr(ace.Header.AceSize)
	}
	return nil
}

// windowsACLHeader 是 x/sys windows.ACL 头的同构镜像（读出未导出的 aclSize
// 供 ACE 遍历起界；布局由 winnt.h ACL 定义钉住）。
type windowsACLHeader struct {
	Revision byte
	Sbz1     byte
	AclSize  uint16
	AceCount uint16
	Sbz2     uint16
}

func ensurePrivateDirOS(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create private dir: %w", err)
	}
	if err := setPrivateOwnerAndDACL(path, true); err != nil {
		return err
	}
	return verifyPrivateACL(path)
}

func ensurePrivateFileOS(path string) error {
	if _, err := os.Lstat(path); err != nil && os.IsNotExist(err) {
		// 父目录缺失时按私有目录语义建立（owner-only DACL），私有文件不落
		// 在未验证的父级里；父级已存在则顺带收紧验证。
		if perr := ensurePrivateDirOS(filepath.Dir(path)); perr != nil {
			return perr
		}
		f, cerr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if cerr != nil {
			return fmt.Errorf("create private file: %w", cerr)
		}
		if cerr := f.Close(); cerr != nil {
			return fmt.Errorf("close new private file: %w", cerr)
		}
	}
	if err := setPrivateOwnerAndDACL(path, false); err != nil {
		return err
	}
	return verifyPrivateACL(path)
}
