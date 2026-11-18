---
id: l01_generic_alg_edges
dim: L01_generics
priority: P1
sources:
  - pgx@v5.7.4: rows_test.go:335 CollectRows 接收 RowTo[int32] 逐行收集标量，覆盖最小结果集路径
  - gorm@v1.30.0: callbacks/create_test.go:16 反射构造 clause.Values，显式覆盖空 slice 与指针 slice
  - quic-go@v0.50.1: internal/wire/stream_frame_test.go:13 OFF/LEN/FIN 组合、空 payload、offset/LEN 溢出与超长帧拒绝
tags: [L01, generics, algorithm-boundary, empty-single-all-equal, sort-search, large-input, gc, UAF_SEED]
requires: [generics]
runtime: "<10s"
---

# 场景

在空切片、单元素、777 个全相等元素、int8/uint64/string 极值和 2 万元素规模上，
验证泛型 `fold/reduce/minmax/partition/chunk/search/uniqueCount/windows` 的边界
语义；大切片先 `slices.Sort`，穿插显式 GC，再按预计算和/唯一数/分块长度复算。
对应 pgx 最小结果集、gorm 空 slice/指针 slice、quic-go 空 payload 与长度溢出。

# 期望行为

1. 空输入下 fold 返回 init、reduce/minmax 返回 `ok=false`、partition/chunk/search/unique/windows 返回空且不 panic；
2. 单元素下 reduce/minmax/partition/chunk/search/unique/windows 均返回该元素；
3. 全相等 int8 切片的 min==max==42、唯一数为 1、partition 全落一侧、chunk 尾部长度正确；
4. int8/uint64/string 极值排序后可二分命中最大值、未命中越界值；
5. 2 万元素排序后有序，fold 和等于预计算和，min/max 等于首尾，search 对存在/不存在值判定正确；
6. partition 不丢元素（两半和等于总和）、chunk 总长等于 n、window 数量与首尾窗口内容正确、uniqueCount 等于相邻去重计数；
7. GC 后上述和与窗口/分块结果仍一致；
8. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_alg_edges`。

# 失败判读

- `... empty must ...` / `single ... mismatch`：泛型算法边界分支错误；
- `large fold sum mismatch after GC`：大切片元素或聚合结果被破坏；
- `large partition lost elements` / `chunk lost elements`：子切片别名/追加逻辑错误；
- `large search ...`：比较器实例化或二分边界错误；
- `large unique count mismatch`：map 键实例化错误；
- `FAILED ... seed=`：失败输出带 UAF_SEED。

# 参数

- `UAF_SEED`：决定大切片长度（20000..20999），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_alg_edges/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
