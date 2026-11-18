---
id: l01_generic_graph
dim: L01_generics
priority: P2
sources:
  - ent@v0.14.4: entc/gen/graph_test.go:68 由 Schema/Edge 构造 Graph，校验节点顺序与边属性
  - ent@v0.14.4: entc/gen/graph_test.go:565 Generator 中间件遍历生成元数据并逐项校验
  - ent@v0.14.4: entc/gen/graph_test.go:449 按 feature 遍历图并产出确定文件集合
tags: [L01, generics, graph, BFS, DFS, topo-sort, cycle-detection, pointer-identity, gc, TORTURE_ROUNDS, UAF_SEED]
requires: [generics]
runtime: "<10s（默认；TORTURE_ROUNDS 放大图规模，上限 8）"
---

# 场景

泛型 `Graph[T]` 以 `map[int64]*Node[T]` 保存节点、`Node.Edges []*Node[T]` 保存邻接表，边按 ID 排序保证遍历确定。构造 n 个带 `*pcell` 负载的二叉树骨架（i→2i+1/2i+2）并加确定性跳边，BFS 与迭代 DFS 各遍历全图；BFS 访问回调每 32 个节点显式 GC，并校验访问唯一性、负载指针身份与字段。随后 Kahn 拓扑排序验证每条边 pos[u]<pos[v]、DAG 无环；独立小图加回边验证环检测与拓扑失败。最后 `Graph[string]` 二次实例化。TORTURE_ROUNDS 放大节点数。

# 期望行为

1. BFS/DFS 均访问全部 n 个节点且无重复，负载指针与创建时相同，GC 穿插后 id/name/blob 不变；
2. 拓扑序长度==n，图上所有边满足 pos[u]<pos[v]，DAG 的 HasCycle()==false；
3. 独立链图初始无环，加 7→0 回边后 HasCycle()==true 且 Topo 返回 ok=false；
4. GC 后随机抽样节点仍能取回原负载指针；
5. `Graph[string]` 的 32 个节点 BFS 全访问，字符串负载在 GC 后正确。

# 失败判读

- `visited twice` / `missed nodes`：邻接表 Link 去重或 seen 标记时机错误；
- `payload pointer identity changed`：节点/负载未被图结构保活，检查 map 与 Edges 的强引用；
- `topo order violated`：Kahn 入度维护错误；`cycle not detected`：三色状态回退错误。

# 参数

- `TORTURE_ROUNDS`：默认 1，最大 8；n=(300+seed%40)*rounds；
- `UAF_SEED`：决定节点数与负载内容，失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_graph/*.go
UAF_SEED=7 TORTURE_ROUNDS=4 GODEBUG=clobberfree=1 /tmp/case
```
