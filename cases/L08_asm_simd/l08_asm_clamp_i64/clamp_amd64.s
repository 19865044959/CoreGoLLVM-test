//go:build amd64

#include "textflag.h"

// func clampAsm(v, lo, hi int64) int64
// 先用 CMOVQLT 抬到 lo，再用 CMOVQGT 压到 hi。
TEXT ·clampAsm(SB), NOSPLIT, $0-32
	MOVQ v+0(FP), AX
	MOVQ lo+8(FP), BX
	MOVQ hi+16(FP), CX
	CMPQ AX, BX
	CMOVQLT BX, AX
	CMPQ AX, CX
	CMOVQGT CX, AX
	MOVQ AX, ret+24(FP)
	RET
