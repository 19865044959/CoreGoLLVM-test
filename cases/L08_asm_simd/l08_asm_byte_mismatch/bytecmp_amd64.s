//go:build amd64

#include "textflag.h"

// func mismatchAsm(a, b *byte, n int) int
// SSE2 逐 16 字节比较（PCMPEQB + PMOVMSKB），统计不相等字节数；
// 尾部逐字节比较。返回差异字节个数。
TEXT ·mismatchAsm(SB), NOSPLIT, $0-32
	MOVQ a+0(FP), SI
	MOVQ b+8(FP), DI
	MOVQ n+16(FP), CX
	XORQ AX, AX
	TESTQ CX, CX
	JLE done
loop16:
	CMPQ CX, $16
	JB tail
	MOVOU (SI), X0
	MOVOU (DI), X1
	PCMPEQB X1, X0
	PMOVMSKB X0, DX
	XORQ $0xFFFF, DX
popcheck:
	TESTQ DX, DX
	JEQ next
	LEAQ -1(DX), R8
	ANDQ R8, DX
	INCQ AX
	JMP popcheck
next:
	ADDQ $16, SI
	ADDQ $16, DI
	SUBQ $16, CX
	JMP loop16
tail:
	TESTQ CX, CX
	JLE done
	MOVBQZX (SI), DX
	MOVBQZX (DI), R8
	CMPQ DX, R8
	JEQ tail_next
	INCQ AX
tail_next:
	INCQ SI
	INCQ DI
	DECQ CX
	JMP tail
done:
	MOVQ AX, ret+24(FP)
	RET
