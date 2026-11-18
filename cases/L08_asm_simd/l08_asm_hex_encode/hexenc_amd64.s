//go:build amd64

#include "textflag.h"

// func hexEncodeAsm(dst, src *byte, n int)
// 逐字节把高/低半字节转成小写十六进制，写入 dst[0:2n]。
// 不写 dst[2n] 之后的任何字节。
TEXT ·hexEncodeAsm(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	TESTQ CX, CX
	JLE done
loop:
	MOVBQZX (SI), AX
	MOVQ AX, DX
	SHRL $4, DX		// 高半字节
	ANDQ $15, AX		// 低半字节
	ADDQ $48, DX
	CMPQ DX, $57
	JBE hi_ok
	ADDQ $39, DX		// 'a' - '0' - 10
hi_ok:
	MOVB DX, (DI)
	ADDQ $48, AX
	CMPQ AX, $57
	JBE lo_ok
	ADDQ $39, AX
lo_ok:
	MOVB AX, 1(DI)
	INCQ SI
	ADDQ $2, DI
	DECQ CX
	JNE loop
done:
	RET
