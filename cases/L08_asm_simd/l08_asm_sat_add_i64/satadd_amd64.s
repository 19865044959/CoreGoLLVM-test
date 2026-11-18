//go:build amd64

#include "textflag.h"

// func satAddAsm(a, b int64) int64
// 有符号饱和加法：先算回绕和，再用 (a^sum)&(b^sum) 的符号位判溢出；
// 溢出时按 a 的符号选 MaxInt64 / MinInt64。
TEXT ·satAddAsm(SB), NOSPLIT, $0-24
	MOVQ a+0(FP), AX
	MOVQ b+8(FP), CX
	MOVQ AX, DX
	ADDQ CX, DX		// sum（可能回绕）
	MOVQ AX, R8
	XORQ DX, R8
	MOVQ CX, R9
	XORQ DX, R9
	ANDQ R9, R8
	TESTQ R8, R8
	JNS ok
	TESTQ AX, AX
	JS neg
	MOVQ $0x7fffffffffffffff, DX
	JMP ok
neg:
	MOVQ $0x8000000000000000, DX
ok:
	MOVQ DX, ret+16(FP)
	RET
