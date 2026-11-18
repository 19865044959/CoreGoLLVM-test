---
id: l01_generic_ringbuf
dim: L01_generics
priority: P2
sources:
  - kitex@v0.13.0: pkg/utils/ring_test.go:134 容量 10 的 ring，100 goroutine Push/Pop 校验成功集合
  - quic-go@v0.50.1: internal/utils/ringbuffer/ringbuffer_test.go:9 泛型 RingBuffer Push/Peek/Pop、扩容回绕与 Clear
  - go-redis@v9.8.0: internal_test.go:86 Ring SetAddrs/GetByKey 分片表并发读写
tags: [L01, generics, ring-buffer, overwrite, wraparound, power-of-two, snapshot, gc, TORTURE_ROUNDS, UAF_SEED]
requires: [generics]
runtime: "<10s（默认 3 轮；TORTURE_ROUNDS 放大轮数，上限 16）"
---

# 场景

自建泛型定长环形缓冲 `Ring[T any]`：容量向上取整为 2 的幂，用掩码做回绕；满时 Push 覆盖最旧元素并返回被覆盖指针；支持按逻辑下标 `At`、跨回绕 `Slice`（复制到新切片）与 `Snapshot`。与 `l01_generic_queue_stack` 的动态扩容队列不同，本例聚焦固定容量的覆盖式环形缓冲语义。

每轮以不同容量（8/16/32/64）和 UAF_SEED 决定的步数推入带 blob 的 `*payload`，用独立历史表校验每次 Push 的覆盖指针与容量上限；每 48 次显式 GC。轮末校验 At/Snapshot/Slice 的顺序与指针身份，并验证修改 Slice 返回值不会影响环形缓冲内部槽位；随后 FIFO 排空并 GC 复查。最后实例化 `Ring[string]`。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_ringbuf`；
2. 未满时 Push 不返回被覆盖元素，满后每次返回历史中恰好"容量 + 1 步之前"的同一指针；
3. 任意时刻 `Len <= Cap` 且 Cap 为 2 的幂；At/Snapshot/Slice 的元素顺序与指针身份正确，越界返回 false；
4. Slice 返回值与内部存储无别名；Pop 严格 FIFO，排空后不可再取；
5. 显式 GC 后历史负载与环内指针内容完好；
6. TORTURE_ROUNDS 放大后结果一致，clobberfree 等 profile 下输出一致。

# 失败判读

- `evicted wrong pointer` / `At ... pointer mismatch`：回绕下标或覆盖顺序错误，优先检查 mask/head 计算与泛型实例的边界；
- `Slice shares backing storage`：跨回绕复制逻辑退化为别名；
- `blob corrupted` 或 bad pointer：环形缓冲持有的指针被错误回收，怀疑栈映射/写屏障；
- 超时：Pop 未推进 head 或 GC 饥饿。

## 参数

- `UAF_SEED`：决定每轮步数（180–219），轮内容量按轮次固定；
- `TORTURE_ROUNDS`：默认 3，最大 16，按轮放大容量档位与重复次数。

## 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_ringbuf/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=7 TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
