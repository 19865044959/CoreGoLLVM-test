---
id: l07_pointer_slot_arena
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - go-redis@v9.8.0: internal/pool/pool_test.go:71 连接移除后唤醒等待者，槽位对象不得悬挂
  - go-redis@v9.8.0: internal/pool/pool_test.go:307 Get/Put/Remove 并发下连接不重复、不泄漏
  - pgx@v5.7.4: pgxpool/pool_test.go:111 Acquire/Release 连接生命周期
tags: [L07, unsafe, pointer-slots, arena, reuse, generation, write-barrier]
requires: []
runtime: "<10s"
---

# 场景

对象池/注册表把 `[N]unsafe.Pointer` 槽位内嵌在堆 struct 中，free-list 管理分配/释放，代际计数防止 ABA。用例填满槽位（对象只经 unsafe.Pointer 数组可达，且带环形 link）、GC 后逐槽校验；释放部分槽位、保存旧对象类型化引用、GC；复用释放槽位（含经 `unsafe.Add` 得到槽位指针后的 unsafe.Pointer 存储）；最后清空全部槽位，验证 GC 扫描与槽位复用不产生悬挂。

# 期望行为

1. 空 arena 的 free-list 数量为 n，首次 alloc 返回 0；槽位地址与 `unsafe.Add(&slots[0], k*Sizeof(unsafe.Pointer))` 一致；
2. 填满 n 个槽位后 free-list 为空，再 alloc 返回 -1；GC 后每个槽位对象的 id/tag/data/link 全部正确；
3. 按 `i%step` 释放槽位后，释放槽位为 nil、代际 +1，未释放槽位跨 GC 正确；
4. 被释放对象仅由类型化引用保活，GC 后仍可读；
5. 复用释放槽位后 free-list 耗尽，代际恰为 `release 前 + 2`；经槽位指针写入的对象 GC 后正确；
6. 旧对象在槽位复用后仍由 typed ref 保活并可读；sentinel 跨 GC 有效；
7. 清空全部槽位后 free-list 重建为 n；再次填充单槽位、GC 后可读；
8. 最终清空槽位并 GC，仅类型化引用保留的旧对象仍全部正确；
9. 最后一行 `ALL PASS l07_pointer_slot_arena`。

# 失败判读

- 槽位对象在 GC 后错乱/efence 崩溃：unsafe.Pointer 数组未被 GC 扫描，或对象只被槽位引用时被回收；
- link 字段失效：槽位对象的指针字段未按类型扫描；
- 复用后代际不符或槽位重复：free-list/代际逻辑错误；
- 经槽位指针写入后对象丢失：unsafe.Pointer 存储缺少写屏障；
- 仅在 stress/k8s 失败：GOGC=1 下并发标记与写屏障问题。

# 参数

- `UAF_SEED`：决定槽位数（12..20）、释放步长（2..4）与对象标签，失败信息带种子；
- 槽位数组上限 24，按 64 位指针宽度运行（linux/amd64、linux/arm64）。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_pointer_slot_arena/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
