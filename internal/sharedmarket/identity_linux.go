package sharedmarket

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func fileIdentity(f *os.File) (string, error) {
	var st unix.Statx_t
	if err := unix.Statx(int(f.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_INO|unix.STATX_BTIME, &st); err != nil {
		return "", err
	}
	if st.Mask&(unix.STATX_INO|unix.STATX_BTIME) != unix.STATX_INO|unix.STATX_BTIME || st.Ino == 0 {
		return "", fmt.Errorf("缺少稳定文件身份：%s", f.Name())
	}
	return fmt.Sprintf("linux:%x:%x:%x:%x:%x", st.Dev_major, st.Dev_minor, st.Ino, st.Btime.Sec, st.Btime.Nsec), nil
}
