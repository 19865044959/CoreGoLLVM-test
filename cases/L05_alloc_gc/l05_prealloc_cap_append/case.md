---
id: l05_prealloc_cap_append
dim: L05_alloc_gc
priority: P0
sources:
  - pgx@v5.7.4: internal/pgio/write_test.go:23 的 make([]byte, 0, 4) 后 append 不扩容断言
tags: [L05, make-cap, append-growth, backing-array, slice-reuse, gc]
requires: []
runtime: "<3s"
---

# 场景

对照 `make(cap)` 与 append 增长路径：3000 个含 string 与 `*item` 的结构体分别写入（1）cap 恰为 N 的预分配切片、（2）nil 起步的 append 增长切片、（3）cap=N/2 后分两批 append；另用满容量切片表达式验证 append 超容量时旧数组不变。各阶段均分配垃圾并显式 GC，最后对 label 序列整体 CRC 做 GC 前后比对。

# 期望行为

1. 预分配 N 的切片 N 次 append 后 `cap==N`，且底层数组首元素地址不变；
2. 满容量切片表达式 + append 一次会重分配，旧数组全部元素保持不变，新元素正确；
3. nil 起步增长后 `len==N`、`cap>=len`，GC 后所有指针字段与 label 正确；
4. cap=N/2 的第二批 append 触发增长后内容完整；
5. 全部 label 的 CRC 在 GC 前后一致且非 0；
6. 最后一行输出 `ALL PASS l05_prealloc_cap_append`。

# 失败判读

- `prealloc cap should stay at initial capacity` / `backing array changed`：append 在容量内错误扩容；
- `append beyond cap did not reallocate` 或 `old backing ... changed`：growslice 复用/覆写旧数组；
- `rec=N corrupted after GC`：string/指针字段在扩容或 GC 中丢失；
- `label checksum changed across GC`：结构体内容被破坏。

# 参数

- `UAF_SEED`：控制 `kind` 与 label 期望偏移（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数，固定 3000 条。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_prealloc_cap_append/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
