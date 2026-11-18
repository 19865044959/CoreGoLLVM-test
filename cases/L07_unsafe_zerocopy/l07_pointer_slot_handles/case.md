---
id: l07_pointer_slot_handles
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - go-redis@v9.8.0: internal_test.go:86 TestRingSetAddrsAndRebalanceRace 分片表重整时替换指针
  - pgx@v5.7.4: pgxpool/pool_test.go:126 Hijack 后连接从池中移除，池计数下降
  - go-redis@v9.8.0: internal/pool/pool_test.go:307 Get/Put/Remove 并发操作连接槽位
tags: [L07, unsafe, pointer-slots, stable-handle, compaction, write-barrier]
requires: []
runtime: "<10s"
---

# 场景

指针槽位与稳定句柄：堆上 `[32]unsafe.Pointer` 槽位表 + 逻辑句柄（id→槽位映射）。填满对象后 GC 校验；删除部分槽位，保留旧对象类型化引用；把存活指针压缩到前部并用 unsafe.Pointer 写入 + 句柄重映射；复用尾槽位；最后只保留槽位子切片与句柄、丢弃 table 变量，GC 后解析所有对象，再清空槽位验证 typed refs 仍可读。

## 期望行为

1. 槽位地址与 `unsafe.Add(&slots[0], k*Sizeof(unsafe.Pointer))` 一致，`unsafe.Slice` 视图与槽位表逐项同址；
2. 填满后 GC，每个句柄解析到的对象 id/tag/data/ref 全部正确；
3. 删除槽位后槽位为 nil、句柄失效，被删对象仅由 typed ref 保活且 GC 后可读；
4. 压缩后存活对象连续位于前部，句柄重映射正确，尾部槽位为 nil，槽位视图看到压缩结果；
5. 复用尾槽位写入新对象后 GC，新对象与旧对象都正确；
6. 丢弃 table 变量、只保留槽位视图与子视图并 GC 后，所有槽位对象仍可解析，子视图与主视图一致；
7. 清空槽位后 GC，typed refs 与释放对象仍完整；
8. 末行 `ALL PASS l07_pointer_slot_handles`。

## 失败判读

- 槽位对象在 GC 后错乱/efence 崩溃：unsafe.Pointer 槽位未被扫描，对象被提前回收；
- 压缩后句柄指向错误对象：重映射逻辑或槽位写入错误；
- 新对象丢失：unsafe.Pointer 存储缺少写屏障；
- 子视图与主视图不一致：子切片 data 指针计算错误；
- 删除后仍能解析：句柄失效逻辑错误；
- 仅 stress/k8s 失败：GOGC=1 下并发标记与槽位写入问题。

## 参数

- `UAF_SEED`：决定槽位数（16..24）、删除步长（3/4）与对象内容，失败信息带种子；
- 无 TORTURE 参数；槽位数组上限 32，按 64 位指针宽度运行。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_pointer_slot_handles/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
