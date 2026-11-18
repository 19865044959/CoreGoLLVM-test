---
id: l03_map_slice_append_loop
dim: L03_async_preempt
priority: P1
sources:
  - caddy@v2.10.0: admin_test.go:916 moduleInstances 为 map[string][]Module，每次请求按需构建并查询分片
  - badger@v4.5.0: levels_test.go:887 levelData 为 map[int][][]keyValVersion，遍历嵌套 map/slice 建层并校验
tags: [L03, async-preempt, map-of-slice, append-growth, delete-reinsert, map-growth, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

`map[int64][]*msNode` 从 cap=8 开始，按 `UAF_SEED` 决定的键序逐步增长到 128 个键。每轮向某个键的 slice append 一个新节点，使其跨越 slice 容量边界；每 7 轮压缩一个键的切片（分配新 backing，旧 backing 变成垃圾）；每 5 轮 delete 一个键并在后续轮次重新插入。每 32 轮分配垃圾并显式 `runtime.GC()`，随后按 0..127 的固定键序复核全部节点的 `tag` 与 `v%1000==key`；另一个 goroutine 并发执行 8 轮 GC。结束后用固定键序模型和一次与顺序无关的 map range 双重核对。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l03_map_slice_append_loop`；
2. 每次 append/压缩/删除后模型计数与和一致，GC 后逐键扫描无损坏节点；
3. 主循环显式 GC 次数为 64，GC pump 至少完成 1 轮；
4. append 总数等于 2048；
5. map range 汇总的键数、节点总数与节点和等于固定键序模型；
6. 在 clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `FAIL: map/slice entries intact across GC`：slice backing 或 map bucket 中的节点指针被破坏；
- `FAIL: map range summary matches model`：append/删除/压缩计数与模型分叉，或 map 状态被内存破坏；
- `SIGSEGV` / `fatal error: found bad pointer`：疑似 map/slice 指针位图或写屏障问题；
- 超时：循环未推进或 GC 饥饿。

## 参数

- `UAF_SEED`：决定键访问步长、偏移与压缩/删除目标键；失败信息带出种子；
- 其他 TORTURE_* 参数：本用例不消费。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_map_slice_append_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
