//go:build amd64

#include "textflag.h"

// func crc32TableAsm(p *byte, n int, crc uint32, table *uint32) uint32
// 查表法 CRC-32 IEEE：crc = table[(crc^b)&0xff] ^ (crc>>8)，
// 入口/出口各取反一次，语义与 hash/crc32.Update 的任意初始 crc 续算一致。
TEXT ·crc32TableAsm(SB), NOSPLIT, $0-40
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	MOVL crc+16(FP), AX
	MOVQ table+24(FP), DI
	NOTL AX
	TESTQ CX, CX
	JLE done
loop:
	MOVBQZX (SI), DX
	XORL AX, DX
	ANDL $0xff, DX
	SHRL $8, AX
	XORL (DI)(DX*4), AX
	INCQ SI
	DECQ CX
	JNE loop
done:
	NOTL AX
	MOVL AX, ret+32(FP)
	RET
