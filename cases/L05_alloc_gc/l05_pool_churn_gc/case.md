---
id: l05_pool_churn_gc
dim: L05_alloc_gc
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: 大量内存分配/GC 压力
tags: [L05, sync.Pool, slice-reuse, gc]
requires: []
runtime: "<5s"
---

# 场景

模拟 HTTP/序列化库的热点路径：从 `sync.Pool` 取缓冲对象，填充变长数据与指针字段，中间触发 GC，再放回池中。GC 可能清空池，但当前被局部变量持有的对象必须完好。

# 期望行为

1. 每轮 GC 后 `b.p.v == i`、`b.data` 内容与长度不变；
2. 300 轮稳定；
3. 末尾多次 Get 取到旧对象时，`data[0] == byte(p.v)` 的内部不变量成立；
4. 最后一行 `ALL PASS l05_pool_churn_gc`。

# 失败判读

- `pool object lost in GC`：池中对象被引用期间的栈/堆漏标；
- `pool buffer content corrupted`：切片底层数组被误回收或复用时越界；
- clobberfree 下取到 `0xdeadbeef` 类值：UAF。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_pool_churn_gc/*.go
GODEBUG=clobberfree=1 /tmp/case
```
