//go:build amd64

#include "textflag.h"

// func countZeroAsm(p *byte, n int) int
// SSE2 统计零字节：PCMPEQB 得掩码，x&(x-1) 逐位清零计数；尾部标量。
TEXT ·countZeroAsm(SB), NOSPLIT, $0-24
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	XORQ AX, AX
	PXOR X1, X1
	TESTQ CX, CX
	JLE done
loop16:
	CMPQ CX, $16
	JB tail
	MOVOU (SI), X0
	PCMPEQB X1, X0
	PMOVMSKB X0, DX
	TESTQ DX, DX
	JEQ next
pop:
	LEAQ -1(DX), R9
	ANDQ R9, DX
	INCQ AX
	TESTQ DX, DX
	JNE pop
next:
	ADDQ $16, SI
	SUBQ $16, CX
	JMP loop16
tail:
	TESTQ CX, CX
	JEQ done
	MOVBQZX (SI), DX
	TESTQ DX, DX
	JNE tail_next
	INCQ AX
tail_next:
	INCQ SI
	DECQ CX
	JMP tail
done:
	MOVQ AX, ret+16(FP)
	RET
