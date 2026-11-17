//go:build amd64 || arm64

package setjmp

import (
	"errors"
	"fmt"
	"io"
	"iter"
	"reflect"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"slices"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
)

type testNode struct {
	value int
	next  *testNode
}

type testScenario struct {
	one    JMPBuf
	two    JMPBuf
	events []string
	err    error
}

type testGenerator struct {
	caller   JMPBuf
	producer JMPBuf
	started  bool
	value    int
	err      error
}

type valueGenerator struct {
	caller   JMPBuf
	producer JMPBuf
	started  bool
	value    int
	err      error
}

type latencyRecorder struct {
	samples []time.Duration
	start   time.Time
}

func BenchmarkChannel_Generator(b *testing.B) {
	values, stop := make(chan int), make(chan struct{})
	defer close(stop)
	go sendValues(values, stop)
	recorder := makeLatencyRecorder(b)
	for index := range b.N {
		recorder.begin()
		value := <-values
		recorder.end()
		if value != index {
			require.Equal(b, index, value)
		}
	}
	recorder.report(b)
}

func BenchmarkChannel_GeneratorLocked(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	BenchmarkChannel_Generator(b)
}

func BenchmarkError_Unwind100(b *testing.B) {
	recorder := makeLatencyRecorder(b)
	for range b.N {
		recorder.begin()
		err := failAtDepth(100)
		recorder.end()
		require.Error(b, err)
	}
	recorder.report(b)
}

func BenchmarkError_Wrap100(b *testing.B) {
	recorder := makeLatencyRecorder(b)
	for range b.N {
		recorder.begin()
		err := wrapAtDepth(100)
		recorder.end()
		require.Error(b, err)
	}
	recorder.report(b)
}

func BenchmarkJump_Generator(b *testing.B) {
	benchmarkGenerator(b)
}

func BenchmarkJump_GeneratorChannel(b *testing.B) {
	require.NoError(b, supported())
	locks := shared.locks
	shared.locks = 0
	benchmarkGenerator(b)
	shared.locks = locks
}

func BenchmarkJump_GeneratorCollector(b *testing.B) {
	stop := startCollector()
	benchmarkGenerator(b)
	stop()
}

func BenchmarkJump_GeneratorLocked(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	benchmarkGenerator(b)
}

func BenchmarkJump_Migration(b *testing.B) {
	var buf JMPBuf
	defer buf.Free()
	recorder := makeLatencyRecorder(b)
	done := make(chan error)
	go relaySource(&buf, recorder, done)
	require.NoError(b, <-done)
	b.ResetTimer()
	for range b.N {
		recorder.begin()
		go relayTarget(&buf, done)
		require.NoError(b, <-done)
	}
	recorder.report(b)
}

func BenchmarkJump_Repeated(b *testing.B) {
	var buf JMPBuf
	defer buf.Free()
	recorder := makeLatencyRecorder(b)
	if set, err := buf.Set(); err != nil {
		require.NoError(b, err)
	} else if !set {
		recorder.end()
	}
	if len(recorder.samples) < b.N {
		recorder.begin()
		require.NoError(b, buf.Jump())
	}
	recorder.report(b)
}

func BenchmarkJump_Unwind100(b *testing.B) {
	var buf JMPBuf
	defer buf.Free()
	recorder := makeLatencyRecorder(b)
	if set, err := buf.Set(); err != nil {
		require.NoError(b, err)
	} else if !set {
		recorder.end()
	}
	if len(recorder.samples) < b.N {
		recorder.begin()
		require.NoError(b, jumpFromDepth(&buf, 100))
	}
	recorder.report(b)
}

func BenchmarkPanic_Unwind100(b *testing.B) {
	recorder := makeLatencyRecorder(b)
	for range b.N {
		recorder.begin()
		err := recoverFromDepth(100)
		recorder.end()
		require.Error(b, err)
	}
	recorder.report(b)
}

func BenchmarkPull_Generator(b *testing.B) {
	next, stop := iter.Pull(countValues)
	defer stop()
	recorder := makeLatencyRecorder(b)
	for index := range b.N {
		recorder.begin()
		value, _ := next()
		recorder.end()
		if value != index {
			require.Equal(b, index, value)
		}
	}
	recorder.report(b)
}

