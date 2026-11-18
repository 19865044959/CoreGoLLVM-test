---
id: l18_map_slice_iface_triple_wb
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - badger@v4.5.0: db_test.go:249（TestConcurrentWrite：并发写 map 后全量顺序校验）
  - go-redis@v9.8.0: internal_test.go:86（TestRingSetAddrsAndRebalanceRace：分片槽位持续替换）
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1466（TestReset：缓存的 interface 指针跨 Reset/GC 必须保留）
tags: [L18, write-barrier, map, slice, interface, noinline, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

老 holder 同时持有 map（128 项）、slice（64 槽）和 interface{} 字段，三者初始对象经两轮 GC 晋升。随后 20 轮，每轮按 `UAF_SEED` 打乱 map key 与 slice 槽位顺序：

- 取前 24 个 key 由 noinline `setMapVal` 覆写为年轻对象；
- 取前 24 个槽位由 noinline `setSliceVal` 覆写为年轻对象；
- 由 noinline `setIfaceVal` 替换 interface 字段为新的具体类型对象；
- GC 后校验所有 128+64+1 项：已改项为当轮代次，未改项保持上一代内容，data 逐字节匹配。

# 期望行为

1. 每轮 GC 后 128 个 map entry 的 slot/ver/name/24 字节 data 全部正确；
2. 64 个 slice 槽位同样按期望代次校验，不出现串槽；
3. interface 字段类型断言成功，动态类型与 payload 未被回收/改写；
4. 20 轮稳定，map 大小不变（不覆盖扩容路径）；
5. 失败输出带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_map_slice_iface_triple_wb`。

# 失败判读

- `map entry ... corrupted`：老 map 写入年轻 value 漏写屏障，或 map 桶被提前复用；
- `slice slot ... corrupted`：老 slice 底层数组写年轻指针漏写屏障；
- `interface dynamic type lost` / `interface payload ... corrupted`：老 interface 字段写入漏屏障，itab 或数据对象被回收；
- clobberfree 下 data 变 0xde、efence 下 SIGSEGV：确认 UAF；
- 失败时用 `UAF_SEED=<seed>` 复现覆写集合与顺序。

# 参数

- `UAF_SEED`：每轮 map/slice 覆写顺序与 interface slot 编号的种子（默认 1），不影响成功输出；
- 固定参数：128 map entry、64 slice 槽、每轮各改写 24 项 + 1 个 interface，共 20 轮。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_map_slice_iface_triple_wb/main.go
UAF_SEED=7 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
