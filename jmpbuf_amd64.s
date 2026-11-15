//go:build amd64

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
	MOVQ buf+0(FP), AX
	MOVQ AX, 0(SP)
	LEAQ -8(SP), AX
	MOVQ AX, 8(SP)
	MOVQ BP, 16(SP)
	MOVQ $0, 24(SP)
	MOVQ $0, 32(SP)
	CALL ·capture(SB)
	MOVBLZX 40(SP), AX
	MOVB AX, ret+8(FP)
	RET

// func longjmp(buf *JMPBuf) error
TEXT ·longjmp(SB), $32-24
	NO_LOCAL_POINTERS
	MOVQ buf+0(FP), AX
	MOVQ AX, 0(SP)
	MOVQ BP, 8(SP)
	CALL ·launch(SB)
	MOVQ 16(SP), AX
	MOVQ AX, ret_itable+8(FP)
	MOVQ 24(SP), AX
	MOVQ AX, ret_data+16(FP)
	RET

// func snapshot(stack, frame unsafe.Pointer, image *uintptr, count uintptr) (uintptr, uintptr)
TEXT ·snapshot(SB), NOSPLIT, $0-48
	MOVQ stack+0(FP), SI
	MOVQ frame+8(FP), AX
	MOVQ image+16(FP), DI
	MOVQ count+24(FP), CX
	MOVQ SI, ret+32(FP)
	MOVQ AX, ret1+40(FP)
	CLD
	REP; MOVSQ
	RET

// func anchor(holder *stackHolder)
TEXT ·anchor(SB), $512-8
	NO_LOCAL_POINTERS
	MOVQ holder+0(FP), DX
	MOVQ SP, DI
	XORQ AX, AX
	MOVQ stackHolder_args(DX), CX
	ADDQ $7, CX
	SHRQ $3, CX
	CLD
	REP; STOSQ
	CALL graft<>(SB)
	RET

// NOTE: Copies the image below the anchor frame and continues in hold() as its callee.
TEXT graft<>(SB), NOSPLIT|NOFRAME, $0-0
	MOVQ BP, -8(SP)
	LEAQ -8(SP), BP
	MOVQ (TLS), AX
	MOVQ g_hi(AX), AX
	SUBQ BP, AX
	MOVQ AX, stackHolder_graft(DX)
	MOVQ stackHolder_count(DX), CX
	MOVQ stackHolder_image(DX), SI
	MOVQ CX, BX
	SHLQ $3, BX
	MOVQ BP, DI
	SUBQ BX, DI
	MOVQ DI, R8
	MOVQ stackHolder_source(DX), R9
	MOVQ stackHolder_span(DX), R10
	MOVQ DI, R11
	SUBQ R9, R11
graftloop:
	TESTQ CX, CX
	JEQ graftdone
	MOVQ (SI), AX
	MOVQ AX, BX
	SUBQ R9, BX
	CMPQ BX, R10
	JCC graftstore
	ADDQ R11, AX
graftstore:
	MOVQ AX, (DI)
	ADDQ $8, SI
	ADDQ $8, DI
	DECQ CX
	JMP graftloop
graftdone:
	MOVQ stackHolder_defers(DX), AX
	CMPQ AX, $-1
	JEQ graftjump
	ADDQ R8, AX
	MOVQ (TLS), BX
	MOVQ g_defer(BX), CX
	MOVQ AX, g_defer(BX)
	MOVQ stackHolder_last(DX), AX
	MOVQ CX, defer_link(R8)(AX*1)
graftjump:
	MOVQ stackHolder_frame(DX), BP
	ADDQ R11, BP
	MOVQ R8, SP
	MOVQ R8, 16(SP)
	MOVQ BP, 24(SP)
	MOVQ DX, 32(SP)
	JMP ·hold(SB)

// func discard(holder *stackHolder)
TEXT ·discard(SB), NOSPLIT|NOFRAME, $0-8
	MOVQ holder+0(FP), DX
	MOVQ (TLS), BX
	MOVQ g_hi(BX), AX
	SUBQ stackHolder_graft(DX), AX
	LEAQ 16(AX), CX
discarddrop:
	MOVQ g_defer(BX), SI
	TESTQ SI, SI
	JEQ discarddone
	CMPQ defer_sp(SI), CX
	JCC discarddone
	MOVQ defer_link(SI), SI
	MOVQ SI, g_defer(BX)
	JMP discarddrop
discarddone:
	LEAQ 8(AX), SP
	RET

// func restore(holder *stackHolder, base, link uintptr)
TEXT ·restore(SB), NOSPLIT|NOFRAME, $0-24
	MOVQ holder+0(FP), DX
	MOVQ (TLS), BX
	MOVQ g_hi(BX), AX
	SUBQ base+8(FP), AX
	MOVQ stackHolder_count(DX), CX
	MOVQ CX, DI
	SHLQ $3, DI
	NEGQ DI
	ADDQ AX, DI
	MOVQ DI, R8
	MOVQ DI, R11
	SUBQ stackHolder_source(DX), R11
	MOVQ stackHolder_marks(DX), R12
	TESTQ R11, R11
	JEQ restoredrop
	TESTQ R12, R12
	JNE restoredrop
	RET
