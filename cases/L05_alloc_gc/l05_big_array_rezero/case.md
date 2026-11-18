---
id: l05_big_array_rezero
dim: L05_alloc_gc
priority: P1
sources:
  - sonic@v1.13.2: internal/rt/stubs_test.go:12 的 TestMakeSlice：含指针/不含指针切片的分配与零化语义
  - pgx@v5.7.4: pgtype/bytea_test.go:86 的 TestPreallocBytes：预分配缓冲复用时 len/cap 语义
tags: [L05, make-zeroing, large-array, pointer-bitmap, clobberfree, span-reuse, gc]
requires: []
runtime: "<10s"
---

# 场景

反复 `new` 固定大小数组，覆盖 32KB 大对象阈值两侧：[4095]*node 与
[4097]*node、[512]/[600] 含指针 cell、[2048]any、约 1MB 的 [1024]含 blob
struct 数组。每代分配后先验证全零，再写满指针/字符串/切片/接口并显式 GC
校验；偶数代 pin 住，奇数代丢弃，使后续分配复用已释放 span，再验证仍全零。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_big_array_rezero`；
2. 每代六类新数组在写入前全部为零：指针 nil、string 空、slice nil、interface nil、blob 全零；
3. 写满并 GC 后，抽样元素的指针/字符串/切片/接口内容与生成规则一致；
4. 丢弃一代并 GC 后再分配同型数组，仍满足全零不变量（含 32KB 阈值两侧）；
5. 约 1MB 大数组的零化路径在 clobberfree 下仍正确；
6. 偶数代 pin 住的数组跨后续 GC 后内容不变；
7. 最后一轮重新分配 cell 与 fixed 数组仍全零；
8. 在 base/clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `non-zero pointer` / `non-zero cell` / `non-zero any`：make/new 未走 memclr；
- `non-zero fixed blob`：大对象零化路径缺陷；
- `non-zero fixed`：含字符串/指针的复合数组未完整零化；
- `nil ptr` / `cell ptr id` / `any int`：填充后 GC 破坏指针或接口数据字；
- `... after GC` / `pin gen=`：仍被 pin 的数组被提前回收或复用；
- clobberfree 下仅后续代失败：已释放 span 复用未清零。

## 参数

- `UAF_SEED`：决定填充字节模式（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数；固定 4 代 × 6 类数组。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_big_array_rezero/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
