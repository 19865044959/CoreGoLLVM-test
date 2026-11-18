---
id: l10_deepcopy_cycle
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: proto/methods_test.go:172 TestLegacyMergeMethod 深拷贝且源对象不被修改
  - ent@v0.14.4: dialect/sql/scan_test.go:219 TestScanNestedStruct 指针层级与嵌套 struct
  - ent@v0.14.4: dialect/sql/scan_test.go:252 TestScanSlicePtr 指针切片分配
tags: [L10, deepcopy, cycle, aliasing, identity-map, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟生成式 `DeepCopy` 在存在环与共享子图时的实现：先登记身份再递归（`map[*node]*node` / `map[*shared]*shared`），保证环终止、源内相同的指针在副本中仍指向同一对象、源内不同的对象不被错误合并。固定图含 Next 环、Prev 双向环、自引用 Links、跨节点的 Shared/Ref 别名；另做双向修改隔离、nil/空容器语义与 180 棵随机图的 GC 校验。

## 期望行为

1. 固定环状图复制后结构与别名关系保持：`cp.Next.Next.Next == cp`，共享的 Shared/Ref 仍互为别名，自引用 Links 指向副本自身；
2. 副本与源不共享任何 node/shared 对象，也不共享底层 []byte；
3. GC 后源与副本均与快照一致，载荷可读；
4. 修改源（Name/Payload/Shared.Data/Attrs/Links/Empty）后副本与快照完全一致；修改副本后源不受影响；
5. nil 图返回 nil；空（非 nil）Payload/Links/Attrs/Empty 复制后仍非 nil，nil 容器仍为 nil；
6. 180 棵 `UAF_SEED` 随机图（节点 1–14、随机环/自引用/共享指针/attrs）：复制图校验、二次拷贝稳定、GC 后校验、双向修改隔离全部通过；
7. 最后一行 `ALL PASS l10_deepcopy_cycle`。

## 失败判读

- 环导致栈溢出/挂死：递归前未用身份表登记；
- 别名关系丢失：每个引用都新建对象，未查身份表；
- 不同源对象被合并：身份映射反向重复；
- 修改源影响副本：slice/map/[]byte 只复制头部存在别名；
- 空容器变 nil：用 `len(x)` 而非 `x != nil` 判断；
- GC 后崩溃/乱码：副本未持有深层对象或载荷未被扫描。

## 参数

- `UAF_SEED`：随机图形状/共享池/载荷种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_deepcopy_cycle/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
