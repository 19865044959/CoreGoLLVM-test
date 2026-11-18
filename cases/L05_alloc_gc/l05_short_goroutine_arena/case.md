---
id: l05_short_goroutine_arena
dim: L05_alloc_gc
priority: P2
sources:
  - sonic@v1.13.2: decoder/decoder_test.go:47 的 TestGC：1 万 goroutine 解码后各自 runtime.GC
  - badger@v4.5.0: db_test.go:249 的 TestConcurrentWrite：20 个写协程各写 500 条后统一校验
tags: [L05, goroutine-local, arena, short-lived-goroutine, ownership-handoff, liveness, gc, torture]
requires: []
runtime: "<10s（默认 2 波）"
---

# 场景

每波启动 24 个短命 goroutine，各自分配一块 4×8KB 的局部 arena，按 8B 对齐
写入 40 条记录（id/name/变长 data/next 链）。worker 在局部 arena 存活时执行
两次显式 GC 与垃圾分配，GC 前后重算链摘要；随后只把第 7 条记录的深拷贝与
摘要交回主协程并退出。主协程在 worker 全部退出后 GC，校验返回值不依赖已消亡
的 arena。默认 2 波，`TORTURE_ROUNDS` 可放大。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_short_goroutine_arena`；
2. 每波收齐 24 份结果，worker 无重复/缺失；
3. 每份结果的 digest/count/owned id/name/data 与主协程独立重算一致；
4. GC 前后链摘要相同，记录 data 长度/首尾字节/next 链均正确；
5. 不同 worker 的 ownedData 深拷贝互不别名，改写一个不影响另一个；
6. worker 退出并 GC 后，结果仍可校验（arena 已成垃圾）；
7. 默认 2 波全部通过，`TORTURE_ROUNDS` 放大后 pin 数线性增长且内容不变；
8. 在 base/clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `missing/duplicate worker`：worker 未按波产出或 channel 交接错乱；
- `digest changed across local GC`：goroutine 栈/局部 arena 上的记录被错误回收或复用；
- `data len` / `first byte` / `next link`：arena 切分或链指针被破坏；
- `owned ... want`：深拷贝不完整，结果仍别名已消亡的 arena；
- `owned data aliases between workers`：多个 worker 共用同一底层数组；
- 超时/死锁：结果 channel 容量不足或 goroutine 未退出。

## 参数

- `UAF_SEED`：决定每条记录长度与字节模式（默认 1，解析失败回退 1），失败汇总行带出种子；
- `TORTURE_ROUNDS`：波数，默认 2，范围 1–8；每波固定 24 个短命 goroutine × 40 条记录。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_short_goroutine_arena/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=4 TORTURE_ROUNDS=8 /tmp/case
```
