//go:build amd64

#include "textflag.h"
#include "funcdata.h"

// X09: 严格 ABI0/NOSPLIT 汇编帧。所有参数/结果位置与 Go 声明一致：
//   asmPtrCall(p *node, x int64) (*node, int64):
//       p+0(FP) x+8(FP) ret+16(FP) canary+24(FP)，argsize=32，frame=8
//   asmPtrReturn(p *node, x int64) (*node, int64):
//       p+0(FP) x+8(FP) ret+16(FP) chk+24(FP)，argsize=32，frame=8
//   asmLoop(p *node, n int64) int64:
//       p+0(FP) n+8(FP) ret+16(FP)，argsize=24，frame=24
//   asmChain0..4(p *node, n int64) int64:
//       p+0(FP) n+8(FP) ret+16(FP)，argsize=24，frame=32
//
// GO_ARGS 生成汇编函数的 args pointer map（含 inputs；GO_RESULTS_INITIALIZED 后
// 同一 bitmap 还包含 results）；NO_LOCAL_POINTERS 声明局部槽只有整型。
// Go ABI 没有 callee-saved 寄存器：每次 CALL 之后都从 FP 重新载入 p、从局部槽读回
// 计数/累加值。

TEXT ·asmPtrCall(SB), NOSPLIT, $8-32
	GO_ARGS
	NO_LOCAL_POINTERS
	MOVQ p+0(FP), AX
	MOVQ (AX), BX          // GC 前触碰 p，确认入参有效
	CALL ·helperCanary(SB)
	MOVQ 0(SP), CX         // helper 返回值（透传）
	MOVQ p+0(FP), DX       // 重新载入 p（调用者保存寄存器）
	MOVQ (DX), DI          // GC 后读 p.v；p 只由汇编帧 args map 保活
	ADDQ DI, CX
	MOVQ DX, ret+16(FP)    // 指针原样透传
	MOVQ CX, canary+24(FP) // helper 返回值 + GC 后 p.v
	RET

TEXT ·asmPtrReturn(SB), NOSPLIT, $8-32
	GO_ARGS
	NO_LOCAL_POINTERS
	CALL ·helperAlloc(SB)
	MOVQ 0(SP), AX
	MOVQ AX, ret+16(FP)
	GO_RESULTS_INITIALIZED  // 之后 results bitmap（inputs∪results）生效
	CALL ·helperSink(SB)    // GC + 批量分配；结果指针只由 results map 保活
	MOVQ p+0(FP), DX
	MOVQ (DX), BX           // 入参 p 也必须活跨该 GC
	MOVQ BX, chk+24(FP)
	RET

TEXT ·asmLoop(SB), NOSPLIT, $24-24
	GO_ARGS
	NO_LOCAL_POINTERS
	MOVQ n+8(FP), BX
	MOVQ BX, 8(SP)          // 计数（整型局部）
	MOVQ $0, 16(SP)         // 累加（整型局部）
	TESTQ BX, BX
	JE   loopdone
loop:
	CALL ·helperCanary(SB)
	MOVQ 0(SP), BX
	ADDQ BX, 16(SP)
	MOVQ 8(SP), BX
	DECQ BX
	MOVQ BX, 8(SP)
	JNE  loop
loopdone:
	MOVQ p+0(FP), DX        // p 活跨 n 次 Go 调用
	MOVQ (DX), BX
	ADDQ 16(SP), BX
	MOVQ BX, ret+16(FP)
	RET

