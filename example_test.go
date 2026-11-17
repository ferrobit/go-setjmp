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
	if set, err := search.buf.Set(); err != nil {
		fmt.Println(err)
	} else if set {
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

func ExampleJMPBuf_Free() {
	var buf setjmp.JMPBuf
	buf.Free()
	if set, err := buf.Set(); err != nil {
		fmt.Println(err)
		return
	} else if set {
		fmt.Println("buffer is set")
	}
	buf.Free()
	if err := buf.Jump(); err != nil {
		fmt.Println(err)
	}
	// Output:
	// buffer is set
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
	if set, err := loop.buf.Set(); err != nil {
		fmt.Println(err)
		return
	} else if set {
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
	if set, err := loop.buf.Set(); err != nil {
		fmt.Println(err)
		return
	} else if set {
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

func ExampleJMPBuf_Set_failure() {
	var buf setjmp.JMPBuf
	defer buf.Free()
	setWithPendingDefers(&buf)
	// Output:
	// set: false
	// error: setjmp.Set failed: captured frames have pending heap defer records
	// deferred call 1
	// deferred call 0
}

func setWithPendingDefers(buf *setjmp.JMPBuf) {
	for index := range 2 {
		defer fmt.Println("deferred call", index)
	}
	set, err := buf.Set()
	fmt.Println("set:", set)
	fmt.Println("error:", err)
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
	if set, err := generator.caller.Set(); err != nil {
		return 0, err
	} else if !set {
		return generator.value, nil
	} else if !generator.started {
		generator.started = true
		generator.produce()
	} else if err := generator.producer.Jump(); err != nil {
		return 0, err
	}
	return generator.value, generator.err
}

func (generator *numberGenerator) produce() {
	first, second := 0, 1
	for {
		generator.value = first
		if set, err := generator.producer.Set(); err != nil {
			generator.err = err
			return
		} else if set {
			generator.err = generator.caller.Jump()
			return
		}
		first, second = second, first+second
	}
}
