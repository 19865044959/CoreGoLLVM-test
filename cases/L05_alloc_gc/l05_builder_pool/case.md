---
id: l05_builder_pool
dim: L05_alloc_gc
priority: P1
sources:
  - go-redis@v9.8.0: internal/proto/writer_test.go:26 的 Describe("WriteBuffer")：复用 bytes.Buffer 逐项编码参数
  - badger@v4.5.0: y/y_test.go:70 的 TestBufferWrite：32B 起步逐轮翻倍写入并与 bytes.Buffer 对照
tags: [L05, sync.Pool, strings.Builder, bytes.Buffer, reset-reuse, snapshot, gc]
requires: []
runtime: "<10s"
---

# 场景

`sync.Pool` 缓存 `*buildPair`（内含 `strings.Builder` 与 `bytes.Buffer`）。
600 轮中每轮 Get、Reset、追加 40 个变长分块，两者长度/逐字节/CRC 必须一致；
用 `strings.Clone` 与切片拷贝保存独立快照后归还。每 41 轮插入垃圾分配 +
两次 `runtime.GC()`，使池可能被清空。另租用 8 个对象不归还，跨 GC 后再校验，
最后连续 32 次 Reset 后只写 `X`，确认无旧内容串入。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_builder_pool`；
2. 每次 Get 后 Builder/Buffer 的 Len 均为 0；写入后两者 Len 相同且等于累计长度；
3. Builder/Buffer 内容逐字节一致，CRC 与增量 CRC 一致，`Cap>=Len`；
4. 全部 600 份独立快照在池被多次 GC 清空后长度/CRC 不变；
5. 8 个租用中的对象跨 GC 后内容仍完整，归还后可再次复用；
6. 每次 Reset 后再写短串只得到该短串，无旧数据泄漏；
7. 在 base/clobberfree 等 profile 下结果一致。

## 失败判读

- `pooled builder not reset` / `reset did not clear`：Put/Get 契约失效；
- `builder/buffer differ` / `checksum`：写入路径或 String/Bytes 视图状态错误；
- `snapshot ... after GC`：快照仍别名池对象底层数组（UAF/错误复用）；
- `post-reset bleed`：Reset 未清理逻辑状态，旧内容串入新消息；
- `leased ...`：租用中的对象被池/GC 破坏。

## 参数

- `UAF_SEED`：决定每轮分块长度与字节内容（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数；固定 600 轮 × 40 分块 + 8 个租用对象。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_builder_pool/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
