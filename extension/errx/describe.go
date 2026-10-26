package errx

import (
	"fmt"
	"log/slog"
	"slices"
)

// Node 是一个错误节点的诊断快照，Parent 指向 Description.Nodes 中的父节点。
// 根节点 Parent 为 -1；共享错误在不同分支分别记录。
type Node struct {
	Parent  int            `json:"parent"`
	Type    string         `json:"type"`
	Message string         `json:"message"`
	Code    string         `json:"code,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
	Stack   []Frame        `json:"stack,omitempty"`
}

// Description 是供内部诊断使用的错误树快照，会显式读取各层 Error 文本。
// Fields 和 Stack 不与包装器共享容器；字段值中的引用类型仍为浅复制。
type Description struct {
	Message string         `json:"message"`
	Code    string         `json:"code,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
	Stack   []Frame        `json:"stack,omitempty"`
	Nodes   []Node         `json:"nodes,omitempty"`
}

var _ slog.LogValuer = Description{}

// Describe 生成完整诊断快照，nil 返回零值；不打印日志，不改变原错误。
// Code、Fields、Stack 分别遵循 CodeOf、Fields、Stack 的提取规则。
func Describe(err error) (description Description) {
	if err == nil {
		return
	}
	for node := range walkNodes(err) {
		detail := Node{Parent: node.parent, Type: fmt.Sprintf("%T", node.err), Message: node.err.Error(), Code: codeOf(node.err), Fields: fieldsOf(node.err), Stack: stackOf(node.err)}
		if node.parent == rootParent {
			description.Message = detail.Message
		}
		if description.Code == "" {
			description.Code = detail.Code
		}
		description.Fields = mergeFields(description.Fields, detail.Fields)
		if len(description.Stack) == 0 {
			description.Stack = slices.Clone(detail.Stack)
		}
		description.Nodes = append(description.Nodes, detail)
	}
	return
}

// LogValue 使诊断快照可直接传入 slog，不触发额外错误遍历。
func (d Description) LogValue() (value slog.Value) {
	attrs := []slog.Attr{slog.String("message", d.Message)}
	if d.Code != "" {
		attrs = append(attrs, slog.String("code", d.Code))
	}
	if len(d.Fields) > 0 {
		attrs = append(attrs, slog.Any("fields", d.Fields))
	}
	if len(d.Stack) > 0 {
		attrs = append(attrs, slog.Any("stack", d.Stack))
	}
	if len(d.Nodes) > 0 {
		attrs = append(attrs, slog.Any("nodes", d.Nodes))
	}
	return slog.GroupValue(attrs...)
}
