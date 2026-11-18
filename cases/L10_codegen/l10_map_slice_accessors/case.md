---
id: l10_map_slice_accessors
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: proto/repeated_test.go:19 TestOpenSetRepeatedNilReceiver repeated 字段 setter 在 nil receiver 上 panic
  - protobuf-go@v1.36.6: proto/repeated_test.go:26 TestOpenSetRepeated nil 与空 repeated 字段的 get/set 语义
  - protobuf-go@v1.36.6: proto/merge_test.go:390 merge map fields 的 map 合并/拷贝语义
  - sqlc@v1.28.0: internal/endtoend/testdata/emit_enum_valid_and_values/go/query.sql.go:52 生成行循环 append 到 slice
  - ent@v0.14.4: dialect/sql/scan_test.go:252 指针切片扫描与逐元素分配
tags: [L10, L13, container-accessors, map, slice, nil-vs-empty, aliasing, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

手写生成器风格的容器访问器：`GetTags/SetTags/AppendTags`、`GetLabels/PutLabel/DeleteLabel/HasLabel`、`GetItems/AddItem/ItemAt/SetItems`、`GetIndex/PutIndex/RemoveIndex` 以及 `Snapshot`。访问器返回活视图，setter 接管调用方 slice/map 引用；`Snapshot` 走深拷贝。验证 nil receiver、nil 与空容器、调用方别名、确定性排序、源释放后的视图存活，并用独立模型对随机操作序列对账。

## 期望行为

1. nil receiver 的 Get/Len/ItemAt 安全返回 nil/0；Set/Append/Put/Delete/Add 均 panic（recover 捕获）；
2. `SetTags(nil)` 保持 nil，`SetTags([]string{})` 保持非 nil 空；`SetItems` 同理；`AddItem(nil)` 保留一个 nil 元素；
3. `GetTags/GetLabels/GetItems/GetIndex` 返回活视图：经视图修改元素、map 键值、item blob 对消息可见；setter 不复制调用方 slice；
4. `PutLabel` 覆盖/`DeleteLabel`/`HasLabel` 与 `SortedLabelKeys`、`SortedIndexIDs` 顺序确定；`PutIndex` 同键覆盖；
5. `Snapshot` 与源逐项相等且不共享 tags/labels/items/blob/index 内存；非 nil 空容器与 nil 容器语义保留；nil receiver Snapshot 为 nil；
6. 丢弃消息后只保留访问器视图，多轮 GC 后 tags/labels/items/index/blob 仍可读；快照在源释放后仍完整；
7. 320 次 `UAF_SEED` 随机操作（append/set/put/delete/add/视图修改/clear）与独立模型逐项一致，周期性 GC 与快照复核通过；
8. 最后一行 `ALL PASS l10_map_slice_accessors`。

## 失败判读

- getter 在 nil receiver panic：缺少 receiver 判空（setter 的 panic 是期望行为）；
- nil/空语义错：用 `len(x)` 而不是 `x != nil` 判断；
- 视图修改不可见：getter 返回了副本，与生成代码语义不符；
- setter 后调用方修改不可见：setter 做了额外复制；
- GC 后崩溃/乱码：视图或快照未保活底层数组/元素；
- 随机失败可凭 `seed=` 复现并定位操作序号。

## 参数

- `UAF_SEED`：随机操作序列种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_map_slice_accessors/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
