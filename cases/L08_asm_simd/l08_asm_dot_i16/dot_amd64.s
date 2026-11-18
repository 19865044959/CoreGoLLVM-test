//go:build amd64

#include "textflag.h"

// func dotI16Asm(a, b *int16, n int) int64
// 每轮 8 个 int16：PMADDWD 得到 4 个 int32，逐个符号扩展到 64 位累加。
TEXT ·dotI16Asm(SB), NOSPLIT, $0-32
	MOVQ a+0(FP), SI
	MOVQ b+8(FP), DI
	MOVQ n+16(FP), CX
	XORQ R9, R9
	TESTQ CX, CX
	JLE done
loop8:
	CMPQ CX, $8
	JB tail
	MOVOU (SI), X0
	MOVOU (DI), X1
	PMADDWL X1, X0
	MOVL X0, AX
	MOVLQSX AX, AX
	ADDQ AX, R9
	PSHUFD $0x39, X0, X0
	MOVL X0, AX
	MOVLQSX AX, AX
	ADDQ AX, R9
	PSHUFD $0x39, X0, X0
	MOVL X0, AX
	MOVLQSX AX, AX
	ADDQ AX, R9
	PSHUFD $0x39, X0, X0
	MOVL X0, AX
	MOVLQSX AX, AX
	ADDQ AX, R9
	ADDQ $16, SI
	ADDQ $16, DI
	SUBQ $8, CX
	JMP loop8
tail:
	TESTQ CX, CX
	JE done
	MOVWQZX (SI), AX
	MOVWQSX AX, AX
	MOVWQZX (DI), DX
	MOVWQSX DX, DX
	IMULQ DX, AX
	ADDQ AX, R9
	ADDQ $2, SI
	ADDQ $2, DI
	DECQ CX
	JMP tail
done:
	MOVQ R9, ret+24(FP)
	RET
