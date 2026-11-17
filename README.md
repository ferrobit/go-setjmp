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

Complete programs are in [examples](examples); `make examples` runs the Go ones:
- [parser](examples/parser/main.go) — an expression calculator. Any error, even from 10000 nested
  parentheses, leaves the recursion with one `Jump`: `go run ./examples/parser`.
- [samefringe](examples/samefringe/main.go) — the "same fringe" problem: two recursive tree
  walkers are resumed in turn to compare the leaves of trees of different shapes, without
  goroutines or channels: `go run ./examples/samefringe`.
- [cgo](examples/cgo) — a shared library and a multithreaded C application: `make run`.

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
- `jmpbuf_test.go` — tests (19 of them) and benchmarks (18 of them).
- `example_test.go` — usage examples (6 of them), checked by `go test`.
- `examples/cgo/library/main.go` — a shared library (`-buildmode=c-shared`) that exports
  `Exported`, `Next`, `Checksum`, `Reference`, `Collect`.
- `examples/cgo/application/main.c` — a C application that calls the library from several threads.
- `examples/parser/main.go` — an expression calculator that reports errors with `Jump`.
- `examples/samefringe/main.go` — two tree walkers resumed in turn (the "same fringe" problem).
- `Makefile` — build, test and stress targets (see below).
- `docs/logo.svg`, `docs/logo.png` — the logo (SVG source and its PNG rendering).

## Building and testing
- `make test` — `go vet` and tests with coverage.
- `make run` — builds the library and the application and runs them (normally and
  under garbage collector pressure).
- `make stress` — tests with `GOGC=1`, `gccheckmark=1`, `clobberfree=1`.
- `make bench` — benchmarks with latency percentiles (see [Performance](#performance)).
- `make examples` — runs the Go programs from `examples` (`parser`, `samefringe`).
- `make clean` — removes the `build` directory.
- `make test-arm64`, `make run-arm64` and `make examples-arm64` — the same for arm64 via the
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

### Measured latency
`make bench` runs the benchmarks from `jmpbuf_test.go`. Each one reports the mean time (`ns/op`)
and the latency of single operations: `p50-ns`, `p99-ns` and `max-ns`. A single measurement
includes about 30 ns of timer overhead. Results on a cloud VM with 2 vCPU (Intel Xeon, 2.1 GHz),
Linux, Go 1.24.7:

| Scenario | Benchmark | p50, ns | p99, ns |
|---|---|---:|---:|
| `Set` on a set buffer (the holder is reused) | `Set_Repeated` | 360 | 544 |
| `Set` and `Free` (a pooled holder) | `Set_Free` | 587 | 1122 |
| `Set` with 100 extra frames | `Set_Depth100` | 2226 | 5585 |
| `Set` with 1000 extra frames | `Set_Depth1000` | 18791 | 50725 |
| `Jump` back to the same `Set` | `Jump_Repeated` | 131 | 262 |
| `Jump` to another goroutine, with its `go` statement | `Jump_Migration` | 265 | 1168 |
| Exit from 100 frames deep with `Jump` | `Jump_Unwind100` | 949 | 1548 |
| The same with `panic` and `recover` | `Panic_Unwind100` | 1734 | 4487 |
| The same with plain `error` returns | `Error_Unwind100` | 180 | 330 |
| The same with `fmt.Errorf` at every level | `Error_Wrap100` | 43883 | 313705 |
| Generator value: two `Set` and two `Jump` | `Jump_Generator` | 918 | 1652 |
| The same on a locked thread, as in cgo calls | `Jump_GeneratorLocked` | 900 | 1774 |
| The same with channel-based holders | `Jump_GeneratorChannel` | 3582 | 15477 |
| The same under a constantly running garbage collector | `Jump_GeneratorCollector` | 1049 | 93154 |
| Generator value with `iter.Pull` | `Pull_Generator` | 102 | 219 |
| Generator value with an unbuffered channel | `Channel_Generator` | 298 | 794 |
| The same on a locked thread | `Channel_GeneratorLocked` | 9345 | 43161 |

What follows from the numbers:
- `Set` costs about 20 ns per captured frame, so keep the base frame close to the `Set` call.
- `Jump` leaves deep recursion about 2 times faster than `panic`, but plain `error` returns are
  still about 5 times faster.
- A generator on this package is about 9 times slower than `iter.Pull`. On a locked thread it
  is about 10 times faster than a channel.
- During a garbage collector mark phase `Set` waits for the phase to end, so p99 grows to tens
  of microseconds.

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
- Before a commit: `make test`, `make stress`, `make run`, `make examples`, and on assembly
  changes also `make test-arm64`, `make run-arm64` and `make examples-arm64`. Format with
  `gofmt` only.
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
go-setjmp is free software distributed under the [MIT License](LICENSE.md), one of the most
permissive open source licenses. In short:
- **You may** use, copy, modify, merge, publish, distribute, sublicense and sell the software,
  in open source and in closed-source commercial products alike.
- **You must** keep the copyright notice and the license text in all copies or substantial
  portions of the software.
- **No warranty:** the software is provided "as is", and the authors are not liable for any
  claim or damages arising from its use.

This summary is for convenience only; the full text in [LICENSE.md](LICENSE.md) is what applies.
SPDX identifier: `MIT`.
