package filepathx

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidPath 表示路径为空、包含无效字符或不符合接口要求。
	ErrInvalidPath = errors.New("filepathx: invalid path")
	// ErrOutsideBase 表示路径不满足基准目录的词法范围限制。
	ErrOutsideBase = errors.New("filepathx: path outside base")
	// ErrInvalidExtension 表示扩展名包含分隔符、空字符或只有点。
	ErrInvalidExtension = errors.New("filepathx: invalid extension")
)

// pathError 为错误添加操作和路径信息，保留错误链。
func pathError(op, name string, cause error) (err error) {
	return fmt.Errorf("filepathx %s %q: %w", op, name, cause)
}

// outsideBaseError 同时保留目录越界类别与底层路径计算错误。
func outsideBaseError(name string, cause error) (err error) {
	return pathError("rel within", name, errors.Join(ErrOutsideBase, cause))
}
