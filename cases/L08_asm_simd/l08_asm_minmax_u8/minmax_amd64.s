//go:build amd64

#include "textflag.h"

// func minmaxU8Asm(p *byte, n int) (minV, maxV int)
// SSE2 PMINUB/PMAXUB 逐 16 字节维护最小/最大向量，尾部逐字节；
// n<=0 返回 (0,0)。
TEXT ·minmaxU8Asm(SB), NOSPLIT, $0-32
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	XORQ AX, AX
	XORQ BX, BX
	TESTQ CX, CX
	JLE done
	MOVBQZX (SI), AX
	MOVQ AX, BX
	INCQ SI
	DECQ CX
	MOVQ $-1, R8
	MOVQ R8, X1
	PUNPCKLQDQ X1, X1
	PXOR X2, X2
	XORQ R9, R9
loop16:
	CMPQ CX, $16
	JB vecreduce
	MOVOU (SI), X0
	PMINUB X0, X1
	PMAXUB X0, X2
	ADDQ $16, SI
	SUBQ $16, CX
	MOVQ $1, R9
	JMP loop16
vecreduce:
	TESTQ R9, R9
	JEQ tail
	MOVOU X1, X3
	PSRLDQ $8, X3
	PMINUB X3, X1
	MOVOU X1, X3
	PSRLDQ $4, X3
	PMINUB X3, X1
	MOVOU X1, X3
	PSRLDQ $2, X3
	PMINUB X3, X1
	MOVOU X1, X3
	PSRLDQ $1, X3
	PMINUB X3, X1
	MOVD X1, R8
	ANDQ $0xff, R8
	CMPQ R8, AX
	JGE min_ok
	MOVQ R8, AX
min_ok:
	MOVOU X2, X3
	PSRLDQ $8, X3
	PMAXUB X3, X2
	MOVOU X2, X3
	PSRLDQ $4, X3
	PMAXUB X3, X2
	MOVOU X2, X3
	PSRLDQ $2, X3
	PMAXUB X3, X2
	MOVOU X2, X3
	PSRLDQ $1, X3
	PMAXUB X3, X2
	MOVD X2, R10
	ANDQ $0xff, R10
	CMPQ R10, BX
	JLE max_ok
	MOVQ R10, BX
max_ok:
tail:
	TESTQ CX, CX
	JEQ done
	MOVBQZX (SI), DX
	CMPQ DX, AX
	JGE tail_max
	MOVQ DX, AX
tail_max:
	CMPQ DX, BX
	JLE tail_next
	MOVQ DX, BX
tail_next:
	INCQ SI
	DECQ CX
	JMP tail
done:
	MOVQ AX, minV+16(FP)
	MOVQ BX, maxV+24(FP)
	RET
