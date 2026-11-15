package setjmp

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"
)

const linkSize = wordSize
const pcQuantum = 4

func callerRecord() (unsafe.Pointer, uintptr)

func regionEnd(outer unsafe.Pointer, inner uintptr) (unsafe.Pointer, error) {
	size, err := frameSize(inner)
	if err != nil {
		return nil, fmt.Errorf("failed to get frame size of '%v': %w",
			runtime.FuncForPC(inner-1).Name(), err)
	} else if next := (*frameLink)(outer).next; next != nil && next != unsafe.Add(outer, size) {
		return nil, fmt.Errorf("unexpected frame size (%v) of '%v'", size,
			runtime.FuncForPC(inner-1).Name())
	}
	return unsafe.Add(outer, size), nil
}

func verifyFrames() error {
	if err := probeOuter(); err != nil {
		return fmt.Errorf("failed to probe frame size: %w", err)
	}
	return nil
}

//go:noinline
func probeOuter() error {
	err := probeInner()
	keep(nil)
	return err
}

//go:noinline
func probeInner() error {
	record, pc := callerRecord()
	if size, err := frameSize(pc); err != nil {
		return err
	} else if (*frameLink)(record).next != unsafe.Add(record, size) {
		return fmt.Errorf("unexpected frame size (%v)", size)
	}
	keep(nil)
	return nil
}

func frameSize(pc uintptr) (uintptr, error) {
	info := findfunc(pc - 1)
	if info.header == nil || info.module == nil {
		return 0, fmt.Errorf("unexpected <nil> function for pc '%#x'", pc)
	}
	table, offset := info.module.values, int(info.header.frames)
	current, value := runtime.FuncForPC(pc-1).Entry(), int32(-1)
	for first := true; offset < len(table); first = false {
		delta, next := readVarint(table, offset)
		if delta == 0 && !first {
			break
		}
		step, next := readVarint(table, next)
		value += int32(-(delta & 1) ^ (delta >> 1))
		current, offset = current+uintptr(step)*pcQuantum, next
		if pc-1 < current && value >= 0 {
			return uintptr(value), nil
		} else if pc-1 < current {
			return 0, fmt.Errorf("invalid frame size (%v)", value)
		}
	}
	return 0, errors.New("value does not exist")
}

func readVarint(table []byte, offset int) (uint32, int) {
	result := uint32(0)
	for shift := 0; offset < len(table); shift += 7 {
		current := table[offset]
		result |= uint32(current&0x7F) << shift
		if offset++; current&0x80 == 0 {
			break
		}
	}
	return result, offset
}
