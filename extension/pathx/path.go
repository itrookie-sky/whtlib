// Package pathx 扩展以斜杠分隔的逻辑路径，不解析 URL 或本地文件系统路径。
package pathx

import (
	"path"
	"strings"
)

// HasPathPrefix 清理路径后按完整分段判断前缀；空参数不匹配。
// / 表示绝对根，. 表示相对根；不解码百分号，反斜杠视为普通字符。
func HasPathPrefix(name, prefix string) (matched bool) {
	_, matched = TrimPathPrefix(name, prefix)
	return
}

// TrimPathPrefix 清理路径后移除完整分段的前缀及其分隔符。
// 完全匹配返回空字符串和 true；不匹配（含空参数）返回原始 name 和 false。
// / 表示绝对根，. 表示相对根且不匹配以 .. 越界的路径；不解析 URL 或百分号。
func TrimPathPrefix(name, prefix string) (rest string, matched bool) {
	if name == "" || prefix == "" {
		return name, false
	}
	cleanName := path.Clean(name)
	cleanPrefix := path.Clean(prefix)
	if cleanName == cleanPrefix {
		return "", true
	}
	if cleanPrefix == "." {
		if !path.IsAbs(cleanName) && cleanName != ".." && !strings.HasPrefix(cleanName, "../") {
			return cleanName, true
		}
		return name, false
	}
	if cleanPrefix == "/" {
		rest, matched = strings.CutPrefix(cleanName, "/")
	} else {
		rest, matched = strings.CutPrefix(cleanName, cleanPrefix+"/")
	}
	if !matched {
		return name, false
	}
	return
}
