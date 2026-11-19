---
id: n01_timer_netpoll_storm
priority: P0
covers: [L19, L04, L02]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

timer/netpoll/context 风暴长跑。多 worker 每轮同时做四件事：

1. 对 `timerSlots` 个 `time.Timer` 逐个做“收取 → `Stop` → `Reset`”风暴，统计每次 arm 的结局；
2. 一条 `time.AfterFunc` 自重挂链：回调计数后 `Reset` 自己，直到 quiesce；
3. 每 worker 一个 ticker、2 个消费者 goroutine 竞争接收，校验同一 tick 不被重复投递；
4. 构造 `context.WithTimeout` → 嵌套 `WithCancel` 的超时树 + 独立 `WithDeadline`，校验 `Done`/`Err`；
5. loopback TCP：`Accept` 回显服务 + 客户端 `Dial` → 写 → `CloseWrite` 半关 → 读满 echo 校验字节；每 4 轮用已过期 `SetReadDeadline` 验证超时错误路径。

阶段边界可选 `STRESS_GMX=1,2,4` 切换 `GOMAXPROCS`；未设置时全程不改（尊重 `soak-single` 的 GOMAXPROCS=1）。独立 GC churn goroutine 持续分配垃圾并 `runtime.GC()`。

来源：`go/test/stress/runstress.go:35-54`（dial/accept 压力形态）、`go/src/time/sleep_test.go:113-177,570-586`（Timer Reset/AfterFunc 语义与竞态）、`repos/quic-go/connection_timer_test.go:12-60`（timer reset storm）。

## 不变量

1. 每个 timer 的 `arms == cancelled + expired`；且 `observed <= expired`（Go 1.23+ timer channel 无缓冲，过期值可能被丢弃，不允许出现“凭空投递”）；
2. AfterFunc 链 `arms == fired + cancelled`，且 quiesce 时链在超时内停止；
3. 同一 ticker tick 的时间戳全局唯一（多消费者不重复投递）；消费计数只记录不断言速率；
4. context 超时树：根与 deadline ctx 的 `Err == DeadlineExceeded`，cancel 子节点继承父 cause（DeadlineExceeded），所有 `Done` 关闭；
5. TCP 客户端读到的 echo 字节与写入完全一致；服务端回显总字节 = 客户端写入总字节 + warmup 字节；
6. 结束后 fd 数不超过运行前基线 + 4（Linux；其他平台只记录）；goroutine 数回到基线 + 4；
7. 固定轮数模式每 worker 恰好完成 `STRESS_FIXED_ROUNDS` 轮；心跳持续增长，watchdog 无停滞；
8. 结束打印 `STRESS PASS n01_timer_netpoll_storm`。

## 参数

- `STRESS_SECONDS`（默认 60，0=固定轮数）
- `STRESS_WORKERS`（默认 6）
- `STRESS_HEARTBEAT`（默认 5s，停滞 6 个周期判 FAIL）
- `UAF_SEED`
- `STRESS_FIXED_ROUNDS`（默认 32，仅 seconds=0）
- `STRESS_GMX`（可选，如 `1,2,4`）：只在阶段边界切换 GOMAXPROCS；未设置则不动

## 失败判读

- `phase=final ... timer arms/cancelled/expired`：timer 堆丢失/重复触发或 Reset/Stop 状态错乱；
- `phase=final ... AfterFunc arms/fired/cancelled`：AfterFunc 自重挂丢回调或重复回调；
- `phase=ticker ... tick ... 被重复投递`：ticker channel 重复投递（timer 队列/唤醒错误）；
- `phase=context ... Err=`：context 取消传播/超时错误类型错误；
- `phase=dial/write/halfclose/read/echo`：netpoll 唤醒、半关或字节流损坏；
- `phase=expired-read`：deadline 超时路径未生效；
- `fd baseline ... leak`：连接/监听 fd 泄漏；`goroutine baseline ... leak`：netpoll/handler goroutine 泄漏；
- `watchdog stalled`：netpoll 死锁/调度饥饿（内层），外层由 runner timeout 兜底；
- 只在 clobberfree/efence 下失败：UAF。

## 运行

```bash
SUITE=stress FILTER=n01 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=n01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
STRESS_GMX=1,2,4 SUITE=stress FILTER=n01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=n01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base ./run.sh
```
