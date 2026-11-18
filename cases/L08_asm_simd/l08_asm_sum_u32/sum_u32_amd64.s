//go:build amd64

#include "textflag.h"

// func sumU32Asm(p *uint32, n int) uint64
// 4 路展开 + 64 位累加，尾部逐个补足。
TEXT ·sumU32Asm(SB), NOSPLIT, $0-24
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	XORQ AX, AX
	TESTQ CX, CX
	JLE done
loop4:
	CMPQ CX, $4
	JL tail
	MOVL 0(SI), DX
	ADDQ DX, AX
	MOVL 4(SI), DX
	ADDQ DX, AX
	MOVL 8(SI), DX
	ADDQ DX, AX
	MOVL 12(SI), DX
	ADDQ DX, AX
	ADDQ $16, SI
	SUBQ $4, CX
	JMP loop4
tail:
	TESTQ CX, CX
	JE done
	MOVL (SI), DX
	ADDQ DX, AX
	ADDQ $4, SI
	DECQ CX
	JMP tail
done:
	MOVQ AX, ret+16(FP)
	RET
