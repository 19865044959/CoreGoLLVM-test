---
id: l01_generic_container_tower
dim: L01_generics
priority: P1
sources:
  - pgx@v5.7.4: pgtype/multirange_test.go:20 Multirange[Range[Int4]] 嵌套泛型实例化覆盖 nil/空/两段区间
  - badger@v4.5.0: trie/trie_test.go:28 嵌套 map 前缀树按多前缀返回 id 集合，含空前缀/重复 id
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/queue_test.go:26 NewQueue[int] 泛型容器的生产/消费与 Size 校验
tags: [L01, generics, nested-instantiation, container-composition, pointer-identity, gc, UAF_SEED]
requires: [generics]
runtime: "<10s"
---

# 场景

构造两层泛型容器组合：`Index[string, Stack[item]]`（每个 tenant 一个 `*item` 栈）与
`Index[int, Index[string, item]]`（group → key → `*item`），再实例化
`Index[string, Stack[int64]]` 作为不同实参的嵌套布局对照。对应 pgx 嵌套区间、
badger 嵌套前缀表和 kitex 泛型队列的真实组合形态。

# 期望行为

1. 每层 `Get` 返回与创建时相同的容器/元素指针，tenant 数、栈长（pop 3 个后）正确；
2. 所有 `*item` 的 id/name 在多轮 GC 后不变，经 `At` 与弹出顺序（LIFO）均可复核；
3. 从 `Index` 删除 tenant 后 key 不可见，但外部保留的栈指针及其载荷在 GC 后仍完整；
4. 二层嵌套 `Index[int, Index[string, item]]` 的内层指针/载荷与 id 总和正确；
5. `Stack[int64]` 实例化的元素指针与值不受 item 实例影响；
6. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_container_tower`。

# 失败判读

- `nested stack pointer identity lost` / `stack element pointer identity lost`：嵌套容器的字段偏移或 `*V` 层数计算错误；
- `second-level pointer identity lost`：多层实例化的指针槽位布局混用；
- `kept stack lost elements after map delete + GC`：仍被局部变量持有的栈被误回收，疑似栈映射漏标；
- `int64 nested stack ...`：不同 V 实参共享了错误的实例布局；
- `FAILED ... seed=`：失败信息带 UAF_SEED，按种子复现。

# 参数

- `UAF_SEED`：决定 tenant 数（5..8）和每栈元素数（18..24），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_container_tower/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
