package errx

type codeSource interface{ ErrorCode() string }

// CodeOf 返回错误树中首个非空业务码；未找到时返回空字符串。
// 支持 CodeError 和实现 ErrorCode() string 的外部错误，查找顺序与 Walk 一致。
func CodeOf(err error) (code string) {
	for current := range Walk(err) {
		code = codeOf(current)
		if code != "" {
			return
		}
	}
	return
}
