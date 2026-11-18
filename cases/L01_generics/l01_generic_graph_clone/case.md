---
id: l01_generic_graph_clone
dim: L01_generics
priority: P2
sources:
  - ent@v0.14.4: entc/gen/graph_test.go:68 由 Schema/Edge 构造 Graph，校验节点顺序与边属性
  - ent@v0.14.4: entc/gen/graph_test.go:565 Generator 中间件遍历图元数据并逐项校验
  - pgx@v5.7.4: pgtype/bytea_test.go:49 结果缓冲会被后续行覆盖，保存结果必须拷贝而非别名
tags: [L01, generics, graph, deep-copy, aliasing, cycles, payload-clone, pointer-identity, gc, TORTURE_ROUNDS, UAF_SEED]
requires: [generics]
runtime: "<10s（默认 1 轮；TORTURE_ROUNDS 放大节点规模，上限 8）"
---

# 场景

泛型图 `ggraph[T any]`：`map[int64]*gnode[T]` + 节点邻接表，边按目标 ID 排序保证确定。`clone` 两阶段深拷贝：先按排序后的 ID 建节点映射与原负载副本，再按原边表重连；共享节点经 memo 保持共享，环保持环，克隆与原图不共享任何节点/边/负载指针。对应代码生成器的图元数据复制与"结果缓冲必须深拷贝"的语义。

按 UAF_SEED 与 TORTURE_ROUNDS 构造 140–1400 个节点的二叉树骨架、稀疏跳边和固定回边（保证有环），在克隆过程中每 64 个节点显式 GC。克隆后逐节点校验节点/负载/边的指针非别名、ID 与内容同构、环保留、负载副本全局唯一；修改克隆负载并 GC 后确认原图不受影响。另测 nil 负载图与 `ggraph[string]` 二次实例化。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_graph_clone`；
2. 克隆节点数、边数与 ID 集合与原图一致；每个克隆节点/负载/边目标指针均不等于原图对应指针；
3. 每条克隆边的目标等于按原目标 ID 查到的克隆节点，边顺序与 ID 保持；环在原图与克隆中均存在；
4. 每个克隆负载是独立副本（指针集合无重复），内容为原负载的深拷贝；
5. 修改克隆负载后 GC，原图负载名称/首字节不变；nil 负载保持 nil；string 图深拷贝内容正确；
6. TORTURE_ROUNDS 放大节点规模后输出仍确定，clobberfree 等 profile 下一致。

# 失败判读

- `aliases original` / `payload alias`：memo 或复制函数错误，深拷贝退化为浅拷贝；
- `edge ... rewired to wrong clone`：第二阶段重连使用的映射错误或边序错乱；
- `cycle not preserved`：回边丢失或克隆时边表截断；
- `payload pointer shared between nodes`：多个节点复用了同一个副本；
- bad pointer / blob corrupted：克隆负载未正确保活，怀疑栈映射/写屏障。

## 参数

- `UAF_SEED`：决定基础节点数（140–170）；
- `TORTURE_ROUNDS`：默认 1，最大 8，按倍数放大节点规模。

## 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_graph_clone/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=4 GODEBUG=clobberfree=1 /tmp/case
```
