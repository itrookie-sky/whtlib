package errx

import (
	"fmt"
	"iter"
	"log/slog"
	"runtime"
	"slices"
)

type treeNode struct {
	err      error
	index    int
	parent   int
	children []error
}

// Walk 深度优先先序遍历，包括根节点，支持两种标准 Unwrap 方法。
// nil 无结果，共享节点在每条路径分别出现；与标准 errors 一样要求无环错误树。
func Walk(err error) (sequence iter.Seq[error]) {
	return func(yield func(error) bool) {
		for node := range walkNodes(err) {
			if !yield(node.err) {
				return
			}
		}
	}
}

// Leaves 按 Walk 顺序遍历全部叶子原因，nil 子节点忽略，nil 输入无结果。
func Leaves(err error) (sequence iter.Seq[error]) {
	return func(yield func(error) bool) {
		for node := range walkNodes(err) {
			if len(node.children) == 0 && !yield(node.err) {
				return
			}
		}
	}
}

func walkNodes(err error) (sequence iter.Seq[treeNode]) {
	return func(yield func(treeNode) bool) {
		if err == nil {
			return
		}
		pending := []treeNode{{err: err, parent: rootParent}}
		index := 0
		for len(pending) > 0 {
			last := len(pending) - 1
			node := pending[last]
			pending[last] = treeNode{}
			pending = pending[:last]
			node.index = index
			index++
			node.children = childrenOf(node.err)
			if !yield(node) {
				return
			}
			for i := len(node.children) - 1; i >= 0; i-- {
				pending = append(pending, treeNode{err: node.children[i], parent: node.index})
			}
		}
	}
}

func childrenOf(err error) (children []error) {
	switch source := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range source.Unwrap() {
			if child != nil {
				children = append(children, child)
			}
		}
	case interface{ Unwrap() error }:
		if child := source.Unwrap(); child != nil {
			children = []error{child}
		}
	}
	return
}

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

// Frame 表示符号化调用帧，可用于日志和 JSON。
type Frame struct {
	Function string `json:"function"` // 完整函数名。
	File     string `json:"file"`     // 源文件路径。
	Line     int    `json:"line"`     // 源文件行号。
}

type stackSource interface{ StackTrace() []Frame }

// Stack 返回错误树中首个非空堆栈的副本，查找顺序与 Walk 一致。
// 支持实现 StackTrace() []Frame 的外部错误，未找到时返回 nil。
func Stack(err error) (frames []Frame) {
	for current := range Walk(err) {
		frames = stackOf(current)
		if len(frames) > 0 {
			return
		}
	}
	return nil
}

func captureStack() (pcs []uintptr) {
	pcs = make([]uintptr, maxStackDepth)
	n := runtime.Callers(stackCallerSkip, pcs)
	return pcs[:n:n]
}

func stackFrames(pcs []uintptr) (frames []Frame) {
	if len(pcs) == 0 {
		return
	}
	iterator := runtime.CallersFrames(pcs)
	for {
		frame, more := iterator.Next()
		frames = append(frames, Frame{Function: frame.Function, File: frame.File, Line: frame.Line})
		if !more {
			return
		}
	}
}

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
