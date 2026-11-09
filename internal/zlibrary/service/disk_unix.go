// s-library: 磁盘剩余空间探测（unix 实现，pipeline.checkDiskSpace 使用）。
//go:build !windows

package zlibservice

import "syscall"

// diskFreeBytes 返回 path 所在卷的可用空间（普通用户可写部分）。
func diskFreeBytes(path string) (int64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, err
	}
	return int64(fs.Bavail) * int64(fs.Bsize), nil
}
