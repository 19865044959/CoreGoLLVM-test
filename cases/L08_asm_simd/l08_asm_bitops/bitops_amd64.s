//go:build amd64

#include "textflag.h"

// func popcountAsm(x uint64) int
// SWAR popcount，不依赖 POPCNT 指令集扩展。
TEXT ·popcountAsm(SB), NOSPLIT, $0-16
	MOVQ x+0(FP), AX
	MOVQ AX, CX
	SHRQ $1, CX
	MOVQ $0x5555555555555555, DX
	ANDQ DX, CX
	SUBQ CX, AX
	MOVQ $0x3333333333333333, DX
	MOVQ AX, CX
	ANDQ DX, CX
	SHRQ $2, AX
	ANDQ DX, AX
	ADDQ CX, AX
	MOVQ AX, CX
	SHRQ $4, CX
	ADDQ CX, AX
	MOVQ $0x0f0f0f0f0f0f0f0f, DX
	ANDQ DX, AX
	MOVQ $0x0101010101010101, DX
	IMULQ DX, AX
	SHRQ $56, AX
	MOVQ AX, ret+8(FP)
	RET

// func rotlAsm(x uint64, k uint) uint64
// x86 ROL 自动按 64 取模，与 bits.RotateLeft64 语义一致。
TEXT ·rotlAsm(SB), NOSPLIT, $0-24
	MOVQ x+0(FP), AX
	MOVQ k+8(FP), CX
	ROLQ CX, AX
	MOVQ AX, ret+16(FP)
	RET
