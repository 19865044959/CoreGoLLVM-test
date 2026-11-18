//go:build amd64

#include "textflag.h"

// func bitrevBytesAsm(p *byte, n int, table *byte)
// 逐字节用 256 项查表替换为位反转结果（原地），n<=0 不访问内存。
TEXT ·bitrevBytesAsm(SB), NOSPLIT, $0-24
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	MOVQ table+16(FP), DI
	TESTQ CX, CX
	JLE done
loop:
	MOVBQZX (SI), AX
	MOVBQZX (DI)(AX*1), DX
	MOVB DX, (SI)
	INCQ SI
	DECQ CX
	JNE loop
done:
	RET
