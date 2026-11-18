---
id: l14_loop_lineinfo
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_loop_lineinfo/main.go:71 loopSite 用 Caller(1) 取调用点行号
  - cases/L14_debug_info/l14_loop_lineinfo/main.go:98 循环内第一个调用点
  - cases/L14_debug_info/l14_loop_lineinfo/main.go:101 循环内第四个调用点
  - cases/L14_debug_info/l14_loop_lineinfo/main.go:112 循环内 panic 调用
  - docs/DIMENSIONS.md:22 L14 调试信息（行号表）
tags: [L14, line-info, loop, runtime.Caller, traceback, TORTURE_ROUNDS, gc]
requires: []
runtime: "<10s（默认 1 轮 × 64 次迭代；TORTURE_ROUNDS 放大轮数，上限 16）"
---

# 场景

同一循环体内在四个不同源码行依次调用 noinline `loopSite()`（内部 `runtime.Caller(1)`），跨迭代比较行号是否稳定、跨调用点是否随源码顺序递增；循环中某次迭代调用 `loopPanic` 并在匿名函数 defer 中 recover 抓栈。`TORTURE_ROUNDS` 放大轮数。

## 期望行为

1. 四个调用点报告的 File 均以 `main.go` 结尾、行号 > 0；
2. 每个调用点在所有迭代中的行号恒定；
3. 四个调用点行号严格递增（只比较相对顺序，不依赖具体值）；
4. 循环内 panic 栈包含 `main.loopPanic(`、匿名循环帧 `main.runRound.func1(` 与 `main.go:<行号>`，且 loopPanic 帧在匿名帧之前；
5. recover 到的 payload 身份正确，GC 后 payload 链有效；
6. 退出码 0，最后一行 `ALL PASS l14_loop_lineinfo`。

## 失败判读

- 行号随迭代漂移：行号归属错乱（常见于内联行号表错误）；
- 行号未随源码顺序递增：line table 顺序错误；
- 循环 panic 栈帧缺失：traceback 在热循环路径上取栈失败；
- GC 后 payload 损坏：循环帧栈映射漏标。

## 参数

- `TORTURE_ROUNDS`：轮数，默认 1，范围 1..16；每轮 64 次迭代；
- `UAF_SEED` 不参与逻辑。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_loop_lineinfo/*.go
TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
