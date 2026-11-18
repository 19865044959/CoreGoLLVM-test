//go:build amd64

#include "textflag.h"

// func memsetAsm(p *byte, n int, v byte)
// 单字节广播为 8 字节重复字，8 字节步进，尾部逐字节。
TEXT ·memsetAsm(SB), NOSPLIT, $0-17
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	MOVBQZX v+16(FP), AX
	MOVQ $0x0101010101010101, DX
	IMULQ DX, AX
loop8:
	CMPQ CX, $8
	JB tail
	MOVQ AX, (SI)
	ADDQ $8, SI
	SUBQ $8, CX
	JMP loop8
tail:
	TESTQ CX, CX
	JE done
	MOVB AL, (SI)
	INCQ SI
	DECQ CX
	JMP tail
done:
	RET
