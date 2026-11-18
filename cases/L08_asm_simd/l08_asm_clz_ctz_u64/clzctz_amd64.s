//go:build amd64

#include "textflag.h"

// func clzCtzAsm(x uint64) (clz, ctz int)
// 零输入返回 (64,64)；非零输入 clz=63-BSR(x)，ctz=BSF(x)。
TEXT ·clzCtzAsm(SB), NOSPLIT, $0-24
	MOVQ x+0(FP), AX
	MOVQ $64, CX
	MOVQ $64, DX
	TESTQ AX, AX
	JZ store
	BSRQ AX, BX
	MOVQ $63, CX
	SUBQ BX, CX
	BSFQ AX, DX
store:
	MOVQ CX, clz+8(FP)
	MOVQ DX, ctz+16(FP)
	RET
