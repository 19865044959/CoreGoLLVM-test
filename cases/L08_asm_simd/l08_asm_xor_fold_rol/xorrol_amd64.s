//go:build amd64

#include "textflag.h"

// func xorFoldRolAsm(p *byte, n int, seed uint64) uint64
// acc = seed；逐字节 acc = ROL64(acc,1) ^ b；n<=0 返回 seed。
TEXT ·xorFoldRolAsm(SB), NOSPLIT, $0-32
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	MOVQ seed+16(FP), AX
	TESTQ CX, CX
	JLE done
loop:
	ROLQ $1, AX
	MOVBQZX (SI), DX
	XORQ DX, AX
	INCQ SI
	DECQ CX
	JNE loop
done:
	MOVQ AX, ret+24(FP)
	RET