func BenchmarkSet_Depth100(b *testing.B) {
	var buf JMPBuf
	defer buf.Free()
	recorder := makeLatencyRecorder(b)
	setAtDepth(b, &buf, recorder, 100)
	recorder.report(b)
}

func BenchmarkSet_Depth1000(b *testing.B) {
	var buf JMPBuf
	defer buf.Free()
	recorder := makeLatencyRecorder(b)
	setAtDepth(b, &buf, recorder, 1000)
	recorder.report(b)
}

func BenchmarkSet_Free(b *testing.B) {
	var buf JMPBuf
	recorder := makeLatencyRecorder(b)
	for range b.N {
		recorder.begin()
		set, err := buf.Set()
		buf.Free()
		recorder.end()
		require.NoError(b, err)
		require.True(b, set)
	}
	recorder.report(b)
}

func BenchmarkSet_Jump(b *testing.B) {
	generator := &testGenerator{}
	for range b.N {
		generator.next()
	}
	generator.free()
}

func BenchmarkSet_Repeated(b *testing.B) {
	var buf JMPBuf
	defer buf.Free()
	recorder := makeLatencyRecorder(b)
	for range b.N {
		recorder.begin()
		set, err := buf.Set()
		recorder.end()
		require.NoError(b, err)
		require.True(b, set)
	}
	recorder.report(b)
}

func TestFree_Unset(t *testing.T) {
	var buf JMPBuf
	buf.Free()
	require.True(t, requireSet(t, &buf))
	buf.Free()
	require.ErrorContains(t, buf.Jump(), "jump buffer is not set")
}

func TestJump_BaseMismatch(t *testing.T) {
	var buf JMPBuf
	defer buf.Free()
	failure := new(error)
	count := makeCounter()
	if requireSet(t, &buf) {
		reflect.ValueOf(func() { *failure = buf.Jump() }).Call(nil)
	}
	*count++
	require.Equal(t, 1, *count)
	require.ErrorContains(t, *failure, "unexpected base frame caller")
}

func TestJump_DropsDefers(t *testing.T) {
	var buf JMPBuf
	defer buf.Free()
	passes := makeCounter()
	_, before, _, _ := deferHead()
	if requireSet(t, &buf) {
		require.NoError(t, jumpWithDefers(&buf, passes))
	}
	_, after, _, _ := deferHead()
	require.Equal(t, before, after)
	require.Equal(t, 0, *passes)
}

func TestJump_Migration(t *testing.T) {
	var buf JMPBuf
	defer buf.Free()
	result := make(chan string, 2)
	done := make(chan struct{})
	go migrateSource(&buf, result, done)
	<-done
	go migrateTarget(&buf, result)
	require.Equal(t, "set", <-result)
	require.Equal(t, "jumped: 2016", <-result)
}

func TestJump_StackWords(t *testing.T) {
	var buf JMPBuf
	defer buf.Free()
	growStack(400)
	local, saved := 7, makeWord()
	pointer := &local
	word := uintptr(unsafe.Pointer(pointer)) &^ 0xFF
	if requireSet(t, &buf) {
		*saved = word
		require.NoError(t, growAndJump(&buf, 4000))
	}
	require.Equal(t, *saved, word)
	require.NotEqual(t, *saved, uintptr(unsafe.Pointer(pointer))&^0xFF)
	require.Equal(t, 7, *pointer)
}

func TestJump_Unset(t *testing.T) {
	var buf JMPBuf
	require.ErrorContains(t, buf.Jump(), "setjmp.Jump failed: jump buffer is not set")
}

func TestSet_ChannelMode(t *testing.T) {
	require.NoError(t, supported())
	locks := shared.locks
	shared.locks = 0
	stop := startCollector()
	scenario := &testScenario{}
	scenario.exported()
	require.NoError(t, scenario.err)
	require.Len(t, scenario.events, 5)
	scenario.free()
	require.NoError(t, runGenerator(2000))
	stop()
	shared.locks = locks
	require.NotZero(t, locks)
}

