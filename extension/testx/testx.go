// testx 包提供了一些单测常用的工具函数
package testx

import (
	"os"
	"path/filepath"
)

// InitRunProjectRoot 单测运行目录设置为项目根目录
//
//	pwd: 单测文件的运行路径
func InitRunProjectRoot(pwd string) {
	// 获取当前测试文件的路径
	dir := pwd
	// 向上查找 go.mod 文件，直到项目根目录
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			panic("project root not found")
		}
		dir = parentDir
	}
	// 切换到项目根目录
	os.Chdir(dir)
}
