---
id: l18_nested_graph_rewire
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493（TestUnsafeAssumptions：多层视图对象跨多轮 GC 存活）
  - go-redis@v9.8.0: ring_test.go:143（dynamic setting ring shards：分片树热更新、旧 shard 复用/关闭）
tags: [L18, write-barrier, nested-graph, map, slice, interface, cycle, noinline, UAF_SEED]
requires: []
runtime: "<5s"
---

# 场景

老对象图：root 的 kids slice 与 map 持有老 a/b，老 a 的 kids/map/interface 持有老 c，节点间有 peer 环。16 轮中，noinline `attachAll` 每轮创建一个年轻子图（y0 的 kids/map/interface 相互别名，y2.peer 回指老 a），并按 `UAF_SEED` 决定三个 setter 的调用顺序，把同一个年轻子图写入：

1. 老 root 的 kids[0]（老 slice 元素覆写）；
2. 老 root 的 byName["dyn"]（老 map value）；
3. 老 a 的 any 字段（老 interface）。

每轮 GC 后沿所有路径遍历：校验老节点未损坏、年轻子图三层结构、别名关系（slice/map/interface 指向同一子图、内部 kids/map/any/peer 别名）、以及年轻→老回指。

# 期望行为

1. 每轮 GC 后老 root/a/b/c 的 id/name/data、peer 环、老 map/slice 关系全部保持；
2. root.kids[0]、root.byName["dyn"]、a.any 指向同一个年轻子图 y0，且 id 为当轮值；
3. y0.kids/byName/any/peer 的内部别名完整，y2.peer 仍指向老 a；
4. 三种老容器写入年轻指针以及年轻→老回指跨 GC 全部生效；
5. 失败输出带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_nested_graph_rewire`。

# 失败判读

- `young subgraph lost from old slice/map/interface`：对应老容器写年轻指针漏写屏障；
- `young leaves/map aliases/interface alias broken`：子图内部年轻→年轻引用被回收，或对象内存被复用；
- `young->old back-pointer lost`：年轻→老回指被错误清理；
- `old child/root/grandchild corrupted`：老节点自身被破坏，优先查根扫描/堆元数据；
- clobberfree 下 data 变 0xde、efence 下 SIGSEGV：确认 UAF；
- 失败时用 `UAF_SEED=<seed>` 复现 setter 顺序。

# 参数

- `UAF_SEED`：每轮三个 setter 调用顺序的种子（默认 1），不影响成功输出；
- 固定参数：16 轮，每轮一个共享年轻子图（y0→y1/y2，深度 3）。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_nested_graph_rewire/main.go
UAF_SEED=7 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
