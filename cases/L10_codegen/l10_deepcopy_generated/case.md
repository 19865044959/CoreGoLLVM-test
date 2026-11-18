---
id: l10_deepcopy_generated
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: proto/methods_test.go:172 Legacy Merge 深拷贝且源对象不被修改
  - ent@v0.14.4: dialect/sql/scan_test.go:219,252 嵌套 struct、指针层级与指针切片的分配
tags: [L10, deepcopy, nested-struct, slice, map, aliasing, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

手写生成风格 `DeepCopyInto`/`DeepCopy`：node 含 Leaf/Ref 指针、tags 切片、props map、children 指针切片与二维 matrix。要求复制后所有层级内存独立、nil 与空容器语义保留、可变对象之间双向修改互不影响。GC 插在复制与读取之间。

## 期望行为

1. 固定树深拷贝与原对象 DeepEqual，递归检查无共享指针/切片/map 底层内存；
2. 源中 Leaf 与 Ref 指向同一对象时，副本中二者各自独立复制；
3. GC 后原对象与副本内容不变；
4. 修改源对象的字符串、tags、map（改值+删键）、leaf blob、matrix、child 名称后，副本与修改前快照仍相等；
5. 修改副本后源对象不变；空（非 nil）容器复制后仍为非 nil 空容器，nil 容器仍为 nil；
6. nil receiver `DeepCopy` 返回 nil；
7. 120 棵 `UAF_SEED` 随机树（深度 4）全部满足：相等、无共享引用、二次拷贝稳定、GC 后存活、源修改不影响副本；
8. 最后一行 `ALL PASS l10_deepcopy_generated`。

## 失败判读

- 相等失败：漏复制某字段或切片元素；
- 共享底层数组：slice/map/指针只赋头未 make+copy；
- 修改源后副本变化：浅拷贝别名（典型生成器 bug）；
- 空容器变 nil：复制前用 `len(x)` 而非 `x != nil` 判断；
- GC 后乱码：副本未持有深层对象。

## 参数

- `UAF_SEED`：随机树形状/叶子/blob/map/matrix 种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_deepcopy_generated/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
