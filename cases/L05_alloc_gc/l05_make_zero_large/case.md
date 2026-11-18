---
id: l05_make_zero_large
dim: L05_alloc_gc
priority: P0
sources:
  - sonic@v1.13.2: internal/rt/stubs_test.go:12 的 TestMakeSlice：含指针/不含指针切片的分配与零化语义
  - pgx@v5.7.4: pgtype/bytea_test.go:86 的 TestPreallocBytes：预分配缓冲复用时 len/cap 语义
tags: [L05, make-zeroing, large-array, pointer-bitmap, clobberfree, span-reuse, gc]
requires: []
runtime: "<3s"
---

# 场景

分配四类数 MB 的对象：65536 个含 `*znode`/string/[]byte 的 cell、262144 个
`*znode`、8192 个含 `[256]byte` 的 struct、6MB byte blob，全部必须在 make 后
全零。写满后丢弃，制造大量垃圾并多轮 GC；再按相同尺寸分配第二代并验证全零，
随后重新填充、GC 后校验；最后再释放并分配第三代，确认已释放 span 被复用时
仍会被零化。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_make_zero_large`；
2. 第一代、第二代、第三代的 `make` 结果中指针为 nil、string 为空、slice 为 nil、标量为 0；
3. 大 byte blob 在所有代中全零（逐字节 OR 检测）；
4. 第一代写满并 GC 后，cell 的指针/字符串/切片/标量与 fixed 数组边界字节正确；
5. 第二代复用第一代释放的 span 后仍全零，填充并 GC 后内容正确；
6. 在 clobberfree（释放内存被填充为垃圾值）profile 下，新分配仍全零；
7. 大对象分配/释放循环不崩溃、不超时。

## 失败判读

- `fresh ... not zeroed` / `reused-span ... not zeroed`：make/makeslice 未走 memclr，或 span 复用未清零；
- `cell=... corrupted after GC`：含指针大数组的堆扫描/位图错误；
- `fresh byte blob not zeroed`：大对象（>32KB）零化路径缺陷；
- `fixed ... buf`：内嵌定长数组边界被破坏；
- clobberfree 下 only 第二代失败：已释放 span 被复用时未以零初始化。

## 参数

- `UAF_SEED`：决定填充字节与标量偏移（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数；固定 65536 cells + 262144 指针 + 8192 fixed + 6MB blob × 3 代。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_make_zero_large/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
