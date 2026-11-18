---
id: l03_ptr_chase_chain
dim: L03_async_preempt
priority: P2
sources:
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:359 随机区间插入/查询/删除 + 递归 Visit，制造指针遍历与栈压力
  - tailscale@v1.80.2: net/art/table_test.go:578 1 万个前缀建 ART，1 万次随机地址查找与慢速参考实现比对
tags: [L03, async-preempt, pointer-chase, linked-chain, permutation, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

用 `UAF_SEED` 生成的 16384 元排列把节点串成单环链表（每个节点含 `v`、`idx`、`next`）。每轮从 head 出发完整追一圈：逐节点解引用 `next` 并累加 `v ^ idx`，共 16384 次依赖加载，循环体无函数调用。每 8 圈显式 `runtime.GC()`，另有 GC pump 并发执行 8 轮 GC，GC 可落在任意一次指针追逐中间。默认 512 圈，`TORTURE_ROUNDS` 可放大；结束后再走一圈标记全部节点，验证链仍是覆盖所有节点的单环。

# 期望行为

1. 进程退出码为 0；
2. 最后一行输出 `ALL PASS l03_ptr_chase_chain`；
3. 每圈都走满 16384 个节点、校验和等于全节点参考和、末节点 `next` 回到 head；
4. 最终遍历标记全部节点且无重复，链仍是单环；
5. 最终校验和与每圈一致，说明 GC 未打断或破坏指针链；
6. 每 8 圈显式 GC 一次，并发 GC pump 至少完成一轮；
7. 全部节点的 `v`/`idx` 与 `next` 非空在 GC 后保持；
8. 在 base/clobberfree/efence/k8s 等 profile 下输出与校验结果一致。

## 失败判读

- `FAIL: every chase pass checksum and cycle closure`：链在追逐中被打断，或 `next` 被 GC 清零；
- `FAIL: chain still a single cycle over all nodes after GC`：节点丢失/重复，指针链被破坏；
- `FAIL: final chase checksum after GC`：GC 后读到错误数据或悬空指针；
- `SIGSEGV` / `fatal error: found bad pointer`：`next`/head 指针未被栈映射正确扫描；
- 超时：GC pump 未启动或链成环错误导致死循环（本用例按固定节点数终止）。

## 参数

- `UAF_SEED`：决定链表节点排列与单环顺序；失败信息带出种子；
- `TORTURE_ROUNDS`：替换默认 512 圈，用于放大追逐次数与 GC 次数。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_ptr_chase_chain/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=4096 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
