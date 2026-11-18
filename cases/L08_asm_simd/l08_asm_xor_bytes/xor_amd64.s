//go:build amd64

#include "textflag.h"

// func xorAsm(dst, src *byte, n int)
// dst[0:n] ^= src[0:n]；16 字节 PXOR 主循环，尾部逐字节。
// 允许 dst 与 src 指向同一缓冲区（此时结果全零）。
TEXT ·xorAsm(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	TESTQ CX, CX
	JLE done
loop16:
	CMPQ CX, $16
	JB tail
	MOVOU (DI), X0
	MOVOU (SI), X1
	PXOR X1, X0
	MOVOU X0, (DI)
	ADDQ $16, DI
	ADDQ $16, SI
	SUBQ $16, CX
	JMP loop16
tail:
	TESTQ CX, CX
	JZ done
tail_loop:
	MOVBQZX (DI), AX
	MOVBQZX (SI), DX
	XORL DX, AX
	MOVB AX, (DI)
	INCQ DI
	INCQ SI
	DECQ CX
	JNE tail_loop
done:
	RET
