//go:build amd64

#include "textflag.h"

// func copyAsm(dst, src *byte, n int) int
// 正向拷贝，16 字节 SIMD + 8 字节 + 单字节尾部；返回拷贝字节数。
// 不支持重叠区间；dst==src 时安全。
TEXT ·copyAsm(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	MOVQ CX, ret+24(FP)
loop16:
	CMPQ CX, $16
	JB tail8
	MOVOU (SI), X0
	MOVOU X0, (DI)
	ADDQ $16, SI
	ADDQ $16, DI
	SUBQ $16, CX
	JMP loop16
tail8:
	CMPQ CX, $8
	JB tail1
	MOVQ (SI), DX
	MOVQ DX, (DI)
	ADDQ $8, SI
	ADDQ $8, DI
	SUBQ $8, CX
tail1:
	TESTQ CX, CX
	JEQ done
	MOVBQZX (SI), DX
	MOVB DX, (DI)
	INCQ SI
	INCQ DI
	DECQ CX
	JMP tail1
done:
	RET
