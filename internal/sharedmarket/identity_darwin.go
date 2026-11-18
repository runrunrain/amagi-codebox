package sharedmarket

import (
	"fmt"
	"os"
	"syscall"
)

func fileIdentity(f *os.File) (string, error) {
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Ino == 0 || st.Birthtimespec.Sec == 0 {
		return "", fmt.Errorf("缺少稳定文件身份：%s", f.Name())
	}
	return fmt.Sprintf("darwin:%x:%x:%x:%x", st.Dev, st.Ino, st.Birthtimespec.Sec, st.Birthtimespec.Nsec), nil
}
