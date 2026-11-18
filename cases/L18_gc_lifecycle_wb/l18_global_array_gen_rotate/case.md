---
id: l18_global_array_gen_rotate
dim: L18_gc_lifecycle_wb
priority: P2
sources:
  - nats-server@v2.11.0: server/avl/norace_test.go:33（TestNoRaceSeqSetSizeComparison：500 万元素数组的内存与 GC 观测）
  - badger@v4.5.0: skl/skl_test.go:162（TestConcurrentBasicBigValues：100 goroutine 各写 1MB value 到大 arena）
tags: [L18, global-array, write-barrier, interface, slice, generation-rotation, noinline, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 6 轮；TORTURE_ROUNDS 放大轮数，上限 36）"
---

# 场景

全局 `[3][40]grSlot`，每槽含 `*grNode`、`*grNode kid`、`interface{}`、`[]*grNode kids`、tag/ver，另有全局 `[3][40]*grNode` 扁平视图。流程：

1. 启动时确认 120 个全局槽位全零；
2. 用 noinline `fillGlobalRow` 逐行整行写入第 1 代对象并两轮 GC 晋升；
3. 默认 6 轮（`TORTURE_ROUNDS` 上限 36）按 `row=(round-2)%3` 轮转，每轮 noinline 整行覆写 40 个槽位为新一代对象；
4. 每轮 GC 后校验 120 个槽位的版本、节点内容、`kids[0]==n`、`kids[1]==kid`、`ifc` 动态类型、tag 与扁平视图；
5. 从第 4 轮起断言三行版本互不相同（三代混合）。

# 期望行为

1. 初始全局二维结构体数组所有指针/接口/切片字段为零；
2. 每轮整行覆写只影响目标行，另两行保持原代次内容；
3. 所有槽位的 interface/slice/指针三重别名跨 GC 保持一致；
4. 三行始终保留不同代次对象（第 4 轮后）；
5. 失败输出带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_global_array_gen_rotate`。

# 失败判读

- `header ver=...`：全局数组结构体整体写屏障缺失或串行；
- `kids alias broken`：结构体内 slice 指针字段未被扫描；
- `interface alias broken`：interface 写屏障缺失或 itab 被破坏；
- `flat view diverged`：两个全局根视图不一致，通常是写序或漏写；
- `rows not generation-mixed`：轮转索引错误（用例逻辑问题，先排除）。

# 参数

- `UAF_SEED`：每行槽位填充顺序与数据模式种子；
- `TORTURE_ROUNDS`：轮数（默认 6，上限 36，即最多 12 次整组轮转）。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_global_array_gen_rotate/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=24 UAF_SEED=3 /tmp/case
```