func TestSet_DeferRecords(t *testing.T) {
	var buf JMPBuf
	defer buf.Free()
	count := makeCounter()
	_, before, _, _ := deferHead()
	deferredRecords(t, &buf, count)
	if *count == 9 {
		require.NoError(t, buf.Jump())
	}
	_, after, _, _ := deferHead()
	require.Equal(t, before, after)
	require.Equal(t, 18, *count)
}

func TestSet_Defers(t *testing.T) {
	var buf JMPBuf
	defer buf.Free()
	count := makeCounter()
	deferredStep(t, &buf, count)
	if *count == 1 {
		require.NoError(t, buf.Jump())
	}
	require.Equal(t, 2, *count)
}

func TestSet_Example(t *testing.T) {
	expected := []string{"one: set", "internal: enter", "one: jumped", "two: jumped",
		"internal: some_string"}
	{
		scenario := &testScenario{}
		scenario.exported()
		require.NoError(t, scenario.err)
		require.Equal(t, expected, scenario.events)
		scenario.free()
	}
	{
		stop := startCollector()
		scenario := &testScenario{}
		scenario.one.Debug, scenario.two.Debug = true, true
		for range 20 {
			scenario.events = nil
			scenario.exported()
			require.NoError(t, scenario.err)
			require.Equal(t, expected, scenario.events)
		}
		scenario.free()
		stop()
	}
}

func TestSet_Generator(t *testing.T) {
	{
		generator := &testGenerator{}
		for index := range 2000 {
			require.Equal(t, fibonacci(index), generator.next())
			require.NoError(t, generator.err)
		}
		generator.free()
	}
	{
		stop := startCollector()
		generator := &testGenerator{}
		for index := range 300 {
			require.Equal(t, fibonacci(index), generator.next())
			require.NoError(t, generator.err)
		}
		generator.free()
		stop()
	}
}

func TestSet_Goroutines(t *testing.T) {
	stop := startCollector()
	results := make(chan error, 16)
	for range cap(results) {
		go func() {
			results <- runGenerator(200)
		}()
	}
	for range cap(results) {
		require.NoError(t, <-results)
	}
	stop()
}

func TestSet_HolderReuse(t *testing.T) {
	var buf JMPBuf
	before := settleGoroutines()
	for range 5000 {
		require.True(t, requireSet(t, &buf))
	}
	buf.Free()
	require.LessOrEqual(t, settleGoroutines(), before+poolLimit+parkLimit)
	stop := startCollector()
	results := make(chan error, 16)
	for range 4 * cap(results) {
		go func() {
			results <- runGenerator(100)
		}()
	}
	for range 4 * cap(results) {
		require.NoError(t, <-results)
	}
	stop()
	for range 20 * cap(results) {
		for writeBarrier.enabled {
			runtime.Gosched()
		}
		require.True(t, requireSet(t, &buf))
		buf.Free()
	}
	shared.lock.Lock()
	parked := len(shared.parked[holderKey{}])
	shared.lock.Unlock()
	require.LessOrEqual(t, parked, parkLimit)
}

func TestSet_Liveness(t *testing.T) {
	stop := startCollector()
	for range 50 {
		buf := &JMPBuf{}
		passes := makeCounter()
		total := sumOnJump(t, buf)
		*passes++
		if *passes == 1 {
			require.Equal(t, -1, total)
			for range 4 {
				makeGarbage(1 << 20)
				runtime.GC()
			}
			require.NoError(t, buf.Jump())
		}
		require.Equal(t, 2, *passes)
		require.Equal(t, 499500, total)
		buf.Free()
	}
	stop()
}

func TestSet_LockedThread(t *testing.T) {
	results := make(chan error, 8)
	for index := range cap(results) {
		go func() {
			runtime.LockOSThread()
			if index%2 == 0 {
				defer runtime.UnlockOSThread()
			}
			err := runGenerator(300)
			runtime.LockOSThread()
			if err == nil {
				err = runGenerator(300)
			}
			runtime.UnlockOSThread()
			results <- err
		}()
	}
	for range cap(results) {
		require.NoError(t, <-results)
	}
}

func TestSet_PendingDefer(t *testing.T) {
	var buf JMPBuf
	for range pendingCount(1) {
		defer keep(nil)
	}
	set, err := buf.Set()
	require.False(t, set)
	require.ErrorContains(t, err, "captured frames have pending heap defer records")
	require.ErrorContains(t, buf.Jump(), "captured frames have pending heap defer records")
}

