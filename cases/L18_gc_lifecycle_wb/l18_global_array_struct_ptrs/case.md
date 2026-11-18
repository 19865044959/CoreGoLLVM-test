---
id: l18_global_array_struct_ptrs
dim: L18_gc_lifecycle_wb
priority: P2
sources:
  - nats-server@v2.11.0: server/norace_test.go:8483（TestNoRaceClientOutboundQueueMemory：大规模订阅者共享指针，GC 前后内存校验）
  - badger@v4.5.0: skl/skl_test.go:162（TestConcurrentBasicBigValues：并发把大 value 写入共享 arena）
tags: [L18, write-barrier, global-array, struct-array, pointer-fields, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 4 轮；TORTURE_ROUNDS 放大轮数，上限 24）"
---

# 场景

全局二维结构体数组 `[48][8]gslot`，每个槽位含 `*gnode`、`string`、`[]byte`、`interface{}` 与版本号；另有 384 项扁平 `*gnode` 视图同步维护。流程：

1. 进程启动时先确认静态全局数组全零；
2. noinline `setGlobalSlot` 整组写入第 1 代对象并两轮 GC 晋升；
3. 默认 4 轮（`TORTURE_ROUNDS` 可放大）按 `UAF_SEED` 随机选择约 1/3 槽位，用 noinline setter 覆写为新一代年轻对象；
4. 每轮 GC 后校验全部 384 个槽位的节点值、tag、[]byte 模式、interface 断言、flat 视图指针一致性。

# 期望行为

1. 初始全局结构体数组所有指针/字符串/切片/接口字段为零；
2. 整组晋升后 GC 不丢失任何槽位对象；
3. 每轮稀疏覆写后，已写槽位为当轮代次，未写槽位保持上一代；`gFlat[slot]` 始终与槽位 `n` 指针相同；
4. 结构体数组赋值中 string/slice/interface/指针四类写屏障均生效；
5. 失败输出带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_global_array_struct_ptrs`。

# 失败判读

- `global array slot N node/data/interface corrupted`：全局结构体数组写年轻对象漏写屏障，或全局 gcprog 漏标指针字段；
- `global flat view diverged`：数组根或扁平指针视图被破坏，优先查全局根扫描与写屏障；
- `initial global array slot N not zeroed`：全局 BSS 零化被破坏；
- clobberfree 下 tag/data 变垃圾、efence 下 SIGSEGV：确认 UAF；
- 失败时用 `UAF_SEED=<seed>` 复现稀疏槽位选择。

# 参数

- `UAF_SEED`：每轮随机选择覆写槽位的种子（默认 1），不影响输出文本；
- `TORTURE_ROUNDS`：覆写轮数（默认 4，上限 24）；每轮约更新 128 个槽位并全量校验。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_global_array_struct_ptrs/main.go
UAF_SEED=7 TORTURE_ROUNDS=16 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
