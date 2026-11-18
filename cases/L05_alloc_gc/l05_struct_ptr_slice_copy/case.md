---
id: l05_struct_ptr_slice_copy
dim: L05_alloc_gc
priority: P1
sources:
  - go-redis@v9.8.0: internal/proto/scan_test.go:25 的 ScanSlice 把行数据批量扫描进 []struct
tags: [L05, slice-of-struct, pointer-fields, append-growth, copy, gc]
requires: []
runtime: "<10s"
---

# 场景

模拟 ORM/协议层批量扫描结果：1200 个 `row` 结构体（含 `string`、`[]byte`、`*item`、`[]string` 指针字段）经多次 append 扩容写入切片；随后取子切片、用 `append([]row(nil), sub...)` 与 `copy` 两种方式复制。每个阶段都插入垃圾分配与 `runtime.GC()`，最后丢弃原始切片，只保留拷贝结果再 GC。

# 期望行为

1. 1200 次 append 后长度正确；容量内 append（`pre` 预分配 8）不更换底层数组；
2. 扩容 + GC 后每个结构体的 `id/name/data/link/extra` 全部与生成规则一致；
3. 子切片与两份拷贝在 GC 后内容一致，且 `cp[i].link == sub[i].link`（copy 保留指针身份）；
4. 满容量切片表达式 + append 触发重分配：旧数组内容不变，新数组末尾新元素正确；
5. 丢弃原切片后，拷贝结果中的指针目标仍存活；
6. 最后一行输出 `ALL PASS l05_struct_ptr_slice_copy`。

# 失败判读

- `row=N corrupted`：结构体复制/扩容时指针或 slice 字段丢失；
- `copy row=N dropped link pointer identity`：copy 路径复制错了指针；
- `append beyond cap reused old backing array` / `old backing row=N changed`：growslice 容量语义错误；
- `retained copy ... after origin drop`：拷贝持有的指针目标被提前回收（UAF）。

# 参数

- `UAF_SEED`：控制 `data` 长度与字节模式（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数，固定 1200 行。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_struct_ptr_slice_copy/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
