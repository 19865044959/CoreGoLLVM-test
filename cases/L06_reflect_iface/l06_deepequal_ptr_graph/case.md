---
id: l06_deepequal_ptr_graph
dim: L06_reflect_iface
priority: P2
sources:
  - gorm@v1.30.0: schema/relationship_test.go:96 自引用 belongs-to 的 Manager *User 指针图
  - caddy@v2.10.0: admin_test.go:51 JSON any 树的 map/slice 嵌套比较
  - gin@v1.10.0: tree_test.go:138 路由树结构逐节点比较
tags: [L06, reflect, DeepEqual, pointer-graph, cycle, func, NaN, GC, torture]
requires: [reflect]
runtime: "<60s"
---

# 场景

构造带 `Next` 链环、`Kids` 切片、`Attr` map 自引用、`Ref` 共享指针的节点图，独立构造两份结构
相等的图交给 `reflect.DeepEqual`；逐项覆盖共享 vs 独立等值节点、自环 vs 双节点环、nil vs 空
slice、非 nil func 永不相等、双端 NaN 不相等，以及删除 map 键/追加 Kid/改标量后的 false 与恢复。
每轮 GC 后重复比较并遍历共享链。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_deepequal_ptr_graph`；
2. 两份独立构造的含环指针图 DeepEqual 为 true，环形态（自环 vs 双节点环）不影响等值；
3. 指针身份不影响结果，共享节点与独立等值节点视为相等；
4. 非 nil func 即使同一函数也不相等，双方 nil 才相等；双端 NaN 不相等；
5. 删除键/追加元素/修改值即 false，恢复后重新 true；环遍历不挂起；
6. 默认 20 轮 ×40 节点在 60s 内完成，支持 `TORTURE_ROUNDS/TORTURE_WIDTH` 放大。

## 失败判读

- DeepEqual 对含环图死循环/栈溢出：环检测（visited 对）缺失或错误；
- 等值图判 false 或变异后仍 true：指针解引用/字段比较逻辑错误；
- func/NaN 语义不符：不可比较类型处理错误；
- GC 后链/共享指针断裂：堆对象扫描或写屏障问题。

## 参数

- `UAF_SEED`：随机种子，决定变异位置；失败输出带出该值；
- `TORTURE_ROUNDS`：轮数，默认 20，上限 2000；
- `TORTURE_WIDTH`：每图节点数，默认 40，上限 2000。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_deepequal_ptr_graph/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=500 TORTURE_WIDTH=200 UAF_SEED=4 /tmp/case
```
