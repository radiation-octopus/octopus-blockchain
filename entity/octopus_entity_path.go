package entity

import "os"

// FileExist检查文件路径中是否存在文件。
func FileExist(filePath string) bool {
	_, err := os.Stat(filePath)
	if err != nil && os.IsNotExist(err) {
		return false
	}

	return true
}
