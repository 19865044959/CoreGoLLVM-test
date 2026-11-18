---
id: l14_goroutine_id_batch_lifecycle
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_goroutine_id_batch_lifecycle/main.go:91 - gidWorker 自抓单 goroutine dump 并解析 id
  - cases/L14_debug_info/l14_goroutine_id_batch_lifecycle/main.go:113 - gidBatch 批量启动、全量 dump 校验、等待退出
  - cases/L14_debug_info/l14_goroutine_id_batch_lifecycle/main.go:196 - 批间 id 不复用断言
  - cases/L14_debug_info/l14_goroutine_id_batch_lifecycle/main.go:211 - main goroutine id 解析
  - cases/L14_debug_info/l14_goroutine_id_batch_lifecycle/main.go:219 - 两批 id 检查
  - docs/DIMENSIONS.md:22 - L14 调试信息（多 goroutine dump）
tags: [L14, goroutine-id, goid-cache, lifecycle, runtime.Stack, created-by, gc]
requires: []
runtime: "<10s（默认 2 轮 × 2 批 × 8 worker；TORTURE_ROUNDS≤16、TORTURE_WORKERS≤32）"
---

# 场景

每批 worker 由同一 main 循环创建，每个 worker 先 `runtime.Stack(buf,false)` 自抓单 goroutine dump 解析 id，再阻塞在同一 channel 上；main 抓全量 dump 验证每个存活 id 恰好出现一次、带 worker 符号/`created by main.gidBatch`/阻塞状态/行号；整批退出后行号对应的 id 从 dump 消失。Go 1.24 使用 per-P goid cache（一次从 `sched.goidgen` 预留 16 个 id），因此**不假设 id 与创建下标全局同序**或无背景协程插入；只断言唯一、不复用与生命周期。

## 期望行为

1. main goroutine 自抓栈能解析出唯一的正整数 id；
2. 每个 worker 自抓栈恰好一个 goroutine 头并解析出正 id；批内 id 唯一，且全部大于 main id；
3. 全量 dump 中每个存活 worker id 的 header 恰好出现一次，含 `main.gidWorker(`、`chan receive`、`created by main.gidBatch` 与 `main.go:<行号>`；
4. 整批 release + `WaitGroup` 退出后，轮询到这批 id 全部从全量 dump 消失；
5. 同一轮第二批判定的每个 id 与第一批、以及跨轮次已见 id 集合完全不相交（goid 不复用，即使 per-P cache 使数值非单调）；
6. 每批 worker 的 payload 在退出后递增一次，显式 GC 后值与后继链仍有效；
7. 最后一行 `ALL PASS l14_goroutine_id_batch_lifecycle`。

## 失败判读

- 自抓栈没有唯一 goroutine 头或 id 解析失败：单 goroutine dump 格式/解析错误；
- 批内 id 重复：自抓栈张冠李戴或 id 解析取错 header；
- 存活 id 在 full dump 中出现 0 次或多次：全量 dump 漏协程或切分错误；
- 退出后旧 id 仍出现：生命周期等待/死协程排查问题；
- 新批次出现旧 id：goid 复用（运行时行为异常，可能是编译器/运行库 bug）。

## 参数

- `TORTURE_ROUNDS`：轮数，默认 2，上限 16；
- `TORTURE_WORKERS`：每批 worker 数（每轮 2 批），默认 8，范围 3..32；
- 无随机路径，无 `UAF_SEED`。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_goroutine_id_batch_lifecycle/*.go
TORTURE_ROUNDS=2 TORTURE_WORKERS=8 GODEBUG=clobberfree=1 /tmp/case
```
