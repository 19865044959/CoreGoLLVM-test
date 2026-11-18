---
id: l05_builder_pool_parallel
dim: L05_alloc_gc
priority: P1
sources:
  - go-redis@v9.8.0: internal/proto/writer_test.go:26 的 Describe("WriteBuffer")：复用 bytes.Buffer 逐项编码参数
  - badger@v4.5.0: y/y_test.go:70 的 TestBufferWrite：缓冲逐轮增长写入并对照
tags: [L05, sync.Pool, strings.Builder, concurrency, reset-reuse, gc]
requires: []
runtime: "<10s"
---

# 场景

6 个并发 worker 共享一个 `sync.Pool` 的 `*strings.Builder`。每 worker 执行 36
轮：借出时断言 Len 为 0，Reset 后追加 48 个变长分块，计算 CRC 并保存
`strings.Clone` 快照，随后归还；借出表禁止同一 builder 同时出现在两个 worker
手中。每 9 轮并发触发一次显式 GC 与本地垃圾分配。最后保留 4 个 builder 跨多轮
GC 校验。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_builder_pool_parallel`；
2. 6×36 份结果的 worker/round 索引、长度与 CRC 与主协程独立重算一致；
3. 每次借出的 builder Len 均为 0，同一对象不会被两个 worker 同时持有；
4. 并发 GC 可能清空池，走 New 或旧对象的 builder 都必须满足 Reset 契约；
5. 全部快照在后续 GC 后内容不变，不复用池底层写缓冲；
6. 4 个未归还 builder 跨 GC 后内容完整，归还后再次借出 Len 为 0；
7. 结束时借出表为空；
8. 在 base/clobberfree/efence/stress/k8s 等 profile 下结果一致（含 GOMAXPROCS=1）。

## 失败判读

- `builder checked out twice`：池在并发路径重复借出同一对象；
- `pooled builder len=`：归还前未 Reset 或池污染；
- `snapshot crc` / `n=... want=`：并发写入串数据或快照别名池缓冲；
- `leased ... checksum`：仍被租用的 builder 被 GC 破坏或提前复用；
- `builders still checked out`：worker 异常退出/泄漏；
- 仅 race 检测报错：借出表或结果槽位存在数据竞争。

## 参数

- `UAF_SEED`：决定每个分块的长度与字节内容（默认 1，解析失败回退 1），失败输出带种子；
- 无 TORTURE 参数；固定 6 worker × 36 轮 × 48 分块 + 4 个长期租用对象。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_builder_pool_parallel/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOMAXPROCS=1 GOGC=1 /tmp/case
```
