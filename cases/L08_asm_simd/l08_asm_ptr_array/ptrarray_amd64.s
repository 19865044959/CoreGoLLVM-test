//go:build amd64

#include "textflag.h"

// func sumNodeVAsm(pp **node, n int) int64
// 遍历 Go 指针数组，解引用每个 *node 并读取首字段 v（int64）；nil 跳过。
TEXT ·sumNodeVAsm(SB), NOSPLIT, $0-24
	MOVQ pp+0(FP), SI
	MOVQ n+8(FP), CX
	XORQ AX, AX
	TESTQ CX, CX
	JLE done
loop:
	MOVQ (SI), DX
	TESTQ DX, DX
	JEQ skip
	ADDQ (DX), AX
skip:
	ADDQ $8, SI
	DECQ CX
	JNE loop
done:
	MOVQ AX, ret+16(FP)
	RET
