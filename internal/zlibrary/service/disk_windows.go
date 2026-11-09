// s-library: 磁盘剩余空间探测（windows 占位实现，M1 主目标为 linux/docker）。
//go:build windows

package zlibservice

import "errors"

// diskFreeBytes windows 下暂不支持（返回错误后 checkDiskSpace 选择放行，由落盘失败兜底）。
func diskFreeBytes(path string) (int64, error) {
	return 0, errors.New("disk free check not supported on windows")
}
