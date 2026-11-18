---
id: l05_builder_vs_buffer
dim: L05_alloc_gc
priority: P1
sources:
  - go-redis@v9.8.0: internal/proto/writer_test.go:26 的复用 bytes.Buffer 逐字节编码混合参数
  - fasthttp@v1.59.0: allocation_test.go:10 的预分配缓冲零分配路径
tags: [L05, strings.Builder, bytes.Buffer, reset-reuse, snapshot, gc]
requires: []
runtime: "<10s"
---

# 场景

对同一批 12 轮、每轮 700 个变长分块，分别用 `strings.Builder` 与 `bytes.Buffer` 追加；每轮比较两者长度与逐字节内容，并把 `Builder.String()`、`Buffer.Bytes()` 的独立副本缓存下来，跨后续多轮 GC 后再校验。轮间 Reset 复用同一对象，最后再 Reset 后写 tail，验证旧快照与新内容不共享可写缓冲。

# 期望行为

1. 每轮 Builder 与 Buffer 的 `Len` 均等于累计分块长度，内容逐字节一致；
2. 两者增量 CRC 与最终缓存副本 CRC 一致；
3. Reset 后长度归零，可继续复用；
4. 所有历史快照在后续 GC 与多次 Reset 后长度/CRC 不变；
5. Reset 后写入 tail 并 GC，tail 往返正确且不污染历史快照；
6. 最后一行输出 `ALL PASS l05_builder_vs_buffer`。

# 失败判读

- `builder/buffer differ`：Builder 的 `String()` 别名或 Buffer 的写入路径出错；
- `snapshot ... after GC`：Reset/复用改写了仍被快照引用的底层数组（UAF）；
- `reset did not clear length`：复用契约错误；
- `tail roundtrip`：多次 Reset 后对象状态损坏。

# 参数

- `UAF_SEED`：控制每轮分块长度与内容（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数，固定 12 轮 × 700 分块。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_builder_vs_buffer/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
