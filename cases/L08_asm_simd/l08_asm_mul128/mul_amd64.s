//go:build amd64

#include "textflag.h"

// func mul128Asm(a, b uint64) (hi, lo uint64)
// 无符号 64x64→128：RDX:RAX。
TEXT ·mul128Asm(SB), NOSPLIT, $0-32
	MOVQ a+0(FP), AX
	MOVQ b+8(FP), BX
	MULQ BX
	MOVQ DX, hi+16(FP)
	MOVQ AX, lo+24(FP)
	RET

// func imulAsm(a, b int64) int64
TEXT ·imulAsm(SB), NOSPLIT, $0-24
	MOVQ a+0(FP), AX
	MOVQ b+8(FP), BX
	IMULQ BX, AX
	MOVQ AX, ret+16(FP)
	RET
