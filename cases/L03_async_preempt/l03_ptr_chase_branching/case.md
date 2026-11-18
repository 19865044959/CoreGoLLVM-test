---
id: l03_ptr_chase_branching
dim: L03_async_preempt
priority: P2
sources:
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:359 随机区间插入/查询 + 递归 Visit，长循环中制造指针遍历与栈压力
  - tailscale@v1.80.2: net/art/table_test.go:578 1 万前缀建树后做 1 万次随机地址查找，与慢速参考实现比对
tags: [L03, async-preempt, pointer-chase, branchy-chain, mid-chase-gc, model-check, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

96 条链、每条 64 个节点，每个节点有 `next[0]`/`next[1]` 两条出边（跳 1 或跳 2）。每轮按节点值、轮次与种子计算的 bit 选择下一跳，逐节点解引用到 nil，并与整数模型逐轮比对节点数/校验和；每 16 轮在一次追逐的第 16 跳处分配垃圾并显式 `runtime.GC()`，GC 后校验当前节点字段再继续沿同一指针前进。另有 8 轮并发 GC pump。结束后逐个复核所有链节点。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l03_ptr_chase_branching`；
2. 每轮指针追逐的节点数与校验和等于整数模型；
3. 第 16 跳处的显式 GC 后当前节点仍可读，后续链不被截断；
4. 中途 GC 次数等于 `ceil(rounds/16)`，GC pump 至少完成 1 轮；
5. 结束后 96×64 个节点的 `v/idx/tag` 全部完整；
6. 在 clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `FAIL: branchy pointer chase matches model`：next[0]/next[1] 指针被破坏或选跳 bit 计算错误；
- `FAIL: mid-chase pointer survives GC`：追逐中的局部指针未被栈映射正确扫描；
- `FAIL: chain nodes intact after loop`：堆上链节点被内存破坏；
- `SIGSEGV` / `fatal error: found bad pointer`：疑似 GC 元数据/写屏障问题；
- 超时：链出现环（检查 next 构造）。

## 参数

- `UAF_SEED`：决定链选择步长/偏移与跳转 bit 输入；失败信息带出种子；
- `TORTURE_ROUNDS`：放大默认 512 轮（上限 65536）。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_ptr_chase_branching/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8192 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
