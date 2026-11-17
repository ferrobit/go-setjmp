<p align="center">
  <img src="docs/logo.png" alt="go-setjmp logo" width="460">
</p>

# go-setjmp — setjmp/longjmp for Go (amd64, arm64)

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
	if set, err := loop.buf.Set(); err != nil {
		fmt.Println(err)
		return
	} else if set {
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
| `Set() (bool, error)` | Saves the stack. Returns `true` when called directly, `false` when resumed by `Jump`, and `false` with an error on failure. |
| `Jump() error` | Resumes the last `Set` of the buffer. Returns only on failure. |
| `Free()` | Releases the saved stack. Safe on an unset buffer. |
| `Debug bool` | Runs a full garbage collection inside `Set` and `Jump`. |

## Contents
- `jmpbuf.go` — the `setjmp` package: the `JMPBuf` type (`Set`, `Jump`, `Free`).
- `jmpbuf_amd64.s`, `jmpbuf_amd64.go`, `jmpbuf_arm64.s`, `jmpbuf_arm64.go` — CPU-specific parts.
- `jmpbuf_test.go` — tests (19 of them) and a benchmark.
- `example_test.go` — usage examples (6 of them), checked by `go test`.
- `library/main.go` — a shared library (`-buildmode=c-shared`) that exports
  `Exported`, `Next`, `Checksum`, `Reference`, `Collect`.
- `application/main.c` — a C application that calls the library from several threads.
- `Makefile` — build, test and stress targets (see below).
- `docs/logo.svg`, `docs/logo.png` — the logo (SVG source and its PNG rendering).

## Building and testing
- `make test` — `go vet` and tests with coverage.
- `make run` — builds the library and the application and runs them (normally and
  under garbage collector pressure).
- `make stress` — tests with `GOGC=1`, `gccheckmark=1`, `clobberfree=1`.
- `make clean` — removes the `build` directory.
- `make test-arm64` and `make run-arm64` — the same for arm64 via the
  `qemu-aarch64-static` emulator and the `aarch64-linux-gnu-gcc` cross compiler.

## How it works
C `setjmp` saves a few registers and `longjmp` restores them. This is not enough in Go: goroutine
stacks move and grow, and the garbage collector must find every pointer on them. So the package
saves the stack frames themselves and keeps them as real frames on a parked goroutine, where the
runtime treats them like any other stack.

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
- `Jump` returns only on error. `Set` returns an error when it cannot save the stack.

## Limitations
- amd64 and arm64, Linux, Go 1.23–1.27. arm64 is tested only in the emulator.
- `defer` inside a loop in captured frames — `Set` refuses and returns an error.
- You cannot jump across runtime frames: `reflect.Value.Call`, `panic` handling.
- `JMPBuf` is not thread-safe. One buffer has one owner at any moment.
- A buffer's holder lives until the buffer is released. Call `Free`.
- Pointers from captured frames into the base frame (above the snapshot) are not shifted when the
  stack moves.
- The package reads runtime internals (`runtime.g`, `runtime.m`, `runtime._defer`,
  `runtime.writeBarrier`, `runtime.findfunc`, the frame size table). The layout is checked on the
  first `Set`; on a mismatch `Set` returns `false` and an error with the reason.

## Maintenance

### Development
- Requirements: Linux, Go 1.23+, `gcc`. For arm64: `qemu-aarch64-static` and
  `aarch64-linux-gnu-gcc`.
- Before a commit: `make test`, `make stress`, `make run`, and on assembly changes also
  `make test-arm64` and `make run-arm64`. Format with `gofmt` only.
- A new Go release: run `GOTOOLCHAIN=go1.X make test stress run`. If `Set` reports a layout error,
  compare the package with the new runtime sources:

| Package | Runtime source |
|---|---|
| `g_lo`, `g_hi`, `g_defer`, `g_m` in `jmpbuf_*.s` | `runtime.g` in `runtime/runtime2.go` |
| `defer_sp`, `defer_link` in `jmpbuf_*.s` | `runtime._defer` in `runtime/runtime2.go` |
| `funcHeader` in `jmpbuf.go` | `runtime._func` in `runtime/runtime2.go` |
| `moduleData` in `jmpbuf.go` | `runtime.moduledata` in `runtime/symtab.go` |
| `writeBarrier` in `jmpbuf.go` | `runtime.writeBarrier` in `runtime/mgc.go` |
| `lockDistance` in `jmpbuf.go` | `runtime.m`: from `curg` to `lockedExt` |

A wrong `lockDistance` does not break the package: it silently switches to the slower
channel-based holders (`TestSet_ChannelMode` catches this). When everything passes, raise the
Go version range in this README.

### Porting to another architecture
The package walks the stack by the frame pointer chain. Go keeps frame pointers only on amd64 and
arm64, so these two ports are the simple case. Other 64-bit targets (riscv64, loong64, ppc64le,
s390x) have no frame pointers: a port also needs a frame walker based on the runtime frame size
table (`frameSize` in `jmpbuf_arm64.go` already decodes it). 32-bit targets also need a
different `wordSize`.

Steps:
1. Add the architecture to the `//go:build` line of `jmpbuf.go`, `jmpbuf_test.go` and
   `example_test.go`.
2. Create `jmpbuf_<arch>.go` with:
   - `linkSize` — the size of the frame link saved by a call (return address and frame pointer);
   - `regionEnd` — where the base frame ends (amd64: at the link; arm64: by the frame size table);
   - `verifyFrames` — a run-time check of these assumptions (may be empty).
3. Create `jmpbuf_<arch>.s`. Start from `jmpbuf_arm64.s` for register-based `g` (RISC targets) or
   from `jmpbuf_amd64.s` for `g` in thread-local storage. Functions to implement:

| Function | Job |
|---|---|
| `setjmp`, `longjmp` | Entry points: pass the stack and frame pointers to `capture` and `launch`. |
| `snapshot` | Copy stack words into the image. |
| `anchor`, `graft` | Lay the image out on the holder stack, shift addresses, link defer records, continue in `hold`. |
| `discard` | Drop the holder frames and their defer records. |
| `restore` | Copy the image below the base, shift the marked words, relink defer records, return from `Set` with `false`. |
| `current`, `threadWord` | Read the current `g` and a word of its `m`. |
| `deferHead`, `scanDefers` | Read the defer chain of `g`. |
| `stackOffset`, `stackDepth`, `stackFree` | Measure positions against the stack bounds of `g`. |

4. Set the offsets (`g_lo` … `defer_link`) from `runtime/runtime2.go`; `verify` checks them on
   the first `Set`.
5. Add `test-<arch>` and `run-<arch>` targets to the `Makefile` with `qemu-<arch>-static` and a
   cross compiler. Under emulation keep `GODEBUG=asyncpreemptoff=1` and skip
   `TestSet_Traceback`, as `test-arm64` does.
6. The port is done when `make test-<arch>` passes all tests and examples, the stress options
   pass, and `make run-<arch>` reports 0 failures. Then add the architecture to the README
   title and to [Limitations](#limitations).

## License
[MIT](LICENSE).
