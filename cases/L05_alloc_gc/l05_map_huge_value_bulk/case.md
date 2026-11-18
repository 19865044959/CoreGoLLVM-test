---
id: l05_map_huge_value_bulk
dim: L05_alloc_gc
priority: P2
sources:
  - badger@v4.5.0: value_test.go:337 的 TestValueGC3：100 个 32KB 大值写入与遍历
  - badger@v4.5.0: skl/skl_test.go:162 的 TestConcurrentBasicBigValues：大 value 分配与 GC 压力
tags: [L05, map-churn, huge-object, large-value, liveness, gc, torture]
requires: []
runtime: "<10s（默认 2 轮）"
---

# 场景

模拟大 value 存储的宽 map 生命周期：`map[int64]*blob` 写入 900 个 36–44KB
（均超过 32KB 大对象阈值）的独立大对象，值内含 string、[]byte 与 CRC。建表后
显式 GC 并逐项校验；删除 1/8、覆盖 1/8 为 ver=2 后再 GC；随后整表丢弃，仅保留
4 个 pin 与覆盖前的旧版本值跨 GC，最后重建下一轮。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_map_huge_value_bulk`；
2. 每轮建表后 map 长度恰为 900，值的 id/ver/tag/长度/首尾字节/CRC 全部正确；
3. 删除后对应 key 不存在；覆盖项为 ver=2，其余项保持 ver=1；
4. GC 后所有存活大 value 内容不变，旧版本值仍被 held 持有且完整；
5. 整表丢弃 + 大块垃圾 + 两次 GC 后，pin 与 held 的大 value 仍可校验；
6. 每轮 pin 数量固定，重建的新 map 不影响旧 pin；
7. 在 base/clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `missing` / `len=` / `tag=`：map 写入或大 value 分配路径错误；
- `crc=` / `first byte` / `last byte`：大对象内容被提前回收/复用（UAF）或写屏障漏标；
- `deleted ... still present`：删除语义错误；
- `overwritten ... ver=`：覆盖未生效或旧值残留；
- `pin after drop+GC` / `held after drop+GC`：仍被外部引用的 value 被回收；
- OOM/超时：`TORTURE_ROUNDS` 过大，按默认 2 轮重试。

## 参数

- `UAF_SEED`：决定每个 value 的长度与字节模式（默认 1，解析失败回退 1），失败输出带种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–6；放大后每轮重建 900 个 36–44KB 大对象。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_map_huge_value_bulk/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=6 /tmp/case
```
