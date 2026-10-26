package errx

type fieldsSource interface{ Fields() map[string]any }

// Fields 汇总错误树所有字段，外层及先访问的分支优先；未找到时返回 nil。
// 支持实现 Fields() map[string]any 的外部错误，返回独立 map，字段值为浅复制。
func Fields(err error) (fields map[string]any) {
	for current := range Walk(err) {
		fields = mergeFields(fields, fieldsOf(current))
	}
	return
}

func mergeFields(target, source map[string]any) (merged map[string]any) {
	if len(source) == 0 {
		return target
	}
	if target == nil {
		target = make(map[string]any, len(source))
	}
	for key, value := range source {
		if _, exists := target[key]; !exists {
			target[key] = value
		}
	}
	return target
}
