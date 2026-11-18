//go:build amd64

#include "textflag.h"

// func fillPatternAsm(p *byte, n int, pat uint64)
// 每 8 字节写入重复模式；剩余 1..7 字节按低字节序依次写出。
TEXT ·fillPatternAsm(SB), NOSPLIT, $0-24
	MOVQ p+0(FP), DI
	MOVQ n+8(FP), CX
	MOVQ pat+16(FP), AX
	TESTQ CX, CX
	JLE done
loop8:
	CMPQ CX, $8
	JB tail
	MOVQ AX, (DI)
	ADDQ $8, DI
	SUBQ $8, CX
	JMP loop8
tail:
	TESTQ CX, CX
	JEQ done
	MOVB AX, (DI)
	SHRQ $8, AX
	INCQ DI
	DECQ CX
	JMP tail
done:
	RET
