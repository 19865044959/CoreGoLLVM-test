//go:build amd64

#include "textflag.h"

// func indexAny2Asm(p *byte, n int, b0, b1 byte) int
// 16 字节块内用两次 PCMPEQB + POR 筛出“等于 b0 或 b1”的字节，
// PMOVMSKB 后 BSFL 取最靠前命中；不足 16 字节逐字节扫描。
// 未命中返回 -1；b0==b1 语义自然退化为单字节查找。
TEXT ·indexAny2Asm(SB), NOSPLIT, $0-32
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	MOVQ $-1, AX
	MOVQ AX, ret+24(FP)
	TESTQ CX, CX
	JLE done
	MOVBQZX b0+16(FP), R10
	MOVBQZX b1+17(FP), R11
	MOVD R10, X1
	PUNPCKLBW X1, X1
	PUNPCKLBW X1, X1
	PSHUFL $0, X1, X1	// 广播 b0
	MOVD R11, X2
	PUNPCKLBW X2, X2
	PUNPCKLBW X2, X2
	PSHUFL $0, X2, X2	// 广播 b1
	XORQ R8, R8		// 已扫描字节数
block16:
	CMPQ CX, $16
	JB tail
	MOVOU (SI), X0
	MOVOU X0, X3
	PCMPEQB X1, X0
	PCMPEQB X2, X3
	POR X3, X0
	PMOVMSKB X0, DX
	TESTQ DX, DX
	JNZ hit
	ADDQ $16, SI
	ADDQ $16, R8
	SUBQ $16, CX
	JMP block16
hit:
	BSFL DX, AX
	ADDQ R8, AX
	MOVQ AX, ret+24(FP)
	RET
tail:
	TESTQ CX, CX
	JZ done
	MOVBQZX (SI), DX
	CMPQ DX, R10
	JE tail_hit
	CMPQ DX, R11
	JE tail_hit
	INCQ SI
	INCQ R8
	DECQ CX
	JMP tail
tail_hit:
	MOVQ R8, AX
	MOVQ AX, ret+24(FP)
done:
	RET
