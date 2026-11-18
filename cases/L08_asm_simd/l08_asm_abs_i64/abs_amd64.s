//go:build amd64

#include "textflag.h"

// func absI64Asm(x int64) int64
// NEGQ 求补后按符号 CMOV 选择；MinInt64 取负仍为 MinInt64（二补码回绕）。
TEXT ·absI64Asm(SB), NOSPLIT, $0-16
	MOVQ x+0(FP), AX
	MOVQ AX, DX
	NEGQ DX
	CMPQ AX, $0
	CMOVQLT DX, AX
	MOVQ AX, ret+8(FP)
	RET
