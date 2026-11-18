//go:build amd64

#include "textflag.h"

// func hashAsm(p *byte, n int) uint64
// FNV-1a 64: h = offset; for each byte: h ^= b; h *= prime
TEXT ·hashAsm(SB), NOSPLIT, $0-24
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	MOVQ $14695981039346656037, AX
	MOVQ $1099511628211, R8
loop:
	TESTQ CX, CX
	JE done
	MOVBQZX (SI), DX
	XORQ DX, AX
	IMULQ R8, AX
	INCQ SI
	DECQ CX
	JMP loop
done:
	MOVQ AX, ret+16(FP)
	RET
