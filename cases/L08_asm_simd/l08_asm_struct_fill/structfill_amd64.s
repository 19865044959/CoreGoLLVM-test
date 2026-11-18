//go:build amd64

#include "textflag.h"

// func fillRecAsm(p *rec, a int64, b, c uint32, sp *byte, sn int, ip *int64) *rec
// 结构体布局（amd64）：
//   0  A int64
//   8  B uint32
//   12 C uint32
//   16 S string (ptr)
//   24 S string (len)
//   32 P *int64
TEXT ·fillRecAsm(SB), NOSPLIT, $0-56
	MOVQ p+0(FP), AX
	MOVQ a+8(FP), DX
	MOVQ DX, 0(AX)
	MOVL b+16(FP), DX
	MOVL DX, 8(AX)
	MOVL c+20(FP), DX
	MOVL DX, 12(AX)
	MOVQ sp+24(FP), DX
	MOVQ DX, 16(AX)
	MOVQ sn+32(FP), DX
	MOVQ DX, 24(AX)
	MOVQ ip+40(FP), DX
	MOVQ DX, 32(AX)
	MOVQ p+0(FP), AX
	MOVQ AX, ret+48(FP)
	RET
