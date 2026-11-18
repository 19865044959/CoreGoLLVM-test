//go:build amd64

#include "textflag.h"

// func clzAsm(x uint64) int
// BSRQ 取最高置位下标，前导零数 = 63 - index；x==0 时返回 64。
TEXT ·clzAsm(SB), NOSPLIT, $0-16
	MOVQ x+0(FP), AX
	XORQ DX, DX
	TESTQ AX, AX
	JZ zero
	BSRQ AX, DX
	XORQ $63, DX		// 63 - index
	JMP done
zero:
	MOVQ $64, DX
done:
	MOVQ DX, ret+8(FP)
	RET
