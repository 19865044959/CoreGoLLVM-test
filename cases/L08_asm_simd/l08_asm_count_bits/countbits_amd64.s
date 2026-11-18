//go:build amd64

#include "textflag.h"

// func countBitsAsm(p *uint64, n int) int64
// 逐字用 BSFQ 定位最低置位，x&(x-1) 清除后继续，统计总置位数。
TEXT ·countBitsAsm(SB), NOSPLIT, $0-24
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	XORQ AX, AX
	TESTQ CX, CX
	JLE done
loop:
	MOVQ (SI), DX
	TESTQ DX, DX
	JEQ next
pop:
	BSFQ DX, R8
	LEAQ -1(DX), R9
	ANDQ R9, DX
	INCQ AX
	TESTQ DX, DX
	JNE pop
next:
	ADDQ $8, SI
	DECQ CX
	JNE loop
done:
	MOVQ AX, ret+16(FP)
	RET
