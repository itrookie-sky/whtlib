package reflectx

import (
	"errors"
	"fmt"
)

var (
	// ErrNotSettable 表示字段不可赋值。
	ErrNotSettable = errors.New("reflectx: field is not settable")
	// ErrNotConvertible 表示输入不能转换为字段类型。
	ErrNotConvertible = errors.New("reflectx: value is not convertible")
)

func conversionError(format string, args ...any) (err error) {
	return fmt.Errorf("%w: %s", ErrNotConvertible, fmt.Sprintf(format, args...))
}
