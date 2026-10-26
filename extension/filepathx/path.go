// Package filepathx 扩展本地文件系统路径操作，沿用 path/filepath 的平台语义。
package filepathx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FindUp 从已有目录 start 向上查找包含 marker 的最近目录，返回绝对路径。
// marker 必须是单个文件名或目录名，不允许 .、..、分隔符或卷名。
// 不改变工作目录，不解析祖先目录的符号链接；未找到时返回 fs.ErrNotExist。
func FindUp(start, marker string) (dir string, err error) {
	if marker == "" || marker == "." || marker == ".." || strings.ContainsAny(marker, "/\\\x00") || filepath.VolumeName(marker) != "" {
		err = pathError("find marker", marker, ErrInvalidPath)
		return
	}
	candidate, err := absolutePath(start)
	if err != nil {
		return
	}
	info, err := os.Stat(candidate)
	if err != nil {
		err = pathError("find start", start, err)
		return
	}
	if !info.IsDir() {
		err = pathError("find start directory", start, ErrInvalidPath)
		return
	}
	for {
		_, statErr := os.Stat(filepath.Join(candidate, marker))
		if statErr == nil {
			return candidate, nil
		}
		if !errors.Is(statErr, fs.ErrNotExist) {
			err = pathError("find marker", marker, statErr)
			return
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			err = pathError("find marker from "+start, marker, fs.ErrNotExist)
			return
		}
		candidate = parent
	}
}

// JoinWithin 将非空相对路径拼接到 base 内，返回绝对路径；. 表示 base 本身。
// 仅做词法检查，不要求路径存在，不防止符号链接逃逸；文件访问边界请使用 os.Root。
func JoinWithin(base, relative string) (joined string, err error) {
	root, err := absolutePath(base)
	if err != nil {
		return
	}
	if relative == "" || strings.ContainsRune(relative, '\x00') {
		err = pathError("join within", relative, ErrInvalidPath)
		return
	}
	if !filepath.IsLocal(relative) {
		err = pathError("join within", relative, ErrOutsideBase)
		return
	}
	return filepath.Join(root, relative), nil
}

// RelWithin 计算 base 到 target 的相对路径，目标必须在 base 内，相同路径返回 .。
// 两个参数均非空，相对参数均相对于当前工作目录解释，不要求路径存在。
// 仅做词法检查，不解析符号链接；文件访问边界请使用 os.Root。
func RelWithin(base, target string) (relative string, err error) {
	root, err := absolutePath(base)
	if err != nil {
		return
	}
	absTarget, err := absolutePath(target)
	if err != nil {
		return
	}
	result, err := filepath.Rel(root, absTarget)
	if err != nil {
		err = outsideBaseError(target, err)
		return
	}
	if !filepath.IsLocal(result) {
		err = pathError("rel within", target, ErrOutsideBase)
		return
	}
	return result, nil
}

// ExpandHome 展开 ~ 和后接平台路径分隔符的 ~/，其他路径保持原样。
// 不展开环境变量，不支持 ~user；展开后的路径会被清理，但不限制在用户目录内。
func ExpandHome(name string) (expanded string, err error) {
	if strings.ContainsRune(name, '\x00') {
		err = pathError("expand home", name, ErrInvalidPath)
		return
	}
	if !strings.HasPrefix(name, "~") {
		return name, nil
	}
	if len(name) > 1 && !os.IsPathSeparator(name[1]) {
		err = pathError("expand home", name, ErrInvalidPath)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		err = pathError("expand home", name, err)
		return
	}
	if name == "~" {
		return home, nil
	}
	return filepath.Join(home, name[2:]), nil
}

// Stem 返回文件名去掉最后一个扩展名后的部分，不读取文件系统。
// .env 保持不变，.env.local 返回 .env；空路径、目录结尾、. 和 .. 返回空字符串。
func Stem(name string) (stem string) {
	_, base := filepath.Split(name)
	if base == "." || base == ".." {
		return
	}
	ext := filepath.Ext(base)
	if ext == base {
		return base
	}
	return strings.TrimSuffix(base, ext)
}

// ReplaceExt 替换最后一个扩展名并保留目录原样；空扩展名表示删除。
// ext 可带前导点或不带点，也可为 tar.gz；不允许分隔符、空字符或只有点。
// 路径必须有文件名；隐藏文件遵循 Stem 的规则，不读取文件系统。
func ReplaceExt(name, ext string) (result string, err error) {
	dir, base := filepath.Split(name)
	if base == "" || base == "." || base == ".." || strings.ContainsRune(name, '\x00') {
		err = pathError("replace extension", name, ErrInvalidPath)
		return
	}
	if strings.ContainsAny(ext, "/\\\x00") || (ext != "" && strings.Trim(ext, ".") == "") {
		err = pathError("replace extension", ext, ErrInvalidExtension)
		return
	}
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return dir + Stem(base) + ext, nil
}

// absolutePath 校验非空路径并转为绝对路径，不要求路径存在。
func absolutePath(name string) (absolute string, err error) {
	if name == "" || strings.ContainsRune(name, '\x00') {
		err = pathError("absolute path", name, ErrInvalidPath)
		return
	}
	absolute, err = filepath.Abs(name)
	if err != nil {
		err = pathError("absolute path", name, err)
	}
	return
}
