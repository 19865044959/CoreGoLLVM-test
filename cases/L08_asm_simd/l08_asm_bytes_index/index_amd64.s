//go:build amd64

#include "textflag.h"

// func indexAsm(hay *byte, n int, needle *byte, m int) int
// 用 SSE2 一次筛出 16 个“首字节候选”，再逐字节校验整段 needle；
// 尾部逐起点扫描。空 needle 返回 0，未命中返回 -1，语义与 bytes.Index 一致。
TEXT ·indexAsm(SB), NOSPLIT, $0-40
	MOVQ hay+0(FP), SI
	MOVQ n+8(FP), CX
	MOVQ needle+16(FP), DI
	MOVQ m+24(FP), R12
	MOVQ $-1, AX
	MOVQ AX, ret+32(FP)
	TESTQ R12, R12
	JNE nonempty
	XORQ AX, AX
	MOVQ AX, ret+32(FP)
	RET
nonempty:
	CMPQ CX, R12
	JL done
	SUBQ R12, CX
	INCQ CX			// 候选起点数 = n-m+1
	MOVBQZX (DI), AX
	MOVD AX, X1
	PUNPCKLBW X1, X1
	PUNPCKLBW X1, X1
	PSHUFL $0, X1, X1	// 广播 needle[0]
	XORQ R8, R8		// 已扫描字节数
block16:
	CMPQ CX, $16
	JB tail
	MOVOU (SI), X0
	PCMPEQB X1, X0
	PMOVMSKB X0, DX
verify:
	TESTQ DX, DX
	JZ nextblock
	BSFL DX, R9
	BTRL R9, DX		// 清除已试候选
	CMPQ R9, CX
	JAE verify
	MOVQ SI, R10
	ADDQ R9, R10
	MOVQ DI, R11
	MOVQ R12, R13
cmploop:
	MOVBQZX (R10), BX
	MOVBQZX (R11), AX
	CMPQ BX, AX
	JNE verify
	INCQ R10
	INCQ R11
	DECQ R13
	JNE cmploop
	MOVQ R8, AX
	ADDQ R9, AX
	MOVQ AX, ret+32(FP)
	RET
nextblock:
	ADDQ $16, SI
	ADDQ $16, R8
	SUBQ $16, CX
	JMP block16
tail:
	TESTQ CX, CX
	JZ done
tail_loop:
	TESTQ CX, CX
	JZ done
	MOVBQZX (SI), BX
	MOVBQZX (DI), AX
	CMPQ BX, AX
	JNE tail_next
	MOVQ SI, R10
	MOVQ DI, R11
	MOVQ R12, R13
tail_cmp:
	MOVBQZX (R10), BX
	MOVBQZX (R11), AX
	CMPQ BX, AX
	JNE tail_next
	INCQ R10
	INCQ R11
	DECQ R13
	JNE tail_cmp
	MOVQ R8, AX
	MOVQ AX, ret+32(FP)
	RET
tail_next:
	INCQ SI
	INCQ R8
	DECQ CX
	JMP tail_loop
done:
	RET
