---
id: l14_multi_goroutine_stack_ids
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_multi_goroutine_stack_ids/main.go:82 - 单 goroutine 自抓栈并解析 id
  - cases/L14_debug_info/l14_multi_goroutine_stack_ids/main.go:95 - 三种 worker 入口符号
  - cases/L14_debug_info/l14_multi_goroutine_stack_ids/main.go:110 - 批量启动与 created by 归属
  - cases/L14_debug_info/l14_multi_goroutine_stack_ids/main.go:200 - id 与全量 dump 记录匹配
  - docs/DIMENSIONS.md:22 - L14 调试信息（多 goroutine 栈）
tags: [L14, goroutine-dump, goroutine-id, runtime.Stack, created-by, gc]
requires: []
runtime: "<10s（默认 2 轮 × 12 goroutine；TORTURE_ROUNDS≤16、TORTURE_WORKERS≤48）"
---

# 场景

N 个 worker 分别从 `mgAlpha`/`mgBeta`/`mgGamma` 三种入口进入并阻塞在同一 channel 上。每个 worker 先用 `runtime.Stack(buf,false)` 抓自己的单 goroutine dump 并解析 goroutine id，随后全量 dump 中该 id 必须恰好出现一次、记录内是同一 worker 的真实符号与 `created by main.mgLaunch`。模拟多协程服务中把单个协程日志与全量 dump 关联定位的场景。

## 期望行为

1. 1MiB 缓冲足以容纳全量 dump，goroutine header 数 >= workers+1 且 id 全部唯一；
2. 全量 dump 含 `main.mgCapture(`、`chan receive`、`created by main.mgLaunch` 与 `main.go:<行号>`；
3. 每个 worker 的自抓栈恰好只有一个 goroutine 头，解析出的 id 与记录一致且非 0；
4. 该 id 能在全量 dump 中切分出唯一记录，记录含同一 worker 类型符号、阻塞状态、created by 与行号；
5. 三种 worker 类型（Alpha/Beta/Gamma）全部出现；
6. 每个 worker id 在全量 dump 中的 header 计数恰好为 1；
7. 释放后所有 worker 退出，显式 GC 后各自 payload 与后继链有效；
8. 最后一行 `ALL PASS l14_multi_goroutine_stack_ids`。

## 失败判读

- 自抓 id 在全量 dump 中缺失或出现多次：goroutine id 解析/记录切分错误；
- 记录符号与 worker 类型不符：多 goroutine 栈串扰或 id 复用；
- created by 不是 `main.mgLaunch`：协程创建归属错误；
- header id 重复：dump 解析或运行时 id 异常；
- 某类 worker 未出现：启动逻辑错误。

## 参数

- `TORTURE_ROUNDS`：轮数，默认 2，上限 16；
- `TORTURE_WORKERS`：每轮 worker 数，默认 12，上限 48（至少 3，保证三种类型都出现）；
- 无随机路径，无 `UAF_SEED`。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_multi_goroutine_stack_ids/*.go
TORTURE_ROUNDS=2 TORTURE_WORKERS=12 GODEBUG=clobberfree=1 /tmp/case
```
