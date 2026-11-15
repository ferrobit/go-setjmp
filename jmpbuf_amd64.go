package setjmp

import "unsafe"

const linkSize = 2 * wordSize

func regionEnd(outer unsafe.Pointer, _ uintptr) (unsafe.Pointer, error) {
	return outer, nil
}

func verifyFrames() error {
	return nil
}
