---
id: l17_recursive_closure_gc
dim: L17_stackmap
priority: P2
sources:
  - sonic@v1.13.2: issue_test/issue_recurse_test.go:16（TestPointerValueRecurseMarshal 递归 Marshaler 处理指针值）
  - sonic@v1.13.2: decode_test.go:2433（TestUnmarshalMaxDepth 深层嵌套触发递归/栈增长）
tags: [L17, stackmap, recursive-closure, tree, parent-pointer, GC, TORTURE_ROUNDS, UAF_SEED]
requires: []
runtime: "<10s（默认 24 轮；TORTURE_ROUNDS 放大轮数，上限 256）"
---

# 场景

递归闭包构造深度 10、扇出 2 的树（2047 个节点），每个节点带 `kids []*treeNode` 和 parent 回指 `back`，递归过程中每 `gcEvery` 次调用强制 GC。随后用另一个递归闭包遍历全树并再次穿插 GC，校验节点总数、id 和、parent 回指；再 GC 后检查两层子链。覆盖递归闭包帧、树环（back 指针）与栈增长下的指针重定位。

# 期望行为

1. builder 恰好访问 2047 个节点；验证 count/sum 等于公式期望；
2. walker 在 GC 前后各遍历一次，count/sum 一致；
3. 所有节点的 `back` 指针等于其父节点（根为 nil），label 未损坏；
4. 最后一行 `ALL PASS l17_recursive_closure_gc`，退出码 0。

# 失败判读

- `back pointer broken`：栈上父指针在递归/GC 后未重定位或未被扫描；
- `kids=N` / count/sum 不符：子节点指针或闭包 capture 被破坏；
- 仅 clobberfree 下失败：递归返回后对象被提前回收；
- 栈溢出/超时：递归深度或 TORTURE_ROUNDS 过大，属参数问题。

# 参数

- `UAF_SEED`：决定 root id 偏移与 `gcEvery`（48..79）；失败信息带种子。
- `TORTURE_ROUNDS`：轮数倍数（默认 24×，上限 256），用于长压测。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_recursive_closure_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=8 UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
