---
id: l05_graph_batch_release
dim: L05_alloc_gc
priority: P2
sources:
  - goccy/go-json@v0.10.5: decode_test.go:2457 的 TestPrefilled：预填充对象图被解码结果整体替换/丢弃
tags: [L05, object-graph, batch-release, rebuild, liveness, gc, torture]
requires: []
runtime: "<10s（默认 3 轮）"
---

# 场景

每轮批量构建 160 棵对象图，每棵 10 个节点（1 根 + 3 子 + 6 孙），节点含 192 字节 payload、CRC 与 `[]*gnode` 链接；整批 GC 后递归校验，长期保留 4 棵，其余整批丢弃并分配约 3MB 垃圾后 GC，再整批重建新版本图。默认 3 轮，`TORTURE_ROUNDS` 可放大到 12 轮，验证旧图在批量释放/重建与多轮 GC 后仍完整。

# 期望行为

1. 每轮新图共 1600 个节点，根/子/叶的链接数分别为 3/2/0，payload 内容与 CRC 正确；
2. 构建后首次 GC 不破坏任何图；
3. 大批图释放 + GC 后，长期持有的 4 张图仍可递归校验通过；
4. 重建新版本图后再次 GC，新图与所有旧 pin 图同时有效；
5. 最终释放全部新建图后，pin 图数量为 `4×轮数` 且内容不变；
6. 最后一行输出 `ALL PASS l05_graph_batch_release`。

# 失败判读

- `graph=N corrupted after build+GC`：构建期间节点/links 被破坏；
- `pinned ... corrupted after rebuild+GC`：仍被引用的旧对象图被提前回收（UAF），或重建复用了旧节点的内存；
- `rebuilt graph=N corrupted`：重建路径分配/填充错误；
- clobberfree/k8s 下失败：写屏障或堆扫描问题。

# 参数

- `UAF_SEED`：控制 payload 字节模式（默认 1，解析失败回退 1），失败汇总行带出种子；
- `TORTURE_ROUNDS`：轮数，默认 3，范围 1–12。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_graph_batch_release/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=8 /tmp/case
```
