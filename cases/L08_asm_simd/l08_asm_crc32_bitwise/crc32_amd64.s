//go:build amd64

#include "textflag.h"

// func crc32Asm(p *byte, n int, crc uint32) uint32
// CRC-32 IEEE（反射多项式 0xedb88320），逐字节 8 位串行更新；
// 支持从任意初始 crc 续算，语义与 hash/crc32.Update 一致。
TEXT ·crc32Asm(SB), NOSPLIT, $0-32
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	MOVL crc+16(FP), AX
	NOTL AX
	MOVL $0xedb88320, R9
	TESTQ CX, CX
	JLE done
loop:
	MOVBQZX (SI), DX
	XORL DX, AX
	MOVQ $8, R8
bitloop:
	MOVL AX, DX
	SHRL $1, DX
	TESTL $1, AX
	JZ noxor
	XORL R9, DX
noxor:
	MOVL DX, AX
	DECQ R8
	JNE bitloop
	INCQ SI
	DECQ CX
	JNE loop
done:
	NOTL AX
	MOVL AX, ret+24(FP)
	RET
