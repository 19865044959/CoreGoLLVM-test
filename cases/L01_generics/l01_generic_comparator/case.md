---
id: l01_generic_comparator
dim: L01_generics
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/order/order_test.go:50 字段排序表与稳定比较函数下的确定序列
  - ent@v0.14.4: entc/gen/graph_test.go:68 图节点/边排序输出确定，依赖比较器全序
  - pgx@v5.7.4: pgtype/array_test.go:8 多维数组元素的确定次序解析
  - ent@v0.14.4: cmd/internal/printer/printer_test.go:17 字段/边两张表按固定顺序输出（确定性排序）
tags: [L01, generics, comparator, stable-sort, container/heap, multi-key, pointer-elements, gc, UAF_SEED]
requires: [generics]
runtime: "<10s"
---

# 场景

定义泛型比较器 `cmpFn[T]`，用 `then`（多键组合）与 `reversed` 构造"dept 升序、score 降序、name 升序"的全序，对 UAF_SEED 洗牌后的 `[]*rec` 做 `sort.SliceStable` 稳定排序；以独立插入排序给出参照顺序，逐项比较指针序列，并验证相等元素保持输入相对次序。随后把排序结果压入泛型 `binHeap[T]`（实现 container/heap.Interface），在压入中途和弹出中途显式 GC，校验弹出顺序、唯一性与 payload。另以 `binHeap[int64]` 二次实例化，并表驱动校验 then/reversed 语义。

# 期望行为

1. 稳定排序结果与独立插入排序的指针序列逐项一致，相邻元素比较器结果非递减；
2. 比较器相等的相邻元素保持输入相对顺序（稳定性）；GC 后每个指针的 blob 未被破坏；
3. 泛型堆弹出全序正确、无重复、数量等于输入，GC 前后 payload 完好；
4. `binHeap[int64]` 弹出序列非递减；
5. `reversed` 反转符号、`then` 依次使用第一/第二关键字、全等返回 0。

# 失败判读

- `stable sort pointer mismatch`：比较器组合/稳定性错误，或 GC 后指针表被破坏；
- `stable sort reordered equal elements`：误用非稳定排序或比较器把 seq 纳入比较；
- `heap pop order violated`：Less 严格性错误（应使用 cmp<0）或 container/heap 接口实现错误。

# 参数

- `UAF_SEED`：决定元素数量（240+seed%24）与初始洗牌顺序；期望值由参照排序推导，失败输出带种子；
- 无 TORTURE 参数（P1，数据量固定）。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_comparator/*.go
UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
