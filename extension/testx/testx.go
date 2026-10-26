// testx 包提供了一些单测常用的工具函数
package testx

import (
	"os"

	"github.com/itrookie-sky/whtlib/extension/filepathx"
)

// ChdirToProjectRoot 单测运行目录设置为项目根目录
//
//	pwd: 单测文件的运行路径
func ChdirToProjectRoot(pwd string) {
	dir, err := filepathx.FindUp(pwd, "go.mod")
	if err != nil {
		panic(err)
	}
	// 切换到项目根目录
	err = os.Chdir(dir)
	if err != nil {
		panic(err)
	}
}
