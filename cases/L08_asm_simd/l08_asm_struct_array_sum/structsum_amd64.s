//go:build amd64

#include "textflag.h"

// func sumItemsAsm(p *item, n int) (sumScore, sumWeight int64)
// item 布局：score int64 @0，name string @8，weight int64 @24，stride=32。
// 遍历值数组，累加 score 与 weight。
TEXT ·sumItemsAsm(SB), NOSPLIT, $0-32
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	XORQ AX, AX
	XORQ BX, BX
	TESTQ CX, CX
	JLE done
loop:
	ADDQ (SI), AX
	MOVQ 24(SI), DX
	ADDQ DX, BX
	ADDQ $32, SI
	DECQ CX
	JNE loop
done:
	MOVQ AX, sumScore+16(FP)
	MOVQ BX, sumWeight+24(FP)
	RET
