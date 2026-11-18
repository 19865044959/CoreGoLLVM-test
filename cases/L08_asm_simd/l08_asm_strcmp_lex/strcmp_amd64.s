//go:build amd64

#include "textflag.h"

// func strcmpAsm(a, b *byte, na, nb int) int
// 有界字典序比较：按无符号字节比较，公共前缀结束后长度短者更小。
TEXT ·strcmpAsm(SB), NOSPLIT, $0-40
	MOVQ a+0(FP), SI
	MOVQ b+8(FP), DI
	MOVQ na+16(FP), CX
	MOVQ nb+24(FP), DX
	MOVQ $0, ret+32(FP)
	XORQ R8, R8
loop:
	CMPQ R8, CX
	JGE aEnd
	CMPQ R8, DX
	JGE greater
	MOVBQZX (SI)(R8*1), AX
	MOVBQZX (DI)(R8*1), BX
	CMPQ AX, BX
	JLT less
	JGT greater
	INCQ R8
	JMP loop
aEnd:
	CMPQ R8, DX
	JGE equal
less:
	MOVQ $-1, ret+32(FP)
	RET
greater:
	MOVQ $1, ret+32(FP)
	RET
equal:
	RET
