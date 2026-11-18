//go:build amd64

#include "textflag.h"

// func firstNonzeroAsm(p *byte, n int) int
// 16 字节块：与零比较 → PMOVMSKB → 取反 → BSFL 得到块内首个非零。
TEXT ·firstNonzeroAsm(SB), NOSPLIT, $0-24
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	MOVQ $-1, ret+16(FP)
	TESTQ CX, CX
	JLE done
	PXOR X2, X2
	XORQ R8, R8
loop16:
	CMPQ CX, $16
	JB tail
	MOVOU (SI), X1
	PCMPEQB X2, X1
	PMOVMSKB X1, R9
	XORL $0xffff, R9
	JNZ found16
	ADDQ $16, SI
	ADDQ $16, R8
	SUBQ $16, CX
	JMP loop16
found16:
	BSFL R9, R9
	ADDQ R8, R9
	MOVQ R9, ret+16(FP)
	RET
tail:
	TESTQ CX, CX
	JE done
	CMPB (SI), $0
	JNE foundtail
	INCQ SI
	INCQ R8
	DECQ CX
	JMP tail
foundtail:
	MOVQ R8, ret+16(FP)
done:
	RET
