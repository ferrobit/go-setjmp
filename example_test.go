//go:build amd64 || arm64

package setjmp_test

import (
	"fmt"

	"github.com/ferrobit/go-setjmp"
)

type depthSearch struct {
	buf   setjmp.JMPBuf
	found int
}

type retryLoop struct {
	buf    setjmp.JMPBuf
	passes int
}

type numberGenerator struct {
	caller   setjmp.JMPBuf
	producer setjmp.JMPBuf
	started  bool
	value    int
	err      error
}

func Example_generator() {
	generator := &numberGenerator{}
	defer generator.free()
	values := make([]int, 0, 10)
	for range 10 {
		if value, err := generator.next(); err != nil {
			fmt.Println(err)
			return
		} else {
			values = append(values, value)
		}
	}
	fmt.Println(values)
	// Output:
	// [0 1 1 2 3 5 8 13 21 34]
}

func ExampleJMPBuf() {
	search := &depthSearch{}
	defer search.buf.Free()
	if search.buf.Set() {
		if err := search.buf.Err(); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("search started")
		search.descend(1)
		fmt.Println("target not found")
	} else {
		fmt.Println("target found at depth", search.found)
	}
	// Output:
	// search started
	// target found at depth 5
}

func ExampleJMPBuf_Err() {
	var buf setjmp.JMPBuf
	defer buf.Free()
	if buf.Set() {
		fmt.Println("first Set:", buf.Err())
	}
	setWithPendingDefers(&buf)
	// Output:
	// first Set: <nil>
	// second Set: setjmp.Set failed: captured frames have pending heap defer records
	// deferred call 1
	// deferred call 0
}

func ExampleJMPBuf_Free() {
	var buf setjmp.JMPBuf
	buf.Free()
	if buf.Set() {
		fmt.Println("set:", buf.Err())
	}
	buf.Free()
	if err := buf.Jump(); err != nil {
		fmt.Println(err)
	}
	// Output:
	// set: <nil>
	// setjmp.Jump failed: jump buffer is not set
}

func ExampleJMPBuf_Jump() {
	var empty setjmp.JMPBuf
	if err := empty.Jump(); err != nil {
		fmt.Println(err)
	}
	loop := &retryLoop{}
	defer loop.buf.Free()
	local := "value at Set"
	if loop.buf.Set() {
		if err := loop.buf.Err(); err != nil {
			fmt.Println(err)
			return
		}
		local = "value changed before Jump"
		loop.passes = 1
		loop.jumpBack()
	}
	fmt.Println("stack variable:", local)
	fmt.Println("heap field:", loop.passes)
	// Output:
	// setjmp.Jump failed: jump buffer is not set
	// stack variable: value at Set
	// heap field: 1
}

func ExampleJMPBuf_Set() {
	loop := &retryLoop{}
	defer loop.buf.Free()
	if loop.buf.Set() {
		if err := loop.buf.Err(); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("Set returned true: buffer is set")
	} else {
		fmt.Println("Set returned false: arrived by Jump")
	}
	if loop.passes++; loop.passes < 3 {
		loop.jumpBack()
	}
	fmt.Println("passes:", loop.passes)
	// Output:
	// Set returned true: buffer is set
	// Set returned false: arrived by Jump
	// Set returned false: arrived by Jump
	// passes: 3
}

func setWithPendingDefers(buf *setjmp.JMPBuf) {
	for index := range 2 {
		defer fmt.Println("deferred call", index)
	}
	if buf.Set() {
		fmt.Println("second Set:", buf.Err())
	}
}

func (search *depthSearch) descend(depth int) {
	if depth == 5 {
		search.found = depth
		if err := search.buf.Jump(); err != nil {
			fmt.Println(err)
		}
		return
	}
	search.descend(depth + 1)
}

func (loop *retryLoop) jumpBack() {
	if err := loop.buf.Jump(); err != nil {
		fmt.Println(err)
	}
}

func (generator *numberGenerator) free() {
	generator.caller.Free()
	generator.producer.Free()
}

func (generator *numberGenerator) next() (int, error) {
	if generator.caller.Set() {
		if err := generator.caller.Err(); err != nil {
			return 0, err
		} else if !generator.started {
			generator.started = true
			generator.produce()
		} else if err := generator.producer.Jump(); err != nil {
			return 0, err
		}
	}
	return generator.value, generator.err
}

func (generator *numberGenerator) produce() {
	first, second := 0, 1
	for {
		generator.value = first
		if generator.producer.Set() {
			if err := generator.producer.Err(); err != nil {
				generator.err = err
				return
			} else if err := generator.caller.Jump(); err != nil {
				generator.err = err
				return
			}
		}
		first, second = second, first+second
	}
}
