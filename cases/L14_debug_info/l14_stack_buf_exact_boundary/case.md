---
id: l14_stack_buf_exact_boundary
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_stack_buf_exact_boundary/main.go:81 - sbDump 通过全局尺寸调用 runtime.Stack
  - cases/L14_debug_info/l14_stack_buf_exact_boundary/main.go:88 - sbCapture 固定调用点抓取
  - cases/L14_debug_info/l14_stack_buf_exact_boundary/main.go:103 - sbProbe 依次抓 full/full-1/full/full+1
  - cases/L14_debug_info/l14_stack_buf_exact_boundary/main.go:139 - full-1 截断语义
  - cases/L14_debug_info/l14_stack_buf_exact_boundary/main.go:145 - full 精确尺寸的歧义标志
  - docs/DIMENSIONS.md:22 - L14 调试信息（runtime.Stack 缓冲）
tags: [L14, runtime.Stack, buffer-size, exact-boundary, truncation, goroutine-dump, gc]
requires: []
runtime: "<10s（默认 2 轮 × 6 worker；TORTURE_ROUNDS≤16、TORTURE_WORKERS≤32）"
---

# 场景

模拟抓栈缓冲区刚好不足/刚好/多一个字节的边界。尺寸与 all 标志通过全局变量传入，使 full dump 与边界尺寸 dump 来自同一调用点、栈文本可逐字节比较：

- `len == full-1`：返回 n==len(buf)，内容是完整 dump 的严格前缀；
- `len == full`：返回 n==len(buf)，但内容**恰好完整**（`n==len` 的截断标志与“刚好容纳”语义歧义）；
- `len == full+1/2/2*full`：返回 n==full<len(buf)，内容完整，n 之后字节保持为零。

`all=false`（单 goroutine）与 `all=true`（多个阻塞 worker）两条路径都覆盖。对应线上抓栈缓冲差一个字节时误判截断/丢失尾部的问题。

## 期望行为

1. 所有尺寸下 `n <= len(buf)`，n 之后字节保持为零；
2. 大缓冲（1MiB）抓到完整 dump：n>0 且 n<len(buf)，以 `goroutine ` 开头、换行结尾；
3. `len=full-1`：n==len，内容长度 n，严格短于 full，且逐字节等于 `full[:n]`；
4. `len=full`：n==len==fullN，内容逐字节等于 full；
5. `len=full+1`、`full+2`、`2*full`：n<len 且 n==fullN，内容逐字节等于 full；
6. 单 goroutine dump 只有 1 个 goroutine 头，含 `main.sbDump(`、`main.sbCapture(`、`main.sbProbe(`、`main.main(` 与 `main.go:<行号>`；
7. all=true dump 含 main+全部 worker 头、id 唯一、恰好 workers 个 `main.sbBlock(`、每个 worker 的 `chan receive`、`created by main.sbLaunch` 与行号；
8. 释放后每个 worker 执行一次；显式 GC 后 payload 有效；最后一行 `ALL PASS l14_stack_buf_exact_boundary`。

## 失败判读

- `len=full-1` 时 n<len 或内容不是前缀：截断写入语义错误；
- `len=full` 时内容不完整：边界处少写一个字节；
- n 之后出现非零字节：runtime.Stack 写越界或缓冲未清零；
- 不同尺寸内容不相等：dump 来自不同调用点或栈内容不稳定；
- all=true 缺少 worker/标签：阻塞状态或全量 dump 采样错误。

## 参数

- `TORTURE_ROUNDS`：轮数，默认 2，上限 16；
- `TORTURE_WORKERS`：每轮阻塞 worker 数，默认 6，范围 2..32；
- 探测期间临时 `debug.SetGCPercent(-1)` 以避免 GC worker 状态变化影响全量 dump 稳定性，前后与每轮结束仍显式 `runtime.GC()`；
- 无随机路径，无 `UAF_SEED`。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_stack_buf_exact_boundary/*.go
TORTURE_ROUNDS=2 TORTURE_WORKERS=6 GODEBUG=clobberfree=1 /tmp/case
```