func TestSet_Repeated(t *testing.T) {
	var buf JMPBuf
	defer buf.Free()
	count := makeCounter()
	local := 10
	if requireSet(t, &buf) {
		local = 20
	}
	*count++
	if *count == 1 {
		require.Equal(t, 20, local)
	} else {
		require.Equal(t, 10, local)
	}
	if *count < 5 {
		local = 30
		require.NoError(t, buf.Jump())
	}
	require.Equal(t, 5, *count)
}

func TestSet_StackGrowth(t *testing.T) {
	{
		var buf JMPBuf
		count := makeCounter()
		values := [8]int{1, 2, 3, 4, 5, 6, 7, 8}
		pointer := &values[3]
		if requireSet(t, &buf) {
			*count++
			values[3] = 100
			require.Equal(t, 100, *pointer)
			require.NoError(t, growAndJump(&buf, 4000))
		}
		require.Equal(t, 1, *count)
		require.Equal(t, 4, *pointer)
		*pointer = 7
		require.Equal(t, [8]int{1, 2, 3, 7, 5, 6, 7, 8}, values)
		buf.Free()
	}
	{
		var buf JMPBuf
		stop := startCollector()
		trail := makeTrail()
		total := descendAndSet(t, &buf, 3000, trail)
		if len(*trail) == 1 {
			runtime.GC()
			require.NoError(t, buf.Jump())
		}
		require.Equal(t, []int{1, 2}, *trail)
		require.Equal(t, descendTotal(3000), total)
		buf.Free()
		stop()
	}
}

func TestSet_Traceback(t *testing.T) {
	{
		var buf JMPBuf
		require.True(t, requireSet(t, &buf))
		dump := make([]byte, 1<<20)
		dump = dump[:runtime.Stack(dump, true)]
		require.Contains(t, string(dump), "setjmp.hold(")
		require.Contains(t, string(dump), "setjmp.TestSet_Traceback(")
		buf.Free()
	}
	{
		require.NoError(t, trace.Start(io.Discard))
		require.NoError(t, pprof.StartCPUProfile(io.Discard))
		require.NoError(t, runGenerator(3000))
		pprof.StopCPUProfile()
		trace.Stop()
	}
}

func benchmarkGenerator(b *testing.B) {
	generator := &valueGenerator{}
	defer generator.free()
	recorder := makeLatencyRecorder(b)
	for index := range b.N {
		recorder.begin()
		value := generator.next()
		recorder.end()
		if value != index {
			require.Equal(b, index, value)
		}
	}
	recorder.report(b)
	require.NoError(b, generator.err)
}

func countValues(yield func(int) bool) {
	for value := 0; yield(value); value++ {
	}
}

//go:noinline
func deferredRecords(t *testing.T, buf *JMPBuf, count *int) {
	defer func() { *count++ }()
	defer func() { *count++ }()
	defer func() { *count++ }()
	defer func() { *count++ }()
	defer func() { *count++ }()
	defer func() { *count++ }()
	defer func() { *count++ }()
	defer func() { *count++ }()
	defer func() { *count++ }()
	requireSet(t, buf)
	runtime.GC()
}

//go:noinline
func deferredStep(t *testing.T, buf *JMPBuf, count *int) {
	defer func() { *count++ }()
	requireSet(t, buf)
}

//go:noinline
func descendAndSet(t *testing.T, buf *JMPBuf, depth int, trail *[]int) int {
	var pad [128]byte
	pad[0], pad[127] = byte(depth), byte(depth>>8)
	if depth > 0 {
		return descendAndSet(t, buf, depth-1, trail) + int(pad[0]) + int(pad[127])
	} else if requireSet(t, buf) {
		*trail = append(*trail, 1)
	} else {
		*trail = append(*trail, 2)
	}
	return int(pad[0]) + int(pad[127])
}

func descendTotal(depth int) int {
	result := 0
	for index := range depth + 1 {
		result += int(byte(index)) + int(byte(index>>8))
	}
	return result
}

//go:noinline
func failAtDepth(depth int) error {
	if depth > 0 {
		return failAtDepth(depth - 1)
	}
	return errors.New("value does not exist")
}

