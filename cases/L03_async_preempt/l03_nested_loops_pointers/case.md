---
id: l03_nested_loops_pointers
dim: L03_async_preempt
priority: P1
sources:
  - tailscale@v1.80.2: net/art/table_test.go:623 同一批 1000 前缀做 10 次随机打乱插入，1 万次查询与插入顺序无关
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:359 随机区间插入/查询 + 递归 Visit，制造栈压力
tags: [L03, async-preempt, nested-loop, local-pointer, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

行×列嵌套循环遍历二维指针视图：外层按 `UAF_SEED` 生成的固定排列访问 48 行，内层每列取一个局部指针 `q`，在 8 次无调用迭代中反复解引用。另一 goroutine 并发执行多轮 GC。用于覆盖内层紧循环中局部指针跨 GC 存活的栈映射路径。

# 期望行为

1. 至少一轮 GC 在嵌套循环运行期间完成；
2. 总校验和等于 `passes * base * 8`，与行访问顺序无关；
3. GC 后所有节点的 `s` 字段仍为 `"n"`；
4. 进程退出码为 0，最后一行 `ALL PASS l03_nested_loops_pointers`；
5. 同一 `UAF_SEED` 下输出一致；不同种子只改变行顺序，不改变判定结果。

# 失败判读

- `FAIL: GC completed during nested loops (seed=...)`：内层循环未在安全点被抢占；
- `FAIL: nested-loop checksum (seed=...)`：局部指针/累加值被破坏；
- `FAIL: nodes intact after nested-loop GC (seed=...)`：指针目标被错误回收；
- 失败信息携带种子，复现时用同样的 `UAF_SEED`。

# 参数

- `UAF_SEED`：行排列种子（runner 每次重复注入不同值），默认 1；失败输出带出该值。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_nested_loops_pointers/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=7 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
