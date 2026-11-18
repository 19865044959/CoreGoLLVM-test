---
id: l01_generic_object_pool
dim: L01_generics
priority: P2
sources:
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/object_pool_test.go:41 每 key 对象池 Push/Close 回调与 Size 归零
  - pgx@v5.7.4: internal/iobufpool/iobufpool_test.go:71 100k 次 Get/Put 穿插 GC，检测缓冲复用与污染
  - kitex@v0.13.0: pkg/mem/span_test.go:87 SpanCache 并发复制与缓存命中/未命中路径
tags: [L01, generics, object-pool, sync.Mutex, reset, reuse, pointer-identity, gc, TORTURE_ROUNDS, UAF_SEED]
requires: [generics]
runtime: "<10s（默认；TORTURE_ROUNDS 放大循环轮数，上限 16）"
---

# 场景

自建泛型 `Pool[T]`（Mutex + free 栈 + New/Reset 回调 + 容量上限），模拟 kitex 对象池与 pgx iobufpool 的借还语义。`Pool[buffer]` 多轮批量 Get/Put，元素带 256B 后备数组；每轮写入标记字节后显式 GC，再校验 in-use 标记、tag 与数据，Put 后校验 Reset 清空长度、标记与后备字节；统计 gets/puts/news/discard 上限。随后 `Pool[node]` 二次实例化，Reset 必须断开 `next` 链，构建 6 节链后 GC 逐节校验，归还后再次 Get 不得看到旧链指针。TORTURE_ROUNDS 放大循环轮数，UAF_SEED 微调轮数。

# 期望行为

1. 每次 Get 返回非 nil、inUse=false、tag 空、len(data)==0、cap>=256；池不超容量、不重复借出同一对象；
2. GC 后使用中的 buffer 标记与数据完好；Put 后 Reset 清空 tag/长度并零化后备数组；
3. gets==puts==cycles*batch，discard==0，news 不超过池容量（无泄漏式新建）；
4. `Pool[node]` 每次 Get 的 next 为 nil，链上逐节字段跨 GC 不变；Put 后 Reset 断开链，下一轮不复用旧指针；
5. TORTURE_ROUNDS 放大后结论不变，最后一行 `ALL PASS l01_generic_object_pool`。

# 失败判读

- `stale tag/data/next`：Reset 未清干净，复用把旧指针带出池；
- `GC 后 payload corrupted`：池内/使用中对象未被正确保活，检查 free 切片与外部持有引用；
- `allocated too many objects`：Get 路径未从 free 复用，或 Put 丢弃了仍在容量内的对象。

# 参数

- `TORTURE_ROUNDS`：默认 1，最大 16；cycles=4*rounds+seed%3；
- `UAF_SEED`：微调循环轮数，失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_object_pool/*.go
UAF_SEED=7 TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
