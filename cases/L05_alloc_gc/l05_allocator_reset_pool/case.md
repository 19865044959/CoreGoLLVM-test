---
id: l05_allocator_reset_pool
dim: L05_alloc_gc
priority: P1
sources:
  - badger@v4.5.0: y/y_test.go:301 的 Allocator Reset 后循环编码与 GC 校验
tags: [L05, sync.Pool, allocator-reset, uvarint, crc32, gc]
requires: []
runtime: "<10s"
---

# 场景

模拟 badger `y.Allocator`：`sync.Pool` 复用 byte slice，每轮 Reset（长度归零、容量保留）后追加编码一条记录（uvarint id、key、变长 value、CRC32），立即解码校验。600 轮中每 20 轮制造垃圾并 `runtime.GC()`；每 50 轮缓存一份编码副本，GC 后再解，验证池缓冲复用未改写历史结果。

# 期望行为

1. 每次从池中取出的缓冲 `len==0`，Reset 后编码从下标 0 开始；
2. 600 条记录往返后 id/key/value/CRC 全部一致；
3. 缓存副本在多次 GC 后仍能解出原 key、原长度与原 CRC；
4. 池被 GC 清空后再次借出，New 对象与旧对象都满足 len=0、cap>=64，写入后内容正确；
5. 最后一行输出 `ALL PASS l05_allocator_reset_pool`。

# 失败判读

- `pooled buffer not reset`：归还/借出路径未清零长度，编码覆盖旧前缀；
- `decode failed` / `value mismatch`：追加写越界或切片复用导致记录串写；
- `snapshot ... after GC`：缓存副本指向了被复用的池缓冲；
- clobberfree 下 checksum 错：UAF 或未初始化内存被读入编码。

# 参数

- `UAF_SEED`：控制 value 长度与内容（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数，固定 600 条记录。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_allocator_reset_pool/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