TEXT ·asmChain0(SB), NOSPLIT, $32-24
	GO_ARGS
	NO_LOCAL_POINTERS
	MOVQ n+8(FP), AX
	TESTQ AX, AX
	JE   done0
	CALL ·helperCanary(SB)
	MOVQ 0(SP), BX
	MOVQ BX, 24(SP)         // helper canary（整型局部）
	MOVQ n+8(FP), AX        // CALL 后重新载入 n
	DECQ AX
	MOVQ p+0(FP), DX
	MOVQ DX, 0(SP)          // 子调用参数 p
	MOVQ AX, 8(SP)          // 子调用参数 n-1
	CALL ·asmChain1(SB)
	MOVQ 16(SP), BX
	ADDQ 24(SP), BX
	MOVQ p+0(FP), DX        // 重新载入 p
	MOVQ (DX), AX
	ADDQ AX, BX
	MOVQ BX, ret+16(FP)
	RET
done0:
	MOVQ p+0(FP), DX
	MOVQ (DX), AX
	MOVQ AX, ret+16(FP)
	RET

TEXT ·asmChain1(SB), NOSPLIT, $32-24
	GO_ARGS
	NO_LOCAL_POINTERS
	MOVQ n+8(FP), AX
	TESTQ AX, AX
	JE   done1
	CALL ·helperCanary(SB)
	MOVQ 0(SP), BX
	MOVQ BX, 24(SP)
	MOVQ n+8(FP), AX
	DECQ AX
	MOVQ p+0(FP), DX
	MOVQ DX, 0(SP)
	MOVQ AX, 8(SP)
	CALL ·asmChain2(SB)
	MOVQ 16(SP), BX
	ADDQ 24(SP), BX
	MOVQ p+0(FP), DX
	MOVQ (DX), AX
	ADDQ AX, BX
	MOVQ BX, ret+16(FP)
	RET
done1:
	MOVQ p+0(FP), DX
	MOVQ (DX), AX
	MOVQ AX, ret+16(FP)
	RET

TEXT ·asmChain2(SB), NOSPLIT, $32-24
	GO_ARGS
	NO_LOCAL_POINTERS
	MOVQ n+8(FP), AX
	TESTQ AX, AX
	JE   done2
	CALL ·helperCanary(SB)
	MOVQ 0(SP), BX
	MOVQ BX, 24(SP)
	MOVQ n+8(FP), AX
	DECQ AX
	MOVQ p+0(FP), DX
	MOVQ DX, 0(SP)
	MOVQ AX, 8(SP)
	CALL ·asmChain3(SB)
	MOVQ 16(SP), BX
	ADDQ 24(SP), BX
	MOVQ p+0(FP), DX
	MOVQ (DX), AX
	ADDQ AX, BX
	MOVQ BX, ret+16(FP)
	RET
done2:
	MOVQ p+0(FP), DX
	MOVQ (DX), AX
	MOVQ AX, ret+16(FP)
	RET

TEXT ·asmChain3(SB), NOSPLIT, $32-24
	GO_ARGS
	NO_LOCAL_POINTERS
	MOVQ n+8(FP), AX
	TESTQ AX, AX
	JE   done3
	CALL ·helperCanary(SB)
	MOVQ 0(SP), BX
	MOVQ BX, 24(SP)
	MOVQ n+8(FP), AX
	DECQ AX
	MOVQ p+0(FP), DX
	MOVQ DX, 0(SP)
	MOVQ AX, 8(SP)
	CALL ·asmChain4(SB)
	MOVQ 16(SP), BX
	ADDQ 24(SP), BX
	MOVQ p+0(FP), DX
	MOVQ (DX), AX
	ADDQ AX, BX
	MOVQ BX, ret+16(FP)
	RET
done3:
	MOVQ p+0(FP), DX
	MOVQ (DX), AX
	MOVQ AX, ret+16(FP)
	RET

// asmChain4 是递归链基例：只读 p.v，不再调用 Go（NOSPLIT 自递归会被链接器拒绝，
// 因此递归展开为 5 个互不相同的 NOSPLIT 符号）。
TEXT ·asmChain4(SB), NOSPLIT, $0-24
	GO_ARGS
	NO_LOCAL_POINTERS
	MOVQ p+0(FP), DX
	MOVQ (DX), AX
	MOVQ AX, ret+16(FP)
	RET
