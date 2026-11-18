package sharedmarket

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func fileIdentity(f *os.File) (string, error) {
	h := windows.Handle(f.Fd())
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return "", err
	}
	var id struct {
		Volume uint64
		File   [16]byte
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", fmt.Errorf("拒绝 reparse point：%s", f.Name())
	}
	if err := windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&id)), uint32(unsafe.Sizeof(id))); err != nil {
		return "", err
	}
	if id.File == [16]byte{} || info.CreationTime == (windows.Filetime{}) {
		return "", fmt.Errorf("缺少稳定文件身份：%s", f.Name())
	}
	return fmt.Sprintf("windows:%x:%x:%x:%x", id.Volume, id.File, info.CreationTime.HighDateTime, info.CreationTime.LowDateTime), nil
}
