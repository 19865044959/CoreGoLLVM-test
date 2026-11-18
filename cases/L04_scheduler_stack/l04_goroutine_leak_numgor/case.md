---
id: l04_goroutine_leak_numgor
dim: L04_scheduler_stack
priority: P1
sources:
  - grpc-go@v1.71.1: internal/leakcheck/leakcheck_test.go:41 用 runtime.Stack 抓取 goroutine 并过滤白名单后报告泄漏数量
  - tailscale@v1.80.2: control/controlbase/conn_test.go:234 1000 连接各起阻塞读 goroutine，关闭后轮询 NumGoroutine 确认全部退出
tags: [L04, goroutine-leak, NumGoroutine, runtime.Stack, WaitGroup, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 gRPC leakcheck 与 tailscale 连接生命周期的泄漏检测：主协程在首轮 GC 后记录 `runtime.NumGoroutine()` 基线，然后连续 4 轮启动 32 个短生命周期 worker 与 8 个阻塞 worker。短 worker 持有两节点链、计算校验和后退出；阻塞 worker 在 `leakWorkerMarker` 内停在 gate channel 上，主协程用 `runtime.Stack(all=true)` 确认标记可见（探测器自检），显式 GC 两次后关闭 gate，等待 WaitGroup 收敛。全部轮次结束后再次 GC，用有界轮询比较 NumGoroutine 前后值、活跃计数与 `main.leakWorkerMarker` 栈标记三重确认无泄漏。

# 期望行为

1. 自检阶段 `runtime.Stack` 输出中必须能找到 `main.leakWorkerMarker`，证明泄漏探测器本身有效；
2. 所有短/阻塞 worker 结果均为非负且总和等于独立公式值；
3. 全部轮次结束后 `runtime.NumGoroutine()` 不超过基线 +2（允许运行时后台协程的正常波动）；
4. 活跃 worker 计数回到 0；
5. 最终 `runtime.Stack` 中不再出现 `main.leakWorkerMarker`；
6. 最后一行输出 `ALL PASS l04_goroutine_leak_numgor`。

# 失败判读

- `leak detector did not observe tagged worker`：自检失败，说明 goroutine 未按预期阻塞在标记函数中，用例协调有问题；
- `tagged worker goroutine leaked`：有 worker 未退出，NumGoroutine 检查同时会失败；
- `NumGoroutine grew before=... after=...`：存在泄漏或运行时协程增长；结合栈标记判断是否为本用例 goroutine；
- `active worker count=...`：阻塞 worker 未执行到退出尾部的计数递减；
- `result sum=...`：worker 返回值被破坏或结果丢失。

# 参数

- `UAF_SEED`：每轮 worker 链基值与轮次偏移种子（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_goroutine_leak_numgor/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 /tmp/case
```
