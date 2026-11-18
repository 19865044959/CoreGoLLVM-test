---
id: l13_sort_stable_dups
dim: L13_runtime_ds
priority: P1
sources:
  - gorm@v1.30.0: tests/count_test.go:117 对同 name 的查询结果做 `sort.SliceStable`，期望保持输入次序
  - etcd@v3.5.0: server/mvcc/kvstore_test.go:808 `sort.Stable(dst)` 合并多来源 kv 后按 key 稳定排序
  - tailscale@v1.80.2: tka/scenario_test.go:86 AUM 需按祖先→叶顺序 `sort.SliceStable`
tags: [L13, sort, stable, duplicate-key, sort.Search, pointer, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

排序稳定性与重复 key 边界，共 12 轮，每轮 900 个 `*row`（key=i%9，sub=(i/9)%3，seq 唯一）：

1. 按 `UAF_SEED` 打乱输入，记录每个指针的输入序号；`sort.SliceStable` 按 key 排序后校验 key 单调、同 key 元素输入序号递增；
2. 对已有序切片再次稳定排序，指针序列必须完全不变；
3. `sort.Stable` + 自定义 `sort.Interface` 重复稳定性校验；
4. `sort.Sort` 不稳定排序只校验 key 有序与各 key 计数不变；
5. 多键 `(key, sub)` 稳定排序：两项均相同的元素保持输入次序；
6. 在重复 key 的已排序切片上用 `sort.Search` 求每个 key 的下界/上界，区间长度与输入计数一致；
7. 每轮结束 GC，校验排序结果指针序列与 row 内容不变。

# 期望行为

1. 两种稳定排序 API 都保持同 key 元素的输入相对次序；
2. 已有序切片再稳定排序是恒等变换（指针序列不变）；
3. 不稳定排序结果 key 有序且元素多重集合不变；
4. 多键稳定排序的两个维度都正确；
5. sort.Search 上下界在重复 key 上精确；
6. GC 后所有 row 内容与排序顺序不变；
7. 最后一行 `ALL PASS l13_sort_stable_dups`。

# 失败判读

- `order broke`：稳定排序实现未保持相等元素的相对次序；
- `slice keys unsorted`：比较函数或排序结果错误；
- `resort changed order`：已有序输入被非幂等重排；
- `search bounds`：二分边界在重复 key 上计算错误；
- GC 后指针错乱：row 元素未被扫描或交换期间指针丢失（UAF）。

# 参数

- `UAF_SEED`：打乱顺序与 row 内容种子，失败输出带该值。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_sort_stable_dups/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
