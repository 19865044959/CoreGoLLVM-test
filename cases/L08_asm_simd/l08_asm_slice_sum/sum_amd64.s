//go:build amd64

#include "textflag.h"

// func sumI64Asm(p *int64, n int) int64
// 4 路展开的 int64 求和，尾部逐个累加。
TEXT ·sumI64Asm(SB), NOSPLIT, $0-24
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	XORQ AX, AX
	TESTQ CX, CX
	JLE done
loop4:
	CMPQ CX, $4
	JL tail
	ADDQ 0(SI), AX
	ADDQ 8(SI), AX
	ADDQ 16(SI), AX
	ADDQ 24(SI), AX
	ADDQ $32, SI
	SUBQ $4, CX
	JMP loop4
tail:
	TESTQ CX, CX
	JEQ done
	ADDQ (SI), AX
	ADDQ $8, SI
	DECQ CX
	JMP tail
done:
	MOVQ AX, ret+16(FP)
	RET