restoredrop:
	LEAQ 16(AX), SI
restoredroploop:
	MOVQ g_defer(BX), AX
	TESTQ AX, AX
	JEQ restorecopy
	CMPQ defer_sp(AX), SI
	JCC restorecopy
	MOVQ defer_link(AX), AX
	MOVQ AX, g_defer(BX)
	JMP restoredroploop
restorecopy:
	MOVQ stackHolder_image(DX), SI
	TESTQ R11, R11
	JNE restoreloop
	CLD
	REP; MOVSQ
	JMP restoredone
restoreloop:
	TESTQ CX, CX
	JEQ restoredone
	MOVQ (SI), AX
	CMPB (R12), $0
	JEQ restorestore
	ADDQ R11, AX
restorestore:
	MOVQ AX, (DI)
	ADDQ $8, SI
	ADDQ $8, DI
	INCQ R12
	DECQ CX
	JMP restoreloop
restoredone:
	MOVQ stackHolder_defers(DX), AX
	CMPQ AX, $-1
	JEQ restorejump
	MOVQ g_defer(BX), CX
	MOVQ stackHolder_last(DX), SI
	MOVQ CX, defer_link(R8)(SI*1)
	ADDQ R8, AX
	MOVQ AX, g_defer(BX)
restorejump:
	MOVQ R8, SP
	MOVB $0, 48(SP)
	RET

// func current() uintptr
TEXT ·current(SB), NOSPLIT, $0-8
	MOVQ (TLS), AX
	MOVQ AX, ret+0(FP)
	RET

// func deferHead() (uintptr, uintptr, uintptr, uintptr)
TEXT ·deferHead(SB), NOSPLIT, $0-32
	MOVQ (TLS), AX
	MOVQ g_defer(AX), BX
	MOVQ BX, ret+0(FP)
	MOVQ $0, ret1+8(FP)
	MOVQ $0, ret2+16(FP)
	MOVQ $0, ret3+24(FP)
	TESTQ BX, BX
	JEQ deferheaddone
	MOVQ g_hi(AX), CX
	SUBQ defer_sp(BX), CX
	MOVQ CX, ret1+8(FP)
	MOVQ defer_link(BX), CX
	MOVQ CX, ret2+16(FP)
	MOVWLZX (BX), CX
	MOVQ CX, ret3+24(FP)
deferheaddone:
	RET

// func scanDefers(stack unsafe.Pointer, base uintptr) (uintptr, uintptr, bool)
TEXT ·scanDefers(SB), NOSPLIT, $0-33
	MOVQ (TLS), AX
	MOVQ g_hi(AX), BX
	SUBQ base+8(FP), BX
	MOVQ stack+0(FP), DX
	MOVQ $-1, ret+16(FP)
	MOVQ $-1, ret1+24(FP)
	MOVB $1, ret2+32(FP)
	MOVQ g_defer(AX), CX
scanloop:
	TESTQ CX, CX
	JEQ scandone
	CMPQ defer_sp(CX), BX
	JCC scandone
	CMPW (CX), $0
	JNE scanfail
	MOVQ CX, SI
	SUBQ DX, SI
	CMPQ ret+16(FP), $-1
	JNE scanlast
	MOVQ SI, ret+16(FP)
scanlast:
	MOVQ SI, ret1+24(FP)
	MOVQ defer_link(CX), CX
	JMP scanloop
scanfail:
	MOVB $0, ret2+32(FP)
scandone:
	RET

// func threadWord(offset uintptr) (uintptr, uintptr)
TEXT ·threadWord(SB), NOSPLIT, $0-24
	MOVQ (TLS), AX
	MOVQ g_m(AX), AX
	MOVQ AX, ret+8(FP)
	MOVQ offset+0(FP), BX
	MOVQ (AX)(BX*1), BX
	MOVQ BX, ret1+16(FP)
	RET

// func stackOffset(pointer unsafe.Pointer) uintptr
TEXT ·stackOffset(SB), NOSPLIT, $0-16
	MOVQ (TLS), AX
	MOVQ g_hi(AX), AX
	SUBQ pointer+0(FP), AX
	MOVQ AX, ret+8(FP)
	RET

// func stackDepth() uintptr
TEXT ·stackDepth(SB), NOSPLIT, $0-8
	MOVQ (TLS), AX
	MOVQ g_hi(AX), AX
	SUBQ SP, AX
	MOVQ AX, ret+0(FP)
	RET

// func stackFree() uintptr
TEXT ·stackFree(SB), NOSPLIT, $0-8
	MOVQ (TLS), AX
	MOVQ SP, BX
	SUBQ g_lo(AX), BX
	MOVQ BX, ret+0(FP)
	RET
