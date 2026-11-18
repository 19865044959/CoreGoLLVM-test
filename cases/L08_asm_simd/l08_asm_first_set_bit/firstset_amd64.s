//go:build amd64

#include "textflag.h"

// func firstSetBitAsm(p *uint64, n int) int64
// 逐字扫描，首个非零字用 BSFQ 定位最低置位；
// 返回全局位下标 = wordIndex*64 + bit，全部为零或空输入返回 -1。
TEXT ·firstSetBitAsm(SB), NOSPLIT, $0-24
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	MOVQ $-1, AX
	TESTQ CX, CX
	JLE done
	XORQ R8, R8		// 字下标
loop:
	MOVQ (SI), DX
	TESTQ DX, DX
	JNZ found
	ADDQ $8, SI
	INCQ R8
	DECQ CX
	JNE loop
	JMP done
found:
	BSFQ DX, DX
	MOVQ R8, AX
	SHLQ $6, AX
	ADDQ DX, AX
done:
	MOVQ AX, ret+16(FP)
	RET
