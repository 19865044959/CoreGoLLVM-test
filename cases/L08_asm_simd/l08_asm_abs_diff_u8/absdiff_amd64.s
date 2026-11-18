//go:build amd64

#include "textflag.h"

// func absDiffU8Asm(dst, a, b *byte, n int)
// 16 字节块：PMAXUB/PMINUB 取两端较大/较小值，PSUBUSB 得 |a-b|；
// 尾部逐字节比较相减。允许 dst 与 a/b 别名。
TEXT ·absDiffU8Asm(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP), DI
	MOVQ a+8(FP), SI
	MOVQ b+16(FP), R8
	MOVQ n+24(FP), CX
loop16:
	CMPQ CX, $16
	JB tail
	MOVOU (SI), X0
	MOVOU (R8), X1
	MOVOU X0, X2
	PMAXUB X1, X0
	PMINUB X1, X2
	PSUBUSB X2, X0
	MOVOU X0, (DI)
	ADDQ $16, SI
	ADDQ $16, R8
	ADDQ $16, DI
	SUBQ $16, CX
	JMP loop16
tail:
	TESTQ CX, CX
	JZ done
tail_loop:
	MOVBQZX (SI), AX
	MOVBQZX (R8), DX
	CMPQ AX, DX
	JAE tail_sub
	MOVQ AX, BX
	MOVQ DX, AX
	MOVQ BX, DX
tail_sub:
	SUBQ DX, AX
	MOVB AX, (DI)
	INCQ SI
	INCQ R8
	INCQ DI
	DECQ CX
	JNE tail_loop
done:
	RET
