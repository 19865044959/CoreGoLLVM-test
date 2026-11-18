//go:build amd64

#include "textflag.h"

// func firstSetFromAsm(p *uint64, n int, start int) int
// start<0 按 0 处理；start>=n*64 或全零返回 -1。
// 首字用 ~0<<(start&63) 掩掉起始位之前的位，BSFQ 取最低置位；
// 之后逐字扫描，字内用 BSFQ。
TEXT ·firstSetFromAsm(SB), NOSPLIT, $0-32
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), R11
	MOVQ start+16(FP), DX
	MOVQ $-1, AX
	MOVQ AX, ret+24(FP)
	TESTQ R11, R11
	JLE done
	TESTQ DX, DX
	JNS start_ok
	XORQ DX, DX
start_ok:
	MOVQ R11, R8
	SHLQ $6, R8		// 总位数
	CMPQ DX, R8
	JAE done
	MOVQ DX, R9
	SHRQ $6, R9		// 起始字下标
	MOVQ DX, R10
	ANDQ $63, R10		// 字内起始位
	MOVQ (SI)(R9*8), BX
	MOVQ R10, CX
	MOVQ $-1, DI
	SHLQ CL, DI
	ANDQ DI, BX
	JNZ found
	INCQ R9
scan:
	CMPQ R9, R11
	JAE done
	MOVQ (SI)(R9*8), BX
	TESTQ BX, BX
	JNZ found
	INCQ R9
	JMP scan
found:
	BSFQ BX, BX
	MOVQ R9, AX
	SHLQ $6, AX
	ADDQ BX, AX
	MOVQ AX, ret+24(FP)
done:
	RET
