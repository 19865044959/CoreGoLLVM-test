---
id: l05_iobuf_size_classes
dim: L05_alloc_gc
priority: P0
sources:
  - pgx@v5.7.4: internal/iobufpool/iobufpool_test.go:10 的 256B/512B/…/32MB 尺寸类分配与 32MB+1 边界
tags: [L05, sync.Pool, size-class, large-object, gc]
requires: []
runtime: "<3s"
---

# 场景

模拟 pgx 协议层的 iobufpool：按 2 的幂尺寸类（256B…32MB）池化字节缓冲，超过 32MB 走精确分配。覆盖 0/255/256/257/511/512/513/32MB/32MB+1 等边界，并在持有缓冲期间显式 GC，最后验证池复用与非法尺寸归还不会污染桶。

# 期望行为

1. 各边界请求返回 `len(b)==req`，池化档 `cap(b)` 恰为对应尺寸类（0→0、1/255→256、257→512、513→1024、4097→8192、1MB→1MB）；
2. 0 尺寸缓冲可正常 append；32MB 命中最大池化桶，32MB+1 精确分配且 cap==len；
3. 所有已写入标记的缓冲在 `runtime.GC()` 后内容逐字节不变；
4. 非法 cap（如 1000）的 Put 被丢弃，后续 257 请求仍返回 cap 512 的缓冲；
5. 160 轮动态尺寸借还、32 次 GC 后，长度/容量契约与内容均保持；
6. 最后一行输出 `ALL PASS l05_iobuf_size_classes`。

# 失败判读

- `size-class len/cap`：尺寸类分桶或切片截断错误；
- `content corrupted ... after GC`：持有中的池缓冲底层数组被提前回收/复用；
- `wrong-size Put polluted bucket`：归桶校验失效，池返回了错误容量；
- clobberfree 下内容变成垃圾值：UAF 或对象被错误零化。

# 参数

- `UAF_SEED`：本用例无随机路径，仅解析并在失败汇总行回显；
- 无 TORTURE 参数，规模固定。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_iobuf_size_classes/*.go
GODEBUG=clobberfree=1 /tmp/case
```
