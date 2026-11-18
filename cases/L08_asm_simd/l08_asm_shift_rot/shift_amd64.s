//go:build amd64

#include "textflag.h"

// func shlAsm(x uint64, k uint) uint64
// x86 SHL 只取计数低 6 位，与测试中的 k&63 对齐。
TEXT ·shlAsm(SB), NOSPLIT, $0-24
	MOVQ x+0(FP), AX
	MOVQ k+8(FP), CX
	SHLQ CX, AX
	MOVQ AX, ret+16(FP)
	RET

// func sarAsm(x int64, k uint) int64
TEXT ·sarAsm(SB), NOSPLIT, $0-24
	MOVQ x+0(FP), AX
	MOVQ k+8(FP), CX
	SARQ CX, AX
	MOVQ AX, ret+16(FP)
	RET

// func ror32Asm(x uint32, k uint) uint32
TEXT ·ror32Asm(SB), NOSPLIT, $0-20
	MOVL x+0(FP), AX
	MOVL k+8(FP), CX
	RORL CX, AX
	MOVL AX, ret+16(FP)
	RET
