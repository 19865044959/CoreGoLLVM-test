---
id: l01_generic_interval_tree
dim: L01_generics
priority: P2
sources:
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:359 随机插入唯一区间，对每个区间做 Stab/Intersects，再逐个删除至空
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:319 Stab 查询覆盖点的所有区间
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:406 Visit 递归中序遍历（排序输出）
tags: [L01, generics, interval-tree, treap, augmented-tree, stab-query, intersects, pointer-identity, gc, UAF_SEED, TORTURE_ROUNDS]
requires: [generics]
runtime: "<30s"
---

# 场景

自建泛型区间树 `intervalTree[T cmp.Ordered, V any]`：按 (lo,hi) 排序的 treap，节点增强 `maxHi` 与 `size`；支持 Insert（重复区间更新值并返回 false）、Delete、Stab(p)（覆盖点查询）、Intersects([lo,hi])、中序 Visit 与结构不变量检查。对应 etcd `pkg/adt.IntervalTree` 的随机区间测试。

UAF_SEED 驱动每轮生成 320 个唯一区间与 220 个随机查询点/查询区间，与顺序表引用模型全量比对 Stab/Intersects 结果（按 id 排序后比较指针）；每轮显式 GC 后校验 `maxHi`、treap 优先级、BST 顺序与 size 不变量。随后按确定顺序删除三分之一区间、再 GC 复查；并实例化 `intervalTree[string,*ivPayload]` 覆盖字符串边界与字典序。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_interval_tree`；
2. 插入计数与模型一致；重复区间 Insert 返回 false 且 Stab 看到新指针；
3. 每个查询点的 Stab 结果、每个查询区间的 Intersects 结果与引用模型完全一致；
4. 每次显式 GC 后 `maxHi` 增强字段、父/子优先级、BST 顺序、size 均正确，payload 指针身份与内容不变；
5. 删除后 Size、查询结果与模型一致，被删区间不再出现，剩余指针不变；
6. 字符串实例：`Stab("b")` = 覆盖 [a,c] 与 [b,f] 的两个值，`Intersects("e","n")` 结果按 lo 升序正确；
7. 在 clobberfree/GOGC=1 等 profile 下结果一致。

# 失败判读

- `Stab/Intersects mismatch`：剪枝条件错误（左子树 maxHi 或右子树 lo 判断）或删除后增强字段未更新；
- `maxHi augmented field wrong` / `size field wrong`：旋转或 merge 后未重新计算；
- `treap priority order broken`：旋转方向错误，树退化为链并可能超时；
- `post-GC pointer mismatch` / `payload corrupted`：节点或负载未被正确保活；
- `SIGSEGV` / `fatal error: found bad pointer`：大概率栈映射漏标或写屏障缺失。

# 参数

- `UAF_SEED`：决定区间集合、查询点与删除集合；失败输出带种子；
- `TORTURE_ROUNDS`：轮数（默认 3）；
- `TORTURE_INTERVALS`：每轮区间数（默认 320）；
- `TORTURE_QUERIES`：每轮查询数（默认 220）。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_interval_tree/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=20 TORTURE_INTERVALS=2000 TORTURE_QUERIES=2000 UAF_SEED=7 /tmp/case
```
