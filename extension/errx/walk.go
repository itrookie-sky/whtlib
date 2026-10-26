package errx

import "iter"

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
