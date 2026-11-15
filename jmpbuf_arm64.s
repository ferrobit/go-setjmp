//go:build arm64

#include "go_asm.h"
#include "textflag.h"
#include "funcdata.h"

// NOTE: Offsets inside runtime.g and runtime._defer are checked at run time by verify().
#define g_lo 0
#define g_hi 8
#define g_defer 40
#define g_m 48
#define defer_sp 8
#define defer_link 32

// func setjmp(buf *JMPBuf) bool
TEXT ·setjmp(SB), $48-9
	NO_LOCAL_POINTERS
	MOVD buf+0(FP), R0
	MOVD R0, 8(RSP)
	MOVD R29, 16(RSP)
	MOVD R29, 24(RSP)
	MOVD ZR, 32(RSP)
	ADR resume, R0
	MOVD R0, 40(RSP)
	BL ·capture(SB)
resume:
	MOVBU 48(RSP), R0
	MOVB R0, ret+8(FP)
	RET

// func longjmp(buf *JMPBuf) error
TEXT ·longjmp(SB), $32-24
	NO_LOCAL_POINTERS
	MOVD buf+0(FP), R0
	MOVD R0, 8(RSP)
	MOVD R29, 16(RSP)
	BL ·launch(SB)
	MOVD 24(RSP), R0
	MOVD R0, ret_itable+8(FP)
	MOVD 32(RSP), R0
	MOVD R0, ret_data+16(FP)
	RET

// func snapshot(stack, frame unsafe.Pointer, image *uintptr, count uintptr) (uintptr, uintptr)
TEXT ·snapshot(SB), NOSPLIT, $0-48
	MOVD stack+0(FP), R1
	MOVD frame+8(FP), R0
	MOVD image+16(FP), R2
	MOVD count+24(FP), R3
	MOVD R1, ret+32(FP)
	MOVD R0, ret1+40(FP)
snapshotloop:
	CBZ R3, snapshotdone
	MOVD.P 8(R1), R4
	MOVD.P R4, 8(R2)
	SUB $1, R3
	B snapshotloop
snapshotdone:
	RET

// func anchor(holder *stackHolder)
TEXT ·anchor(SB), $512-8
	NO_LOCAL_POINTERS
	MOVD holder+0(FP), R0
	MOVD stackHolder_args(R0), R1
	ADD $7, R1
	LSR $3, R1
	ADD $8, RSP, R2
anchorloop:
	CBZ R1, anchorcall
	MOVD.P ZR, 8(R2)
	SUB $1, R1
	B anchorloop
anchorcall:
	BL graft<>(SB)
	RET

// NOTE: Copies the image below the anchor frame and continues in hold() as its callee.
TEXT graft<>(SB), NOSPLIT|NOFRAME, $0-0
	MOVD RSP, R1
	MOVD g_hi(g), R2
	SUB R1, R2
	MOVD R2, stackHolder_graft(R0)
	MOVD R30, stackHolder_anchor(R0)
	MOVD stackHolder_count(R0), R3
	MOVD stackHolder_image(R0), R4
	SUB $8, R1, R5
	LSL $3, R3, R6
	SUB R6, R5
	MOVD R5, R6
	MOVD stackHolder_source(R0), R7
	MOVD stackHolder_span(R0), R8
	SUB R7, R5, R9
graftloop:
	CBZ R3, graftdone
	MOVD.P 8(R4), R10
	SUB R7, R10, R11
	CMP R8, R11
	BHS graftstore
	ADD R9, R10
graftstore:
	MOVD.P R10, 8(R6)
	SUB $1, R3
	B graftloop
graftdone:
	MOVD stackHolder_link(R0), R10
	ADD R5, R10
	MOVD R29, (R10)
	MOVD R30, 8(R10)
	MOVD stackHolder_defers(R0), R10
	CMN $1, R10
	BEQ graftjump
	ADD R5, R10
	MOVD g_defer(g), R11
	MOVD R10, g_defer(g)
	MOVD stackHolder_last(R0), R10
	ADD R5, R10
	MOVD R11, defer_link(R10)
graftjump:
	MOVD R5, R29
	ADD $8, R5, R10
	MOVD R10, RSP
	MOVD R5, 16(RSP)
	MOVD R5, 24(RSP)
	MOVD R0, 32(RSP)
	MOVD stackHolder_resume(R0), R30
	B ·hold(SB)

// func discard(holder *stackHolder)
TEXT ·discard(SB), NOSPLIT|NOFRAME, $0-8
	MOVD holder+0(FP), R0
	MOVD g_hi(g), R1
	MOVD stackHolder_graft(R0), R2
	SUB R2, R1
discarddrop:
	MOVD g_defer(g), R3
	CBZ R3, discarddone
	MOVD defer_sp(R3), R4
	CMP R1, R4
	BHS discarddone
	MOVD defer_link(R3), R3
	MOVD R3, g_defer(g)
	B discarddrop
discarddone:
	MOVD stackHolder_anchor(R0), R3
	MOVD R1, RSP
	B (R3)