func fibonacci(count int) int {
	first, second := 0, 1
	for range count {
		first, second = second, first+second
	}
	return first
}

//go:noinline
func growAndJump(buf *JMPBuf, depth int) error {
	var pad [256]byte
	pad[0] = byte(depth)
	if depth > 0 {
		err := growAndJump(buf, depth-1)
		keepPad(&pad)
		return err
	}
	return buf.Jump()
}

//go:noinline
func keepPad(_ *[256]byte) {
}

func makeChain(count int) *testNode {
	var result *testNode
	for index := range count {
		result = &testNode{value: index, next: result}
	}
	return result
}

//go:noinline
func growStack(depth int) int {
	var pad [256]byte
	pad[0] = byte(depth)
	if depth > 0 {
		return growStack(depth-1) + int(pad[0])
	}
	return 0
}

//go:noinline
func jumpWithDefers(buf *JMPBuf, passes *int) error {
	for range pendingCount(3) {
		defer func() { *passes++ }()
	}
	return buf.Jump()
}

//go:noinline
func jumpFromDepth(buf *JMPBuf, depth int) error {
	if depth > 0 {
		return jumpFromDepth(buf, depth-1)
	}
	return buf.Jump()
}

//go:noinline
func makeCounter() *int {
	return new(int)
}

//go:noinline
func makeGarbage(size int) []byte {
	return make([]byte, size)
}

//go:noinline
func makeLatencyRecorder(b *testing.B) *latencyRecorder {
	result := &latencyRecorder{samples: make([]time.Duration, 0, b.N)}
	b.ResetTimer()
	return result
}

//go:noinline
func makeTrail() *[]int {
	return new([]int)
}

//go:noinline
func makeWord() *uintptr {
	return new(uintptr)
}

func migrateSource(buf *JMPBuf, result chan string, done chan struct{}) {
	text := "jumped: "
	pointer := &text
	chain := makeChain(64)
	if set, err := buf.Set(); err != nil {
		result <- err.Error()
		close(done)
		return
	} else if set {
		result <- "set"
		close(done)
		return
	}
	runtime.GC()
	result <- *pointer + strconv.Itoa(sumChain(chain))
}

func migrateTarget(buf *JMPBuf, result chan string) {
	if err := buf.Jump(); err != nil {
		result <- err.Error()
	}
}

//go:noinline
func panicAtDepth(depth int) {
	if depth > 0 {
		panicAtDepth(depth - 1)
		return
	}
	panic(errors.New("value does not exist"))
}

//go:noinline
func pendingCount(count int) int {
	return count
}

func recoverFromDepth(depth int) error {
	failure := make(chan error, 1)
	func() {
		defer func() {
			if value, ok := recover().(error); ok {
				failure <- value
			}
		}()
		panicAtDepth(depth)
	}()
	return <-failure
}

func relaySource(buf *JMPBuf, recorder *latencyRecorder, done chan error) {
	if set, err := buf.Set(); err != nil {
		done <- err
		return
	} else if !set {
		recorder.end()
	}
	done <- nil
}

func relayTarget(buf *JMPBuf, done chan error) {
	if err := buf.Jump(); err != nil {
		done <- err
	}
}

func requireSet(t *testing.T, buf *JMPBuf) bool {
	set, err := buf.Set()
	require.NoError(t, err)
	return set
}

func runGenerator(count int) error {
	generator := &testGenerator{}
	defer generator.free()
	for index := range count {
		if value := generator.next(); generator.err != nil {
			return generator.err
		} else if value != fibonacci(index) {
			return errors.New("unexpected generator value")
		}
	}
	return nil
}

func sendValues(values chan int, stop chan struct{}) {
	for value := 0; ; value++ {
		select {
		case values <- value:
		case <-stop:
			return
		}
	}
}

//go:noinline
func setAtDepth(b *testing.B, buf *JMPBuf, recorder *latencyRecorder, depth int) {
	if depth > 0 {
		setAtDepth(b, buf, recorder, depth-1)
		return
	}
	for range b.N {
		recorder.begin()
		set, err := buf.Set()
		recorder.end()
		require.NoError(b, err)
		require.True(b, set)
	}
}

