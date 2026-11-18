---
id: l13_slice_append_self_overlap
dim: L13_runtime_ds
priority: P1
sources:
  - badger@v4.5.0: stream_test.go:114 `c.kv = c.kv[:0]` 清空长度后复用底层数组重写
  - badger@v4.5.0: y/y_test.go:117 `append(wb[:512], wb[:]...)` 源与目标重叠的 append
  - etcd@v3.5.0: server/mvcc/kvstore_test.go:807 `dst = append(dst, src...)` 多来源拼接
tags: [L13, slice, append, self-overlap, growslice, full-slice-expression, pointer, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟流式编码/归并路径中 `append(s, s...)` 的自重叠用法，以及用 full slice expression 限制容量后追加：

1. `len==cap` 时 `append(s, s...)`：必须扩容，两个半区指向同一批对象；
2. `cap` 有富余时自追加：原地重叠复制，结果序列等于“原序列 + 原序列”；
3. `s[:k:k]` 后自追加：必须分配新底层数组，改写结果槽位不得影响父切片；
4. 源区间与目标区间非首尾对齐（`append(s[5:15], s[10:20]...)`）：按 copy 语义处理重叠；
5. `UAF_SEED` 驱动初始长度，连续 6 次翻倍自追加，校验长度与指针序列。

阶段间执行显式 `runtime.GC()`，校验所有指针元素指向对象的 tag/pad 未被破坏。

# 期望行为

1. 扩容与原地两种路径的长度、容量下限、指针序列均正确；
2. full slice expression 后的 append 与原底层数组断链；
3. 重叠源区间的结果与先快照再拼接一致；
4. 幂次自追加后前后半区逐一相等；
5. GC 后所有指针元素仍有效；最后一行输出 `ALL PASS l13_slice_append_self_overlap`。

# 失败判读

- `grown halves` / `room halves`：自重叠复制方向或长度计算错误；
- `full-slice append aliased parent array`：full slice expression 的 cap 未生效，growslice 判定错误；
- `overlap tail`：重叠区间 memmove 语义错误；
- GC 后 `payload corrupted`：指针元素未被正确扫描（UAF）。

# 参数

- `UAF_SEED`：初始自追加长度与失败输出的种子。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_slice_append_self_overlap/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
