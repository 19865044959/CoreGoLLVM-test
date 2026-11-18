---
id: l01_generic_avl_tree
dim: L01_generics
priority: P1
sources:
  - ent@v0.14.4: entql/types_test.go:14 谓词表达式树 And/Or/Not 组合并递归遍历
  - ent@v0.14.4: entc/gen/graph_test.go:68 由 Schema/Edge 构造 Graph，校验节点顺序与属性
  - sqlc@v1.28.0: internal/engine/postgresql/rewrite_test.go:13 SQL AST 重写并保持树结构
tags: [L01, generics, AVL, tree, ordered-constraint, rotation, in-order, deletion, gc, UAF_SEED]
requires: [generics]
runtime: "<10s"
---

# 场景

泛型 AVL 树 `AVL[K ordered, V any]`，约束 `ordered` 覆盖 `~int | ~int64 | ~string`，节点持有 `*V` 负载。实现插入（重复 key 替换并返回旧指针）、删除（含双子节点后继替换）、四种旋转、中序遍历与结构校验（BST 序、节点高度、平衡因子、节点计数）。

以 UAF_SEED 洗牌插入 160–200 个唯一 key，每 32 次插入显式 GC；校验中序 key 严格升序、指针身份与高度上界。随后乱序删除一半 key，校验删除返回值、剩余中序集合与存活 key 查询，GC 后再校验一次；最后删除全部 key 并验证空树。再实例化 `AVL[string, rec]` 覆盖字符串键。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_avl_tree`；
2. 插入后 Len 正确、中序 key 为 0..n-1、每个值指针与创建时相同；
3. `Valid()` 为真：BST 序成立、每个节点高度字段正确、平衡因子绝对值 ≤1；
4. 重复 key 插入返回旧指针且 Len 不变；删除 half 后中序等于升序存活集合，被删 key 查询失败；
5. 删除全部后 Len==0、Height==0、空树删除返回 false；
6. GC 后存活负载指针、blob 不变；string 键实例结构合法；clobberfree 等 profile 下一致。

# 失败判读

- `AVL invariants violated`：旋转/高度维护错误，优先检查删除双子节点路径与 rebalance；
- `in-order key got=... want=...`：BST 序被破坏或删除未正确接续；
- `payload pointer mismatch`：节点值指针在 GC 后漂移/丢失；
- 栈溢出：树退化为链或旋转错误导致深度异常；超时为 GC 饥饿。

## 参数

- `UAF_SEED`：决定 key 数量、插入/删除顺序。

## 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_avl_tree/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
