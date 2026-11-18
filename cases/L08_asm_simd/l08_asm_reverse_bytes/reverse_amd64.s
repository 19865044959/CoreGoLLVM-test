//go:build amd64

#include "textflag.h"

// func reverseAsm(p *byte, n int)
// 双指针交换，n<=1 直接返回。
TEXT ·reverseAsm(SB), NOSPLIT, $0-16
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	CMPQ CX, $2
	JL done
	LEAQ -1(SI)(CX*1), DI
loop:
	CMPQ SI, DI
	JGE done
	MOVBQZX (SI), AX
	MOVBQZX (DI), DX
	MOVB DX, (SI)
	MOVB AX, (DI)
	INCQ SI
	DECQ DI
	JMP loop
done:
	RET