// func restore(holder *stackHolder, base, link uintptr)
TEXT ·restore(SB), NOSPLIT|NOFRAME, $0-24
	MOVD holder+0(FP), R0
	MOVD g_hi(g), R1
	MOVD link+16(FP), R2
	SUB R2, R1, R2
	MOVD (R2), R14
	MOVD base+8(FP), R2
	SUB R2, R1
	MOVD stackHolder_count(R0), R3
	LSL $3, R3, R6
	SUB R6, R1, R5
	MOVD stackHolder_source(R0), R7
	SUB R7, R5, R9
	MOVD stackHolder_marks(R0), R12
	CBZ R9, restoredrop
	CBNZ R12, restoredrop
	RET
restoredrop:
	ADD $8, R1, R2
restoredroploop:
	MOVD g_defer(g), R7
	CBZ R7, restorecopy
	MOVD defer_sp(R7), R4
	CMP R2, R4
	BHS restorecopy
	MOVD defer_link(R7), R7
	MOVD R7, g_defer(g)
	B restoredroploop
restorecopy:
	MOVD stackHolder_image(R0), R4
	MOVD R5, R6
restoreloop:
	CBZ R3, restoredone
	MOVD.P 8(R4), R10
	CBZ R9, restorestore
	MOVBU.P 1(R12), R11
	CBZ R11, restorestore
	ADD R9, R10
restorestore:
	MOVD.P R10, 8(R6)
	SUB $1, R3
	B restoreloop
restoredone:
	MOVD stackHolder_link(R0), R10
	ADD R5, R10
	MOVD R14, (R10)
	MOVD stackHolder_defers(R0), R10
	CMN $1, R10
	BEQ restorejump
	MOVD g_defer(g), R3
	MOVD stackHolder_last(R0), R4
	ADD R5, R4
	MOVD R3, defer_link(R4)
	ADD R5, R10
	MOVD R10, g_defer(g)
restorejump:
	ADD $8, R5, R10
	MOVD R10, RSP
	MOVB ZR, 48(RSP)
	MOVD stackHolder_resume(R0), R10
	B (R10)

// func current() uintptr
TEXT ·current(SB), NOSPLIT, $0-8
	MOVD g, ret+0(FP)
	RET

// func callerRecord() (unsafe.Pointer, uintptr)
TEXT ·callerRecord(SB), NOSPLIT, $0-16
	MOVD (R29), R0
	MOVD R0, ret+0(FP)
	MOVD 8(R29), R0
	MOVD R0, ret1+8(FP)
	RET

// func deferHead() (uintptr, uintptr, uintptr, uintptr)
TEXT ·deferHead(SB), NOSPLIT, $0-32
	MOVD g_defer(g), R0
	MOVD R0, ret+0(FP)
	MOVD ZR, ret1+8(FP)
	MOVD ZR, ret2+16(FP)
	MOVD ZR, ret3+24(FP)
	CBZ R0, deferheaddone
	MOVD g_hi(g), R1
	MOVD defer_sp(R0), R2
	SUB R2, R1
	MOVD R1, ret1+8(FP)
	MOVD defer_link(R0), R1
	MOVD R1, ret2+16(FP)
	MOVHU (R0), R1
	MOVD R1, ret3+24(FP)
deferheaddone:
	RET

// func scanDefers(stack unsafe.Pointer, base uintptr) (uintptr, uintptr, bool)
TEXT ·scanDefers(SB), NOSPLIT, $0-33
	MOVD g_hi(g), R1
	MOVD base+8(FP), R2
	SUB R2, R1
	MOVD stack+0(FP), R2
	MOVD $-1, R3
	MOVD $-1, R4
	MOVD $1, R5
	MOVD g_defer(g), R6
scanloop:
	CBZ R6, scandone
	MOVD defer_sp(R6), R7
	CMP R1, R7
	BHS scandone
	MOVHU (R6), R7
	CBNZ R7, scanfail
	SUB R2, R6, R4
	CMN $1, R3
	BNE scannext
	MOVD R4, R3
scannext:
	MOVD defer_link(R6), R6
	B scanloop
scanfail:
	MOVD ZR, R5
scandone:
	MOVD R3, ret+16(FP)
	MOVD R4, ret1+24(FP)
	MOVB R5, ret2+32(FP)
	RET

// func threadWord(offset uintptr) (uintptr, uintptr)
TEXT ·threadWord(SB), NOSPLIT, $0-24
	MOVD g_m(g), R0
	MOVD R0, ret+8(FP)
	MOVD offset+0(FP), R1
	ADD R0, R1
	MOVD (R1), R1
	MOVD R1, ret1+16(FP)
	RET

// func stackOffset(pointer unsafe.Pointer) uintptr
TEXT ·stackOffset(SB), NOSPLIT, $0-16
	MOVD g_hi(g), R0
	MOVD pointer+0(FP), R1
	SUB R1, R0
	MOVD R0, ret+8(FP)
	RET

// func stackDepth() uintptr
TEXT ·stackDepth(SB), NOSPLIT, $0-8
	MOVD g_hi(g), R0
	MOVD RSP, R1
	SUB R1, R0
	MOVD R0, ret+0(FP)
	RET

// func stackFree() uintptr
TEXT ·stackFree(SB), NOSPLIT, $0-8
	MOVD RSP, R0
	MOVD g_lo(g), R1
	SUB R1, R0
	MOVD R0, ret+0(FP)
	RET
