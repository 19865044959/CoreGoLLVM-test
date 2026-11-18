---
id: l06_reflect_deep_copy
dim: L06_reflect_iface
priority: P2
sources:
  - gorm@v1.30.0: statement_test.go:11 多层 WHERE 条件 clone 后互不污染（深拷贝语义）
  - caddy@v2.10.0: admin_test.go:51 JSON any 树递归展开/复制
tags: [L06, reflect, deep-copy, shallow-copy, pointer-graph, GC, torture]
requires: [reflect]
runtime: "<60s"
---

# 场景

用 reflect 实现递归深拷贝（Ptr/Slice/Array/Map/Struct/Interface，指针按地址 memo 以保留环），复制含 `*leaf`、`[]*leaf`、`[][]int64`、`map[string]*leaf`、嵌套 struct 的 `doc`；GC 后修改原对象，逐项验证深拷贝独立、浅拷贝共享，并覆盖自引用指针环。

# 期望行为

1. 副本的标量、Tag、Tags、Attr、Grid、Inner 值与原始快照一致，指针身份不同；
2. 深拷贝后修改原对象任意指针字段，副本对应值不变；
3. `shallow := orig` 后修改原对象，浅拷贝能看到 Tag/Tags/Attr/Grid 变化；
4. GC 后全局保存的副本仍可解引用；
5. 指针环 a→b→a 深拷贝后保持 `cc.Next.Next == cc`，改副本不影响原图；
6. 最后一行 `ALL PASS l06_reflect_deep_copy`。

# 失败判读

- `deep copy ... independent`：深拷贝漏复制某类节点（常见 slice/map 元素），退化为浅拷贝；
- `shallow copy shares ...`：赋值语义被错误改写；
- `preserves pointer cycle`：memo 未按指针身份复用，导致环断裂或无限递归（栈溢出）；
- 崩溃/超时：深拷贝对环未终止。

# 参数

- `TORTURE_ROUNDS`：轮数（默认 1000，上限 50000），每轮 120 个叶子节点；
- `UAF_SEED`：runner 注入，失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_deep_copy/*.go
UAF_SEED=1 TORTURE_ROUNDS=1000 GODEBUG=clobberfree=1 /tmp/case
```
