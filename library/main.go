package main

/*
#include <stdint.h>
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"

	"github.com/ferrobit/go-setjmp"
)

type chainNode struct {
	value uint64
	next  *chainNode
}

type valueSequence struct {
	caller   setjmp.JMPBuf
	producer setjmp.JMPBuf
	started  bool
	state    uint64
	value    uint64
	err      error
}

const chainLength = 64
const foldPrime = 1099511628211

var one, two setjmp.JMPBuf
var shared = makeValueSequence(1)

//export Exported
func Exported() C.int {
	one.Debug, two.Debug = true, true
	if one.Set() {
		fmt.Println("one: set")
		Internal()
	} else {
		fmt.Println("one: jumped")
		if err := two.Jump(); err != nil {
			fmt.Fprintf(os.Stderr, "Exported failed: %v\n", err)
			return 1
		}
	}
	return 0
}

//export Next
func Next() C.uint64_t {
	result := shared.next()
	if shared.err != nil {
		fmt.Fprintf(os.Stderr, "Next failed: %v\n", shared.err)
		return 0
	}
	return C.uint64_t(result)
}

//export Checksum
func Checksum(seed C.uint64_t, rounds C.int) C.uint64_t {
	sequence := makeValueSequence(uint64(seed))
	defer sequence.free()
	result := uint64(0)
	for range int(rounds) {
		result = result*foldPrime ^ sequence.next()
	}
	if sequence.err != nil {
		fmt.Fprintf(os.Stderr, "Checksum failed: %v\n", sequence.err)
		return 0
	}
	return C.uint64_t(result)
}

//export Reference
func Reference(seed C.uint64_t, rounds C.int) C.uint64_t {
	result, state := uint64(0), uint64(seed)
	for range int(rounds) {
		state = advance(state)
		result = result*foldPrime ^ state
		state ^= sumChain(makeChain(state, chainLength))
	}
	return C.uint64_t(result)
}

//export Collect
func Collect() {
	runtime.GC()
}

func Internal() {
	s := "some_string"
	p := &s
	chain := makeChain(1, chainLength)
	fmt.Println("internal: enter")
	if two.Set() {
		if err := one.Jump(); err != nil {
			fmt.Fprintf(os.Stderr, "Internal failed: %v\n", err)
		}
	} else {
		fmt.Println("two: jumped")
	}
	fmt.Println("internal: "+*p, sumChain(chain))
}

func advance(state uint64) uint64 {
	state ^= state << 13
	state ^= state >> 7
	state ^= state << 17
	return state
}

func makeChain(seed uint64, length int) *chainNode {
	var result *chainNode
	for index := range length {
		result = &chainNode{value: seed + uint64(index), next: result}
	}
	return result
}

func sumChain(node *chainNode) uint64 {
	result := uint64(0)
	for ; node != nil; node = node.next {
		result += node.value
	}
	return result
}

func main() {
}

func makeValueSequence(seed uint64) *valueSequence {
	return &valueSequence{state: seed}
}

func (sequence *valueSequence) free() {
	sequence.caller.Free()
	sequence.producer.Free()
}

func (sequence *valueSequence) next() uint64 {
	if sequence.caller.Set() {
		if err := sequence.caller.Err(); err != nil {
			sequence.err = err
		} else if !sequence.started {
			sequence.started = true
			sequence.produce()
		} else if err := sequence.producer.Jump(); err != nil {
			sequence.err = err
		}
	}
	return sequence.value
}

func (sequence *valueSequence) produce() {
	state := sequence.state
	for {
		state = advance(state)
		chain := makeChain(state, chainLength)
		sequence.value = state
		if sequence.producer.Set() {
			if err := sequence.caller.Jump(); err != nil {
				sequence.err = err
				return
			}
		}
		state ^= sumChain(chain)
	}
}
