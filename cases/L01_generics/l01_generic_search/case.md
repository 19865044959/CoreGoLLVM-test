---
id: l01_generic_search
dim: L01_generics
priority: P2
sources:
  - protobuf-go@v1.36.6: internal/order/order_test.go:50 字段排序表（extension/oneof/number 顺序）与稳定比较函数
  - pgx@v5.7.4: rows_test.go:335 CollectRows 收集指针结果后按列查找/匹配
  - ent@v0.14.4: entql/types_test.go:14 谓词表达式树按字段过滤与查找
tags: [L01, generics, sort.Slice, sort.Search, binary-search, pointer-elements, gc, TORTURE_ROUNDS, UAF_SEED]
requires: [generics]
runtime: "<3s（默认 192 项；TORTURE_ROUNDS 放大项数，上限 32）"
---

# 场景

泛型 `SortBy[T]`（`sort.Slice`）、手写 `LowerBound[T]` 与 `FindFirst[T]`（`sort.Search`）处理 `[]*Item` 指针元素。用 UAF_SEED 驱动的 LCG 洗牌生成唯一 key 的指针数组，GC 后排序，逐项校验 key 序列/名字/权重与总和，再做命中/未命中二分查找，再次 GC 后复查找；另对 `[]*num` 二次实例化排序。对应 protobuf-go 字段排序表、pgx 结果收集查找与 ent 谓词过滤场景。

# 期望行为

1. GC 后排序结果 key 严格为 `7,10,13,...`，每个指针的 Name/Weight 正确，权重总和等于洗牌前复算值；
2. 首/中/尾 key 的 `LowerBound` 全部命中且索引正确；缺失 key 的插入位置为 1 / 0 / n；
3. GC 后再查找仍命中，元素指针载荷不变；
4. `FindFirst` 谓词首次匹配索引与线性扫描一致；
5. 第二实例化 `SortBy[*num]` 有序，`LowerBound` 正确；
6. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_search`。

# 失败判读

- `sorted key[...]` / `sorted item ... corrupted`：排序器比较回调或指针元素解引用在实例化后错误；
- `sorted sum mismatch`：元素丢失/重复（指针被回收或比较函数读写错位）；
- `LowerBound ...`：`sort.Search` 谓词单调性被破坏或元素指针在 GC 后指向错误对象；
- `post-GC ... corrupted`：排序切片里的指针未被扫描；
- `FAILED ... seed=`：失败输出带 UAF_SEED，可复现同一洗牌顺序。

# 参数

- `UAF_SEED`：LCG 洗牌种子（决定初始顺序，不影响期望值），失败输出带种子；
- `TORTURE_ROUNDS`：项数倍数（默认 1 → 192 项；上限 32 → 6144 项），用于长压测。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_search/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=8 UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
