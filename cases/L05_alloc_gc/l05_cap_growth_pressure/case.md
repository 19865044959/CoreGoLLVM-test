---
id: l05_cap_growth_pressure
dim: L05_alloc_gc
priority: P1
sources:
  - pgx@v5.7.4: internal/pgio/write_test.go:23 的 TestAppendUint16BufWithCapacityDoesNotAllocate：cap 足够时 append 不扩容
  - fasthttp@v1.59.0: round2_64_test.go:10 的 TestRound2ForSliceCap：切片容量向上取整到 2 的幂
tags: [L05, cap-growth, slices-Grow, insert-delete, pointer-elements, clone, gc]
requires: []
runtime: "<10s"
---

# 场景

用 `slices.Grow` 预留容量、`append` 填 193 个指针元素，连续 90 代；期间每 7 代
`Insert` 5 个元素、每 11 代 `Delete` 7 个元素，并偶发截断到一半后继续增长。
维护同步的预期 id 序列，每 10 代在垃圾分配 + 两次 `runtime.GC()` 前后逐项
校验。最后 `Clone`/`Concat` 复制指针切片，GC 后校验副本、原切片和 pin 节点。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_cap_growth_pressure`；
2. `slices.Grow(n)` 不改变 len，且 `cap>=len+n`；
3. 每代 append/Insert/Delete 后，元素 id/name/data 与预期序列完全一致；
4. 每 10 代显式 GC 前后，所有指针目标内容不变，无错位/丢元素；
5. 截断后继续增长不残留旧元素，顺序仍与预期一致；
6. Clone/Concat 保留指针身份，GC 后副本与原切片内容一致；
7. 丢弃原切片后 pin 节点仍有效。

## 失败判读

- `Grow changed len` / `cap < len+n`：Grow 路径容量语义错误；
- `id=` / `data byte`：扩容或移动元素时指针/数据丢失、错位；
- `len=... want=...`：Insert/Delete 的位移或截断路径错误；
- `pointer identity`：Clone/Concat 复制了指针值但身份错误；
- `pinned ... corrupted`：仍被引用的节点被提前回收（UAF）。

## 参数

- `UAF_SEED`：决定节点 data 长度与字节内容（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数；固定 90 代 × 193 元素。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_cap_growth_pressure/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
