//go:build amd64

#include "textflag.h"

// func bswap64Asm(x uint64) uint64
TEXT ·bswap64Asm(SB), NOSPLIT, $0-16
	MOVQ x+0(FP), AX
	BSWAPQ AX
	MOVQ AX, ret+8(FP)
	RET

// func bswap32Asm(x uint64) uint64
// 只交换低 32 位；ABI0 参数/返回值统一用 8 字节槽，Go 侧负责窄化。
TEXT ·bswap32Asm(SB), NOSPLIT, $0-16
	MOVQ x+0(FP), AX
	BSWAPL AX
	MOVQ AX, ret+8(FP)
	RET

// func bswap16Asm(x uint64) uint64
// 只交换低 16 位；ABI0 参数/返回值统一用 8 字节槽，Go 侧负责窄化。
TEXT ·bswap16Asm(SB), NOSPLIT, $0-16
	MOVQ x+0(FP), AX
	ROLW $8, AX
	MOVQ AX, ret+8(FP)
	RET

// func bswapBufAsm(p *uint64, n int)
// 原地逐字字节交换。
TEXT ·bswapBufAsm(SB), NOSPLIT, $0-16
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	TESTQ CX, CX
	JLE done
loop:
	MOVQ (SI), AX
	BSWAPQ AX
	MOVQ AX, (SI)
	ADDQ $8, SI
	DECQ CX
	JNE loop
done:
	RET
