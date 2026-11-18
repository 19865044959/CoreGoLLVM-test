//go:build amd64

#include "textflag.h"

// func adler32Asm(p *byte, n int) uint32
// Adler-32：a = 1 + sum(bytes), b = sum(a)，均对 65521 取模。
// 每字节累加后做一次条件减（累加值 < 2*65521），避免 DIV。
TEXT ·adler32Asm(SB), NOSPLIT, $0-20
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	MOVQ $1, R8
	XORQ R9, R9
	TESTQ CX, CX
	JLE done
loop:
	MOVBQZX (SI), DX
	ADDQ DX, R8
	CMPQ R8, $65521
	JB a_ok
	SUBQ $65521, R8
a_ok:
	ADDQ R8, R9
	CMPQ R9, $65521
	JB b_ok
	SUBQ $65521, R9
b_ok:
	INCQ SI
	DECQ CX
	JNE loop
done:
	MOVQ R9, DX
	SHLQ $16, DX
	ORQ R8, DX
	MOVL DX, ret+16(FP)
	RET
