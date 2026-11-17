# setjmp — setjmp/longjmp for Go (amd64, arm64)

[![Go Reference](https://pkg.go.dev/badge/github.com/ferrobit/go-setjmp.svg)](https://pkg.go.dev/github.com/ferrobit/go-setjmp)

## Installation
```sh
go get github.com/ferrobit/go-setjmp
```
The import path is `github.com/ferrobit/go-setjmp`, the package name is `setjmp`.

## Example
```go
package main

import (
	"fmt"

	"github.com/ferrobit/go-setjmp"
)

type retryLoop struct {
	buf    setjmp.JMPBuf
	passes int
}

func main() {
	loop := &retryLoop{}
	defer loop.buf.Free()
	if loop.buf.Set() {
		if err := loop.buf.Err(); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("set")
	} else {
		fmt.Println("jumped")
	}
	if loop.passes++; loop.passes < 3 {
		if err := loop.buf.Jump(); err != nil {
			fmt.Println(err)
		}
	}
	fmt.Println("passes:", loop.passes)
}
```
Output:
```
set
jumped
jumped
passes: 3
```
`loop` lives on the heap, so `passes` survives the jumps. More examples — a non-local exit from
recursion, a generator that resumes finished frames, error handling — are in
[example_test.go](example_test.go) and on [pkg.go.dev](https://pkg.go.dev/github.com/ferrobit/go-setjmp#pkg-examples).

## API
| Method | Description |
|---|---|
| `Set() bool` | Saves the stack. Returns `true` when called directly (also on failure, see `Err`) and `false` when resumed by `Jump`. |
| `Jump() error` | Resumes the last `Set` of the buffer. Returns only on failure. |
| `Err() error` | Reports why the last `Set` failed, or `nil`. |
| `Free()` | Releases the saved stack. Safe on an unset buffer. |
| `Debug bool` | Runs a full garbage collection inside `Set` and `Jump`. |

## Contents
- `jmpbuf.go` — the `setjmp` package: the `JMPBuf` type (`Set`, `Jump`, `Free`, `Err`).
- `jmpbuf_amd64.s`, `jmpbuf_amd64.go`, `jmpbuf_arm64.s`, `jmpbuf_arm64.go` — CPU-specific parts.
- `jmpbuf_test.go` — tests (19 of them) and a benchmark.
- `example_test.go` — usage examples (6 of them), checked by `go test`.
- `library/main.go` — a shared library (`-buildmode=c-shared`) that exports
  `Exported`, `Next`, `Checksum`, `Reference`, `Collect`.
- `application/main.c` — a C application that calls the library from several threads.

## Building and testing
The build file is named `build.mk`; it can be renamed to `Makefile`.
- `make -f build.mk test` — `go vet` and tests with coverage.
- `make -f build.mk run` — builds the library and the application and runs them (normally and
  under garbage collector pressure).
- `make -f build.mk stress` — tests with `GOGC=1`, `gccheckmark=1`, `clobberfree=1`.
- `make -f build.mk test-arm64` and `run-arm64` — the same for arm64 via the
  `qemu-aarch64-static` emulator and the `aarch64-linux-gnu-gcc` cross compiler.

## How it works
1. `Set` finds the "base": the first frame called from the runtime (`runtime.*`) or from a cgo
   wrapper (`_cgoexp_*`). Everything from the `Set` call site up to the base is captured.
2. The frames are copied into a pointer-free buffer. The copy is handed to a "holder" goroutine:
   it lays the frames out on its own stack and sleeps beneath them.
3. The garbage collector sees real frames with real pointer maps on the holder. So everything
   the snapshot refers to stays alive, and the runtime itself moves and scans this stack as usual.
4. `Jump` copies the snapshot back — below the base of the current goroutine — and returns from
   `Set` with `false`.
5. `Free` (or another `Set`) releases the holder.

## Performance
- The holder is a coroutine (`iter.Pull`). Control is transferred to it directly, without the
  scheduler and without putting the calling thread to sleep. This matters most for calls from C:
  there the goroutine is locked to its thread, and a plain wait on a channel costs about 10 µs.
- A repeated `Set` on the same buffer reuses its holder. Released holders are kept in a pool,
  separately for each locked thread.
- The fallback is channel-based holders. It is enabled automatically if the package does not
  find the thread-locking counters in the runtime.

## Addresses when the stack moves
If a snapshot is restored to a different place (the stack grew, another goroutine, another
thread), the addresses inside it must be shifted. The runtime itself decides which words are
addresses: the package lays out a scratch copy of the snapshot on a separate goroutine, forces
its stack to move, and compares "before" and "after". Only the words the runtime shifted are
shifted. This is done once per snapshot and only when the location has changed.

## Semantics
- Stack variables revert after `Jump` to their values at the time of `Set`. This also applies to
  `new(T)` and `&T{}` that the compiler kept on the stack. Keep everything that must survive
  the jump on the heap or in global variables. A value whose address reaches `Set` or `Jump`
  (for example, a struct that contains the `JMPBuf`) always lives on the heap.
- Heap data is not rolled back.
- Deferred calls (`defer`) of abandoned frames are not run — as in C. In restored frames they
  run when the function returns.
- You can jump up, down (into frames that have already returned), many times into one buffer,
  from another call of an exported function, and from another thread or goroutine.
- `Jump` returns only on error. `Err` reports why `Set` failed.

## Limitations
- amd64 and arm64, Linux, Go 1.23–1.27. arm64 is tested only in the emulator.
- `defer` inside a loop in captured frames — `Set` refuses (the error is in `Err`).
- You cannot jump across runtime frames: `reflect.Value.Call`, `panic` handling.
- `JMPBuf` is not thread-safe. One buffer has one owner at any moment.
- A buffer's holder lives until the buffer is released. Call `Free`.
- Pointers from captured frames into the base frame (above the snapshot) are not shifted when the
  stack moves.
- The package reads runtime internals (`runtime.g`, `runtime.m`, `runtime._defer`,
  `runtime.writeBarrier`, `runtime.findfunc`, the frame size table). The layout is checked on the
  first `Set`; on a mismatch `Set` returns `true` and `Err` reports the reason.

## License
[MIT](LICENSE).
