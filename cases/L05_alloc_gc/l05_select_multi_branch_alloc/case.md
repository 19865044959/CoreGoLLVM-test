---
id: l05_select_multi_branch_alloc
dim: L05_alloc_gc
priority: P2
sources:
  - fasthttp@v1.59.0: server_test.go:944 的 TestServerConcurrencyLimit：select + 信号量循环接收请求
  - badger@v4.5.0: db_test.go:1246 的 TestGetSetRace：无缓冲 chan 上写后立即读的值所有权交接
tags: [L05, select, channel, per-iteration-allocation, closure, map, gc, torture]
requires: []
runtime: "<10s（默认 2 轮）"
---

# 场景

三路已预填并关闭的带缓冲 channel 通过 `select` 接收；每个数据分支内分配
独立的 ack body 副本、tag map 与捕获本次 val 的闭包，ack 与消息对象分离。
每 128 条显式 GC + 垃圾分配；关闭的分支置 nil 后退出。另用 nil channel 的
`select/default` 分支做 128 次固定的逐次分配探测。默认 2 轮，
`TORTURE_ROUNDS` 可放大。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_select_multi_branch_alloc`；
2. 每轮三路各收 500 条，共 1500 条，无重复、无缺失；
3. 每条 ack 的 src/id/val/body/CRC 与发送前消息一致，闭包返回原 val；
4. ack body 与消息 body 不共享底层数组，改写 ack 不影响原消息；
5. select 循环中的显式 GC 不导致丢消息、重复消费或死锁；
6. 默认分支探测 128 次全部执行，探测分配跨 GC 仍可用；
7. 关闭的 channel 被置 nil 后循环正常退出，无零值消息被误收；
8. 在 base/clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `received=` / `count=` / `duplicate` / `missing`：select 分支丢失/重复消费或关闭语义错误；
- `ack body aliases message body`：分支内拷贝退化为别名；
- `closure=` / `tag map corrupted`：逐条分配的闭包/ map 在 GC 中丢失指针；
- `default probes=`：nil channel 的 default 分支未执行；
- 超时/死锁：关闭分支未置 nil 或退出条件错误。

## 参数

- `UAF_SEED`：决定消息 body 长度与字节内容（默认 1，解析失败回退 1），失败输出带种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–8；每轮重新预填 3×500 条消息。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_select_multi_branch_alloc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 TORTURE_ROUNDS=8 /tmp/case
```
