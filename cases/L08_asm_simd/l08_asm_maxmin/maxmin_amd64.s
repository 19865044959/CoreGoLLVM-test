//go:build amd64

#include "textflag.h"

// func maxI64Asm(p *int64, n int) int64
// n<=0 返回 0；否则返回切片最大值。
TEXT ·maxI64Asm(SB), NOSPLIT, $0-24
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	XORQ AX, AX
	TESTQ CX, CX
	JLE done
	MOVQ (SI), AX
	DECQ CX
	JLE done
	ADDQ $8, SI
loop:
	CMPQ (SI), AX
	JLE skip
	MOVQ (SI), AX
skip:
	ADDQ $8, SI
	DECQ CX
	JNE loop
done:
	MOVQ AX, ret+16(FP)
	RET

// func minI64Asm(p *int64, n int) int64
TEXT ·minI64Asm(SB), NOSPLIT, $0-24
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	XORQ AX, AX
	TESTQ CX, CX
	JLE done
	MOVQ (SI), AX
	DECQ CX
	JLE done
	ADDQ $8, SI
loop:
	CMPQ (SI), AX
	JGE skip
	MOVQ (SI), AX
skip:
	ADDQ $8, SI
	DECQ CX
	JNE loop
done:
	MOVQ AX, ret+16(FP)
	RET
