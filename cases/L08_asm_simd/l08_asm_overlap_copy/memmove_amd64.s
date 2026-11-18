//go:build amd64

#include "textflag.h"

// func moveAsm(dst, src *byte, n int)
// memmove 语义：dst<=src 或区间不重叠时正向拷贝；src<dst<src+n 时
// 从尾部反向拷贝。16 字节 MOVOU 块 + 单字节尾部。
TEXT ·moveAsm(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	TESTQ CX, CX
	JLE done
	CMPQ DI, SI
	JBE forward
	MOVQ SI, R8
	ADDQ CX, R8
	CMPQ DI, R8
	JAE forward
	// 反向拷贝。
	ADDQ CX, SI
	ADDQ CX, DI
back16:
	CMPQ CX, $16
	JB back1
	SUBQ $16, SI
	SUBQ $16, DI
	MOVOU (SI), X0
	MOVOU X0, (DI)
	SUBQ $16, CX
	JMP back16
back1:
	TESTQ CX, CX
	JEQ done
	DECQ SI
	DECQ DI
	MOVBQZX (SI), DX
	MOVB DX, (DI)
	DECQ CX
	JMP back1
forward:
loop16:
	CMPQ CX, $16
	JB fwd1
	MOVOU (SI), X0
	MOVOU X0, (DI)
	ADDQ $16, SI
	ADDQ $16, DI
	SUBQ $16, CX
	JMP loop16
fwd1:
	TESTQ CX, CX
	JEQ done
	MOVBQZX (SI), DX
	MOVB DX, (DI)
	INCQ SI
	INCQ DI
	DECQ CX
	JMP fwd1
done:
	RET
