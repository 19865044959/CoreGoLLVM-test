---
id: l14_stack_buf_growth
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_stack_buf_growth/main.go:76 - runtime.Stack(buf,false) 单 goroutine dump
  - cases/L14_debug_info/l14_stack_buf_growth/main.go:83 - runtime.Stack(buf,true) 全量 dump
  - cases/L14_debug_info/l14_stack_buf_growth/main.go:97 - 截断/前缀/零尾断言
  - cases/L14_debug_info/l14_stack_buf_growth/main.go:136 - 翻倍增长循环
  - docs/DIMENSIONS.md:22 - L14 调试信息（栈 dump）
tags: [L14, runtime.Stack, buffer-size, truncation, goroutine-dump, gc]
requires: []
runtime: "<10s（默认 2 轮 × 8 个阻塞 goroutine；TORTURE_ROUNDS≤16、TORTURE_WORKERS≤32）"
---

# 场景

模拟 expvar/pprof 中常见的 `runtime.Stack` 采集模式：先给小缓冲，返回 n==len(buf) 表示截断，再翻倍直到 n<len(buf)。单 goroutine（all=false）与全量（all=true）两条路径都覆盖，并检查截断内容是完整 dump 的严格前缀、n 之后字节未被写、header/行号/符号真实。对应线上抓栈缓冲区不足导致日志被截断的问题。

## 期望行为

1. 缓冲较小时 `runtime.Stack` 返回 n==len(buf)，且已写内容是完整 dump 的前缀；
2. 缓冲足够大时返回 n<len(buf)，内容以 `goroutine ` 开头、以换行结尾，n 之后字节保持为零；
3. 缓冲长度为 1/4/8/9 时写入内容精确等于 `"goroutine "` 的对应前缀；
4. `all=false` 的完整 dump 只有一个 goroutine 头，包含 `main.bufSelfDump(`、`main.main(` 与 `main.go:<行号>`；
5. 增长循环从 16 开始翻倍，最终 n<size 且 size/2 < n（首达足够大小），输出含真实符号与行号；
6. `all=true` 的全量 dump 含 main 与每个阻塞 worker 的 header、id 唯一、`main.bufBlocker(`、`chan receive`、`created by main.main` 与行号；
7. all=true 的增长循环同样终止且包含全部 worker；
8. 释放后每个 worker 执行一次，显式 GC 后 payload 有效，最后一行 `ALL PASS l14_stack_buf_growth`。

## 失败判读

- 截断时 n 不等于 len(buf)：截断约定错误或写越界；
- 截断内容不是完整 dump 前缀：采集过程中栈内容不稳定或写入顺序错误；
- n 之后出现非零字节：runtime.Stack 写越界；
- 增长循环不终止：返回长度与缓冲大小关系错误；
- all=true 缺少 worker：全量 dump 漏 goroutine 或阻塞态判断错误。

## 参数

- `TORTURE_ROUNDS`：轮数，默认 2，上限 16；
- `TORTURE_WORKERS`：每轮阻塞 goroutine 数，默认 8，上限 32；
- 无随机路径，无 `UAF_SEED`。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_stack_buf_growth/*.go
TORTURE_ROUNDS=2 TORTURE_WORKERS=8 GODEBUG=clobberfree=1 /tmp/case
```