func settleGoroutines() int {
	result, stable := runtime.NumGoroutine(), 0
	for stable < 50 {
		time.Sleep(time.Millisecond)
		if count := runtime.NumGoroutine(); count == result {
			stable++
		} else {
			result, stable = count, 0
		}
	}
	return result
}

func startCollector() func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				makeGarbage(1 << 16)
				runtime.GC()
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

//go:noinline
func sumOnJump(t *testing.T, buf *JMPBuf) int {
	chain := makeChain(1000)
	if requireSet(t, buf) {
		return -1
	}
	return sumChain(chain)
}

func sumChain(node *testNode) int {
	result := 0
	for ; node != nil; node = node.next {
		result += node.value
	}
	return result
}

func wrapAtDepth(depth int) error {
	if depth > 0 {
		if err := wrapAtDepth(depth - 1); err != nil {
			return fmt.Errorf("failed to unwind level %v: %w", depth, err)
		}
		return nil
	}
	return errors.New("value does not exist")
}

func (recorder *latencyRecorder) begin() {
	recorder.start = time.Now()
}

func (recorder *latencyRecorder) end() {
	recorder.samples = append(recorder.samples, time.Since(recorder.start))
}

func (recorder *latencyRecorder) report(b *testing.B) {
	b.StopTimer()
	if count := len(recorder.samples); count != 0 {
		slices.Sort(recorder.samples)
		b.ReportMetric(float64(recorder.samples[count/2]), "p50-ns")
		b.ReportMetric(float64(recorder.samples[count*99/100]), "p99-ns")
		b.ReportMetric(float64(recorder.samples[count-1]), "max-ns")
	}
}

func (scenario *testScenario) exported() {
	if set, err := scenario.one.Set(); err != nil {
		scenario.err = err
	} else if set {
		scenario.events = append(scenario.events, "one: set")
		scenario.internal()
	} else {
		scenario.events = append(scenario.events, "one: jumped")
		scenario.err = scenario.two.Jump()
	}
}

func (scenario *testScenario) free() {
	scenario.one.Free()
	scenario.two.Free()
}

func (scenario *testScenario) internal() {
	text := "some_string"
	pointer := &text
	chain := makeChain(1000)
	scenario.events = append(scenario.events, "internal: enter")
	if set, err := scenario.two.Set(); err != nil {
		scenario.err = err
	} else if set {
		scenario.err = scenario.one.Jump()
	} else {
		scenario.events = append(scenario.events, "two: jumped")
	}
	runtime.GC()
	if sumChain(chain) != 499500 {
		scenario.err = errors.New("chain is corrupted")
	}
	scenario.events = append(scenario.events, "internal: "+*pointer)
}

func (generator *testGenerator) free() {
	generator.caller.Free()
	generator.producer.Free()
}

func (generator *testGenerator) next() int {
	if set, err := generator.caller.Set(); err != nil {
		generator.err = err
	} else if set {
		if !generator.started {
			generator.started = true
			generator.produce()
		} else if err := generator.producer.Jump(); err != nil {
			generator.err = err
		}
	}
	return generator.value
}

func (generator *testGenerator) produce() {
	first, second := 0, 1
	chain := makeChain(64)
	for {
		generator.value = first
		if set, err := generator.producer.Set(); err != nil {
			generator.err = err
			return
		} else if set {
			if err := generator.caller.Jump(); err != nil {
				generator.err = err
				return
			}
		}
		makeGarbage(1024)
		if sumChain(chain) != 2016 {
			generator.err = errors.New("chain is corrupted")
			return
		}
		first, second = second, first+second
	}
}

func (generator *valueGenerator) free() {
	generator.caller.Free()
	generator.producer.Free()
}

func (generator *valueGenerator) next() int {
	if set, err := generator.caller.Set(); err != nil {
		generator.err = err
	} else if set && !generator.started {
		generator.started = true
		generator.produce()
	} else if set {
		if err := generator.producer.Jump(); err != nil {
			generator.err = err
		}
	}
	return generator.value
}

func (generator *valueGenerator) produce() {
	for value := 0; ; value++ {
		generator.value = value
		if set, err := generator.producer.Set(); err != nil {
			generator.err = err
			return
		} else if set {
			generator.err = generator.caller.Jump()
			return
		}
	}
}
