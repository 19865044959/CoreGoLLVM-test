//go:build amd64

#include "textflag.h"

// func memchrAsm(p *byte, n int, c int64) int
// SSE2 逐 16 字节扫描，命中返回下标，未命中返回 -1。
TEXT ·memchrAsm(SB), NOSPLIT, $0-32
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	MOVQ c+16(FP), DX
	MOVD DX, X1
	PUNPCKLBW X1, X1
	PUNPCKLBW X1, X1
	PSHUFL $0, X1, X1
	MOVQ $-1, AX
	TESTQ CX, CX
	JLE done
	MOVQ SI, R8
loop16:
	CMPQ CX, $16
	JL tail
	MOVOU (SI), X0
	PCMPEQB X1, X0
	PMOVMSKB X0, DX
	TESTQ DX, DX
	JNE found
	ADDQ $16, SI
	SUBQ $16, CX
	JMP loop16
found:
	BSFL DX, DX
	ADDQ SI, DX
	SUBQ R8, DX
	MOVQ DX, ret+24(FP)
	RET
tail:
	TESTQ CX, CX
	JLE done
	MOVBQZX (SI), DX
	CMPQ DX, c+16(FP)
	JEQ tail_found
	INCQ SI
	DECQ CX
	JMP tail
tail_found:
	SUBQ R8, SI
	MOVQ SI, ret+24(FP)
	RET
done:
	MOVQ AX, ret+24(FP)
	RET
