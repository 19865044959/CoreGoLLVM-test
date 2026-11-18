---
id: l06_type_switch_many
dim: L06_reflect_iface
priority: P1
sources:
  - gorm@v1.30.0: utils/utils_test.go:114 ToString 对 int/uint/string/bool 的 type switch
  - gorm@v1.30.0: schema/schema_test.go:135 命名类型/别名类型的 type switch 判定
tags: [L06, type-switch, interface-boxing, itab, GC]
requires: []
runtime: "<10s"
---

# 场景

把 25 种动态类型（各宽度整数、浮点、string/bool、命名类型、结构体值、含指针的结构体、slice、map、typed nil、nil 接口、接口实现者）装箱进 `[]any`，用 type switch 分类并累加。每轮装箱后 GC，按 UAF_SEED 打乱顺序后再分类一次，并跨 GC 解引用装箱的 `*point`。

# 期望行为

1. 每个元素的 type switch 分类码与求和值都等于固定 golden 表；
2. 打乱顺序后的分类聚合值不变，说明结果与遍历顺序无关；
3. 装箱的 `*point` 及其 `Tag *node` 在多次 GC 后仍可解引用，值保持 5/6/7/"p"；
4. 最后一行 `ALL PASS l06_type_switch_many`。

# 失败判读

- `type switch classification mismatch`：动态类型被错误归类，或类型元数据/itab 在 GC 后损坏；
- `boxed pointer survives GC`：接口数据区中的指针未被 GC 扫描，属于漏标；
- `shuffled ... aggregate`：类型 switch 结果依赖顺序，通常是编译器优化错误；
- 崩溃：接口数据区指针位图错误。

# 参数

- `UAF_SEED`：打乱顺序的随机种子（runner 注入），失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_type_switch_many/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
