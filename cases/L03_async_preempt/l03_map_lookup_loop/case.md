---
id: l03_map_lookup_loop
dim: L03_async_preempt
priority: P0
sources:
  - tailscale@v1.80.2: net/art/table_test.go:578 1 万个前缀建树后做 1 万次随机地址查找，长循环中插入 runtime.GC()
  - etcd@v3.5.0: client/pkg/types/set_test.go:27 map[string]struct{} 的 Contains/Values/Length 全套断言
tags: [L03, async-preempt, map-lookup, pointer-chase, hit-miss, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

构建 8192 项的 `map[int64]*mlNode`（插入顺序由 `UAF_SEED` 随机排列），节点各自持有指向下一节点的 `next` 指针。主 goroutine 用可复现 LCG 生成 524288 个 key（key 空间比 map 大 2048，因此命中与未命中混合），每轮在 map 中查找并按命中/未命中做固定校验。每 65536 次查找分配垃圾并显式 `runtime.GC()`；查找通常返回指针，代码在 GC 之后继续解引用该指针及其 `next`。另有一个 GC pump 并发执行 8 轮 GC。

# 期望行为

1. 进程退出码为 0；
2. 最后一行输出 `ALL PASS l03_map_lookup_loop`；
3. 命中次数与重放 LCG 得到的期望命中次数一致，命中+未命中等于迭代总数；
4. 每次命中取回的节点 `v`/`w`/`next.v` 与 key 推导值一致；
5. 命中/未命中的累加校验和与参考值相等；
6. 每 65536 次查找发生一次显式 GC（共 8 次），并发 GC 至少完成一轮；
7. map 长度及全部节点字段、`next` 环在 GC 后保持完好；
8. 在 base/clobberfree/efence/k8s 等 profile 下输出与校验结果一致。

## 失败判读

- `FAIL: map lookup returned intact nodes`：map 返回的指针错误或节点字段被破坏，优先查 GC/写屏障；
- `FAIL: map lookup checksum after GC`：查找结果跨 GC 后丢失或读到悬空对象；
- `FAIL: hit/miss accounting`：LCG 或 key 空间计算错误（用例/环境问题）；
- `FAIL: map nodes intact after GC`：节点或指针环被破坏；
- 超时：GC pump 未启动或 map 查找死循环。

## 参数

- `UAF_SEED`：决定 map 插入顺序与 LCG 初始状态；失败信息带出种子；
- 无其他放大参数，P0 固定规模，保证 <3s。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_map_lookup_loop/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
