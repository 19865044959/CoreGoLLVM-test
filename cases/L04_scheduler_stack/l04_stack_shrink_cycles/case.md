---
id: l04_stack_shrink_cycles
dim: L04_scheduler_stack
priority: P1
sources:
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:359 随机区间插入/删除后的递归 Visit 造成栈增长
  - tailscale@v1.80.2: net/art/table_test.go:578 1 万前缀长循环插入/查找中穿插 GC 与调度点
tags: [L04, stack-shrink, morestack, recursion, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 etcd interval tree 的递归 Visit 与 tailscale ART 的长循环查询交替出现：每轮先做 600–899 层、每帧 32 个指针槽的深递归（栈涨到约 200KB），叶子处显式 GC；返回后主协程立即再分配垃圾并 GC 两次。此时 goroutine 栈使用量远低于峰值，runtime 的 scanstack/shrinkstack 会收缩栈。收缩后再进入深递归、再返回，共 6 轮，另加一轮重入。全程持有 `anchor` 与 `keep[]` 指针，逐轮校验跨栈收缩仍有效。

# 期望行为

1. 每轮 `shrinkDeep` 返回值等于 `expectedShrink(depth, salt)`；
2. 每轮回溯时 32 个指针槽仍指向本帧 `local`，无 -2^60 哨兵；
3. 每轮返回到浅栈后显式 GC 两次，`anchor` 与 8 个 `keep` 指针内容保持不变；
4. 6 轮收缩后再次进入深递归仍返回正确结果（栈可重新增长）；
5. 最后一行输出 `ALL PASS l04_stack_shrink_cycles`。

# 失败判读

- 返回 -2^60：深递归回溯时指针槽被破坏；
- `anchor/keep corrupted after stack shrink`：栈收缩（复制/回退）后主协程栈上指针未重定位；
- 返回值错误但无哨兵：深栈数据丢失；
- `SIGSEGV`/`found bad pointer`：栈收缩路径的栈映射问题。

# 参数

- `UAF_SEED`：决定基础深度（600 + seed%300）、每轮盐值与对象初值。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_stack_shrink_cycles/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=4 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
