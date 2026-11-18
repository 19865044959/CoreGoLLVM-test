---
id: l18_global_iface_slice_rotate
dim: L18_gc_lifecycle_wb
priority: P2
sources:
  - nats-server@v2.11.0: server/norace_test.go:8483（TestNoRaceClientOutboundQueueMemory：大规模订阅者共享指针，GC 前后内存校验）
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1466（messageState 缓存的 interface 指针跨 Reset/GC 必须保留）
  - badger@v4.5.0: db_test.go:249（并发写 map 后全量顺序校验）
tags: [L18, global-root, interface-slice, map-interface, generation-rotation, noinline, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 6 轮；TORTURE_ROUNDS 上限 30）"
---

# 场景

全局老 `[]interface{}`（长度 96）与 `map[string]interface{}`（64 个固定 key）
经双 GC 晋升。每轮 noinline `giFillSegment` 覆写 slice 的半段槽位，noinline
`giFillMap` 覆写轮转的 key 子集；值的动态类型随代次在 `*giNode` / string /
[]byte 之间切换（interface itab 也会变化）。GC 后逐槽位、逐 key 校验代次、
动态类型与 payload；map 校验只按固定 key 列表进行，不依赖遍历顺序。

与 `global_array_gen_rotate`（结构体数组+interface 字段）不同，本用例覆盖
老 interface slice 底层数组与老 map 值区同时轮转、且动态类型反复切换的路径。

## 期望行为

1. 每轮仅目标半段 slice 与目标 key 子集更新，其余槽位/key 保持旧代内容；
2. 所有槽位与 key 的动态类型符合 `(index+ver)%3` 规则，pointer/string/[]byte 内容正确；
3. 全局 slice 长度恒为 96、map 长度恒为 64；
4. 年轻节点、字符串、字节切片跨 GC 存活，老容器头未被意外替换；
5. 失败信息带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_global_iface_slice_rotate`。

## 失败判读

- `slice slot ... corrupted`：老 interface slice 写屏障缺失或元素被提前回收；
- `map key ... corrupted`：老 map 值区写屏障或 itab 更新错误；
- `container size broken`：slice/map 头被误改写；
- 类型断言失败通常对应该位置的 interface 动态类型未按代次切换。

## 参数

- `UAF_SEED`：数据模式与名称长度；
- `TORTURE_ROUNDS`：轮数（默认 6，上限 30）。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_global_iface_slice_rotate/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=20 UAF_SEED=5 /tmp/case
```
