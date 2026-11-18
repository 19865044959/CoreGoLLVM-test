//go:build amd64

#include "textflag.h"

// func memeqAsm(a, b *byte, n int) bool
// 16 字节 SIMD 比较 + 标量尾部；返回 1 相等，0 不等。
TEXT ·memeqAsm(SB), NOSPLIT, $0-25
	MOVB $0, ret+24(FP)
	MOVQ a+0(FP), SI
	MOVQ b+8(FP), DI
	MOVQ n+16(FP), CX
	TESTQ CX, CX
	JEQ eq
loop16:
	CMPQ CX, $16
	JB tail
	MOVOU (SI), X0
	MOVOU (DI), X1
	PCMPEQB X1, X0
	PMOVMSKB X0, DX
	CMPL DX, $0xffff
	JNE ne
	ADDQ $16, SI
	ADDQ $16, DI
	SUBQ $16, CX
	JMP loop16
tail:
	TESTQ CX, CX
	JEQ eq
	MOVBQZX (SI), DX
	MOVBQZX (DI), AX
	CMPQ DX, AX
	JNE ne
	INCQ SI
	INCQ DI
	DECQ CX
	JMP tail
eq:
	MOVB $1, ret+24(FP)
ne:
	RET
