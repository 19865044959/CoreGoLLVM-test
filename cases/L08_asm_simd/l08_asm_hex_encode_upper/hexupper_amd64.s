//go:build amd64

#include "textflag.h"

// func hexUpperAsm(src, dst, table *byte, n int)
// 逐字节查 16 项大写字母表，把高/低半字节写到 dst[2i]/dst[2i+1]；
// 只写 dst[0:2n]，不触碰其后字节。
TEXT ·hexUpperAsm(SB), NOSPLIT, $0-32
	MOVQ src+0(FP), SI
	MOVQ dst+8(FP), DI
	MOVQ table+16(FP), R10
	MOVQ n+24(FP), CX
	TESTQ CX, CX
	JLE done
loop:
	MOVBQZX (SI), AX
	MOVQ AX, DX
	SHRQ $4, DX
	ANDQ $15, AX
	MOVBQZX (R10)(DX*1), BX
	MOVB BX, (DI)
	MOVBQZX (R10)(AX*1), BX
	MOVB BX, 1(DI)
	INCQ SI
	ADDQ $2, DI
	DECQ CX
	JNE loop
done:
	RET
