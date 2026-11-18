---
id: l05_map_bytes_churn
dim: L05_alloc_gc
priority: P2
sources:
  - sonic@v1.13.2: decoder/decoder_test.go:47 的反复解码到嵌套 map/interface{} 且每轮触发 GC
tags: [L05, map-churn, map[string][]byte, liveness, gc, torture]
requires: []
runtime: "<10s（默认 3 轮）"
---

# 场景

模拟 JSON/缓存解码后的大 map 生命周期：每轮构建 1500 个 `map[string][]byte` 条目（值长 96–543 字节，内容由 `UAF_SEED` 决定），构建后整表逐 key 校验并显式 GC；随后整表丢弃、分配 2MB 垃圾再 GC；每轮保留少量 value 副本跨轮持有，验证旧表被回收后保活副本不被复用破坏。默认 3 轮，`TORTURE_ROUNDS` 可放大到 20 轮，最后再重建一次完整表做终检。

# 期望行为

1. 每轮 map 长度恰为 1500，全部 key 可查、值长度/内容与 `valueSize`/`fillValue` 生成的期望一致；
2. map 仍被持有时 `runtime.GC()` 后抽查 key 内容不变；
3. 整表丢弃 + 垃圾分配 + 两次 GC 后，此前保留的副本 CRC 与按轮次重算的内容一致；
4. 多轮之间 map 相互独立，旧表释放不影响新表与保活副本；
5. 最后一行输出 `ALL PASS l05_map_bytes_churn`。

# 失败判读

- `key=N missing before GC` / `content mismatch`：map 写入或 value 填充错误；
- `corrupted after GC`：被持有 map/value 被提前回收或写屏障漏标；
- `pinned ... after map drop+GC`：已释放 map 的底层内存被误复用，保活副本被改写（UAF）；
- clobberfree/efence 下集中失败：value 底层数组的生命周期管理有问题。

# 参数

- `UAF_SEED`：控制值长度偏移与字节内容（默认 1，解析失败回退 1），失败汇总行带出种子；
- `TORTURE_ROUNDS`：重建轮数，默认 3，范围 1–20。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_map_bytes_churn/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=10 /tmp/case
```
