//go:build amd64

#include "textflag.h"

TEXT ·addAsm(SB), NOSPLIT, $0-24
	MOVQ a+0(FP), AX
	MOVQ b+8(FP), BX
	ADDQ BX, AX
	MOVQ AX, ret+16(FP)
	RET

TEXT ·keepPtr(SB), NOSPLIT, $0-16
	MOVQ p+0(FP), AX
	MOVQ AX, ret+8(FP)
	RET

// readV 读取 node.v（结构体首字段，int64）。
TEXT ·readV(SB), NOSPLIT, $0-16
	MOVQ p+0(FP), AX
	MOVQ (AX), AX
	MOVQ AX, ret+8(FP)
	RET
