//go:build amd64

#include "textflag.h"

// func bitrevAsm(x uint64) uint64
// SWAR 反转：先交换相邻 1 位，再 2 位、4 位（字节内位序反转），
// 最后 BSWAPQ 反转字节序，得到完整的 64 位位反转。
TEXT ·bitrevAsm(SB), NOSPLIT, $0-16
	MOVQ x+0(FP), AX
	// 1-bit swaps
	MOVQ AX, BX
	SHRQ $1, BX
	MOVQ $0x5555555555555555, DX
	ANDQ DX, BX
	ANDQ DX, AX
	SHLQ $1, AX
	ORQ BX, AX
	// 2-bit swaps
	MOVQ AX, BX
	SHRQ $2, BX
	MOVQ $0x3333333333333333, DX
	ANDQ DX, BX
	ANDQ DX, AX
	SHLQ $2, AX
	ORQ BX, AX
	// 4-bit swaps
	MOVQ AX, BX
	SHRQ $4, BX
	MOVQ $0x0f0f0f0f0f0f0f0f, DX
	ANDQ DX, BX
	ANDQ DX, AX
	SHLQ $4, AX
	ORQ BX, AX
	// byte order
	BSWAPQ AX
	MOVQ AX, ret+8(FP)
	RET
