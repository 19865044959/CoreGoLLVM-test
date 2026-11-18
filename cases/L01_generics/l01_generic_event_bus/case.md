---
id: l01_generic_event_bus
dim: L01_generics
priority: P2
sources:
  - kitex@v0.13.0: pkg/event/bus_test.go:27 EventBus Watch/Unwatch/Dispatch，异步回调只命中订阅者
  - go-redis@v9.8.0: pubsub_test.go:37 模式订阅状态机 subscribe→timeout→message→unsubscribe
  - kitex@v0.13.0: pkg/stats/event_test.go:25 全局事件注册表新增/重名/冻结语义
tags: [L01, generics, event-bus, subscribe, unsubscribe, ordering, snapshot-dispatch, gc, TORTURE_ROUNDS, UAF_SEED]
requires: [generics]
runtime: "<10s（默认 2 轮；TORTURE_ROUNDS 放大轮数，上限 16）"
---

# 场景

泛型事件总线 `Bus[E any]`：按 topic 保存订阅者切片，订阅返回单调递增 id；Publish 对当前订阅者做快照后按注册顺序同步派发；Unsubscribe 幂等返回 bool。对应用户态的事件注册表与订阅连接状态机，但把异步分发收敛为确定性同步模型。

每轮建立 3–5 个 orders 订阅者与 1 个 users 订阅者，发布 120–159 个带 blob 的事件，逐次校验派发计数、调用顺序、负载指针与 topic 隔离；每 32 次 GC。随后退订半数订阅者并复查剩余调用顺序。额外验证"派发中退订他者"的快照语义：当前轮仍全部执行，下一轮不再命中。最后实例化 `Bus[int64]` 验证值型事件。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_event_bus`；
2. 每次 Publish 的返回值等于快照中的订阅者数，回调按注册顺序执行且只收到对应 topic 的事件；
3. 退订后计数与后续派发序列正确，未知 id 退订返回 false；
4. 派发期间退订在当轮不生效（快照），下一轮生效；
5. GC 后所有事件负载指针身份与 blob 不变；int64 总线累加结果正确；
6. TORTURE_ROUNDS 放大后输出确定，clobberfree 等 profile 下一致。

# 失败判读

- `handlers not called in subscription order`：订阅者切片被 map 遍历或并发修改打乱；
- `cross-topic delivery mismatch`：topic 索引错误；
- `event payload corrupted` / pointer changed：事件负载在 GC 后失效；
- 超时：订阅者切片成环或死循环（本用例无阻塞）。

## 参数

- `UAF_SEED`：决定订阅者数量、事件数；
- `TORTURE_ROUNDS`：默认 2，最大 16，放大轮数。

## 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_event_bus/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
