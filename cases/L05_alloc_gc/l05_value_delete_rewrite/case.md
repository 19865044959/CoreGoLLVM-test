---
id: l05_value_delete_rewrite
dim: L05_alloc_gc
priority: P1
sources:
  - badger@v4.5.0: value_test.go:207 的大值批量写入、部分删除与全量重写
tags: [L05, large-value, delete, rewrite, liveness, gc]
requires: []
runtime: "<10s"
---

# 场景

模拟 badger value log 的批量写/删/重写：96 个 32KB 大值写入内存 store，随后按种子决定的步长（3 或 4）删除约 1/3 的 key，GC 后校验存活与删除语义；再对所有 key 全量重写为 ver=2 新值，逐批 GC。重写前保留一个旧值指针，验证仍被引用的旧对象不会被误回收。

# 期望行为

1. 首轮写入后 96 个 key 全部存在，CRC 与抽样内容符合 ver=1 模式；
2. 删除后 GC：被删 key 不存在，未删 key 仍完整；
3. 全量重写后 GC：96 个 key 全部恢复且 ver=2、内容/CRC 与新版本一致；
4. 被 `held` 引用的旧 ver=1 对象在多次 GC 后 CRC 与内容不变；
5. 最后一行输出 `ALL PASS l05_value_delete_rewrite`。

# 失败判读

- `should be deleted`：删除语义失效或 map 键管理错误；
- `missing` / `ver=` / `content mismatch`：重写未覆盖或旧值残留；
- `held old value corrupted`：仍被引用的旧大对象被提前回收（UAF）；
- `stored checksum mismatch`：值内容在堆上被破坏。

# 参数

- `UAF_SEED`：控制值字节模式与删除步长（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数，固定 96×32KB。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_value_delete_rewrite/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
