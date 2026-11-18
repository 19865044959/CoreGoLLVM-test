//go:build amd64

#include "textflag.h"

// func memrchrAsm(p *byte, n int, c int64) int
// 从尾部按 16 字节块倒扫，BSRL 定位块内最后一个命中；尾部逐字节倒扫。
// c 只在 0..255 范围内使用；未命中返回 -1。
TEXT ·memrchrAsm(SB), NOSPLIT, $0-32
	MOVQ p+0(FP), R9
	MOVQ n+8(FP), CX
	MOVQ $-1, AX
	MOVQ AX, ret+24(FP)
	TESTQ CX, CX
	JLE done
	MOVQ R9, SI
	ADDQ CX, SI		// SI = p+n，块尾
	MOVQ c+16(FP), DX
	MOVD DX, X1
	PUNPCKLBW X1, X1
	PUNPCKLBW X1, X1
	PSHUFL $0, X1, X1
block16:
	CMPQ CX, $16
	JB tail
	SUBQ $16, SI
	SUBQ $16, CX
	MOVOU (SI), X0
	PCMPEQB X1, X0
	PMOVMSKB X0, DX
	TESTQ DX, DX
	JZ block16
	BSRL DX, AX
	ADDQ SI, AX
	SUBQ R9, AX
	MOVQ AX, ret+24(FP)
	RET
tail:
	TESTQ CX, CX
	JZ done
	DECQ SI
	MOVBQZX (SI), AX
	CMPQ AX, c+16(FP)
	JE found_tail
	DECQ CX
	JMP tail
found_tail:
	SUBQ R9, SI
	MOVQ SI, ret+24(FP)
done:
	RET
