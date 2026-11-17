//go:build amd64 || arm64

// Package setjmp implements setjmp and longjmp for Go on amd64 and arm64.
//
// [JMPBuf.Set] saves the stack frames from its call site up to the base frame: the first frame
// called by the runtime (a goroutine function or main.main) or by a cgo export wrapper.
// [JMPBuf.Jump] later restores these frames, and the saved Set call returns again, this time
// with false. A jump may go up the stack, down into frames that have already returned, and
// across goroutines and threads.
//
// After a jump, stack variables hold their values from the time of Set, while heap data is not
// rolled back. Keep the state that must survive a jump on the heap, for example in a struct
// that also contains the [JMPBuf]. Deferred calls of abandoned frames are not run, as in C.
//
// The package reads runtime internals. It supports Linux with Go 1.23 to 1.27, checks the
// runtime layout on the first Set, and reports a mismatch as an error from [JMPBuf.Set].
package setjmp

import (
	"errors"
	"fmt"
	"iter"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

// JMPBuf holds a stack saved by [JMPBuf.Set] and resumed by [JMPBuf.Jump].
//
// The zero value is an unset buffer ready to use. A JMPBuf is not safe for concurrent use and
// must not be copied after Set. A set buffer keeps the goroutine that holds its frames alive
// until [JMPBuf.Free] is called.
type JMPBuf struct {
	Debug  bool // NOTE: Runs a full garbage collection inside Set and Jump.
	holder *stackHolder
	err    error
}

type stackHolder struct {
	image   []uintptr
	count   uintptr
	source  uintptr
	span    uintptr
	frame   uintptr
	graft   uintptr
	defers  uintptr
	last    uintptr
	link    uintptr
	resume  uintptr
	anchor  uintptr
	args    uintptr
	pc      uintptr
	marks   []uint8
	probe   chan error
	key     holderKey
	next    func() (struct{}, bool)
	stop    func()
	yield   func(struct{}) bool
	wake    chan struct{}
	ready   chan struct{}
	release chan struct{}
}

type holderKey struct {
	thread uintptr
	locks  uintptr
}

type frameLink struct {
	next unsafe.Pointer
	pc   uintptr
}

type funcInfo struct {
	header *funcHeader
	module *moduleData
}

type funcHeader struct {
	entry       uint32
	name        int32
	args        int32
	deferreturn uint32
	frames      uint32
}

type moduleData struct {
	header unsafe.Pointer
	names  []byte
	units  []uint32
	files  []byte
	values []byte
}

type holderRegistry struct {
	symbols atomic.Pointer[map[uintptr]bool]
	lock    sync.Mutex
	idle    chan *stackHolder
	parked  map[holderKey][]*stackHolder
	locks   uintptr
}

const wordSize = 8
const padSize = 512
const argsLimit = 512
const frameLimit = 1 << 20
const stackReserve = 8192
const poolLimit = 64
const parkLimit = 16
const threadLimit = 1024
const lockDistance = 33 * wordSize
const noDefers = ^uintptr(0)

//go:linkname writeBarrier runtime.writeBarrier
var writeBarrier struct {
	enabled bool
	pad     [3]byte
	alignme uint64
}

var shared = makeHolderRegistry()
var supported = sync.OnceValue(verify)

//go:linkname findfunc runtime.findfunc
func findfunc(pc uintptr) funcInfo

func setjmp(buf *JMPBuf) bool

func longjmp(buf *JMPBuf) error

func snapshot(stack, frame unsafe.Pointer, image *uintptr, count uintptr) (uintptr, uintptr)

func anchor(holder *stackHolder)

func discard(holder *stackHolder)

func restore(holder *stackHolder, base, link uintptr)

func current() uintptr

func deferHead() (uintptr, uintptr, uintptr, uintptr)

func scanDefers(stack unsafe.Pointer, base uintptr) (uintptr, uintptr, bool)

func stackOffset(pointer unsafe.Pointer) uintptr

func stackDepth() uintptr

func stackFree() uintptr

func threadWord(offset uintptr) (uintptr, uintptr)

func capture(buf *JMPBuf, stack, frame unsafe.Pointer, _ *stackHolder, resume uintptr) bool {
	if buf.Debug {
		runtime.GC()
	}
	holder, err := prepareHolder(buf, stack, frame, resume)
	if err != nil {
		buf.Free()
		buf.err = fmt.Errorf("setjmp.Set failed: %w", err)
		return true
	}
	// NOTE: The holder may be already scanned in the current mark phase, so the captured
	// frames must stay alive on this stack until the phase is over.
	for writeBarrier.enabled {
		runtime.Gosched()
	}
	buf.holder, buf.err = holder, nil
	return true
}

func hold(_ *JMPBuf, stack, _ unsafe.Pointer, holder *stackHolder, _ uintptr) bool {
	if holder.probe != nil {
		holder.probe <- holder.mark(stack)
	} else if holder.yield != nil {
		holder.yield(struct{}{})
	} else {
		holder.ready <- struct{}{}
		<-holder.release
		// NOTE: A stack restored during the mark phase relies on this holder to be scanned.
		for writeBarrier.enabled {
			runtime.Gosched()
		}
	}
	discard(holder)
	return false
}

func launch(buf *JMPBuf, frame unsafe.Pointer) error {
	holder := buf.holder
	if holder == nil && buf.err != nil {
		return fmt.Errorf("jump buffer is not set: %w", buf.err)
	} else if holder == nil {
		return errors.New("jump buffer is not set")
	}
	outer, pc, inner, err := findBase(frame)
	if err != nil {
		return fmt.Errorf("failed to find base frame: %w", err)
	} else if pc != holder.pc {
		return fmt.Errorf("unexpected base frame caller '%v'", runtime.FuncForPC(pc-1).Name())
	}
	end, err := regionEnd(outer, inner)
	if err != nil {
		return fmt.Errorf("failed to find base frame end: %w", err)
	}
	if buf.Debug {
		runtime.GC()
	}
	for range 2 {
		holder.dive(stackOffset(end), stackOffset(outer))
		if err := holder.resolve(); err != nil {
			return fmt.Errorf("failed to mark stack pointers: %w", err)
		}
	}
	return errors.New("unexpected return from restored stack")
}

func prepareHolder(buf *JMPBuf, stack, frame unsafe.Pointer, resume uintptr) (*stackHolder,
	error) {
	outer, pc, inner, err := findBase(frame)
	if err != nil {
		return nil, fmt.Errorf("failed to find base frame: %w", err)
	}
	end, err := regionEnd(outer, inner)
	if err != nil {
		return nil, fmt.Errorf("failed to find base frame end: %w", err)
	}
	info := findfunc(inner)
	if info.header == nil {
		return nil, fmt.Errorf("unexpected <nil> function for pc '%#x'", inner)
	} else if args := info.header.args; args < 0 || args > argsLimit {
		return nil, fmt.Errorf("invalid arguments size (%v) of '%v'", args,
			runtime.FuncForPC(inner-1).Name())
	}
	count := (uintptr(end) - uintptr(stack)) / wordSize
	defers, last, ok := scanDefers(stack, stackOffset(end)-linkSize)
	if !ok {
		return nil, errors.New("captured frames have pending heap defer records")
	} else if defers != noDefers && max(defers, last) >= count*wordSize {
		return nil, fmt.Errorf("invalid defer record offsets (%v, %v)", defers, last)
	}
	result := shared.exchange(buf.holder)
	buf.holder = nil
	if capacity := uintptr(cap(result.image)); capacity < count || capacity > 4*count+padSize {
		result.image = make([]uintptr, count)
	}
	result.image, result.count, result.pc, result.marks = result.image[:count], count, pc, nil
	result.args = uintptr(info.header.args)
	result.span = count*wordSize + 2*wordSize + result.args
	result.defers, result.last, result.resume = defers, last, resume
	result.link = uintptr(outer) - uintptr(stack)
	result.source, result.frame = snapshot(stack, frame, unsafe.SliceData(result.image), count)
	result.install()
	return result, nil
}

func verify() error {
	before, inside, offset, link, flags, depth := probeDefers(1)
	if after, _, _, _ := deferHead(); inside == 0 || inside == before || link != before ||
		after != before {
		return errors.New("unexpected defer chain layout")
	} else if offset > depth || depth-offset > padSize || flags != 1 {
		return fmt.Errorf("unexpected defer record layout (%v, %v, %v)", offset, depth, flags)
	} else if err := verifyFrames(); err != nil {
		return fmt.Errorf("failed to verify frame layout: %w", err)
	}
	located := make(chan uintptr)
	go locateLocks(located)
	shared.locks = <-located
	return nil
}

func locateLocks(located chan uintptr) {
	result := uintptr(0)
	runtime.LockOSThread()
	self := current()
	for offset := uintptr(0); offset < threadLimit && result == 0; offset += wordSize {
		if _, word := threadWord(offset); word != self {
			continue
		} else if _, locks := threadWord(offset + lockDistance); locks == 1 {
			result = offset + lockDistance
		}
	}
	runtime.LockOSThread()
	_, twice := threadWord(result)
	runtime.UnlockOSThread()
	_, once := threadWord(result)
	runtime.UnlockOSThread()
	if _, none := threadWord(result); twice != 2 || once != 1 || none != 0 {
		result = 0
	}
	located <- result
}

//go:noinline
func probeDefers(count int) (uintptr, uintptr, uintptr, uintptr, uintptr, uintptr) {
	before, _, _, _ := deferHead()
	for range count {
		defer keep(nil)
	}
	inside, offset, link, flags := deferHead()
	return before, inside, offset, link, flags, stackDepth()
}

func findBase(frame unsafe.Pointer) (unsafe.Pointer, uintptr, uintptr, error) {
	inner := uintptr(0)
	for range frameLimit {
		link := (*frameLink)(frame)
		if shared.isForeign(link.pc) {
			return frame, link.pc, inner, nil
		} else if offset := stackOffset(link.next); offset == 0 || offset >= stackOffset(frame) {
			return nil, 0, 0, fmt.Errorf("broken frame pointer chain after '%v'",
				runtime.FuncForPC(link.pc-1).Name())
		}
		frame, inner = link.next, link.pc
	}
	return nil, 0, 0, fmt.Errorf("invalid number of frames (%v)", frameLimit)
}

//go:noinline
func forceMove(top uintptr) {
	var pad [padSize]byte
	if stackOffset(nil) == top {
		forceMove(top)
		keep(&pad)
	}
}

//go:noinline
func keep(_ *[padSize]byte) {
}

func makeHolderRegistry() *holderRegistry {
	return &holderRegistry{idle: make(chan *stackHolder, poolLimit),
		parked: make(map[holderKey][]*stackHolder)}
}

func (registry *holderRegistry) exchange(previous *stackHolder) *stackHolder {
	if registry.locks == 0 {
		registry.release(previous)
		select {
		case result := <-registry.idle:
			return result
		default:
			return makeStackHolder(holderKey{}, false)
		}
	}
	key := registry.identity()
	if writeBarrier.enabled {
		registry.release(previous)
		return makeStackHolder(key, true)
	} else if previous != nil && previous.next != nil && previous.key == key {
		return previous
	}
	registry.release(previous)
	var result, surplus *stackHolder
	registry.lock.Lock()
	parked := registry.parked[key]
	if count := len(parked); count > parkLimit {
		result, surplus, parked = parked[count-1], parked[count-2], parked[:count-2]
	} else if count != 0 {
		result, parked = parked[count-1], parked[:count-1]
	}
	registry.parked[key] = parked
	registry.lock.Unlock()
	if surplus != nil {
		surplus.count = 0
		surplus.stop()
	}
	if result == nil {
		result = makeStackHolder(key, true)
	}
	return result
}

func (registry *holderRegistry) identity() holderKey {
	if thread, locks := threadWord(registry.locks); locks != 0 {
		return holderKey{thread: thread, locks: locks}
	}
	return holderKey{}
}

func (registry *holderRegistry) isForeign(pc uintptr) bool {
	if symbols := registry.symbols.Load(); symbols != nil {
		if result, ok := (*symbols)[pc]; ok {
			return result
		}
	}
	name := runtime.FuncForPC(pc - 1).Name()
	result := name == "" || strings.HasPrefix(name, "runtime.") ||
		strings.Contains(name, "_cgoexp_")
	registry.lock.Lock()
	symbols := map[uintptr]bool{pc: result}
	if previous := registry.symbols.Load(); previous != nil {
		for key, value := range *previous {
			symbols[key] = value
		}
	}
	registry.symbols.Store(&symbols)
	registry.lock.Unlock()
	return result
}

func (registry *holderRegistry) recycle(holder *stackHolder) bool {
	select {
	case registry.idle <- holder:
		return true
	default:
		return false
	}
}

func (registry *holderRegistry) release(holder *stackHolder) {
	if holder == nil {
		return
	} else if holder.next == nil {
		holder.release <- struct{}{}
		return
	}
	// NOTE: A stack restored during the mark phase relies on its holder to be scanned,
	// so the frames of the holder are dropped only when the phase is over.
	clean := !writeBarrier.enabled && holder.key == registry.identity()
	if holder.count = 0; clean {
		holder.next()
	}
	var surplus *stackHolder
	registry.lock.Lock()
	parked := append(registry.parked[holder.key], holder)
	if clean && len(parked) > parkLimit {
		surplus, parked = parked[0], parked[1:]
	}
	registry.parked[holder.key] = parked
	registry.lock.Unlock()
	if surplus != nil {
		surplus.count = 0
		surplus.stop()
	}
}

func makeStackHolder(key holderKey, direct bool) *stackHolder {
	result := &stackHolder{key: key}
	if direct {
		result.next, result.stop = iter.Pull(result.serve)
	} else {
		result.wake, result.ready = make(chan struct{}, 1), make(chan struct{})
		result.release = make(chan struct{}, 1)
	}
	return result
}

func (holder *stackHolder) install() {
	if holder.next != nil {
		holder.next()
		return
	} else if holder.graft == 0 {
		go holder.run()
	} else {
		holder.wake <- struct{}{}
	}
	<-holder.ready
}

func (holder *stackHolder) run() {
	for {
		holder.plant()
		if !shared.recycle(holder) {
			return
		}
		<-holder.wake
	}
}

func (holder *stackHolder) serve(yield func(struct{}) bool) {
	holder.yield = yield
	for holder.count != 0 || yield(struct{}{}) {
		if holder.count != 0 {
			holder.plant()
		}
	}
}

func (holder *stackHolder) plant() {
	var pad [padSize]byte
	if stackFree() < holder.count*wordSize+stackReserve {
		holder.plant()
		keep(&pad)
	} else {
		anchor(holder)
	}
}

func (holder *stackHolder) dive(base, link uintptr) {
	var pad [padSize]byte
	if stackDepth() < base+holder.count*wordSize+padSize {
		holder.dive(base, link)
		keep(&pad)
	} else {
		restore(holder, base, link)
	}
}

func (holder *stackHolder) mark(stack unsafe.Pointer) error {
	before, after := make([]uintptr, holder.count), make([]uintptr, holder.count)
	source, _ := snapshot(stack, stack, unsafe.SliceData(before), holder.count)
	forceMove(stackOffset(nil))
	target, _ := snapshot(stack, stack, unsafe.SliceData(after), holder.count)
	if source == target {
		return errors.New("stack was not moved")
	}
	holder.marks = make([]uint8, holder.count)
	for index, word := range before {
		if moved := after[index]; moved == word {
			continue
		} else if moved-word != target-source {
			return fmt.Errorf("unexpected stack word %v change (%#x)", index, moved-word)
		}
		holder.marks[index] = 1
	}
	return nil
}

// NOTE: The runtime adjusts exactly the live stack pointers when it moves a stack, so the
// words changed by a forced move of a scratch copy are the ones to relocate on restore.
func (holder *stackHolder) resolve() error {
	scratch := &stackHolder{image: holder.image, count: holder.count, source: holder.source,
		span: holder.span, frame: holder.frame, defers: holder.defers, last: holder.last,
		link: holder.link, resume: holder.resume, args: holder.args, probe: make(chan error, 1)}
	go scratch.plant()
	err := <-scratch.probe
	holder.marks = scratch.marks
	return err
}

// Free releases the saved stack and the goroutine that holds it. The Debug field is kept.
// Free is safe on an unset buffer and may be called more than once. After Free, [JMPBuf.Jump]
// fails until the next [JMPBuf.Set].
func (buf *JMPBuf) Free() {
	shared.release(buf.holder)
	buf.holder, buf.err = nil, nil
}

// Jump restores the frames saved by the last [JMPBuf.Set] of the buffer and makes that Set
// call return false. On success Jump does not return. The frames between the base frame and
// the caller of Jump are abandoned without running their deferred calls.
//
// Jump must run on a stack whose base frame is called from the same place as at Set time. So a
// buffer set in one goroutine started by a go statement may be resumed in another one, and a
// buffer set in an exported cgo function may be resumed in another call of the same function.
// Jump cannot cross runtime frames such as [reflect.Value.Call] or panic handling.
//
// Jump returns an error if the buffer is unset or freed, if the last Set failed, or if the
// base frame does not match.
func (buf *JMPBuf) Jump() error {
	if err := longjmp(buf); err != nil {
		return fmt.Errorf("setjmp.Jump failed: %w", err)
	}
	return nil
}

// Set saves the stack from the caller of Set up to the base frame and returns true and nil.
// When [JMPBuf.Jump] resumes the buffer, this Set call returns again, this time with false
// and nil. Setting the buffer again replaces the previous snapshot.
//
// On failure Set returns false and an error, and the buffer becomes unset. So check the error
// first:
//
//	if set, err := buf.Set(); err != nil {
//		return err
//	} else if set {
//		fmt.Println("buffer is set")
//	} else {
//		fmt.Println("resumed by Jump")
//	}
//
// Set fails if a captured frame has a pending defer statement inside a loop, or if the runtime
// layout differs from the one the package expects.
func (buf *JMPBuf) Set() (bool, error) {
	if err := supported(); err != nil {
		buf.Free()
		buf.err = fmt.Errorf("setjmp.Set failed: %w", err)
		return false, buf.err
	} else if !setjmp(buf) {
		return false, nil
	} else if buf.err != nil {
		return false, buf.err
	}
	return true, nil
}
