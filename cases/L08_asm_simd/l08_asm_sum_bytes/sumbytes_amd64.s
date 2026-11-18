//go:build amd64

#include "textflag.h"

// func sumBytesAsm(p *byte, n int) int64
// SSE2 逐 16 字节用 PSADBW 求两组 8 字节和，PADDQ 分别累加到两个
// 64 位通道；尾部逐字节累加；最后水平相加两个通道。
TEXT ·sumBytesAsm(SB), NOSPLIT, $0-24
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	XORQ AX, AX
	PXOR X1, X1
	PXOR X2, X2
	TESTQ CX, CX
	JLE done
loop16:
	CMPQ CX, $16
	JB tail
	MOVOU (SI), X0
	PSADBW X1, X0
	PADDQ X0, X2
	ADDQ $16, SI
	SUBQ $16, CX
	JMP loop16
tail:
	TESTQ CX, CX
	JEQ reduce
	MOVBQZX (SI), DX
	ADDQ DX, AX
	INCQ SI
	DECQ CX
	JMP tail
reduce:
	MOVQ X2, DX
	ADDQ DX, AX
	PSRLDQ $8, X2
	MOVQ X2, DX
	ADDQ DX, AX
done:
	MOVQ AX, ret+16(FP)
	RET
