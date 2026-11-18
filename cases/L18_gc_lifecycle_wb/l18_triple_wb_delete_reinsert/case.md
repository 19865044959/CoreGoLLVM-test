---
id: l18_triple_wb_delete_reinsert
dim: L18_gc_lifecycle_wb
priority: P0
sources:
  - badger@v4.5.0: value_test.go:207（TestValueGC：100 个 32KB 大值随机删除后重写剩余值）
  - nats-server@v2.11.0: server/client_test.go:1304（TestUnsubRace：退订 tombstone 与发布并发）
tags: [L18, write-barrier, map, slice, interface, delete-reinsert, slice-reuse, noinline, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

老 holder 同时持有 map（128 项）、slice（64 槽）和 interface，全部经两轮 GC 晋升。每轮按 `UAF_SEED` 打乱 key 顺序：

1. noinline `twDelete` 删除 32 个 map entry；noinline `twTruncate` 把老 slice 截断 8 槽；noinline `twClearIface` 把 interface 置 nil；GC 后校验删除集合缺失、其余值保持上一代、slice 长度 56；
2. noinline `twInsert` 重插这 32 个 key（新值），`twOverwrite` 覆写另 32 个 key，`twAppend` 在截断后的老底层数组上追加 8 个年轻节点，`twSetIface` 重设 interface；GC 后校验 128 个 key、64 个槽位、interface 的动态类型与逐字节 data。

默认 12 轮（`TORTURE_ROUNDS` 上限 40），覆盖 map 删除/重插、slice 截断/扩容复用、interface 清空/重建三条写屏障路径。

# 期望行为

1. 删除阶段后 map 长度恰为 96，被删 key 不存在，未删 key 版本/数据不变；
2. 截断后 slice 长度 56，前 56 槽内容不变，interface 为 nil；
3. 重插/覆写/append 后 map 恢复 128 项、slice 恢复 64 槽，新一代与上一代槽位按期望混合；
4. 每轮全部已写入对象跨 GC 存活，data 逐字节匹配；
5. 失败输出带 `seed=<UAF_SEED>`，同种子可复现 key 顺序；
6. 退出码 0，最后一行 `ALL PASS l18_triple_wb_delete_reinsert`。

# 失败判读

- `deleted key ... still present`：map 删除未生效或桶被错误复用；
- `map len=...`：删除/插入计数错误，先看是否发生提前回收；
- `slice ... data corrupted`：截断后 append 复用了老底层数组但漏写屏障，年轻对象被回收；
- `interface lost or type changed`：interface 字段写屏障或 itab 丢失；
- 失败信息中的 `(seed=N)` 用于复现。

# 参数

- `UAF_SEED`：key 顺序与数据偏移种子；
- `TORTURE_ROUNDS`：轮数（默认 12，上限 40）。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_triple_wb_delete_reinsert/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=24 UAF_SEED=7 /tmp/case
```
