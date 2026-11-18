//go:build amd64

#include "textflag.h"

// func chaseSumAsm(head *node) int64
// node 布局：v int64 @0，next *node @8，name string @16。
// 沿 next 追逐到 nil，累加 v。
TEXT ·chaseSumAsm(SB), NOSPLIT, $0-16
	MOVQ head+0(FP), SI
	XORQ AX, AX
loop:
	TESTQ SI, SI
	JEQ done
	ADDQ (SI), AX
	MOVQ 8(SI), SI
	JMP loop
done:
	MOVQ AX, ret+8(FP)
	RET
