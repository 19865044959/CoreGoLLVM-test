---
id: l05_huge_self_slice
dim: L05_alloc_gc
priority: P1
sources:
  - badger@v4.5.0: y/y_test.go:44 的 TestPageBuffer：512B 页缓冲跨页写，同一大缓冲派生子切片视图
  - pgx@v5.7.4: pgtype/bytea_test.go:86 的 TestPreallocBytes：保留大缓冲 cap，小结果不缩容
  - klauspost/compress@v1.17.11: s2/writer_test.go:579 的 TestWriterBufferDone：10B/100B/10KB/1MB/8MB 尺寸缓冲归还
tags: [L05, huge-object, sub-slice, interior-pointer, liveness, gc]
requires: []
runtime: "<10s"
---

# 场景

模拟页缓冲/大 value 场景：先分配一个 8MB 大对象，再在其上切出 96 个相互相邻或
重叠的子切片、内部 `*byte` 指针和满容量表达式视图。随后丢弃父切片，只靠子切片
与内部指针跨 GC 保活，验证大对象 span、内部指针扫描与别名语义。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_huge_self_slice`；
2. 每个子切片的长度、抽查字节与 FNV-1a 校验和与种子推导值一致；
3. 通过子切片写入立即可在父缓冲同一偏移看到（真实别名）；
4. full-slice 表达式的 `append` 只写自身容量范围，不覆盖相邻字节；
5. 父切片置空并制造垃圾、两次 `runtime.GC()` 后，全部子切片与内部指针仍有效；
6. 仅保留一个内部 `*byte`（无 slice header）时，8MB 对象仍被保活且值正确。

## 失败判读

- `view=... corrupted after parent drop+GC`：大对象被提前回收或子切片底层数组被复用；
- `not aliasing parent` / `tail append overran`：切片容量或 full-slice 表达式语义错误；
- `interior pointer survives...` 失败：内部指针未正确保活大对象，或指针位图漏标；
- `SIGSEGV` / `fatal error: found bad pointer`：内部指针扫描问题。

## 参数

- `UAF_SEED`：决定大对象填充模式、视图偏移与校验和；失败输出带种子；
- 无其他 TORTURE 参数；固定 8MB，单轮完成。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_huge_self_slice/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
