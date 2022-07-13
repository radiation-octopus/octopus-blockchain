package entity

import (
	"os"
	"path/filepath"
)

// FileExist检查文件路径中是否存在文件。
func FileExist(filePath string) bool {
	_, err := os.Stat(filePath)
	if err != nil && os.IsNotExist(err) {
		return false
	}

	return true
}

// AbsolutePath返回datadir+filename，如果是绝对值，则返回filename。
func AbsolutePath(datadir string, filename string) string {
	if filepath.IsAbs(filename) {
		return filename
	}
	return filepath.Join(datadir, filename)
}
