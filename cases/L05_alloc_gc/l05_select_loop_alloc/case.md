---
id: l05_select_loop_alloc
dim: L05_alloc_gc
priority: P0
sources:
  - fasthttp@v1.59.0: server_test.go:944 的 TestServerConcurrencyLimit：select + 信号量循环接收请求
  - badger@v4.5.0: db_test.go:1246 的 TestGetSetRace：无缓冲 chan 上写后立即读的值所有权交接
tags: [L05, select, channel, per-iteration-allocation, linked-list, liveness, gc]
requires: []
runtime: "<3s"
---

# 场景

生产者 goroutine 向两个带缓冲 channel 发送 1200 条消息（每条含变长 body 与
指向同源上一条消息的 `prev` 指针），发送完成后关闭 channel。主协程在 `select`
中接收，乱序也没关系：每收到一条就在分支内分配 `selAck`（独立 body 拷贝，
并保留 `*selMsg`），每 64 条制造垃圾并两次 `runtime.GC()`。全部接收后，
再跨 GC 反向遍历两条链表并校验每条 ack。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_select_loop_alloc`；
2. 共接收 1200 条，两个来源各 600 条，无重复/缺失 ack；
3. 每条 ack 的 body 长度/内容与种子推导值一致，msg 指针与摘要跨 GC 不变；
4. select 分支内分配的 ack body 是独立副本：改写消息 body 不影响 ack；
5. 两条 `prev` 链长度各 600，反向遍历无环，id 集合与期望完全一致；
6. 循环期间的显式 GC 不导致消息丢失、死锁或重复消费；
7. 生产者 goroutine 在 channel 关闭后正常退出。

## 失败判读

- `received` / `counts`：select 分支丢失或重复消费；
- `ack ... missing` / `body byte`：分支内分配的 ack 或拷贝被 GC 破坏；
- `lost msg pointer` / `msg sum changed`：消息对象未被 ack 保活或被复用（UAF）；
- `link ... cycle` / `len` / `sum`：链表指针在分配/GC 中损坏；
- `ack body aliases msg body`：拷贝路径退化为别名，select 循环内分配语义错误；
- 超时/死锁：channel 未关闭或 select 退出条件错误。

## 参数

- `UAF_SEED`：决定消息 body 长度与内容（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数；固定 1200 条消息、每 64 条 GC 一次。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_select_loop_alloc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
