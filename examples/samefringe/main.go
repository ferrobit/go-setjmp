package main

import (
	"fmt"

	"github.com/ferrobit/go-setjmp"
)

type treeNode struct {
	left  *treeNode
	right *treeNode
	value int
}

type treePair struct {
	name  string
	left  *treeNode
	right *treeNode
}

type fringeWalker struct {
	caller   setjmp.JMPBuf
	producer setjmp.JMPBuf
	root     *treeNode
	started  bool
	done     bool
	value    int
	err      error
}

func main() {
	balanced := makeNode(makeNode(makeLeaf(1), makeLeaf(2)), makeNode(makeLeaf(3), makeLeaf(4)))
	leftDeep := makeNode(makeNode(makeNode(makeLeaf(1), makeLeaf(2)), makeLeaf(3)), makeLeaf(4))
	swapped := makeNode(makeLeaf(1), makeNode(makeLeaf(2), makeNode(makeLeaf(4), makeLeaf(3))))
	shorter := makeNode(makeLeaf(1), makeNode(makeLeaf(2), makeLeaf(3)))
	pairs := []treePair{
		{"balanced vs left-deep", balanced, leftDeep},
		{"balanced vs swapped", balanced, swapped},
		{"balanced vs shorter", balanced, shorter},
		{"1000 leaves, balanced vs right-deep", makeBalanced(1, 1000), makeRightDeep(1, 1000)},
	}
	for _, pair := range pairs {
		if same, err := sameFringe(pair.left, pair.right); err != nil {
			fmt.Printf("%-36v error: %v\n", pair.name, err)
		} else if same {
			fmt.Printf("%-36v same fringe\n", pair.name)
		} else {
			fmt.Printf("%-36v different fringe\n", pair.name)
		}
	}
}

func makeBalanced(first, last int) *treeNode {
	if first == last {
		return makeLeaf(first)
	}
	middle := (first + last) / 2
	return makeNode(makeBalanced(first, middle), makeBalanced(middle+1, last))
}

func makeLeaf(value int) *treeNode {
	return &treeNode{value: value}
}

func makeNode(left, right *treeNode) *treeNode {
	return &treeNode{left: left, right: right}
}

func makeRightDeep(first, last int) *treeNode {
	if first == last {
		return makeLeaf(first)
	}
	return makeNode(makeLeaf(first), makeRightDeep(first+1, last))
}

func sameFringe(left, right *treeNode) (bool, error) {
	first, second := &fringeWalker{root: left}, &fringeWalker{root: right}
	defer first.free()
	defer second.free()
	for {
		if firstValue, firstOk, err := first.next(); err != nil {
			return false, fmt.Errorf("failed to walk first tree: %w", err)
		} else if secondValue, secondOk, err := second.next(); err != nil {
			return false, fmt.Errorf("failed to walk second tree: %w", err)
		} else if firstOk != secondOk || firstValue != secondValue {
			return false, nil
		} else if !firstOk {
			return true, nil
		}
	}
}

func (walker *fringeWalker) finish(err error) {
	walker.done, walker.err = true, err
	if jumpErr := walker.caller.Jump(); jumpErr != nil {
		walker.err = fmt.Errorf("failed to return from walker: %w", jumpErr)
	}
}

func (walker *fringeWalker) free() {
	walker.caller.Free()
	walker.producer.Free()
}

func (walker *fringeWalker) next() (int, bool, error) {
	if walker.done {
		return 0, false, walker.err
	} else if set, err := walker.caller.Set(); err != nil {
		return 0, false, err
	} else if !set {
		return walker.value, !walker.done, walker.err
	} else if !walker.started {
		walker.started = true
		walker.produce()
	} else if err := walker.producer.Jump(); err != nil {
		return 0, false, err
	}
	return 0, false, walker.err
}

func (walker *fringeWalker) produce() {
	walker.walk(walker.root)
	walker.finish(nil)
}

func (walker *fringeWalker) walk(node *treeNode) {
	if node == nil || walker.done {
		return
	} else if node.left == nil && node.right == nil {
		walker.yield(node.value)
		return
	}
	walker.walk(node.left)
	walker.walk(node.right)
}

func (walker *fringeWalker) yield(value int) {
	walker.value = value
	if set, err := walker.producer.Set(); err != nil {
		walker.finish(err)
	} else if set {
		if err := walker.caller.Jump(); err != nil {
			walker.done, walker.err = true, fmt.Errorf("failed to yield %v: %w", value, err)
		}
	}
}
