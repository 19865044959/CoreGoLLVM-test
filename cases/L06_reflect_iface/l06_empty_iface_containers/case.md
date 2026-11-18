---
id: l06_empty_iface_containers
dim: L06_reflect_iface
priority: P2
sources:
  - caddy@v2.10.0: admin_test.go:51 JSON any 树的增删改、数组下标与递归展开
  - goccy-json@v0.10.5: decode_test.go:2457 预填充 map[string]any / []any 的整树替换语义
tags: [L06, empty-interface, nested-containers, type-switch, GC, torture]
requires: []
runtime: "<60s"
---

# 场景

递归构造 `[]interface{}` / `map[string]interface{}` 混合树，叶子为含指针的 `*node`；用 type switch 深度遍历并求和，遍历过程中每 128 步显式 GC。丢弃局部根后仅靠全局 `any` 槽再遍历一遍。默认 1000 轮、深度 6，可用 `TORTURE_ROUNDS` 放大。

# 期望行为

1. 每轮 type switch 遍历的节点值之和等于构造时返回的期望和；
2. 树中只有 `*node`、`[]any`、`map[string]any` 三种动态类型（`bad=0`），节点/容器/标签计数非零；
3. 多次 GC、丢弃局部根后再遍历，sum/节点数/容器数与首次完全一致；
4. 任意 UAF_SEED 下结构随机但断言恒成立；
5. 最后一行 `ALL PASS l06_empty_iface_containers`。

# 失败判读

- `nested interface tree sum`：接口容器中的 `*node` 被回收或 map/slice 元素丢失；
- `unexpected dynamic type`：接口装箱类型元数据损坏；
- `survives repeated GC`：递归遍历路径上的栈/接口数据未被正确扫描；
- 崩溃：空接口容器里的指针位图错误。

# 参数

- `UAF_SEED`：树形状随机种子（runner 注入），失败输出带种子；
- `TORTURE_ROUNDS`：轮数（默认 1000，上限 50000）。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_empty_iface_containers/*.go
UAF_SEED=1 TORTURE_ROUNDS=1000 GODEBUG=clobberfree=1 /tmp/case
```
