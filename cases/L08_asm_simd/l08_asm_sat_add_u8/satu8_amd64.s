//go:build amd64

#include "textflag.h"

// func satAddU8Asm(dst, a, b *byte, n int)
// 16 字节块 PADDUSB 饱和相加，尾部逐字节钳制到 255；允许 dst 与 a/b 别名。
TEXT ·satAddU8Asm(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP), DI
	MOVQ a+8(FP), SI
	MOVQ b+16(FP), R8
	MOVQ n+24(FP), CX
loop16:
	CMPQ CX, $16
	JB tail
	MOVOU (SI), X0
	MOVOU (R8), X1
	PADDUSB X1, X0
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
	ADDQ DX, AX
	CMPQ AX, $255
	JBE tail_ok
	MOVQ $255, AX
tail_ok:
	MOVB AX, (DI)
	INCQ SI
	INCQ R8
	INCQ DI
	DECQ CX
	JNE tail_loop
done:
	RET

// func satSubU8Asm(dst, a, b *byte, n int)
// 16 字节块 PSUBUSB 饱和相减，尾部逐字节钳制到 0；允许 dst 与 a/b 别名。
TEXT ·satSubU8Asm(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP), DI
	MOVQ a+8(FP), SI
	MOVQ b+16(FP), R8
	MOVQ n+24(FP), CX
subloop16:
	CMPQ CX, $16
	JB subtail
	MOVOU (SI), X0
	MOVOU (R8), X1
	PSUBUSB X1, X0
	MOVOU X0, (DI)
	ADDQ $16, SI
	ADDQ $16, R8
	ADDQ $16, DI
	SUBQ $16, CX
	JMP subloop16
subtail:
	TESTQ CX, CX
	JZ subdone
subtail_loop:
	MOVBQZX (SI), AX
	MOVBQZX (R8), DX
	SUBQ DX, AX
	JNS subok
	XORQ AX, AX
subok:
	MOVB AX, (DI)
	INCQ SI
	INCQ R8
	INCQ DI
	DECQ CX
	JNE subtail_loop
subdone:
	RET
