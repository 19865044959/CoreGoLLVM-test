//go:build amd64

#include "textflag.h"

// func lastIndexSubAsm(hay *byte, n int, needle *byte, m int) int
// 空 needle 返回 n；m>n 返回 -1。否则从最后一个起点 n-m 起倒扫：
// 16 字节窗口覆盖 16 个候选起点，PCMPEQB 筛首字节命中，
// BSRL 取窗口内最右候选并整段校验；窗口推进到起点 0 后逐起点倒扫。
TEXT ·lastIndexSubAsm(SB), NOSPLIT, $0-40
	MOVQ hay+0(FP), SI
	MOVQ n+8(FP), CX
	MOVQ needle+16(FP), DI
	MOVQ m+24(FP), R12
	MOVQ $-1, AX
	MOVQ AX, ret+32(FP)
	TESTQ R12, R12
	JNE nonempty
	MOVQ CX, AX
	MOVQ AX, ret+32(FP)
	RET
nonempty:
	CMPQ CX, R12
	JL done
	MOVQ CX, R8
	SUBQ R12, R8		// R8 = 最后候选起点
	MOVBQZX (DI), AX
	MOVD AX, X1
	PUNPCKLBW X1, X1
	PUNPCKLBW X1, X1
	PSHUFL $0, X1, X1
	CMPQ R8, $15
	JB scalar_entry
	SUBQ $15, R8		// 首个窗口基址 = lastStart-15
block:
	MOVQ SI, R10
	ADDQ R8, R10
	MOVOU (R10), X0
	PCMPEQB X1, X0
	PMOVMSKB X0, DX
cand:
	TESTL DX, DX
	JZ next_block
	BSRL DX, R9
	BTRL R9, DX
	MOVQ SI, R10
	ADDQ R8, R10
	ADDQ R9, R10
	MOVQ DI, R11
	MOVQ R12, R13
cmploop:
	MOVBQZX (R10), BX
	MOVBQZX (R11), AX
	CMPQ BX, AX
	JNE cand
	INCQ R10
	INCQ R11
	DECQ R13
	JNE cmploop
	MOVQ R8, AX
	ADDQ R9, AX
	MOVQ AX, ret+32(FP)
	RET
next_block:
	CMPQ R8, $16
	JB after_blocks
	SUBQ $16, R8
	JMP block
after_blocks:
	TESTQ R8, R8
	JZ done
	DECQ R8			// 剩余起点 0..R8-1
	JMP scalar
scalar_entry:
	// R8 = 最后候选起点（<15）
scalar:
	MOVQ SI, R10
	ADDQ R8, R10
	MOVQ DI, R11
	MOVQ R12, R13
scalar_cmp:
	MOVBQZX (R10), BX
	MOVBQZX (R11), AX
	CMPQ BX, AX
	JNE scalar_next
	INCQ R10
	INCQ R11
	DECQ R13
	JNE scalar_cmp
	MOVQ R8, AX
	MOVQ AX, ret+32(FP)
	RET
scalar_next:
	DECQ R8
	JNS scalar
done:
	RET
